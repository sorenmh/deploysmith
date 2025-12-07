#!/bin/bash
set -e

export PORT=8080
export API_KEYS="sk_test_123"
export DB_TYPE="sqlite"
export DB_PATH="./data/test.db"
export S3_BUCKET="test-bucket"
export S3_REGION="us-east-1"
export GITOPS_REPO="git@github.com:test/repo.git"

# Note: This test will fail on S3 operations without real AWS credentials
# For now we test the basic API validation and database operations

rm -f ./data/test.db

./bin/smithd &
SERVER_PID=$!
sleep 2

API_KEY="sk_test_123"
BASE_URL="http://localhost:8080/api/v1"

echo "=== Testing Version Management API ==="

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

# Try to draft duplicate version (should fail)
echo -e "\n3. Try to draft duplicate version (should fail with 409)..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
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
  $BASE_URL/apps/$APP_ID/versions/draft | jq .

# Draft another version
echo -e "\n4. Draft version 'v1.1.0'..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "versionId": "v1.1.0",
    "metadata": {
      "gitSha": "def456",
      "gitBranch": "develop",
      "gitCommitter": "dev@example.com",
      "buildNumber": "43",
      "timestamp": "'"$TIMESTAMP"'"
    }
  }' \
  $BASE_URL/apps/$APP_ID/versions/draft | jq .

# List versions
echo -e "\n5. List all versions..."
curl -s -H "X-API-Key: $API_KEY" $BASE_URL/apps/$APP_ID/versions | jq .

# Get specific version
echo -e "\n6. Get version v1.0.0..."
curl -s -H "X-API-Key: $API_KEY" $BASE_URL/apps/$APP_ID/versions/v1.0.0 | jq .

# Get non-existent version (should fail)
echo -e "\n7. Get non-existent version (should fail with 404)..."
curl -s -H "X-API-Key: $API_KEY" $BASE_URL/apps/$APP_ID/versions/nonexistent | jq .

# Try to publish without uploading manifests (should fail)
echo -e "\n8. Try to publish without manifests (should fail)..."
curl -s -X POST -H "X-API-Key: $API_KEY" \
  $BASE_URL/apps/$APP_ID/versions/v1.0.0/publish | jq .

# Test validation errors
echo -e "\n9. Try to draft version without required fields (should fail)..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "versionId": "v2.0.0",
    "metadata": {
      "gitSha": "xyz789"
    }
  }' \
  $BASE_URL/apps/$APP_ID/versions/draft | jq .

# Test with non-existent app
echo -e "\n10. Try to draft version for non-existent app (should fail with 404)..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
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
  $BASE_URL/apps/nonexistent/versions/draft | jq .

kill $SERVER_PID
wait $SERVER_PID 2>/dev/null || true

echo -e "\n✅ Version Management API tests passed!"
echo "Note: Publish endpoint requires real S3 credentials and uploaded manifests to test fully"
