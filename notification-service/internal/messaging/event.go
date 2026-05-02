package messaging

// PaymentCompletedEvent mirrors the producer's envelope so we can decode
// without sharing a Go module. The producer marshals the same shape.
type PaymentCompletedEvent struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	OrderID       int64  `json:"order_id"`
	Amount        int64  `json:"amount"`
	CustomerEmail string `json:"customer_email"`
	Status        string `json:"status"`
	OccurredAt    string `json:"occurred_at"`
}
