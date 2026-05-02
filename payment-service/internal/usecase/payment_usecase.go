package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/messaging"
)

const PaymentThreshold int64 = 100000

type PaymentUseCase struct {
	paymentRepo domain.PaymentRepository
	publisher   messaging.EventPublisher
	routingKey  string
}

func NewPaymentUseCase(paymentRepo domain.PaymentRepository, publisher messaging.EventPublisher, routingKey string) *PaymentUseCase {
	return &PaymentUseCase{
		paymentRepo: paymentRepo,
		publisher:   publisher,
		routingKey:  routingKey,
	}
}

func (uc *PaymentUseCase) ProcessPayment(ctx context.Context, orderID int64, amount int64) (*domain.Payment, error) {
	existingPayment, err := uc.paymentRepo.GetByOrderID(orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing payment: %w", err)
	}

	if existingPayment != nil {
		return existingPayment, nil
	}

	var status domain.PaymentStatus
	var transactionID string

	if amount > PaymentThreshold {
		status = domain.StatusDeclined
		transactionID = ""
	} else {
		status = domain.StatusAuthorized
		transactionID = generateTransactionID(orderID)
	}

	payment := &domain.Payment{
		OrderID:       orderID,
		TransactionID: transactionID,
		Amount:        amount,
		Status:        status,
		CreatedAt:     time.Now().UTC(),
	}

	if err := uc.paymentRepo.Create(payment); err != nil {
		return nil, fmt.Errorf("failed to create payment: %w", err)
	}

	uc.publishPaymentCompleted(ctx, payment)

	return payment, nil
}

// publishPaymentCompleted emits an event after the DB commit succeeded.
// A publish failure does not fail the payment — the DB row is the source of
// truth. We just log; in production you'd persist to an outbox and retry.
func (uc *PaymentUseCase) publishPaymentCompleted(ctx context.Context, p *domain.Payment) {
	if uc.publisher == nil {
		return
	}
	event := messaging.Event{
		ID:            newEventID(),
		Type:          "payment.completed",
		OrderID:       p.OrderID,
		Amount:        p.Amount,
		CustomerEmail: deriveCustomerEmail(p.OrderID),
		Status:        string(p.Status),
		OccurredAt:    p.CreatedAt.Format(time.RFC3339),
	}
	if err := uc.publisher.Publish(ctx, uc.routingKey, event); err != nil {
		log.Printf("publish %s failed for order=%d: %v", uc.routingKey, p.OrderID, err)
		return
	}
	log.Printf("published %s id=%s order=%d", uc.routingKey, event.ID, p.OrderID)
}

// deriveCustomerEmail produces a synthetic address.
// The current gRPC contract doesn't carry customer email; rather than do a
// cross-repo proto change for an academic deliverable, we synthesise one and
// document this in the README.
func deriveCustomerEmail(orderID int64) string {
	return fmt.Sprintf("customer-%d@example.com", orderID)
}

func newEventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (uc *PaymentUseCase) GetPaymentByOrderID(ctx context.Context, orderID int64) (*domain.Payment, error) {
	payment, err := uc.paymentRepo.GetByOrderID(orderID)
	if err != nil {
		return nil, err
	}
	if payment == nil {
		return nil, errors.New("payment not found")
	}
	return payment, nil
}

func (uc *PaymentUseCase) GetPaymentByID(ctx context.Context, id int64) (*domain.Payment, error) {
	payment, err := uc.paymentRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if payment == nil {
		return nil, errors.New("payment not found")
	}
	return payment, nil
}

func (uc *PaymentUseCase) GetPaymentStats(ctx context.Context) (*domain.PaymentStats, error) {
	stats, err := uc.paymentRepo.GetStats()
	if err != nil {
		return nil, fmt.Errorf("failed to get payment stats: %w", err)
	}
	return stats, nil
}

func generateTransactionID(orderID int64) string {
	return fmt.Sprintf("txn_%d_%d", orderID, time.Now().UnixNano())
}
