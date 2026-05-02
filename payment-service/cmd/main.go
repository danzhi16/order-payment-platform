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

	"payment-service/internal/repository"
	grpctransport "payment-service/internal/transport/grpc"
	httptransport "payment-service/internal/transport/http"
	"payment-service/internal/usecase"
	pb "github.com/danzhi16/contracts/payment"
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
	dbName := getEnv("DB_NAME", "payment_db")
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

	repo := repository.NewPaymentRepository(db)
	uc := usecase.NewPaymentUseCase(repo)

	grpcPort := getEnv("PAYMENT_GRPC_PORT", "50051")
	go func() {
		lis, err := net.Listen("tcp", ":"+grpcPort)
		if err != nil {
			log.Fatalf("listen: %v", err)
		}
		srv := grpc.NewServer(grpc.UnaryInterceptor(grpctransport.UnaryLogger))
		pb.RegisterPaymentServiceServer(srv, grpctransport.NewPaymentGrpcHandler(uc))
		log.Printf("Payment gRPC server :%s", grpcPort)
		if err := srv.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()

	httpHandler := httptransport.NewPaymentHandler(uc)
	router := gin.Default()
	router.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	router.POST("/payments", func(c *gin.Context) { httpHandler.ProcessPayment(c.Writer, c.Request) })
	router.GET("/payments", func(c *gin.Context) { httpHandler.GetPaymentByOrderID(c.Writer, c.Request) })

	restPort := getEnv("PAYMENT_REST_PORT", "8082")
	log.Printf("Payment REST :%s", restPort)
	if err := router.Run(":" + restPort); err != nil {
		log.Fatalf("http: %v", err)
	}
}
