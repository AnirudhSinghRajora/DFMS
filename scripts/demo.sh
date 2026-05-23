#!/bin/bash
# DFMS Interactive Demo Script
# This script walks through the core features of the Distributed File Management System.
# Run this against a running local instance (make docker-up && make build && run services).

set -e

# Configuration
API_URL="http://localhost:8080/api/v1"
TEST_FILE="demo_file.txt"
TEST_FILE_DUP="demo_file_duplicate.txt"

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}======================================================${NC}"
echo -e "${BLUE}        DFMS (Distributed File Management System)       ${NC}"
echo -e "${BLUE}                  Interactive Demo                      ${NC}"
echo -e "${BLUE}======================================================${NC}\n"

# Helper to pause and wait for user
pause() {
    echo -e "\n${YELLOW}Press Enter to continue...${NC}"
    read -r
}

# ─── 1. Generate Test Data ───────────────────────────────────────────────────
echo -e "${GREEN}Step 1: Generating test data...${NC}"
echo "This is a test file for the DFMS demo." > "$TEST_FILE"
# Make it a bit larger (1MB) to ensure it gets chunked
for i in {1..20000}; do
    echo "Line $i: Content defined chunking is awesome!" >> "$TEST_FILE"
done
cp "$TEST_FILE" "$TEST_FILE_DUP"
echo "Created $TEST_FILE ($(wc -c < $TEST_FILE) bytes)"
pause

# ─── 2. User Registration & Login ───────────────────────────────────────────
echo -e "${GREEN}Step 2: User Registration and Authentication${NC}"
echo "Registering user: demo@example.com..."

# Register (ignore error if already exists)
curl -s -X POST "$API_URL/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"email":"demo@example.com","password":"demo_password_123","display_name":"Demo User"}' > /dev/null || true

echo "Logging in..."
AUTH_RESPONSE=$(curl -s -X POST "$API_URL/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"demo@example.com","password":"demo_password_123"}')

TOKEN=$(echo "$AUTH_RESPONSE" | grep -o '"access_token":"[^"]*' | grep -o '[^"]*$')

if [ -z "$TOKEN" ]; then
    echo -e "${RED}Failed to authenticate. Is the API Gateway running?${NC}"
    exit 1
fi
echo -e "Authentication successful! Token acquired.\n"
pause

# ─── 3. Upload File ─────────────────────────────────────────────────────────
echo -e "${GREEN}Step 3: Uploading File (Content-Defined Chunking)${NC}"
echo "Uploading $TEST_FILE..."

UPLOAD_RESPONSE=$(curl -s -X POST "$API_URL/files/upload" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/octet-stream" \
  -H "X-File-Name: $TEST_FILE" \
  --data-binary "@$TEST_FILE")

FILE_ID=$(echo "$UPLOAD_RESPONSE" | grep -o '"id":"[^"]*' | grep -o '[^"]*$')
echo "Upload complete! File ID: $FILE_ID"
echo "The file was automatically split into variable-sized chunks using Rabin fingerprinting."
pause

# ─── 4. Deduplication ───────────────────────────────────────────────────────
echo -e "${GREEN}Step 4: Deduplication (CAS)${NC}"
echo "Now uploading a duplicate file ($TEST_FILE_DUP)..."
echo "Watch the response time - it should be nearly instant because 0 bytes are stored!"

time curl -s -X POST "$API_URL/files/upload" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/octet-stream" \
  -H "X-File-Name: $TEST_FILE_DUP" \
  --data-binary "@$TEST_FILE_DUP" | grep -o '"id":"[^"]*'

echo -e "\nDFMS hashed the chunks, found they already exist in MinIO, and just created metadata references!"
pause

# ─── 5. List Files ──────────────────────────────────────────────────────────
echo -e "${GREEN}Step 5: Listing Files${NC}"
curl -s -X GET "$API_URL/files" \
  -H "Authorization: Bearer $TOKEN" | grep -o '"name":"[^"]*' | sed 's/"name":"/  - /'
pause

# ─── 6. Download & Verify ───────────────────────────────────────────────────
echo -e "${GREEN}Step 6: Downloading File${NC}"
echo "Downloading file $FILE_ID..."

curl -s -X GET "$API_URL/files/$FILE_ID/download" \
  -H "Authorization: Bearer $TOKEN" \
  -o "downloaded_$TEST_FILE"

echo "Comparing original and downloaded file..."
if cmp -s "$TEST_FILE" "downloaded_$TEST_FILE"; then
    echo -e "${BLUE}Success! Files match exactly. Reassembly worked perfectly.${NC}"
else
    echo -e "${RED}Error: Files do not match!${NC}"
fi
pause

# ─── 7. Cleanup ─────────────────────────────────────────────────────────────
echo -e "${GREEN}Step 7: Cleanup${NC}"
echo "Deleting test files..."
rm "$TEST_FILE" "$TEST_FILE_DUP" "downloaded_$TEST_FILE"

echo "The files remain in DFMS until deleted."
echo -e "\n${BLUE}Demo complete!${NC}"
echo "Check out Grafana at http://localhost:3000 to see metrics from this run."
