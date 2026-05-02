package repository

import (
	"database/sql"
	"errors"

	"payment-service/internal/domain"
)

type PaymentRepository struct {
	db *sql.DB
}

func NewPaymentRepository(db *sql.DB) *PaymentRepository {
	return &PaymentRepository{db: db}
}

func (r *PaymentRepository) Create(payment *domain.Payment) error {
	query := `
		INSERT INTO payments (order_id, transaction_id, amount, status, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id, created_at
	`
	err := r.db.QueryRow(
		query,
		payment.OrderID,
		payment.TransactionID,
		payment.Amount,
		payment.Status,
	).Scan(&payment.ID, &payment.CreatedAt)

	if err != nil {
		return err
	}

	return nil
}

func (r *PaymentRepository) GetByOrderID(orderID int64) (*domain.Payment, error) {
	query := `
		SELECT id, order_id, transaction_id, amount, status, created_at
		FROM payments
		WHERE order_id = $1
	`

	payment := &domain.Payment{}
	err := r.db.QueryRow(query, orderID).Scan(
		&payment.ID,
		&payment.OrderID,
		&payment.TransactionID,
		&payment.Amount,
		&payment.Status,
		&payment.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return payment, nil
}

func (r *PaymentRepository) GetByID(id int64) (*domain.Payment, error) {
	query := `
		SELECT id, order_id, transaction_id, amount, status, created_at
		FROM payments
		WHERE id = $1
	`

	payment := &domain.Payment{}
	err := r.db.QueryRow(query, id).Scan(
		&payment.ID,
		&payment.OrderID,
		&payment.TransactionID,
		&payment.Amount,
		&payment.Status,
		&payment.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return payment, nil
}

func (r *PaymentRepository) GetStats() (*domain.PaymentStats, error) {
	query := `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'Authorized'),
			COUNT(*) FILTER (WHERE status = 'Declined'),
			COALESCE(SUM(amount), 0)
		FROM payments
	`

	stats := &domain.PaymentStats{}
	err := r.db.QueryRow(query).Scan(
		&stats.TotalCount,
		&stats.AuthorizedCount,
		&stats.DeclinedCount,
		&stats.TotalAmount,
	)
	if err != nil {
		return nil, err
	}

	return stats, nil
}
