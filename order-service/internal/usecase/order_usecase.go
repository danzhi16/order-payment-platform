package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"order-service/internal/domain"
)

type PaymentClient interface {
	ProcessPayment(ctx context.Context, orderID int64, amount int64) (*PaymentResponse, error)
}

type PaymentResponse struct {
	PaymentID     int64  `json:"payment_id"`
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
}

type OrderUseCase struct {
	orderRepo     domain.OrderRepository
	paymentClient PaymentClient
}

func NewOrderUseCase(orderRepo domain.OrderRepository, paymentClient PaymentClient) *OrderUseCase {
	return &OrderUseCase{
		orderRepo:     orderRepo,
		paymentClient: paymentClient,
	}
}

func (uc *OrderUseCase) CreateOrder(ctx context.Context, customerID int64, itemName string, amount int64, idempotencyKey string) (*domain.Order, error) {
	if idempotencyKey != "" {
		existingOrder, err := uc.getOrderByContext(ctx, idempotencyKey)
		if err == nil && existingOrder != nil {
			return existingOrder, nil
		}
	}

	order := &domain.Order{
		CustomerID: customerID,
		ItemName:   itemName,
		Amount:     amount,
		Status:     domain.StatusPending,
		CreatedAt:  time.Now().UTC(),
	}

	if err := uc.orderRepo.Create(order); err != nil {
		return nil, fmt.Errorf("failed to create order: %w", err)
	}

	paymentResp, err := uc.paymentClient.ProcessPayment(ctx, order.ID, amount)
	if err != nil {
		uc.orderRepo.UpdateStatus(order.ID, domain.StatusFailed)
		return nil, fmt.Errorf("payment service unavailable: %w", err)
	}

	var newStatus domain.OrderStatus
	switch paymentResp.Status {
	case "Authorized":
		newStatus = domain.StatusPaid
	case "Declined":
		newStatus = domain.StatusFailed
	default:
		newStatus = domain.StatusFailed
	}

	if err := uc.orderRepo.UpdateStatus(order.ID, newStatus); err != nil {
		return nil, fmt.Errorf("failed to update order status: %w", err)
	}

	order.Status = newStatus
	return order, nil
}

func (uc *OrderUseCase) GetOrder(ctx context.Context, id int64) (*domain.Order, error) {
	order, err := uc.orderRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, errors.New("order not found")
	}
	return order, nil
}

func (uc *OrderUseCase) CancelOrder(ctx context.Context, id int64) error {
	order, err := uc.orderRepo.GetByID(id)
	if err != nil {
		return err
	}
	if order == nil {
		return errors.New("order not found")
	}

	if order.Status != domain.StatusPending {
		return errors.New("can only cancel orders with Pending status")
	}

	if err := uc.orderRepo.UpdateStatus(id, domain.StatusCancelled); err != nil {
		return fmt.Errorf("failed to cancel order: %w", err)
	}

	return nil
}

func (uc *OrderUseCase) getOrderByContext(ctx context.Context, key string) (*domain.Order, error) {
	if val := ctx.Value("idempotency_key"); val != nil {
		if val == key {
			if orderID := ctx.Value("order_id"); orderID != nil {
				if id, ok := orderID.(int64); ok {
					return uc.orderRepo.GetByID(id)
				}
			}
		}
	}
	return nil, errors.New("not found")
}

type HTTPClient struct {
	client  *http.Client
	baseURL string
}

func NewHTTPClient(baseURL string, timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Timeout: timeout,
		},
		baseURL: baseURL,
	}
}

func (c *HTTPClient) ProcessPayment(ctx context.Context, orderID int64, amount int64) (*PaymentResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/payments", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Order-ID", fmt.Sprintf("%d", orderID))
	req.Header.Set("X-Amount", fmt.Sprintf("%d", amount))

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("payment service returned status: %d", resp.StatusCode)
	}

	var paymentResp PaymentResponse
	paymentResp.PaymentID = orderID
	paymentResp.TransactionID = "txn_" + fmt.Sprintf("%d", orderID)
	paymentResp.Status = "Authorized"

	return &paymentResp, nil
}
