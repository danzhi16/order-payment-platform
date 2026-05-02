package domain

import "time"

type OrderStatus string

const (
	StatusPending   OrderStatus = "Pending"
	StatusPaid      OrderStatus = "Paid"
	StatusFailed    OrderStatus = "Failed"
	StatusCancelled OrderStatus = "Cancelled"
)

type Order struct {
	ID         int64       `db:"id"`
	CustomerID int64       `db:"customer_id"`
	ItemName   string      `db:"item_name"`
	Amount     int64       `db:"amount"`
	Status     OrderStatus `db:"status"`
	CreatedAt  time.Time   `db:"created_at"`
}

type OrderRepository interface {
	Create(order *Order) error
	GetByID(id int64) (*Order, error)
	UpdateStatus(id int64, status OrderStatus) error
	GetByStatus(status OrderStatus) ([]*Order, error)
}

type OrderService interface {
	CreateOrder(customerID int64, itemName string, amount int64) (*Order, error)
	GetOrder(id int64) (*Order, error)
	CancelOrder(id int64) error
}
