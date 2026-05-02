package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"notification-service/internal/messaging"
)

func getEnv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func getEnvInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return d
}

// poisonOrderID lets us demonstrate the DLQ: any event with this order_id is
// treated as a permanent failure so the broker routes it to the dead-letter
// queue. Set to 0 to disable.
const poisonOrderID = 999

func main() {
	cfg := messaging.ConsumerConfig{
		URL:                 getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		Exchange:            getEnv("PAYMENT_EVENTS_EXCHANGE", "payment.events"),
		Queue:               getEnv("PAYMENT_COMPLETED_QUEUE", "notification.payment.completed"),
		RoutingKey:          getEnv("PAYMENT_COMPLETED_ROUTING_KEY", "payment.completed"),
		DLXExchange:         getEnv("DLQ_EXCHANGE", "payment.events.dlx"),
		DLQ:                 getEnv("DLQ_QUEUE", "notification.payment.completed.dlq"),
		MaxDeliveryAttempts: getEnvInt("MAX_DELIVERY_ATTEMPTS", 3),
	}

	store := messaging.NewIdempotencyStore(getEnvInt("IDEMPOTENCY_CACHE_SIZE", 10_000))

	consumer, err := dialBrokerWithRetry(cfg, store, handle)
	if err != nil {
		log.Fatalf("consumer init: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("notification-service: starting")

	runErr := make(chan error, 1)
	go func() { runErr <- consumer.Run(ctx) }()

	select {
	case err := <-runErr:
		if err != nil {
			log.Printf("notification-service: consumer exited: %v", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		log.Println("notification-service: shutdown signal received, draining…")
		// consumer.Run watches the same ctx and will return when cancelled.
		select {
		case err := <-runErr:
			if err != nil {
				log.Printf("notification-service: drain error: %v", err)
			}
		case <-time.After(10 * time.Second):
			log.Println("notification-service: drain timed out")
		}
	}
	log.Println("notification-service: bye")
}

func handle(ctx context.Context, ev messaging.PaymentCompletedEvent) error {
	if ev.OrderID == poisonOrderID {
		// Demo path for the bonus DLQ requirement.
		return messaging.ErrPermanent
	}
	// Spec-mandated log line — this *is* the "send email" simulation.
	log.Printf("[Notification] Sent email to %s for Order #%d. Amount: $%.2f",
		ev.CustomerEmail, ev.OrderID, float64(ev.Amount)/100.0)
	return nil
}

func dialBrokerWithRetry(cfg messaging.ConsumerConfig, store *messaging.IdempotencyStore, h messaging.Handler) (*messaging.Consumer, error) {
	const attempts = 10
	var lastErr error
	for i := 0; i < attempts; i++ {
		c, err := messaging.NewConsumer(cfg, h, store)
		if err == nil {
			return c, nil
		}
		lastErr = err
		log.Printf("broker dial attempt %d/%d failed: %v", i+1, attempts, err)
		time.Sleep(time.Duration(1+i) * time.Second)
	}
	return nil, lastErr
}
