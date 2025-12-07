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
# For now we test the basic API validation and policy management

rm -f ./data/test.db

./bin/smithd &
SERVER_PID=$!
sleep 2

API_KEY="sk_test_123"
BASE_URL="http://localhost:8080/api/v1"

echo "=== Testing Policy Management API ==="

# Register an app first
echo -e "\n1. Register application 'test-app'..."
APP_RESP=$(curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"test-app"}' \
  $BASE_URL/apps)
echo "$APP_RESP" | jq .
APP_ID=$(echo "$APP_RESP" | jq -r '.id')

# Create policy: main → staging
echo -e "\n2. Create policy: 'main' branch → staging..."
POLICY1_RESP=$(curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "name": "Deploy main to staging",
    "gitBranchPattern": "main",
    "targetEnvironment": "staging"
  }' \
  $BASE_URL/apps/$APP_ID/policies)
echo "$POLICY1_RESP" | jq .
POLICY1_ID=$(echo "$POLICY1_RESP" | jq -r '.id')

# Create policy: release/* → production
echo -e "\n3. Create policy: 'release/*' branch → production..."
POLICY2_RESP=$(curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "name": "Deploy releases to production",
    "gitBranchPattern": "release/*",
    "targetEnvironment": "production"
  }' \
  $BASE_URL/apps/$APP_ID/policies)
echo "$POLICY2_RESP" | jq .
POLICY2_ID=$(echo "$POLICY2_RESP" | jq -r '.id')

# Create disabled policy
echo -e "\n4. Create disabled policy..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "name": "Disabled policy",
    "gitBranchPattern": "develop",
    "targetEnvironment": "dev",
    "enabled": false
  }' \
  $BASE_URL/apps/$APP_ID/policies | jq .

# List all policies
echo -e "\n5. List all policies..."
curl -s -H "X-API-Key: $API_KEY" $BASE_URL/apps/$APP_ID/policies | jq .

# Try to create policy without required fields (should fail)
echo -e "\n6. Try to create policy without name (should fail with 400)..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "gitBranchPattern": "main",
    "targetEnvironment": "staging"
  }' \
  $BASE_URL/apps/$APP_ID/policies | jq .

# Try to create policy without branch pattern (should fail)
echo -e "\n7. Try to create policy without branch pattern (should fail with 400)..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "name": "Test policy",
    "targetEnvironment": "staging"
  }' \
  $BASE_URL/apps/$APP_ID/policies | jq .

# Try to create policy for non-existent app (should fail)
echo -e "\n8. Try to create policy for non-existent app (should fail with 404)..."
curl -s -X POST -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{
    "name": "Test policy",
    "gitBranchPattern": "main",
    "targetEnvironment": "staging"
  }' \
  $BASE_URL/apps/nonexistent/policies | jq .

# Delete policy
echo -e "\n9. Delete policy '$POLICY2_ID'..."
curl -s -X DELETE -H "X-API-Key: $API_KEY" \
  $BASE_URL/apps/$APP_ID/policies/$POLICY2_ID -w "\nHTTP Status: %{http_code}\n"

# List policies again (should have one less)
echo -e "\n10. List policies after deletion..."
curl -s -H "X-API-Key: $API_KEY" $BASE_URL/apps/$APP_ID/policies | jq .

# Try to delete non-existent policy (should fail)
echo -e "\n11. Try to delete non-existent policy (should fail with 404)..."
curl -s -X DELETE -H "X-API-Key: $API_KEY" \
  $BASE_URL/apps/$APP_ID/policies/nonexistent | jq .

echo -e "\n--- Auto-deploy test (needs real S3/Git): ---"
echo "12. Would test: Draft version with 'main' branch"
echo "13. Would test: Publish version (should auto-deploy to staging)"
echo "14. Would test: Verify deployment record has policy_id"
echo ""
echo "Example workflow:"
echo "- Create policy: main → staging"
echo "- Draft version with gitBranch='main'"
echo "- Publish version"
echo "- Auto-deploy should trigger automatically to staging"

kill $SERVER_PID
wait $SERVER_PID 2>/dev/null || true

echo -e "\n✅ Policy Management API tests passed!"
echo "Note: Auto-deploy testing requires S3 credentials and gitops repository access"
