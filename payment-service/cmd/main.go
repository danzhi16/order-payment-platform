package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"

	"payment-service/internal/messaging"
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

	publisher, err := dialBrokerWithRetry(messaging.RabbitConfig{
		URL:         getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		Exchange:    getEnv("PAYMENT_EVENTS_EXCHANGE", "payment.events"),
		DLXExchange: getEnv("PAYMENT_EVENTS_DLX", "payment.events.dlx"),
	})
	if err != nil {
		log.Fatalf("rabbitmq publisher: %v", err)
	}
	defer publisher.Close()

	repo := repository.NewPaymentRepository(db)
	routingKey := getEnv("PAYMENT_COMPLETED_ROUTING_KEY", "payment.completed")
	uc := usecase.NewPaymentUseCase(repo, publisher, routingKey)

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	grpcPort := getEnv("PAYMENT_GRPC_PORT", "50051")
	grpcSrv := grpc.NewServer(grpc.UnaryInterceptor(grpctransport.UnaryLogger))
	pb.RegisterPaymentServiceServer(grpcSrv, grpctransport.NewPaymentGrpcHandler(uc))

	go func() {
		lis, err := net.Listen("tcp", ":"+grpcPort)
		if err != nil {
			log.Fatalf("listen: %v", err)
		}
		log.Printf("Payment gRPC server :%s", grpcPort)
		if err := grpcSrv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			log.Fatalf("grpc serve: %v", err)
		}
	}()

	httpHandler := httptransport.NewPaymentHandler(uc)
	router := gin.Default()
	router.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	router.POST("/payments", func(c *gin.Context) { httpHandler.ProcessPayment(c.Writer, c.Request) })
	router.GET("/payments", func(c *gin.Context) { httpHandler.GetPaymentByOrderID(c.Writer, c.Request) })

	restPort := getEnv("PAYMENT_REST_PORT", "8082")
	httpSrv := &http.Server{Addr: ":" + restPort, Handler: router}
	go func() {
		log.Printf("Payment REST :%s", restPort)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	<-rootCtx.Done()
	log.Println("payment-service: shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	grpcSrv.GracefulStop()
	if err := publisher.Close(); err != nil {
		log.Printf("publisher close: %v", err)
	}
	log.Println("payment-service: bye")
}

// dialBrokerWithRetry retries the RabbitMQ connection: in docker compose
// the broker may report "healthy" while it's still loading plugins, so the
// first dial can race.
func dialBrokerWithRetry(cfg messaging.RabbitConfig) (*messaging.RabbitPublisher, error) {
	const attempts = 10
	var lastErr error
	for i := 0; i < attempts; i++ {
		p, err := messaging.NewRabbitPublisher(cfg)
		if err == nil {
			return p, nil
		}
		lastErr = err
		log.Printf("broker dial attempt %d/%d failed: %v", i+1, attempts, err)
		time.Sleep(time.Duration(1+i) * time.Second)
	}
	return nil, lastErr
}
