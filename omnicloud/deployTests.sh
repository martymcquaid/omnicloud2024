#!/bin/bash
# Build OmniCloud, copy binary to main and test, then restart both.
# Edit the variables below for your paths and restart method.

set -e
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# --- Configure these for your setup ---
# Defaults: local run from DCPCLOUDAPP (main = omnicloud, test = omnicloud-client via run.sh).
# Override MAIN_DEST/TEST_DEST and restart commands for /opt or systemd.
MAIN_DEST="${MAIN_DEST:-$SCRIPT_DIR/bin}"
TEST_DEST="${TEST_DEST:-$SCRIPT_DIR/../omnicloud-client/bin}"

# Restart via run.sh in each dir (no sudo). Override for systemctl if you use /opt install.
# Stop test before copying so we can overwrite the running binary (avoids "Text file busy").
STOP_TEST_CMD="${STOP_TEST_CMD:-cd \"$SCRIPT_DIR/../omnicloud-client\" && ./run.sh stop}"
RESTART_MAIN_CMD="${RESTART_MAIN_CMD:-cd \"$SCRIPT_DIR\" && ./run.sh stop && ./run.sh start}"
RESTART_TEST_CMD="${RESTART_TEST_CMD:-cd \"$SCRIPT_DIR/../omnicloud-client\" && ( ./run.sh stop || true ) && ./run.sh start}"

# --- Build ---
echo "[1/4] Building OmniCloud..."
make build
BIN="$SCRIPT_DIR/bin/omnicloud"
if [ ! -f "$BIN" ]; then
  echo "Build failed: $BIN not found"
  exit 1
fi
echo "    Built: $BIN"

# --- Copy to main ---
echo "[2/4] Copying to main ($MAIN_DEST)..."
if [ "$BIN" -ef "$MAIN_DEST/omnicloud" ]; then
  echo "    (binary already in place, skipping copy)"
else
  mkdir -p "$MAIN_DEST"
  cp "$BIN" "$MAIN_DEST/omnicloud"
  chmod +x "$MAIN_DEST/omnicloud"
fi
echo "    Done."

# --- Copy to test ---
echo "[3/4] Copying to test ($TEST_DEST)..."
if [ -n "$STOP_TEST_CMD" ]; then
  echo "    Stopping test client..."
  eval $STOP_TEST_CMD || true
  sleep 1
fi
mkdir -p "$TEST_DEST"
# Copy to .new then replace: avoids "Text file busy" if process is slow to release the file
cp "$BIN" "$TEST_DEST/omnicloud.new"
chmod +x "$TEST_DEST/omnicloud.new"
rm -f "$TEST_DEST/omnicloud"
mv "$TEST_DEST/omnicloud.new" "$TEST_DEST/omnicloud"
echo "    Done."

# --- Restart both ---
echo "[4/4] Restarting main and test..."
if [ -n "$RESTART_MAIN_CMD" ]; then
  echo "    Restarting main..."
  eval $RESTART_MAIN_CMD
  echo "    Main restarted."
else
  echo "    (RESTART_MAIN_CMD not set, skipping main restart)"
fi
if [ -n "$RESTART_TEST_CMD" ]; then
  echo "    Restarting test..."
  eval $RESTART_TEST_CMD
  echo "    Test restarted."
else
  echo "    (RESTART_TEST_CMD not set, skipping test restart)"
fi

echo "Deploy complete."
