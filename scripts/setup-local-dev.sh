#!/bin/bash
set -e

echo "🚀 Setting up DeploySmith local development environment..."

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check dependencies
echo "📋 Checking dependencies..."
command -v docker >/dev/null 2>&1 || { echo "❌ Docker is required but not installed. Aborting." >&2; exit 1; }
command -v docker-compose >/dev/null 2>&1 || { echo "❌ Docker Compose is required but not installed. Aborting." >&2; exit 1; }
echo "✅ Dependencies satisfied"

# Generate SSH key for Gitea if it doesn't exist
echo ""
echo "🔑 Setting up SSH keys for GitOps..."
mkdir -p scripts/ssh
if [ ! -f scripts/ssh/id_rsa ]; then
    ssh-keygen -t rsa -b 4096 -f scripts/ssh/id_rsa -N "" -C "smithd@deploysmith.local"
    echo "✅ Generated SSH key pair"
else
    echo "✅ SSH key already exists"
fi

# Create .env file if it doesn't exist
if [ ! -f .env ]; then
    echo ""
    echo "📝 Creating .env file..."
    cat > .env <<EOF
# DeploySmith Local Development Environment

# Server Configuration
PORT=8080
API_KEYS=sk_local_dev_key_$(openssl rand -hex 16)

# Database
DB_TYPE=sqlite
DB_PATH=./data/smithd.db

# S3 (MinIO)
S3_BUCKET=deploysmith-versions
S3_REGION=us-east-1
AWS_ACCESS_KEY_ID=minioadmin
AWS_SECRET_ACCESS_KEY=minioadmin123
AWS_ENDPOINT=http://localhost:9000

# GitOps
GITOPS_REPO=http://deploysmith:password123@gitea:3000/deploysmith/gitops.git
GITOPS_SSH_KEY_PATH=./scripts/ssh/id_rsa
GITOPS_USER_NAME=smithd
GITOPS_USER_EMAIL=smithd@deploysmith.io
EOF
    echo "✅ Created .env file"
else
    echo "✅ .env file already exists"
fi

# Start Docker Compose
echo ""
echo "🐳 Starting Docker containers..."
docker-compose up -d

# Wait for services to be healthy
echo ""
echo "⏳ Waiting for services to be ready..."
sleep 5

# Check MinIO
echo -n "  Checking MinIO... "
until curl -sf http://localhost:9000/minio/health/ready > /dev/null 2>&1; do
    echo -n "."
    sleep 2
done
echo " ${GREEN}✅${NC}"

# Check Gitea
echo -n "  Checking Gitea... "
until curl -sf http://localhost:3000/ > /dev/null 2>&1; do
    echo -n "."
    sleep 2
done
echo " ${GREEN}✅${NC}"

echo ""
echo "${GREEN}✅ Local development environment is ready!${NC}"
echo ""
echo "📍 Service URLs:"
echo "  • MinIO Console:  http://localhost:9001 (minioadmin / minioadmin123)"
echo "  • Gitea:          http://localhost:3000"
echo "  • smithd API:     http://localhost:8080"
echo ""
echo "🔧 Next steps:"
echo "  1. Run: ${YELLOW}./scripts/init-gitea.sh${NC} to create GitOps repository"
echo "  2. Build smithd: ${YELLOW}go build -o bin/smithd ./cmd/smithd${NC}"
echo "  3. Run smithd: ${YELLOW}source .env && ./bin/smithd${NC}"
echo "  4. Run tests: ${YELLOW}./test-apps-api.sh${NC}"
echo ""
