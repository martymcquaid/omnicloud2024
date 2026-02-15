#!/bin/bash
# Verify that this OmniCloud instance is configured and running as a client.
# Usage: ./verify-client.sh [path-to-auth.config]
# Default config: ./auth.config (from omnicloud dir) or /etc/omnicloud/auth.config

set -e
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OMNI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG="${1:-$OMNI_DIR/auth.config}"
if [ -n "$1" ]; then
  CONFIG="$1"
elif [ -f "$OMNI_DIR/auth.config" ]; then
  CONFIG="$OMNI_DIR/auth.config"
elif [ -f /etc/omnicloud/auth.config ]; then
  CONFIG=/etc/omnicloud/auth.config
fi

echo "=========================================="
echo "OmniCloud client verification"
echo "=========================================="
echo "Config: $CONFIG"
echo ""

if [ ! -f "$CONFIG" ]; then
  echo "ERROR: Config file not found: $CONFIG"
  exit 1
fi

MODE=$(grep -E "^server_mode=" "$CONFIG" | cut -d= -f2- | tr -d ' ')
MAIN_URL=$(grep -E "^main_server_url=" "$CONFIG" | cut -d= -f2- | tr -d ' ')
HAS_KEY=$(grep -E "^registration_key=" "$CONFIG" | cut -d= -f2- | tr -d ' ')

IS_CLIENT=0
if [ "$MODE" = "client" ]; then
  echo "  server_mode          = client (OK)"
  IS_CLIENT=1
else
  echo "  server_mode          = $MODE (expected 'client')"
fi

if [ -n "$MAIN_URL" ] && [ "$MAIN_URL" != "null" ]; then
  echo "  main_server_url      = $MAIN_URL (OK)"
else
  echo "  main_server_url      = (empty - required for client)"
  IS_CLIENT=0
fi

if [ -n "$HAS_KEY" ] && [ "$HAS_KEY" != "your-secure-registration-key-here" ]; then
  echo "  registration_key     = (set)"
else
  echo "  registration_key     = (missing or placeholder)"
  IS_CLIENT=0
fi

echo ""
if [ "$IS_CLIENT" -eq 1 ]; then
  echo "Result: This instance is CONFIGURED as a client."
  echo ""
  echo "Runtime checks (if OmniCloud is running):"
  echo "  1. Log should show: 'Server Mode: client' and 'Main Server URL: $MAIN_URL'"
  echo "  2. Log should show: 'Client sync service started' and 'Successfully registered with main server'"
  echo "  3. On main server UI (Sites): this server should appear (authorize if pending)"
  echo ""
  echo "Quick log check:"
  echo "  grep -E 'Server Mode|Main Server URL|Client sync|registered with main' /var/log/omnicloud/omnicloud.log 2>/dev/null || grep -E 'Server Mode|Main Server URL|Client sync|registered with main' $OMNI_DIR/omnicloud.log 2>/dev/null | tail -20"
else
  echo "Result: This instance is NOT configured as a client."
  echo "To set as client: edit $CONFIG and set server_mode=client, main_server_url=http://YOUR_MAIN:10858, registration_key=YOUR_KEY"
  exit 1
fi
