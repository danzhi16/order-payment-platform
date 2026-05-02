package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"payment-service/internal/usecase"
	pb "github.com/danzhi16/contracts/payment"
)

type PaymentGrpcHandler struct {
	pb.UnimplementedPaymentServiceServer
	uc *usecase.PaymentUseCase
}

func NewPaymentGrpcHandler(uc *usecase.PaymentUseCase) *PaymentGrpcHandler {
	return &PaymentGrpcHandler{uc: uc}
}

func (h *PaymentGrpcHandler) ProcessPayment(ctx context.Context, req *pb.PaymentRequest) (*pb.PaymentResponse, error) {
	if req.OrderId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "order_id must be > 0")
	}
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}
	p, err := h.uc.ProcessPayment(ctx, req.OrderId, req.Amount)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "process payment: %v", err)
	}
	return &pb.PaymentResponse{
		Id: p.ID, OrderId: p.OrderID, TransactionId: p.TransactionID,
		Amount: p.Amount, Status: string(p.Status),
		CreatedAt: timestamppb.New(p.CreatedAt),
	}, nil
}

func (h *PaymentGrpcHandler) GetPaymentStats(ctx context.Context, req *pb.GetPaymentStatsRequest) (*pb.PaymentStats, error) {
	stats, err := h.uc.GetPaymentStats(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get payment stats: %v", err)
	}
	return &pb.PaymentStats{
		TotalCount:      stats.TotalCount,
		AuthorizedCount: stats.AuthorizedCount,
		DeclinedCount:   stats.DeclinedCount,
		TotalAmount:     stats.TotalAmount,
	}, nil
}
