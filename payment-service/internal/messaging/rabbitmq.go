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

type RabbitConfig struct {
	URL          string
	Exchange     string // topic exchange for payment events
	DLXExchange  string // dead-letter exchange (consumer side declares the DLQ)
}

// RabbitPublisher is a topic-exchange publisher with publisher confirms enabled
// so we know the broker actually accepted the message.
type RabbitPublisher struct {
	conn     *amqp.Connection
	ch       *amqp.Channel
	exchange string
	mu       sync.Mutex
}

func NewRabbitPublisher(cfg RabbitConfig) (*RabbitPublisher, error) {
	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("amqp dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("amqp channel: %w", err)
	}

	if err := ch.ExchangeDeclare(
		cfg.Exchange, "topic",
		true,  // durable
		false, // auto-delete
		false, // internal
		false, // no-wait
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("exchange declare: %w", err)
	}

	if cfg.DLXExchange != "" {
		if err := ch.ExchangeDeclare(
			cfg.DLXExchange, "topic",
			true, false, false, false, nil,
		); err != nil {
			ch.Close()
			conn.Close()
			return nil, fmt.Errorf("dlx exchange declare: %w", err)
		}
	}

	if err := ch.Confirm(false); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("enable confirms: %w", err)
	}

	log.Printf("RabbitMQ publisher connected, exchange=%q", cfg.Exchange)
	return &RabbitPublisher{conn: conn, ch: ch, exchange: cfg.Exchange}, nil
}

func (p *RabbitPublisher) Publish(ctx context.Context, routingKey string, event Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	confirm, err := p.ch.PublishWithDeferredConfirmWithContext(
		ctx,
		p.exchange,
		routingKey,
		true,  // mandatory: fail if no queue is bound
		false, // immediate (deprecated)
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent, // survive broker restart
			MessageId:    event.ID,
			Type:         event.Type,
			Timestamp:    parseRFC3339OrZero(event.OccurredAt),
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	ok, err := confirm.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("await confirm: %w", err)
	}
	if !ok {
		return errors.New("broker nacked the message")
	}
	return nil
}

func (p *RabbitPublisher) Close() error {
	var first error
	if p.ch != nil {
		if err := p.ch.Close(); err != nil {
			first = err
		}
	}
	if p.conn != nil {
		if err := p.conn.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
