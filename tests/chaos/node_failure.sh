#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────
# DFMS Chaos Test Suite
#
# Deliberately injects failures to verify the system handles
# them gracefully. Requires Docker and a running DFMS stack.
#
# Usage: ./tests/chaos/node_failure.sh
# ──────────────────────────────────────────────────────────────

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
COMPOSE_FILE="${COMPOSE_FILE:-deployments/docker-compose.yml}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "  ${GREEN}✓ PASS${NC}: $1"; }
fail() { echo -e "  ${RED}✗ FAIL${NC}: $1"; FAILURES=$((FAILURES + 1)); }
info() { echo -e "  ${YELLOW}→${NC} $1"; }

FAILURES=0

# ── Helper: Register and get auth token ───────────────────────
get_token() {
    local email="chaos-test-$(date +%s)@test.com"
    local resp
    resp=$(curl -s -X POST "${BASE_URL}/api/v1/auth/register" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"${email}\",\"password\":\"chaostest123\",\"display_name\":\"Chaos\"}")
    echo "$resp" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4
}

# ── Helper: Upload a test file ────────────────────────────────
upload_file() {
    local token="$1"
    local size="${2:-1024}"  # Default 1KB
    dd if=/dev/urandom bs="$size" count=1 2>/dev/null | \
        curl -s -X POST "${BASE_URL}/api/v1/files/upload" \
            -H "Authorization: Bearer ${token}" \
            -H "Content-Type: application/octet-stream" \
            -H "X-File-Name: chaos-$(date +%s%N).bin" \
            --data-binary @-
}

# ── Helper: Download a file ──────────────────────────────────
download_file() {
    local token="$1"
    local file_id="$2"
    curl -s -o /dev/null -w "%{http_code}" \
        "${BASE_URL}/api/v1/files/${file_id}/download" \
        -H "Authorization: Bearer ${token}"
}

echo "═══════════════════════════════════════════════════════════"
echo " DFMS Chaos Test Suite"
echo "═══════════════════════════════════════════════════════════"
echo ""

# ── Test 1: MinIO Node Failure During Download ────────────────
echo "Test 1: MinIO node failure during download"
info "Uploading test file..."
TOKEN=$(get_token)
UPLOAD_RESP=$(upload_file "$TOKEN" 10240)
FILE_ID=$(echo "$UPLOAD_RESP" | grep -o '"file_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$FILE_ID" ]; then
    fail "Could not upload file for test"
else
    info "Stopping minio-1..."
    docker compose -f "$COMPOSE_FILE" stop minio-1 2>/dev/null || true
    sleep 2

    STATUS=$(download_file "$TOKEN" "$FILE_ID")
    if [ "$STATUS" = "200" ]; then
        pass "Download succeeded after minio-1 failure (replica served)"
    else
        fail "Download failed (HTTP $STATUS) after minio-1 failure"
    fi

    info "Restarting minio-1..."
    docker compose -f "$COMPOSE_FILE" start minio-1 2>/dev/null || true
    sleep 3
fi

echo ""

# ── Test 2: Redis Failure (Fail-Open) ────────────────────────
echo "Test 2: Redis failure (fail-open)"
info "Stopping Redis..."
docker compose -f "$COMPOSE_FILE" stop redis 2>/dev/null || true
sleep 2

STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${BASE_URL}/health")
if [ "$STATUS" = "200" ] || [ "$STATUS" = "503" ]; then
    # Health check may report 503 (redis unhealthy) or 200 — both are OK
    # The key test is whether uploads still work (fail-open)
    UPLOAD_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
        -X POST "${BASE_URL}/api/v1/files/upload" \
        -H "Authorization: Bearer ${TOKEN}" \
        -H "Content-Type: application/octet-stream" \
        -H "X-File-Name: redis-down-test.bin" \
        --data-binary "test data")

    if [ "$UPLOAD_STATUS" != "500" ]; then
        pass "System remained functional with Redis down (HTTP $UPLOAD_STATUS)"
    else
        fail "System returned 500 with Redis down"
    fi
else
    fail "Health endpoint returned unexpected status: $STATUS"
fi

info "Restarting Redis..."
docker compose -f "$COMPOSE_FILE" start redis 2>/dev/null || true
sleep 3

echo ""

# ── Test 3: Kafka Failure ────────────────────────────────────
echo "Test 3: Kafka failure (upload should still succeed)"
info "Stopping Kafka..."
docker compose -f "$COMPOSE_FILE" stop kafka 2>/dev/null || true
sleep 2

UPLOAD_RESP=$(upload_file "$TOKEN" 2048)
UPLOAD_ID=$(echo "$UPLOAD_RESP" | grep -o '"file_id"')
if [ -n "$UPLOAD_ID" ]; then
    pass "Upload succeeded with Kafka down (sync to primary storage)"
else
    fail "Upload failed with Kafka down"
fi

info "Restarting Kafka..."
docker compose -f "$COMPOSE_FILE" start kafka 2>/dev/null || true
sleep 5

echo ""

# ── Test 4: PostgreSQL Failure ───────────────────────────────
echo "Test 4: PostgreSQL failure (should return 503)"
info "Stopping PostgreSQL..."
docker compose -f "$COMPOSE_FILE" stop postgres 2>/dev/null || true
sleep 2

STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${BASE_URL}/readiness")
if [ "$STATUS" = "503" ]; then
    pass "System correctly reports not-ready when DB is down"
else
    fail "Expected 503, got $STATUS when DB is down"
fi

info "Restarting PostgreSQL..."
docker compose -f "$COMPOSE_FILE" start postgres 2>/dev/null || true
sleep 5

# Verify recovery
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${BASE_URL}/readiness")
if [ "$STATUS" = "200" ]; then
    pass "System recovered after PostgreSQL restart"
else
    fail "System did not recover after PostgreSQL restart (HTTP $STATUS)"
fi

echo ""

# ── Summary ──────────────────────────────────────────────────
echo "═══════════════════════════════════════════════════════════"
if [ "$FAILURES" -eq 0 ]; then
    echo -e " ${GREEN}All chaos tests passed!${NC}"
else
    echo -e " ${RED}${FAILURES} chaos test(s) failed${NC}"
fi
echo "═══════════════════════════════════════════════════════════"

exit "$FAILURES"
