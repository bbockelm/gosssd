#!/bin/bash
set -e

echo "Setting up development environment..."

# Create test users in /etc/passwd for SSSD to read
# These will be available through SSSD
if ! grep -q "testuser1" /etc/passwd; then
    useradd -u 10001 -g users -d /home/testuser1 -s /bin/bash -c "Test User 1" testuser1 || true
fi

if ! grep -q "testuser2" /etc/passwd; then
    useradd -u 10002 -g users -d /home/testuser2 -s /bin/bash -c "Test User 2" testuser2 || true
fi

# Create test group
if ! grep -q "testgroup" /etc/group; then
    groupadd -g 10100 testgroup || true
    usermod -a -G testgroup testuser1 || true
    usermod -a -G testgroup testuser2 || true
fi

# Initialize Go module dependencies
cd /workspaces/gosssd
if [ -f "go.mod" ]; then
    echo "Installing Go dependencies..."
    go mod download
fi

# Setup pre-commit hooks (pre-commit is already installed in the container)
if [ -f ".pre-commit-config.yaml" ]; then
    echo "Installing pre-commit hook environments..."
    pre-commit install-hooks
    pre-commit install
fi

echo "Setup complete!"
echo ""
echo "Note: Integration tests build and run a self-contained SSSD instance"
echo "No system SSSD configuration is needed."
echo ""
echo "To run pre-commit checks:"
echo "  pre-commit run --all-files"
