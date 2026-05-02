package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

type ConsumerConfig struct {
	URL                 string
	Exchange            string
	Queue               string
	RoutingKey          string
	DLXExchange         string
	DLQ                 string
	MaxDeliveryAttempts int
}

// Handler processes a decoded event. Returning a non-nil error triggers
// Nack-with-requeue (transient failure) up to MaxDeliveryAttempts, after
// which the message is sent to the DLQ. Returning ErrPermanent skips retries
// and routes straight to the DLQ.
type Handler func(ctx context.Context, ev PaymentCompletedEvent) error

var ErrPermanent = errors.New("permanent processing error")

type Consumer struct {
	conn         *amqp.Connection
	ch           *amqp.Channel
	cfg          ConsumerConfig
	store        *IdempotencyStore
	handler      Handler

	// attempt counter per MessageId — used together with Nack-requeue to
	// implement bounded retries before DLQ.
	attemptsMu sync.Mutex
	attempts   map[string]int
}

func NewConsumer(cfg ConsumerConfig, handler Handler, store *IdempotencyStore) (*Consumer, error) {
	if cfg.MaxDeliveryAttempts <= 0 {
		cfg.MaxDeliveryAttempts = 3
	}
	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("amqp dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("amqp channel: %w", err)
	}

	// Declare main exchange (matches producer).
	if err := ch.ExchangeDeclare(cfg.Exchange, "topic", true, false, false, false, nil); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("declare exchange: %w", err)
	}

	// Declare dead-letter exchange and queue. The main queue points at the
	// DLX so messages we permanently reject end up here.
	if cfg.DLXExchange != "" {
		if err := ch.ExchangeDeclare(cfg.DLXExchange, "topic", true, false, false, false, nil); err != nil {
			ch.Close()
			conn.Close()
			return nil, fmt.Errorf("declare dlx: %w", err)
		}
		if cfg.DLQ != "" {
			if _, err := ch.QueueDeclare(cfg.DLQ, true, false, false, false, nil); err != nil {
				ch.Close()
				conn.Close()
				return nil, fmt.Errorf("declare dlq: %w", err)
			}
			if err := ch.QueueBind(cfg.DLQ, cfg.RoutingKey, cfg.DLXExchange, false, nil); err != nil {
				ch.Close()
				conn.Close()
				return nil, fmt.Errorf("bind dlq: %w", err)
			}
		}
	}

	// Main queue: durable, with DLX routing for permanent rejections.
	args := amqp.Table{}
	if cfg.DLXExchange != "" {
		args["x-dead-letter-exchange"] = cfg.DLXExchange
		args["x-dead-letter-routing-key"] = cfg.RoutingKey
	}
	if _, err := ch.QueueDeclare(cfg.Queue, true, false, false, false, args); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("declare queue: %w", err)
	}
	if err := ch.QueueBind(cfg.Queue, cfg.RoutingKey, cfg.Exchange, false, nil); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("bind queue: %w", err)
	}

	// Fair dispatch: one un-acked message per consumer at a time.
	if err := ch.Qos(1, 0, false); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("qos: %w", err)
	}

	log.Printf("consumer ready: queue=%q exchange=%q routing=%q dlq=%q",
		cfg.Queue, cfg.Exchange, cfg.RoutingKey, cfg.DLQ)

	return &Consumer{
		conn: conn, ch: ch, cfg: cfg,
		store: store, handler: handler,
		attempts: make(map[string]int),
	}, nil
}

// Run blocks until ctx is cancelled, then drains in-flight deliveries and
// closes the channel/connection cleanly.
func (c *Consumer) Run(ctx context.Context) error {
	deliveries, err := c.ch.Consume(
		c.cfg.Queue,
		"notification-service",
		false, // auto-ack DISABLED — manual acks required
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,
	)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("consumer: context cancelled, stopping")
			return c.shutdown()
		case d, ok := <-deliveries:
			if !ok {
				log.Println("consumer: delivery channel closed by broker")
				return errors.New("broker channel closed")
			}
			c.processDelivery(ctx, d)
		}
	}
}

func (c *Consumer) processDelivery(ctx context.Context, d amqp.Delivery) {
	msgID := d.MessageId
	if msgID == "" {
		// fall back to body hash so dedup still works for ill-formed producers
		msgID = fmt.Sprintf("anon-%d", d.DeliveryTag)
	}

	var ev PaymentCompletedEvent
	if err := json.Unmarshal(d.Body, &ev); err != nil {
		log.Printf("consumer: malformed event id=%s: %v — sending to DLQ", msgID, err)
		// Permanent: bad payload will never decode no matter how many times we retry.
		_ = d.Nack(false, false)
		return
	}

	// Idempotency: short-circuit duplicate deliveries before doing the work.
	if !c.store.MarkIfNew(msgID) {
		log.Printf("[Idempotency] skip duplicate id=%s order=%d", msgID, ev.OrderID)
		_ = d.Ack(false)
		return
	}

	if err := c.handler(ctx, ev); err != nil {
		c.handleFailure(d, msgID, ev, err)
		return
	}

	if err := d.Ack(false); err != nil {
		log.Printf("consumer: ack failed for id=%s: %v", msgID, err)
		return
	}
	c.clearAttempts(msgID)
}

func (c *Consumer) handleFailure(d amqp.Delivery, msgID string, ev PaymentCompletedEvent, err error) {
	// Re-allow processing on next attempt — the handler failed so the
	// idempotency mark was premature. Without this, a transient failure
	// would be permanently swallowed by the dedup store.
	c.store.Forget(msgID)

	if errors.Is(err, ErrPermanent) {
		log.Printf("consumer: permanent error id=%s order=%d: %v — routing to DLQ", msgID, ev.OrderID, err)
		_ = d.Nack(false, false)
		c.clearAttempts(msgID)
		return
	}

	attempts := c.bumpAttempts(msgID)
	if attempts >= c.cfg.MaxDeliveryAttempts {
		log.Printf("consumer: gave up id=%s order=%d after %d attempts: %v — routing to DLQ",
			msgID, ev.OrderID, attempts, err)
		_ = d.Nack(false, false) // false = don't requeue → goes to DLX → DLQ
		c.clearAttempts(msgID)
		return
	}
	log.Printf("consumer: transient failure id=%s order=%d attempt=%d/%d: %v — requeueing",
		msgID, ev.OrderID, attempts, c.cfg.MaxDeliveryAttempts, err)
	_ = d.Nack(false, true) // true = requeue
}

func (c *Consumer) bumpAttempts(id string) int {
	c.attemptsMu.Lock()
	defer c.attemptsMu.Unlock()
	c.attempts[id]++
	return c.attempts[id]
}

func (c *Consumer) clearAttempts(id string) {
	c.attemptsMu.Lock()
	defer c.attemptsMu.Unlock()
	delete(c.attempts, id)
}

func (c *Consumer) shutdown() error {
	var first error
	if c.ch != nil {
		if err := c.ch.Close(); err != nil {
			first = err
		}
	}
	if c.conn != nil {
		if err := c.conn.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
