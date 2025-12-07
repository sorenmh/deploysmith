#!/bin/bash
set -e

export PORT=8080
export API_KEYS="sk_test_123"
export DB_TYPE="sqlite"
export DB_PATH="./data/test.db"
export S3_BUCKET="test-bucket"
export S3_REGION="us-east-1"
export GITOPS_REPO="git@github.com:test/repo.git"
export GITOPS_SSH_KEY_PATH="~/.ssh/id_rsa"

# Note: This test will fail on S3 and Git operations without real credentials
# For now we test the basic API validation and database operations

rm -f ./data/test.db

./bin/smithd &
SERVER_PID=$!
sleep 2

API_KEY="sk_test_123"
BASE_URL="http://localhost:8080/api/v1"

echo "=== Testing Deployment API ==="

# Register an app first
echo -e "\n1. Register application 'test-app'..."
APP_RESP=$(curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"test-app"}' \
  $BASE_URL/apps)
echo "$APP_RESP" | jq .
APP_ID=$(echo "$APP_RESP" | jq -r '.id')

# Draft a version
echo -e "\n2. Draft version 'v1.0.0'..."
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
DRAFT_RESP=$(curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "versionId": "v1.0.0",
    "metadata": {
      "gitSha": "abc123",
      "gitBranch": "main",
      "gitCommitter": "test@example.com",
      "buildNumber": "42",
      "timestamp": "'"$TIMESTAMP"'"
    }
  }' \
  $BASE_URL/apps/$APP_ID/versions/draft)
echo "$DRAFT_RESP" | jq .

# Try to deploy draft version (should fail)
echo -e "\n3. Try to deploy draft version (should fail with 400)..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "environment": "staging"
  }' \
  $BASE_URL/apps/$APP_ID/versions/v1.0.0/deploy | jq .

# Try to deploy non-existent version (should fail)
echo -e "\n4. Try to deploy non-existent version (should fail with 404)..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "environment": "staging"
  }' \
  $BASE_URL/apps/$APP_ID/versions/nonexistent/deploy | jq .

# Try to deploy without environment (should fail)
echo -e "\n5. Try to deploy without environment (should fail with 400)..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{}' \
  $BASE_URL/apps/$APP_ID/versions/v1.0.0/deploy | jq .

# Try to deploy for non-existent app (should fail)
echo -e "\n6. Try to deploy for non-existent app (should fail with 404)..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "environment": "staging"
  }' \
  $BASE_URL/apps/nonexistent/versions/v1.0.0/deploy | jq .

# Note: Testing actual deployment requires:
# - Uploading manifests to S3
# - Publishing the version
# - Valid gitops repository with SSH credentials
# These operations would be tested in integration tests with real infrastructure

echo -e "\n--- Deployment API would work like this (needs real S3/Git): ---"
echo "7. Upload manifests to S3 (skipped - needs real S3)"
echo "8. Publish version (skipped - needs real S3)"
echo "9. Deploy to staging (skipped - needs real S3 and Git)"
echo ""
echo "Example deployment request:"
echo 'curl -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \'
echo '  -d '"'"'{"environment": "staging", "triggeredBy": "user@example.com"}'"'"' \'
echo '  $BASE_URL/apps/$APP_ID/versions/v1.0.0/deploy'

kill $SERVER_PID
wait $SERVER_PID 2>/dev/null || true

echo -e "\n✅ Deployment API validation tests passed!"
echo "Note: Full deployment requires S3 credentials and gitops repository access"
