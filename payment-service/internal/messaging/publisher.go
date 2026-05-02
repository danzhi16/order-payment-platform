package messaging

import "context"

// EventPublisher abstracts publishing domain events. Hides the broker
// (RabbitMQ today, anything else tomorrow) from the use-case layer.
type EventPublisher interface {
	Publish(ctx context.Context, routingKey string, event Event) error
	Close() error
}

// Event is a serialisable envelope so the consumer can dedupe by ID.
type Event struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	OrderID       int64  `json:"order_id"`
	Amount        int64  `json:"amount"`
	CustomerEmail string `json:"customer_email"`
	Status        string `json:"status"`
	OccurredAt    string `json:"occurred_at"`
}
