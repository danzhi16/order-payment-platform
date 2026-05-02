package grpc

import (
	"database/sql"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"order-service/internal/domain"
	"order-service/internal/repository"

	pb "github.com/danzhi16/contracts/order"
)

type OrderGrpcHandler struct {
	pb.UnimplementedOrderServiceServer
	dsn string
}

func NewOrderGrpcHandler(dsn string) *OrderGrpcHandler {
	return &OrderGrpcHandler{dsn: dsn}
}

func (h *OrderGrpcHandler) SubscribeToOrderUpdates(req *pb.OrderRequest, stream pb.OrderService_SubscribeToOrderUpdatesServer) error {
	if req.OrderId <= 0 {
		return status.Error(codes.InvalidArgument, "order_id must be > 0")
	}
	db, err := sql.Open("postgres", h.dsn)
	if err != nil {
		return status.Errorf(codes.Internal, "db open: %v", err)
	}
	defer db.Close()
	repo := repository.NewOrderRepository(db)

	var last domain.OrderStatus = ""
	isFirstRead := true
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stream.Context().Done():
			log.Printf("[stream] client disconnected order_id=%d", req.OrderId)
			return nil
		case <-t.C:
			o, err := repo.GetByID(req.OrderId)
			if err != nil {
				return status.Errorf(codes.Internal, "db: %v", err)
			}
			if o == nil {
				return status.Error(codes.NotFound, "order not found")
			}
			if o.Status == last {
				continue
			}
			last = o.Status
			if err := stream.Send(&pb.OrderStatusUpdate{OrderId: o.ID, Status: string(o.Status)}); err != nil {
				return err
			}
			log.Printf("[stream] sent order_id=%d status=%s", o.ID, o.Status)

			// Закрываем поток только если статус СМЕНИЛСЯ на терминальный во время подписки.
			// Если клиент подключился к уже завершённому заказу — отправляем снимок и ждём отключения клиента.
			if !isFirstRead && (o.Status == domain.StatusPaid || o.Status == domain.StatusFailed || o.Status == domain.StatusCancelled) {
				return nil
			}
			isFirstRead = false
		}
	}
}
