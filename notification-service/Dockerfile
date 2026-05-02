FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY notification-service /app
RUN go mod tidy && go build -o /app/server ./cmd

FROM alpine:latest
COPY --from=builder /app/server /server
CMD ["/server"]
