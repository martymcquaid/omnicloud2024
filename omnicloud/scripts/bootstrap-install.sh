#!/bin/bash
# Bootstrap installer: download latest OmniCloud release from main server and run install
# Usage:
#   MAIN_SERVER_URL=http://dcp1.omniplex.services:10858 ./bootstrap-install.sh
#   MAIN_SERVER_URL=http://dcp1.omniplex.services:10858 REGISTRATION_KEY=your-key ./bootstrap-install.sh
#
# Requires: curl, tar, (optional) jq for parsing JSON. Uses grep/sed if jq not available.

set -e
MAIN_SERVER_URL="${MAIN_SERVER_URL:-http://dcp1.omniplex.services:10858}"
BASE="${MAIN_SERVER_URL%/}"
WORK_DIR="/tmp/omnicloud-bootstrap-$$"
trap "rm -rf '$WORK_DIR'" EXIT

echo "=========================================="
echo "OmniCloud bootstrap installer"
echo "=========================================="
echo "Main server: $MAIN_SERVER_URL"
echo ""

# Fetch latest version metadata
echo "[1/4] Fetching latest version from $BASE/api/v1/versions/latest ..."
LATEST_JSON=$(curl -sSf "$BASE/api/v1/versions/latest")
if [ -z "$LATEST_JSON" ]; then
    echo "Error: No response from server. Is the main server running and reachable?"
    exit 1
fi

# Parse download_url (and optionally version, checksum)
if command -v jq &>/dev/null; then
    DOWNLOAD_URL=$(echo "$LATEST_JSON" | jq -r '.download_url')
    VERSION=$(echo "$LATEST_JSON" | jq -r '.version')
    CHECKSUM=$(echo "$LATEST_JSON" | jq -r '.checksum // empty')
else
    DOWNLOAD_URL=$(echo "$LATEST_JSON" | grep -o '"download_url"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"\([^"]*\)".*/\1/')
    VERSION=$(echo "$LATEST_JSON" | grep -o '"version"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"\([^"]*\)".*/\1/')
    CHECKSUM=$(echo "$LATEST_JSON" | grep -o '"checksum"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"\([^"]*\)".*/\1/')
fi

if [ -z "$DOWNLOAD_URL" ] || [ "$DOWNLOAD_URL" = "null" ]; then
    echo "Error: Could not get download_url from server."
    exit 1
fi

# download_url is relative (e.g. /releases/omnicloud-xxx.tar.gz)
case "$DOWNLOAD_URL" in
    http://*|https://*) FULL_URL="$DOWNLOAD_URL" ;;
    *)                  FULL_URL="${BASE}/${DOWNLOAD_URL#/}" ;;
esac

echo "  Version: ${VERSION:-unknown}"
echo "  Package: $DOWNLOAD_URL"
echo ""

# Download package
echo "[2/4] Downloading package..."
mkdir -p "$WORK_DIR"
TARBALL="$WORK_DIR/omnicloud-latest.tar.gz"
curl -sSfL -o "$TARBALL" "$FULL_URL"
if [ ! -s "$TARBALL" ]; then
    echo "Error: Download failed or empty file."
    exit 1
fi

# Optional checksum verify
if [ -n "$CHECKSUM" ] && [ "$CHECKSUM" != "null" ]; then
    echo "  Verifying SHA256..."
    echo "$CHECKSUM  $TARBALL" | sha256sum -c -w
fi

# Extract
echo "[3/4] Extracting..."
tar -xzf "$TARBALL" -C "$WORK_DIR"
# Tarball contains single top-level dir: omnicloud-{version}-linux-amd64
EXTRACTED=$(find "$WORK_DIR" -maxdepth 1 -type d -name 'omnicloud-*' | head -1)
if [ -z "$EXTRACTED" ] || [ ! -f "$EXTRACTED/install.sh" ]; then
    echo "Error: Extracted package missing install.sh."
    exit 1
fi

# Run install
echo "[4/4] Running install script (sudo required)..."
echo ""
export MAIN_SERVER_URL
export REGISTRATION_KEY
if [ -n "${REGISTRATION_KEY:-}" ]; then
    echo "Using REGISTRATION_KEY from environment."
else
    echo "REGISTRATION_KEY not set. Install will generate one; you must set the same key on the main server or edit /etc/omnicloud/auth.config with the main server's key."
fi
echo ""

cd "$EXTRACTED"
exec sudo env MAIN_SERVER_URL="$MAIN_SERVER_URL" REGISTRATION_KEY="${REGISTRATION_KEY:-}" ./install.sh
