package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"order-service/internal/domain"
)

type OrderHandler struct {
	orderUseCase OrderUseCase
}

type OrderUseCase interface {
	CreateOrder(ctx context.Context, customerID int64, itemName string, amount int64, idempotencyKey string) (*domain.Order, error)
	GetOrder(ctx context.Context, id int64) (*domain.Order, error)
	CancelOrder(ctx context.Context, id int64) error
}

func NewOrderHandler(orderUseCase OrderUseCase) *OrderHandler {
	return &OrderHandler{
		orderUseCase: orderUseCase,
	}
}

type CreateOrderRequest struct {
	CustomerID int64  `json:"customer_id"`
	ItemName   string `json:"item_name"`
	Amount     int64  `json:"amount"`
}

type OrderResponse struct {
	ID         int64  `json:"id"`
	CustomerID int64  `json:"customer_id"`
	ItemName   string `json:"item_name"`
	Amount     int64  `json:"amount"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func (h *OrderHandler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey != "" {
		ctx = context.WithValue(ctx, "idempotency_key", idempotencyKey)
	}

	var req CreateOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.CustomerID <= 0 {
		http.Error(w, `{"error": "customer_id must be positive"}`, http.StatusBadRequest)
		return
	}
	if req.ItemName == "" {
		http.Error(w, `{"error": "item_name is required"}`, http.StatusBadRequest)
		return
	}
	if req.Amount <= 0 {
		http.Error(w, `{"error": "amount must be positive"}`, http.StatusBadRequest)
		return
	}

	order, err := h.orderUseCase.CreateOrder(ctx, req.CustomerID, req.ItemName, req.Amount, idempotencyKey)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, `{"error": "payment service unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		http.Error(w, `{"error": "`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toOrderResponse(order))
}

func (h *OrderHandler) GetOrder(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Error(w, `{"error": "order id is required"}`, http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error": "invalid order id"}`, http.StatusBadRequest)
		return
	}

	order, err := h.orderUseCase.GetOrder(r.Context(), id)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, `{"error": "payment service unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		http.Error(w, `{"error": "`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	if order == nil {
		http.Error(w, `{"error": "order not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toOrderResponse(order))
}

func (h *OrderHandler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Error(w, `{"error": "order id is required"}`, http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error": "invalid order id"}`, http.StatusBadRequest)
		return
	}

	err = h.orderUseCase.CancelOrder(r.Context(), id)
	if err != nil {
		if err.Error() == "order not found" {
			http.Error(w, `{"error": "order not found"}`, http.StatusNotFound)
			return
		}
		if err.Error() == "can only cancel orders with Pending status" {
			http.Error(w, `{"error": "`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		http.Error(w, `{"error": "`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
}

func toOrderResponse(order *domain.Order) OrderResponse {
	return OrderResponse{
		ID:         order.ID,
		CustomerID: order.CustomerID,
		ItemName:   order.ItemName,
		Amount:     order.Amount,
		Status:     string(order.Status),
		CreatedAt:  order.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
