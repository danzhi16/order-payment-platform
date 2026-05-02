package repository

import (
	"context"

	"google.golang.org/grpc"

	"order-service/internal/usecase"
	pb "github.com/danzhi16/contracts/payment"
)

type GrpcPaymentClient struct {
	c pb.PaymentServiceClient
}

func NewGrpcPaymentClient(conn *grpc.ClientConn) *GrpcPaymentClient {
	return &GrpcPaymentClient{c: pb.NewPaymentServiceClient(conn)}
}

func (g *GrpcPaymentClient) ProcessPayment(ctx context.Context, orderID, amount int64) (*usecase.PaymentResponse, error) {
	resp, err := g.c.ProcessPayment(ctx, &pb.PaymentRequest{OrderId: orderID, Amount: amount})
	if err != nil {
		return nil, err
	}
	return &usecase.PaymentResponse{
		PaymentID: resp.Id, TransactionID: resp.TransactionId, Status: resp.Status,
	}, nil
}
