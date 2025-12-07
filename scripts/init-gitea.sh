#!/bin/bash
set -e

echo "🔧 Initializing Gitea GitOps repository..."

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

GITEA_URL="http://localhost:3000"
GITEA_USER="deploysmith"
GITEA_PASSWORD="password123"
GITEA_EMAIL="admin@deploysmith.local"
REPO_NAME="gitops"

# Check if Gitea is running
if ! curl -sf $GITEA_URL > /dev/null 2>&1; then
    echo "❌ Gitea is not running at $GITEA_URL"
    echo "   Run: docker-compose up -d gitea"
    exit 1
fi

echo "✅ Gitea is running"

# Create user (will fail if already exists, that's OK)
echo ""
echo "👤 Creating Gitea user '$GITEA_USER'..."
docker exec deploysmith-gitea gitea admin user create \
    --username $GITEA_USER \
    --password $GITEA_PASSWORD \
    --email $GITEA_EMAIL \
    --admin \
    --must-change-password=false \
    2>/dev/null && echo "✅ User created" || echo "ℹ️  User already exists"

# Get API token
echo ""
echo "🔑 Creating API token..."
TOKEN=$(docker exec deploysmith-gitea gitea admin user generate-access-token \
    --username $GITEA_USER \
    --token-name deploysmith-setup \
    --scopes write:repository,write:user \
    2>/dev/null | grep -v "New access token" || true)

if [ -z "$TOKEN" ]; then
    echo "${YELLOW}⚠️  Could not create token, trying to use existing repository${NC}"
    # Try to check if repository already exists
    REPO_EXISTS=$(curl -sf "$GITEA_URL/api/v1/repos/$GITEA_USER/$REPO_NAME" 2>/dev/null && echo "yes" || echo "no")
    if [ "$REPO_EXISTS" = "yes" ]; then
        echo "✅ Repository already exists"
        exit 0
    fi
    echo "❌ Failed to create token and repository doesn't exist"
    exit 1
fi

echo "✅ API token created"

# Create repository
echo ""
echo "📦 Creating GitOps repository..."
RESPONSE=$(curl -sf -X POST "$GITEA_URL/api/v1/user/repos" \
    -H "Authorization: token $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
        \"name\": \"$REPO_NAME\",
        \"description\": \"DeploySmith GitOps Repository\",
        \"private\": false,
        \"auto_init\": true,
        \"default_branch\": \"main\"
    }" 2>/dev/null)

if [ $? -eq 0 ]; then
    echo "✅ Repository created"
else
    echo "${YELLOW}ℹ️  Repository might already exist${NC}"
fi

# Clone repository and set up structure
echo ""
echo "📁 Setting up GitOps directory structure..."
TMP_DIR=$(mktemp -d)
cd "$TMP_DIR"

# Clone with token auth
git clone "http://$GITEA_USER:$GITEA_PASSWORD@localhost:3000/$GITEA_USER/$REPO_NAME.git" 2>/dev/null || {
    echo "${YELLOW}⚠️  Could not clone repository${NC}"
    cd - > /dev/null
    rm -rf "$TMP_DIR"
    exit 0
}

cd $REPO_NAME

# Create directory structure
mkdir -p environments/staging/apps
mkdir -p environments/production/apps

# Create README
cat > README.md <<EOF
# DeploySmith GitOps Repository

This repository contains Kubernetes manifests managed by DeploySmith.

## Structure

\`\`\`
environments/
├── staging/
│   └── apps/
│       └── {app_name}/
│           ├── deployment.yaml
│           ├── service.yaml
│           └── ...
└── production/
    └── apps/
        └── {app_name}/
            └── ...
\`\`\`

## Usage

This repository is automatically updated by DeploySmith when deploying applications.
Do not manually edit files in this repository unless you know what you're doing.

## Environments

- **staging**: Automatic deployments from main branch
- **production**: Manual deployments or from release branches
EOF

# Create placeholder files
cat > environments/staging/apps/.gitkeep <<EOF
# Placeholder - apps will be deployed here
EOF

cat > environments/production/apps/.gitkeep <<EOF
# Placeholder - apps will be deployed here
EOF

# Commit and push
git config user.name "DeploySmith Setup"
git config user.email "setup@deploysmith.local"
git add .
git commit -m "Initial GitOps repository structure" 2>/dev/null || echo "No changes to commit"
git push origin main 2>/dev/null || echo "Nothing to push"

cd - > /dev/null
rm -rf "$TMP_DIR"

echo ""
echo "${GREEN}✅ Gitea GitOps repository initialized!${NC}"
echo ""
echo "📍 Repository URL: $GITEA_URL/$GITEA_USER/$REPO_NAME"
echo "   Username: $GITEA_USER"
echo "   Password: $GITEA_PASSWORD"
echo ""
echo "🔧 To configure smithd, use:"
echo "   GITOPS_REPO=http://$GITEA_USER:$GITEA_PASSWORD@localhost:3000/$GITEA_USER/$REPO_NAME.git"
echo ""
