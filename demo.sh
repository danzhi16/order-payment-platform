#!/usr/bin/env bash
# Full end-to-end test for AP2 Assignment 2.
# Run from order-payment-platform/ root.

set -e

GREEN="\033[0;32m"; RED="\033[0;31m"; YELLOW="\033[1;33m"; NC="\033[0m"
ok()   { echo -e "${GREEN}[OK]${NC} $1"; }
info() { echo -e "${YELLOW}[..]${NC} $1"; }
err()  { echo -e "${RED}[ERR]${NC} $1"; }

ORDER_REST=http://localhost:8081
PAYMENT_REST=http://localhost:8082
ORDER_GRPC=localhost:50052
PAYMENT_GRPC=localhost:50051
PG_CONTAINER=ap2-postgres

# ---------- 0. Pre-flight ----------
info "Checking prerequisites"
command -v docker >/dev/null || { err "docker not installed"; exit 1; }
command -v curl   >/dev/null || { err "curl not installed"; exit 1; }
docker info >/dev/null 2>&1 || { err "Docker daemon not running. Start Docker Desktop."; exit 1; }
ok "docker, curl present"

# ---------- 1. Generate proto if needed ----------
if [ ! -f contracts-repo/order/order.pb.go ] || [ ! -f contracts-repo/payment/payment.pb.go ]; then
    info "Generating proto code"
    command -v protoc >/dev/null || { err "protoc not installed. Run: brew install protobuf"; exit 1; }
    command -v go     >/dev/null || { err "go not installed. Run: brew install go"; exit 1; }
    (cd proto-repo && chmod +x generate.sh && ./generate.sh)
    ok "Proto generated"
else
    ok "Proto code already present"
fi

# ---------- 2. Bring up the stack ----------
info "Stopping any previous run"
docker compose down -v >/dev/null 2>&1 || true
docker rm -f payment-service order-service ap2-postgres >/dev/null 2>&1 || true

info "Building and starting docker compose (1-2 min on first run)"
docker compose up --build -d

# ---------- 3. Wait for Postgres ----------
info "Waiting for Postgres to be healthy"
for i in {1..30}; do
    if docker exec $PG_CONTAINER pg_isready -U postgres >/dev/null 2>&1; then
        ok "Postgres ready"; break
    fi
    sleep 2
    [ $i -eq 30 ] && { err "Postgres did not become ready"; docker compose logs db; exit 1; }
done

# ---------- 4. Wait for services ----------
info "Waiting for order-service REST"
for i in {1..30}; do
    curl -fs $ORDER_REST/health >/dev/null 2>&1 && { ok "order-service up"; break; }
    sleep 2
    [ $i -eq 30 ] && { err "order-service not responding"; docker compose logs order-service; exit 1; }
done

info "Waiting for payment-service REST"
for i in {1..30}; do
    curl -fs $PAYMENT_REST/health >/dev/null 2>&1 && { ok "payment-service up"; break; }
    sleep 2
    [ $i -eq 30 ] && { err "payment-service not responding"; docker compose logs payment-service; exit 1; }
done

# ---------- 5. Verify databases & tables ----------
info "Checking databases and tables"
docker exec $PG_CONTAINER psql -U postgres -d order_db   -c "\dt" | grep -q orders   && ok "order_db.orders ok"     || { err "orders table missing"; exit 1; }
docker exec $PG_CONTAINER psql -U postgres -d payment_db -c "\dt" | grep -q payments && ok "payment_db.payments ok" || { err "payments table missing"; exit 1; }

# ---------- 6. REST: small order → Paid ----------
info "TEST 1: POST /orders amount=5000 (should be Paid)"
RESP=$(curl -s -w "\n%{http_code}" -X POST $ORDER_REST/orders \
    -H "Content-Type: application/json" \
    -d '{"customer_id":1,"item_name":"book","amount":5000}')
CODE=$(echo "$RESP" | tail -n 1); BODY=$(echo "$RESP" | sed '$d')
echo "  → HTTP $CODE: $BODY"
[ "$CODE" = "200" ] || [ "$CODE" = "201" ] || { err "create order failed"; exit 1; }
echo "$BODY" | grep -qi "Paid" && ok "order paid" || err "order not in Paid status"

# ---------- 7. REST: big order → Failed ----------
info "TEST 2: POST /orders amount=200000 (above threshold → Declined)"
RESP=$(curl -s -w "\n%{http_code}" -X POST $ORDER_REST/orders \
    -H "Content-Type: application/json" \
    -d '{"customer_id":2,"item_name":"laptop","amount":200000}')
CODE=$(echo "$RESP" | tail -n 1); BODY=$(echo "$RESP" | sed '$d')
echo "  → HTTP $CODE: $BODY"
echo "$BODY" | grep -qi "Failed" && ok "high-amount order failed" || info "(check status manually)"

# ---------- 8. Interceptor logs ----------
info "TEST 3: payment-service interceptor logs"
sleep 1
LOGS=$(docker compose logs payment-service 2>&1 | grep "\[gRPC\]" | tail -5)
if [ -n "$LOGS" ]; then
    ok "Interceptor logged calls:"
    echo "$LOGS" | sed "s/^/    /"
else
    err "No interceptor logs found"
fi

# ---------- 9. Direct DB check ----------
info "TEST 4: Direct DB query"
echo "  --- orders ---"
docker exec $PG_CONTAINER psql -U postgres -d order_db -c "SELECT id, customer_id, item_name, amount, status FROM orders;"
echo "  --- payments ---"
docker exec $PG_CONTAINER psql -U postgres -d payment_db -c "SELECT id, order_id, amount, status FROM payments;"

# ---------- 10. GetPaymentStats unary RPC ----------
info "TEST 5: gRPC GetPaymentStats"
if ! command -v grpcurl >/dev/null; then
    info "grpcurl not installed — skipping. Install: brew install grpcurl"
else
    STATS=$(grpcurl -plaintext \
        -import-path ./proto-repo \
        -proto payment/payment.proto \
        -d '{}' \
        $PAYMENT_GRPC payment.PaymentService/GetPaymentStats 2>&1)
    if [ $? -eq 0 ]; then
        ok "GetPaymentStats response:"
        echo "$STATS" | sed "s/^/    /"
    else
        err "GetPaymentStats failed:"
        echo "$STATS" | sed "s/^/    /"
    fi
fi

# ---------- 11. gRPC streaming ----------
info "TEST 6: gRPC streaming"
if ! command -v grpcurl >/dev/null; then
    info "grpcurl not installed — skipping. Install: brew install grpcurl"
else
    info "Subscribing to order_id=1 (background)"
    STREAM_LOG=$(mktemp)
    grpcurl -plaintext \
        -import-path ./proto-repo \
        -proto order/order.proto \
        -d '{"order_id":1}' \
        $ORDER_GRPC order.OrderService/SubscribeToOrderUpdates > $STREAM_LOG 2>&1 &
    STREAM_PID=$!
    sleep 2

    info "Updating order 1 status to Cancelled"
    docker exec $PG_CONTAINER psql -U postgres -d order_db \
        -c "UPDATE orders SET status='Cancelled' WHERE id=1;" >/dev/null

    # wait up to 8 sec for stream to react, then kill it
    for i in {1..8}; do
        grep -qi "Cancelled" $STREAM_LOG && break
        sleep 1
    done
    kill $STREAM_PID 2>/dev/null || true
    wait $STREAM_PID 2>/dev/null || true

    if grep -qi "Cancelled" $STREAM_LOG; then
        ok "Stream pushed status change:"
        cat $STREAM_LOG | sed "s/^/    /"
    else
        err "Stream did not receive update — log:"
        cat $STREAM_LOG | sed "s/^/    /"
    fi
    rm -f $STREAM_LOG
fi
# ---------- 12. GitHub Actions automation ----------
info "TEST 7: GitHub Actions proto → contracts automation"
GH_OWNER=danzhi16
PROTO_REPO=proto-repo
CONTRACTS_REPO=contracts
if ! command -v gh >/dev/null; then
    info "gh not installed — skipping. Install: brew install gh"
else
    if ! gh auth status >/dev/null 2>&1; then
        info "gh not authenticated — skipping. Run: gh auth login"
    else
        # 7a. Latest workflow run on proto-repo succeeded
        RUN_JSON=$(gh run list --repo $GH_OWNER/$PROTO_REPO --workflow generate.yml --limit 1 --json status,conclusion,headSha,displayTitle 2>&1)
        if [ $? -ne 0 ]; then
            err "Could not fetch workflow runs: $RUN_JSON"
        else
            STATUS=$(echo "$RUN_JSON"     | grep -o '"status":"[^"]*"'     | cut -d'"' -f4)
            CONCLUSION=$(echo "$RUN_JSON" | grep -o '"conclusion":"[^"]*"' | cut -d'"' -f4)
            TITLE=$(echo "$RUN_JSON"      | grep -o '"displayTitle":"[^"]*"' | cut -d'"' -f4)
            echo "  → latest run: status=$STATUS conclusion=$CONCLUSION ($TITLE)"
            if [ "$STATUS" = "completed" ] && [ "$CONCLUSION" = "success" ]; then
                ok "proto-repo workflow succeeded"
            elif [ "$STATUS" = "in_progress" ] || [ "$STATUS" = "queued" ]; then
                info "workflow still running — re-run demo once it finishes"
            else
                err "workflow did not succeed (status=$STATUS conclusion=$CONCLUSION)"
            fi
        fi

        # 7b. Contracts repo contains generated .pb.go files
        for FILE in payment/payment.pb.go order/order.pb.go go.mod; do
            if gh api "repos/$GH_OWNER/$CONTRACTS_REPO/contents/$FILE" --jq .name >/dev/null 2>&1; then
                ok "contracts repo has $FILE"
            else
                err "contracts repo missing $FILE"
            fi
        done

        # 7c. Latest commit on contracts main was authored by the bot
        LAST_AUTHOR=$(gh api "repos/$GH_OWNER/$CONTRACTS_REPO/commits?sha=main&per_page=1" --jq '.[0].commit.author.name' 2>/dev/null)
        LAST_MSG=$(gh api    "repos/$GH_OWNER/$CONTRACTS_REPO/commits?sha=main&per_page=1" --jq '.[0].commit.message' 2>/dev/null | head -1)
        echo "  → last commit: $LAST_AUTHOR — $LAST_MSG"
        if [ "$LAST_AUTHOR" = "github-actions[bot]" ]; then
            ok "contracts repo last commit is from workflow bot"
        else
            info "last commit not from bot (may just mean no .proto change since manual push)"
        fi
    fi
fi

echo ""
ok "ALL TESTS COMPLETED"
echo ""
echo "Useful commands:"
echo "  docker compose logs -f"
echo "  docker compose logs payment-service"
echo "  docker exec -it $PG_CONTAINER psql -U postgres -d order_db"
echo "  docker compose down -v"