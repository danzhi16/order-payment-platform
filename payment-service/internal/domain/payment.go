package domain

import "time"

type PaymentStatus string

const (
	StatusAuthorized PaymentStatus = "Authorized"
	StatusDeclined   PaymentStatus = "Declined"
)

type Payment struct {
	ID            int64         `db:"id"`
	OrderID       int64         `db:"order_id"`
	TransactionID string        `db:"transaction_id"`
	Amount        int64         `db:"amount"`
	Status        PaymentStatus `db:"status"`
	CreatedAt     time.Time     `db:"created_at"`
}

type PaymentStats struct {
	TotalCount      int64
	AuthorizedCount int64
	DeclinedCount   int64
	TotalAmount     int64
}

type PaymentRepository interface {
	Create(payment *Payment) error
	GetByOrderID(orderID int64) (*Payment, error)
	GetByID(id int64) (*Payment, error)
	GetStats() (*PaymentStats, error)
}

type PaymentService interface {
	ProcessPayment(orderID int64, amount int64) (*Payment, error)
	GetPaymentByOrderID(orderID int64) (*Payment, error)
}
