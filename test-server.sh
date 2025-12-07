#!/bin/bash
set -e

# Set minimal test config
export PORT=8080
export API_KEYS="sk_test_123"
export DB_TYPE="sqlite"
export DB_PATH="./data/test.db"
export S3_BUCKET="test-bucket"
export S3_REGION="us-east-1"
export GITOPS_REPO="git@github.com:test/repo.git"

# Clean up old test data
rm -f ./data/test.db

# Start server in background
./bin/smithd &
SERVER_PID=$!

# Wait for server to start
sleep 2

# Test health endpoint
echo "Testing health endpoint..."
curl -s http://localhost:8080/health | jq .

# Test authenticated endpoint (should work)
echo -e "\nTesting authenticated endpoint (valid key)..."
curl -s -H "X-API-Key: sk_test_123" http://localhost:8080/api/v1/apps | jq .

# Test authenticated endpoint (should fail)
echo -e "\nTesting authenticated endpoint (invalid key)..."
curl -s -H "X-API-Key: wrong" http://localhost:8080/api/v1/apps | jq .

# Kill server
kill $SERVER_PID
wait $SERVER_PID 2>/dev/null || true

echo -e "\n✅ Server tests passed!"
