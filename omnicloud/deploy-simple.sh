#!/bin/bash
set -e

echo "=========================================="
echo "Deploying New OmniCloud Binary"
echo "=========================================="

# Kill the old process
echo "[1/5] Stopping old process..."
pkill -f "bin/omnicloud" || true
sleep 2

# Backup old binary
echo "[2/5] Backing up old binary..."
if [ -f bin/omnicloud ]; then
  cp bin/omnicloud bin/omnicloud.backup-$(date +%Y%m%d-%H%M%S)
fi

# Install new binary
echo "[3/5] Installing new binary..."
cp bin/omnicloud-new bin/omnicloud
chmod +x bin/omnicloud

# Start new process
echo "[4/5] Starting new process..."
cd /home/appbox/DCPCLOUDAPP/omnicloud
nohup ./bin/omnicloud > omnicloud.log 2>&1 &
NEW_PID=$!
echo "Started with PID: $NEW_PID"

# Wait for startup
echo "[5/5] Waiting for service to start..."
sleep 3

# Test the API
echo ""
echo "Testing version API..."
curl -s http://127.0.0.1:10858/api/v1/health && echo ""
echo ""
curl -s http://127.0.0.1:10858/api/v1/versions/latest && echo ""

echo ""
echo "=========================================="
echo "✓ Deployment Complete!"
echo "=========================================="
