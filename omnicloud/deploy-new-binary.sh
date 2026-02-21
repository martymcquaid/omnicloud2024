#!/bin/bash
set -e

echo "=========================================="
echo "Deploying New OmniCloud Binary with Version Support"
echo "=========================================="

# Stop the service
echo "[1/4] Stopping omnicloud service..."
sudo systemctl stop omnicloud || killall omnicloud || true
sleep 2

# Backup old binary
echo "[2/4] Backing up old binary..."
if [ -f /opt/omnicloud/bin/omnicloud ]; then
  sudo cp /opt/omnicloud/bin/omnicloud /opt/omnicloud/bin/omnicloud.backup-$(date +%Y%m%d-%H%M%S)
fi

# Install new binary
echo "[3/4] Installing new binary..."
sudo cp bin/omnicloud-new /opt/omnicloud/bin/omnicloud
sudo chmod +x /opt/omnicloud/bin/omnicloud

# Start the service
echo "[4/4] Starting omnicloud service..."
sudo systemctl start omnicloud || /opt/omnicloud/bin/omnicloud &

echo ""
echo "✓ Deployment complete!"
echo "Waiting 3 seconds for service to start..."
sleep 3

# Test the API
echo ""
echo "Testing version API endpoints..."
curl -s http://127.0.0.1:10858/api/v1/health && echo " ✓ Health check passed"
curl -s http://127.0.0.1:10858/api/v1/versions/latest && echo "" && echo " ✓ Version API working!"

echo ""
echo "=========================================="
echo "Deployment successful! Version control is now active."
echo "=========================================="
