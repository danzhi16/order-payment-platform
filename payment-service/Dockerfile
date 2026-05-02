FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY contracts-repo /contracts-repo
COPY payment-service /app
RUN go mod tidy && go build -o /app/server ./cmd

FROM alpine:latest
COPY --from=builder /app/server /server
EXPOSE 8081 8082 50051 50052
CMD ["/server"]
