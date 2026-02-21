#!/bin/bash
set -e

# OmniCloud Release Build Script
# Creates a versioned release package with binary, configs, and installation scripts

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RELEASE_DIR="/home/appbox/DCPCLOUDAPP/releases"

# Generate version from timestamp
VERSION="${VERSION:-$(date -u +%Y%m%d-%H%M%S)}"
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
PACKAGE_NAME="omnicloud-${VERSION}-linux-amd64"
PACKAGE_FILE="${PACKAGE_NAME}.tar.gz"

echo "=========================================="
echo "OmniCloud Release Builder"
echo "=========================================="
echo "Version: $VERSION"
echo "Build Time: $BUILD_TIME"
echo "Package: $PACKAGE_FILE"
echo "=========================================="

# Create staging directory
STAGING_DIR="/tmp/omnicloud-build-${VERSION}"
rm -rf "$STAGING_DIR"
mkdir -p "$STAGING_DIR/$PACKAGE_NAME"

cd "$PROJECT_ROOT"

# Build the binary with version embedded
echo "[1/8] Building binary..."
make release VERSION="$VERSION"

# Copy binary to staging
echo "[2/8] Copying binary..."
cp bin/omnicloud "$STAGING_DIR/$PACKAGE_NAME/"
chmod +x "$STAGING_DIR/$PACKAGE_NAME/omnicloud"

# Copy configuration template
echo "[3/8] Copying configuration template..."
cp auth.config "$STAGING_DIR/$PACKAGE_NAME/auth.config.example"

# Copy migrations
echo "[4/8] Copying database migrations..."
mkdir -p "$STAGING_DIR/$PACKAGE_NAME/migrations"
cp -r internal/db/migrations/*.sql "$STAGING_DIR/$PACKAGE_NAME/migrations/"

# Copy PostgreSQL 9.1 fix scripts
echo "[4.5/8] Copying PostgreSQL 9.1 compatibility fix scripts..."
mkdir -p "$STAGING_DIR/$PACKAGE_NAME/scripts"
cp scripts/fix-postgresql-9.1-migrations.sh "$STAGING_DIR/$PACKAGE_NAME/scripts/" 2>/dev/null || true
cp scripts/fix-all-postgresql-9.1-migrations.sh "$STAGING_DIR/$PACKAGE_NAME/scripts/" 2>/dev/null || true
chmod +x "$STAGING_DIR/$PACKAGE_NAME/scripts/fix-postgresql-9.1-migrations.sh" 2>/dev/null || true
chmod +x "$STAGING_DIR/$PACKAGE_NAME/scripts/fix-all-postgresql-9.1-migrations.sh" 2>/dev/null || true

# Copy systemd service file
echo "[5/8] Copying systemd service..."
cp systemd/omnicloud.service "$STAGING_DIR/$PACKAGE_NAME/"

# Copy installation script
echo "[6/8] Copying installation script..."
cp scripts/install.sh "$STAGING_DIR/$PACKAGE_NAME/"
chmod +x "$STAGING_DIR/$PACKAGE_NAME/install.sh"

# Create README
cat > "$STAGING_DIR/$PACKAGE_NAME/README.md" << EOF
# OmniCloud DCP Manager v${VERSION}

Built: ${BUILD_TIME}

## Installation

For new installations:
\`\`\`bash
sudo ./install.sh
\`\`\`

For upgrades on existing installations:
The update agent will handle this automatically, or run:
\`\`\`bash
sudo systemctl stop omnicloud
sudo cp omnicloud /opt/omnicloud/bin/omnicloud
sudo systemctl start omnicloud
\`\`\`

## Configuration

Edit /etc/omnicloud/auth.config with your settings.

## Service Management

- Start: \`sudo systemctl start omnicloud\`
- Stop: \`sudo systemctl stop omnicloud\`
- Restart: \`sudo systemctl restart omnicloud\`
- Status: \`sudo systemctl status omnicloud\`
- Logs: \`sudo journalctl -u omnicloud -f\`

For more information, visit: https://github.com/omnicloud/omnicloud
EOF

# Create tarball
echo "[7/8] Creating release package..."
cd "$STAGING_DIR"
tar czf "$PACKAGE_FILE" "$PACKAGE_NAME"

# Compute checksum
echo "[8/8] Computing SHA256 checksum..."
CHECKSUM=$(sha256sum "$PACKAGE_FILE" | awk '{print $1}')
echo "$CHECKSUM  $PACKAGE_FILE" > "${PACKAGE_FILE}.sha256"

# Get file size
FILE_SIZE=$(stat -f%z "$PACKAGE_FILE" 2>/dev/null || stat -c%s "$PACKAGE_FILE")

# Move to release directory
mkdir -p "$RELEASE_DIR"
mv "$PACKAGE_FILE" "$RELEASE_DIR/"
mv "${PACKAGE_FILE}.sha256" "$RELEASE_DIR/"

# Update manifest
MANIFEST_FILE="$RELEASE_DIR/manifest.json"
if [ ! -f "$MANIFEST_FILE" ]; then
    echo '{"versions":[]}' > "$MANIFEST_FILE"
fi

# Add new version to manifest using a temporary Python script
python3 - <<PYTHON_SCRIPT
import json
import sys

manifest_file = "$MANIFEST_FILE"
with open(manifest_file, 'r') as f:
    manifest = json.load(f)

# Add new version
new_version = {
    "version": "$VERSION",
    "build_time": "$BUILD_TIME",
    "checksum": "$CHECKSUM",
    "size_bytes": $FILE_SIZE,
    "download_url": "/releases/$PACKAGE_FILE",
    "is_stable": True
}

# Check if version already exists
versions = manifest.get("versions", [])
existing = [v for v in versions if v["version"] == "$VERSION"]
if existing:
    # Update existing
    for v in versions:
        if v["version"] == "$VERSION":
            v.update(new_version)
else:
    # Add new
    versions.insert(0, new_version)

manifest["versions"] = versions
manifest["latest"] = new_version

with open(manifest_file, 'w') as f:
    json.dump(manifest, f, indent=2)

print(f"✓ Added version $VERSION to manifest")
PYTHON_SCRIPT

# Cleanup
rm -rf "$STAGING_DIR"

# Register version with the API (main server DB) so the UI and upgrade flow see it
API_URL="${OMNICLOUD_API_URL:-http://localhost:10858}"
if curl -sf -X POST "$API_URL/api/v1/versions" \
  -H "Content-Type: application/json" \
  -d "{\"version\":\"$VERSION\",\"build_time\":\"$BUILD_TIME\",\"checksum\":\"$CHECKSUM\",\"size_bytes\":$FILE_SIZE,\"download_url\":\"/releases/$PACKAGE_FILE\",\"is_stable\":true}" > /dev/null; then
  echo "✓ Registered version $VERSION with API ($API_URL)"
else
  echo "⚠ Could not register with API (is the server running?). Register manually:"
  echo "  curl -X POST $API_URL/api/v1/versions -H 'Content-Type: application/json' \\"
  echo "    -d '{\"version\":\"$VERSION\",\"build_time\":\"$BUILD_TIME\",\"checksum\":\"$CHECKSUM\",\"size_bytes\":$FILE_SIZE,\"download_url\":\"/releases/$PACKAGE_FILE\",\"is_stable\":true}'"
fi

echo ""
echo "=========================================="
echo "✓ Release package created successfully!"
echo "=========================================="
echo "Package: $RELEASE_DIR/$PACKAGE_FILE"
echo "Checksum: $CHECKSUM"
echo "Size: $(numfmt --to=iec-i --suffix=B $FILE_SIZE 2>/dev/null || echo ${FILE_SIZE} bytes)"
echo "Manifest: $MANIFEST_FILE"
echo ""
echo "To deploy this release:"
echo "  1. Main server serves it at /releases/$PACKAGE_FILE"
echo "  2. Sites page shows this version in the Upgrade dropdown"
echo "  3. Trigger upgrade per server from the UI, or let the update agent pick it up"
echo "=========================================="
