package grpc

import (
	"context"
	"log"
	"time"

	g "google.golang.org/grpc"
)

func UnaryLogger(ctx context.Context, req interface{}, info *g.UnaryServerInfo, handler g.UnaryHandler) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	log.Printf("[gRPC] method=%s duration=%s err=%v", info.FullMethod, time.Since(start), err)
	return resp, err
}
