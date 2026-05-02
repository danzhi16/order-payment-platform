package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"payment-service/internal/domain"
)

const PaymentThreshold int64 = 100000

type PaymentUseCase struct {
	paymentRepo domain.PaymentRepository
}

func NewPaymentUseCase(paymentRepo domain.PaymentRepository) *PaymentUseCase {
	return &PaymentUseCase{
		paymentRepo: paymentRepo,
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

	return payment, nil
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
