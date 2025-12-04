#!/bin/bash
set -e

export PORT=8080
export API_KEYS="sk_test_123"
export DB_TYPE="sqlite"
export DB_PATH="./data/test.db"
export S3_BUCKET="test-bucket"
export S3_REGION="us-east-1"
export GITOPS_REPO="git@github.com:test/repo.git"

rm -f ./data/test.db

./bin/smithd &
SERVER_PID=$!
sleep 2

API_KEY="sk_test_123"
BASE_URL="http://localhost:8080/api/v1"

echo "=== Testing Application API ==="

# Register an app
echo -e "\n1. Register application 'my-api-service'..."
APP_RESP=$(curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"my-api-service"}' \
  $BASE_URL/apps)
echo "$APP_RESP" | jq .
APP_ID=$(echo "$APP_RESP" | jq -r '.id')

# Try to register duplicate (should fail)
echo -e "\n2. Try to register duplicate (should fail with 409)..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"my-api-service"}' \
  $BASE_URL/apps | jq .

# Register another app
echo -e "\n3. Register application 'hello-world'..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"hello-world"}' \
  $BASE_URL/apps | jq .

# List apps
echo -e "\n4. List all applications..."
curl -s -H "X-API-Key: $API_KEY" $BASE_URL/apps | jq .

# Get specific app
echo -e "\n5. Get application by ID..."
curl -s -H "X-API-Key: $API_KEY" $BASE_URL/apps/$APP_ID | jq .

# Get non-existent app (should fail)
echo -e "\n6. Get non-existent app (should fail with 404)..."
curl -s -H "X-API-Key: $API_KEY" $BASE_URL/apps/nonexistent | jq .

kill $SERVER_PID
wait $SERVER_PID 2>/dev/null || true

echo -e "\n✅ Application API tests passed!"
