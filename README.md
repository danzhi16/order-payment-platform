# AP2 Assignment 2 — gRPC Migration (Order/Payment)

Layout mirrors the contract-first pattern:

```
order-payment-platform/
├── proto-repo/          # .proto sources (push to github.com/danzhi16/proto-repo)
├── contracts-repo/      # generated .pb.go (push to github.com/danzhi16/contracts)
├── order-service/       # gRPC client (Payment) + REST + gRPC stream server
├── payment-service/     # gRPC server + REST
├── init-db/             # SQL init for both databases
├── docker-compose.yml
└── demo.sh              # automated end-to-end test script
```

Business logic (domain/usecase/repository/http) is unchanged from Assignment 1.
Only the delivery layer (transport/grpc) is new.

## Run
```
cd proto-repo && ./generate.sh && cd ..
docker compose up --build
```

Or run the full automated demo:
```
./demo.sh
```

## Test
```
curl -X POST localhost:8081/orders -H "Content-Type: application/json" \
  -d "{\"customer_id\":1,\"item_name\":\"book\",\"amount\":5000}"

grpcurl -plaintext -d "{\"order_id\":1}" localhost:50052 order.OrderService/SubscribeToOrderUpdates
```

---

## Как работает система — пошаговый разбор

### Поток 1: Создание заказа (POST /orders)

1. Пользователь шлёт `POST /orders` на **Order Service** (REST, порт 8081). Это обычный HTTP с JSON.
2. Внутри Order Service срабатывает **Gin-хендлер**. Он парсит JSON и зовёт **Order UseCase**.
3. UseCase создаёт запись заказа в `order_db` со статусом **Pending**.
4. UseCase вызывает метод `ProcessPayment` у объекта `PaymentClient`. Для него это просто интерфейс — он не знает, что там gRPC.
5. На самом деле это **GrpcPaymentClient**, который делает gRPC-вызов на `payment-service:50051`.
6. На стороне Payment Service запрос сначала проходит через **Interceptor** (bonus — логирует метод и длительность).
7. Потом попадает в **Payment gRPC Handler**. Он проверяет входные данные и зовёт **Payment UseCase**.
8. Payment UseCase применяет бизнес-правило: если `amount > 100000` → **Declined**, иначе → **Authorized**. Сохраняет в `payment_db`.
9. Ответ возвращается обратно по цепочке: UseCase → Handler → gRPC → Order Service.
10. Order UseCase видит статус **Authorized** → обновляет заказ на **Paid**. Если **Declined** — ставит **Failed**.
11. Пользователю возвращается JSON с результатом.

```
Пользователь                Order Service (8081)                    Payment Service (50051)
    │                              │                                        │
    │  POST /orders (JSON)         │                                        │
    │─────────────────────────────>│                                        │
    │                              │  Gin Handler → Order UseCase           │
    │                              │  INSERT orders (Pending)               │
    │                              │                                        │
    │                              │  gRPC ProcessPayment ─────────────────>│
    │                              │                                        │  Interceptor (лог)
    │                              │                                        │  Handler → UseCase
    │                              │                                        │  amount > 100000?
    │                              │                                        │    да → Declined
    │                              │                                        │    нет → Authorized
    │                              │                                        │  INSERT payments
    │                              │  <── PaymentResponse (status) ─────────│
    │                              │                                        │
    │                              │  Authorized → Paid / Declined → Failed │
    │                              │  UPDATE orders                         │
    │  <── JSON {status: "Paid"}   │                                        │
    │                              │                                        │
```

### Поток 2: Получение статистики (GetPaymentStats)

1. Клиент (grpcurl или другой gRPC-клиент) шлёт вызов `GetPaymentStats` на **Payment Service** (gRPC, порт 50051) с пустым запросом `{}`.
2. Запрос проходит через **Interceptor** — он логирует метод `/payment.PaymentService/GetPaymentStats` и засекает время.
3. Попадает в **Payment gRPC Handler** (`GetPaymentStats`). Никакой валидации не нужно — запрос пустой.
4. Handler зовёт **Payment UseCase** (`GetPaymentStats`).
5. UseCase зовёт **PaymentRepository** (`GetStats`).
6. Repository выполняет один SQL-запрос к `payment_db`:
   - `COUNT(*)` — общее количество платежей
   - `COUNT(*) FILTER (WHERE status = 'Authorized')` — сколько одобрено
   - `COUNT(*) FILTER (WHERE status = 'Declined')` — сколько отклонено
   - `COALESCE(SUM(amount), 0)` — сумма всех платежей в центах
7. Результат идёт обратно: Repository → UseCase → Handler маппит domain-структуру в protobuf `PaymentStats`.
8. Interceptor логирует длительность вызова.
9. Клиент получает ответ с агрегированной статистикой.
    
```
gRPC-клиент                          Payment Service (50051)              PostgreSQL (payment_db)
    │                                        │                                    │
    │  GetPaymentStats({})                   │                                    │
    │───────────────────────────────────────>│                                    │
    │                                        │  Interceptor (лог)                 │
    │                                        │  Handler → UseCase → Repository    │
    │                                        │                                    │
    │                                        │  SELECT COUNT(*),                  │
    │                                        │    COUNT(*) FILTER (Authorized),   │
    │                                        │    COUNT(*) FILTER (Declined),     │
    │                                        │    COALESCE(SUM(amount), 0)        │
    │                                        │  FROM payments ──────────────────> │
    │                                        │  <── (2, 1, 1, 205000) ────────── │
    │                                        │                                    │
    │                                        │  Interceptor (длительность)        │
    │  <── PaymentStats {                    │                                    │
    │        totalCount: 2,                  │                                    │
    │        authorizedCount: 1,             │                                    │
    │        declinedCount: 1,               │                                    │
    │        totalAmount: 205000             │                                    │
    │      }                                 │                                    │
```

---

## Задача 1 — GetPaymentStats (unary RPC)

Добавлен новый unary RPC метод `GetPaymentStats` в `PaymentService`, который возвращает агрегированную статистику по всем платежам.

### Что изменено

#### 1. Proto-файл (`proto-repo/payment/payment.proto`)

Добавлены два новых message и новый RPC метод в сервис:

```protobuf
message GetPaymentStatsRequest {}

message PaymentStats {
  int64 total_count = 1;
  int64 authorized_count = 2;
  int64 declined_count = 3;
  int64 total_amount = 4;   // сумма всех amount в центах
}

service PaymentService {
  rpc ProcessPayment(PaymentRequest) returns (PaymentResponse);
  rpc GetPaymentStats(GetPaymentStatsRequest) returns (PaymentStats); // новый
}
```

После изменения `.proto` запущен `generate.sh` — обновлены `payment.pb.go` и `payment_grpc.pb.go` в `contracts-repo/`.

#### 2. Domain (`payment-service/internal/domain/payment.go`)

Добавлена структура `PaymentStats` и метод `GetStats()` в интерфейс репозитория:

```go
type PaymentStats struct {
    TotalCount      int64
    AuthorizedCount int64
    DeclinedCount   int64
    TotalAmount     int64
}

type PaymentRepository interface {
    Create(payment *Payment) error
    GetByOrderID(orderID int64) (*Payment, error)
    GetByID(id int64) (*Payment, error)
    GetStats() (*PaymentStats, error)  // новый
}
```

#### 3. Repository (`payment-service/internal/repository/payment_repository.go`)

Реализован метод `GetStats()` — один SQL-запрос с `COUNT` и `SUM`:

```go
func (r *PaymentRepository) GetStats() (*domain.PaymentStats, error) {
    query := `
        SELECT
            COUNT(*),
            COUNT(*) FILTER (WHERE status = 'Authorized'),
            COUNT(*) FILTER (WHERE status = 'Declined'),
            COALESCE(SUM(amount), 0)
        FROM payments
    `
    stats := &domain.PaymentStats{}
    err := r.db.QueryRow(query).Scan(
        &stats.TotalCount,
        &stats.AuthorizedCount,
        &stats.DeclinedCount,
        &stats.TotalAmount,
    )
    if err != nil {
        return nil, err
    }
    return stats, nil
}
```

- `COUNT(*) FILTER (WHERE ...)` — подсчитывает записи по каждому статусу за один проход
- `COALESCE(SUM(amount), 0)` — возвращает 0 если таблица пуста (вместо NULL)

#### 4. Use Case (`payment-service/internal/usecase/payment_usecase.go`)

Добавлен метод `GetPaymentStats`, который вызывает репозиторий:

```go
func (uc *PaymentUseCase) GetPaymentStats(ctx context.Context) (*domain.PaymentStats, error) {
    stats, err := uc.paymentRepo.GetStats()
    if err != nil {
        return nil, fmt.Errorf("failed to get payment stats: %w", err)
    }
    return stats, nil
}
```

#### 5. gRPC Handler (`payment-service/internal/transport/grpc/handler.go`)

Добавлен обработчик `GetPaymentStats`, который маппит domain-структуру в protobuf-ответ:

```go
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
```

#### 6. Demo-скрипт (`demo.sh`)

Добавлен **TEST 5** — вызов `GetPaymentStats` через `grpcurl`:

```bash
grpcurl -plaintext \
    -import-path ./proto-repo \
    -proto payment/payment.proto \
    -d '{}' \
    localhost:50051 payment.PaymentService/GetPaymentStats
```

Тест запускается после создания двух заказов (один Authorized, один Declined), поэтому ожидаемый ответ:

```json
{
  "totalCount": "2",
  "authorizedCount": "1",
  "declinedCount": "1",
  "totalAmount": "205000"
}
```

### Архитектура вызова

```
grpcurl → gRPC Handler (GetPaymentStats)
            → UseCase (GetPaymentStats)
              → Repository (GetStats) → PostgreSQL
```

### Ручной вызов

```bash
grpcurl -plaintext -d '{}' localhost:50051 payment.PaymentService/GetPaymentStats
```

---

# Assignment 3 — Event-Driven Architecture (RabbitMQ)

Adds a `notification-service` consumer fed by `payment-service` over RabbitMQ.
The synchronous gRPC link between Order and Payment is unchanged; the new flow
is purely additive.

## Architecture

```
   ┌──────────────┐  POST /orders   ┌────────────────┐   gRPC    ┌──────────────────┐
   │  Client      │────────────────▶│ Order Service  │──────────▶│ Payment Service  │
   └──────────────┘                 └────────────────┘           │ (Producer)       │
                                                                 └────────┬─────────┘
                                                                          │ publish (durable, persistent,
                                                                          │ publisher-confirm, MessageId=uuid)
                                                                          ▼
                                                       ┌─────────────────────────────────┐
                                                       │ RabbitMQ                        │
                                                       │  exchange: payment.events       │  topic
                                                       │  └─ rk: payment.completed       │
                                                       │     └─ queue: notification.     │  durable
                                                       │        payment.completed        │  prefetch=1
                                                       │           │ x-dead-letter-exch  │
                                                       │           ▼                     │
                                                       │  exchange: payment.events.dlx   │
                                                       │  └─ queue: notification.        │
                                                       │     payment.completed.dlq       │
                                                       └────────────────┬────────────────┘
                                                                        │ consume (auto-ack=false)
                                                                        ▼
                                                              ┌────────────────────┐
                                                              │ Notification       │
                                                              │ Service (Consumer) │
                                                              │  - idempotency map │
                                                              │  - manual ack      │
                                                              │  - bounded retries │
                                                              └────────────────────┘
```

## Event flow

1. Client → `POST /orders` → Order Service writes to `order_db`.
2. Order Service → gRPC `ProcessPayment` → Payment Service.
3. Payment Service writes the payment row, then calls `EventPublisher.Publish`
   on the `payment.events` exchange with routing key `payment.completed`.
4. RabbitMQ routes the message to the durable `notification.payment.completed`
   queue.
5. Notification Service consumes the message, dedupes by `MessageId`, prints
   `[Notification] Sent email to … for Order #… Amount: $…`, and **then**
   acknowledges.

Event payload (JSON):

```json
{
  "id":             "<uuid>",
  "type":           "payment.completed",
  "order_id":       42,
  "amount":         5000,
  "customer_email": "customer-42@example.com",
  "status":         "Authorized",
  "occurred_at":    "2026-05-02T18:00:00Z"
}
```

> **Note on `customer_email`.** The existing gRPC contract carries only
> `order_id` and `amount`. To avoid a cross-repo proto change for an academic
> deliverable, the payment service synthesises `customer-{order_id}@example.com`.
> Threading the real email end-to-end is straightforward (extend the proto +
> regenerate) and not architecturally interesting for this assignment.

## Idempotency strategy

The producer stamps every published message with a unique `MessageId` (a 16-byte
random hex string, generated *once* per business event). The consumer keeps a
thread-safe FIFO-evicted in-memory set of processed ids
([`notification-service/internal/messaging/idempotency.go`](notification-service/internal/messaging/idempotency.go)).

Per-delivery flow:

1. `MarkIfNew(MessageId)` → if **false**, the message has already been processed.
   The consumer Acks it (so the broker stops redelivering) and skips the work.
2. If **true**, the consumer runs the handler.
3. If the handler **fails**, the id is `Forget`-ten so the retry actually
   reprocesses (otherwise the dedup store would permanently swallow it).

This is "at-least-once delivery + idempotent consumer" — the standard pattern
when you can't get exactly-once from the broker. A production deployment would
swap the in-memory set for Redis or a `processed_messages(message_id PK)` table.

## ACK logic (manual, never auto-ack)

The consumer is created with `auto-ack = false`
([`notification-service/internal/messaging/consumer.go`](notification-service/internal/messaging/consumer.go)).
Outcomes per delivery:

| Outcome                            | Action                          | Result                              |
|------------------------------------|---------------------------------|-------------------------------------|
| Handler success                    | `d.Ack(false)`                  | Broker drops the message.           |
| Duplicate (seen MessageId)         | `d.Ack(false)`                  | Broker drops; no duplicate work.    |
| Malformed JSON                     | `d.Nack(false, false)`          | Permanent — straight to DLQ.        |
| Permanent error (`ErrPermanent`)   | `d.Nack(false, false)`          | Straight to DLQ. (Demo: order_id 999.) |
| Transient error, attempts < N      | `d.Nack(false, true)`           | Requeued for redelivery.            |
| Transient error, attempts = N      | `d.Nack(false, false)`          | Goes to DLQ.                        |

Combined with the producer's **persistent delivery mode** and the **durable**
queue, this gives at-least-once semantics: a consumer crash mid-processing
leaves the message un-acked, so the broker redelivers it on reconnect.

## Dead Letter Queue (bonus)

- Exchange `payment.events.dlx` (topic, durable).
- Queue `notification.payment.completed.dlq` bound to it on routing key
  `payment.completed`.
- The main queue is declared with `x-dead-letter-exchange` =
  `payment.events.dlx`, so any `Nack(false, false)` flows there.

Demo: send a payment for `order_id == 999`. The handler returns `ErrPermanent`
on every delivery, so after the first attempt the message lands in the DLQ.
You can inspect it in the management UI at <http://localhost:15672> (guest /
guest).

## Run

```bash
docker compose up --build
```

Watch the logs of `notification-service` in another terminal:

```bash
docker compose logs -f notification-service
```

## End-to-end test

```bash
# happy path — should produce a [Notification] log line
curl -X POST localhost:8081/orders -H "Content-Type: application/json" \
  -d '{"customer_id":1,"item_name":"book","amount":5000}'

# idempotency — same Idempotency-Key returns the same order, only one event
curl -X POST localhost:8081/orders \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: abc-123" \
  -d '{"customer_id":1,"item_name":"book","amount":5000}'

# DLQ demo — order_id 999 will permanently fail and end up on the DLQ
curl -X POST localhost:8082/payments \
  -H "Content-Type: application/json" \
  -d '{"order_id":999,"amount":1000}'
```

Then in the RabbitMQ UI (`http://localhost:15672`, guest/guest) check the
`notification.payment.completed.dlq` queue — depth should be 1.

## Graceful shutdown

Both `payment-service` and `notification-service` install a
`signal.NotifyContext(SIGINT, SIGTERM)` and use that context to stop accepting
new work, drain in-flight work, and close their AMQP channels and connections
cleanly. Try `docker compose stop notification-service` and watch the
`shutdown signal received, draining…` line in the logs.

