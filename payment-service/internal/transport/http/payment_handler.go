package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"payment-service/internal/domain"
)

type PaymentHandler struct {
	paymentUseCase PaymentUseCase
}

type PaymentUseCase interface {
	ProcessPayment(ctx context.Context, orderID int64, amount int64) (*domain.Payment, error)
	GetPaymentByOrderID(ctx context.Context, orderID int64) (*domain.Payment, error)
	GetPaymentByID(ctx context.Context, id int64) (*domain.Payment, error)
}

func NewPaymentHandler(paymentUseCase PaymentUseCase) *PaymentHandler {
	return &PaymentHandler{
		paymentUseCase: paymentUseCase,
	}
}

type PaymentRequest struct {
	OrderID int64 `json:"order_id"`
	Amount  int64 `json:"amount"`
}

type PaymentResponse struct {
	PaymentID     int64  `json:"payment_id"`
	OrderID       int64  `json:"order_id"`
	TransactionID string `json:"transaction_id"`
	Amount        int64  `json:"amount"`
	Status        string `json:"status"`
	CreatedAt     string `json:"created_at"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func (h *PaymentHandler) ProcessPayment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	orderIDStr := r.Header.Get("X-Order-ID")
	amountStr := r.Header.Get("X-Amount")

	var orderID, amount int64
	var err error

	if orderIDStr != "" && amountStr != "" {
		orderID, err = strconv.ParseInt(orderIDStr, 10, 64)
		if err != nil {
			http.Error(w, `{"error": "invalid X-Order-ID header"}`, http.StatusBadRequest)
			return
		}

		amount, err = strconv.ParseInt(amountStr, 10, 64)
		if err != nil {
			http.Error(w, `{"error": "invalid X-Amount header"}`, http.StatusBadRequest)
			return
		}
	} else {
		var req PaymentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error": "invalid request body"}`, http.StatusBadRequest)
			return
		}

		orderID = req.OrderID
		amount = req.Amount
	}

	if orderID <= 0 {
		http.Error(w, `{"error": "order_id must be positive"}`, http.StatusBadRequest)
		return
	}
	if amount <= 0 {
		http.Error(w, `{"error": "amount must be positive"}`, http.StatusBadRequest)
		return
	}

	payment, err := h.paymentUseCase.ProcessPayment(ctx, orderID, amount)
	if err != nil {
		http.Error(w, `{"error": "`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toPaymentResponse(payment))
}

func (h *PaymentHandler) GetPaymentByOrderID(w http.ResponseWriter, r *http.Request) {
	orderIDStr := r.URL.Query().Get("order_id")
	if orderIDStr == "" {
		http.Error(w, `{"error": "order_id is required"}`, http.StatusBadRequest)
		return
	}

	orderID, err := strconv.ParseInt(orderIDStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error": "invalid order_id"}`, http.StatusBadRequest)
		return
	}

	payment, err := h.paymentUseCase.GetPaymentByOrderID(r.Context(), orderID)
	if err != nil {
		if err.Error() == "payment not found" {
			http.Error(w, `{"error": "payment not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error": "`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toPaymentResponse(payment))
}

func (h *PaymentHandler) GetPaymentByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Error(w, `{"error": "payment id is required"}`, http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error": "invalid payment id"}`, http.StatusBadRequest)
		return
	}

	payment, err := h.paymentUseCase.GetPaymentByID(r.Context(), id)
	if err != nil {
		if err.Error() == "payment not found" {
			http.Error(w, `{"error": "payment not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error": "`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toPaymentResponse(payment))
}

func toPaymentResponse(payment *domain.Payment) PaymentResponse {
	return PaymentResponse{
		PaymentID:     payment.ID,
		OrderID:       payment.OrderID,
		TransactionID: payment.TransactionID,
		Amount:        payment.Amount,
		Status:        string(payment.Status),
		CreatedAt:     payment.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
