package main

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"order-service/internal/repository"
	grpctransport "order-service/internal/transport/grpc"
	httptransport "order-service/internal/transport/http"
	"order-service/internal/usecase"
	pb "github.com/danzhi16/contracts/order"
)

func getEnv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5436")
	dbUser := getEnv("DB_USER", "postgres")
	dbPass := getEnv("DB_PASSWORD", "1234")
	dbName := getEnv("DB_NAME", "order_db")
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", dbHost, dbPort, dbUser, dbPass, dbName)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("db ping: %v", err)
	}
	log.Println("Connected to database")

	orderRepo := repository.NewOrderRepository(db)

	paymentAddr := getEnv("PAYMENT_GRPC_ADDR", "localhost:50051")
	conn, err := grpc.Dial(paymentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial payment: %v", err)
	}
	defer conn.Close()
	paymentClient := repository.NewGrpcPaymentClient(conn)

	uc := usecase.NewOrderUseCase(orderRepo, paymentClient)
	httpHandler := httptransport.NewOrderHandler(uc)
	grpcHandler := grpctransport.NewOrderGrpcHandler(dsn)

	router := gin.Default()
	router.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	router.POST("/orders", func(c *gin.Context) { httpHandler.CreateOrder(c.Writer, c.Request) })
	router.GET("/orders", func(c *gin.Context) { httpHandler.GetOrder(c.Writer, c.Request) })

	grpcPort := getEnv("ORDER_GRPC_PORT", "50052")
	go func() {
		lis, err := net.Listen("tcp", ":"+grpcPort)
		if err != nil {
			log.Fatalf("listen: %v", err)
		}
		srv := grpc.NewServer()
		pb.RegisterOrderServiceServer(srv, grpcHandler)
		log.Printf("Order gRPC stream server :%s", grpcPort)
		if err := srv.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()

	restPort := getEnv("ORDER_REST_PORT", "8081")
	log.Printf("Order REST :%s", restPort)
	if err := router.Run(":" + restPort); err != nil {
		log.Fatalf("http: %v", err)
	}
}
