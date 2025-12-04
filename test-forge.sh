#!/bin/bash
set -e

echo "🔨 Testing forge CLI"
echo ""

# Configuration
export SMITHD_URL="http://localhost:8080"
export SMITHD_API_KEY="test_key_123"
APP_NAME="test-forge-app"
VERSION="v1.0.0-test"
TEST_DIR="test-manifests"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Helper functions
pass() {
  echo -e "${GREEN}✓${NC} $1"
}

fail() {
  echo -e "${RED}✗${NC} $1"
  exit 1
}

warn() {
  echo -e "${YELLOW}⚠${NC} $1"
}

# Check if smithd is running
echo "Checking if smithd is running..."
if ! curl -s http://localhost:8080/health > /dev/null; then
  warn "smithd is not running at http://localhost:8080"
  echo "This test requires smithd to be running."
  echo "Start it with: ./bin/smithd"
  echo ""
  echo "For now, testing forge binary directly..."
  echo ""
fi

# Test 1: forge version
echo "Test 1: forge version"
if ./bin/forge version | grep -q "forge version"; then
  pass "forge version works"
else
  fail "forge version failed"
fi
echo ""

# Test 2: forge help
echo "Test 2: forge help"
if ./bin/forge help | grep -q "Available Commands"; then
  pass "forge help works"
else
  fail "forge help failed"
fi
echo ""

# Test 3: forge init --help
echo "Test 3: forge init --help"
if ./bin/forge init --help | grep -q "Initialize a new version draft"; then
  pass "forge init --help works"
else
  fail "forge init --help failed"
fi
echo ""

# Test 4: forge upload --help
echo "Test 4: forge upload --help"
if ./bin/forge upload --help | grep -q "Upload manifest"; then
  pass "forge upload --help works"
else
  fail "forge upload --help failed"
fi
echo ""

# Test 5: forge publish --help
echo "Test 5: forge publish --help"
if ./bin/forge publish --help | grep -q "Publish"; then
  pass "forge publish --help works"
else
  fail "forge publish --help failed"
fi
echo ""

# Only run integration tests if smithd is running
if curl -s http://localhost:8080/health > /dev/null; then
  echo "Running integration tests with live smithd..."
  echo ""

  # Register app if it doesn't exist
  echo "Registering application..."
  curl -s -X POST http://localhost:8080/api/v1/apps \
    -H "Authorization: Bearer $SMITHD_API_KEY" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"$APP_NAME\"}" > /dev/null 2>&1 || true
  pass "App registered"
  echo ""

  # Test 6: forge init
  echo "Test 6: forge init (integration)"
  if ./bin/forge init \
    --app "$APP_NAME" \
    --version "$VERSION" \
    --git-sha "abc123def456" \
    --git-branch "main" \
    --git-committer "test@example.com" \
    --build-number 42 | grep -q "versionId"; then
    pass "forge init created draft version"
  else
    fail "forge init failed"
  fi
  echo ""

  # Create test manifests
  echo "Creating test manifests..."
  mkdir -p "$TEST_DIR"

  cat > "$TEST_DIR/deployment.yaml" << 'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-app
spec:
  replicas: 2
  selector:
    matchLabels:
      app: test-app
  template:
    metadata:
      labels:
        app: test-app
    spec:
      containers:
      - name: app
        image: nginx:latest
        ports:
        - containerPort: 80
EOF

  cat > "$TEST_DIR/service.yaml" << 'EOF'
apiVersion: v1
kind: Service
metadata:
  name: test-app
spec:
  selector:
    app: test-app
  ports:
  - port: 80
    targetPort: 80
EOF

  pass "Test manifests created"
  echo ""

  # Test 7: forge upload
  echo "Test 7: forge upload (integration)"
  if ./bin/forge upload "$TEST_DIR" 2>&1 | grep -q "Uploaded"; then
    pass "forge upload succeeded"
  else
    fail "forge upload failed"
  fi
  echo ""

  # Test 8: forge publish
  echo "Test 8: forge publish (integration)"
  if ./bin/forge publish \
    --app "$APP_NAME" \
    --version "$VERSION" 2>&1 | grep -q "now live"; then
    pass "forge publish succeeded"
  else
    fail "forge publish failed"
  fi
  echo ""

  # Cleanup
  rm -rf "$TEST_DIR" .forge
  pass "Cleaned up test files"
  echo ""

  echo "✅ All integration tests passed!"
else
  echo "⏭️  Skipping integration tests (smithd not running)"
  echo ""
  echo "To run full integration tests:"
  echo "  1. Start smithd: ./bin/smithd"
  echo "  2. Run this script again: ./test-forge.sh"
fi

echo ""
echo "✅ forge CLI tests complete!"
