#!/bin/bash
set -e

# OmniCloud Installation Script
# Installs OmniCloud DCP Manager with PostgreSQL database and systemd service

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check if running as root
if [ "$EUID" -ne 0 ]; then 
    echo -e "${RED}Error: This script must be run as root${NC}"
    exit 1
fi

echo "=========================================="
echo "OmniCloud DCP Manager - Installation"
echo "=========================================="

# Detect OS
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS=$ID
    VER=$VERSION_ID
else
    echo -e "${RED}Error: Cannot detect OS${NC}"
    exit 1
fi

echo -e "${GREEN}Detected OS: $OS $VER${NC}"

# Check prerequisites
echo ""
echo "[1/12] Checking prerequisites..."

# Check for systemd
if ! command -v systemctl &> /dev/null; then
    echo -e "${RED}Error: systemd is required but not found${NC}"
    exit 1
fi

# Install PostgreSQL if not present
echo ""
echo "[2/12] Checking PostgreSQL..."
if ! command -v psql &> /dev/null; then
    echo -e "${YELLOW}PostgreSQL not found. Installing...${NC}"
    case "$OS" in
        ubuntu|debian)
            apt-get update
            apt-get install -y postgresql postgresql-contrib
            ;;
        centos|rhel|fedora)
            dnf install -y postgresql-server postgresql-contrib
            postgresql-setup --initdb
            ;;
        *)
            echo -e "${RED}Error: Unsupported OS for auto-install: $OS${NC}"
            echo "Please install PostgreSQL 12+ manually"
            exit 1
            ;;
    esac
    systemctl enable postgresql
    systemctl start postgresql
    echo -e "${GREEN}✓ PostgreSQL installed${NC}"
else
    echo -e "${GREEN}✓ PostgreSQL found${NC}"
fi

# Create system user
echo ""
echo "[3/12] Creating system user..."
if ! id "omnicloud" &>/dev/null; then
    useradd -r -s /bin/false -d /opt/omnicloud -m omnicloud
    echo -e "${GREEN}✓ User 'omnicloud' created${NC}"
else
    echo -e "${GREEN}✓ User 'omnicloud' already exists${NC}"
fi

# Create directories
echo ""
echo "[4/12] Creating directories..."
mkdir -p /opt/omnicloud/bin
mkdir -p /opt/omnicloud/data
mkdir -p /var/log/omnicloud
mkdir -p /etc/omnicloud

chown -R omnicloud:omnicloud /opt/omnicloud
chown -R omnicloud:omnicloud /var/log/omnicloud
echo -e "${GREEN}✓ Directories created${NC}"

# Setup database
echo ""
echo "[5/12] Setting up database..."

# Generate random password
DB_PASSWORD=$(openssl rand -base64 32 | tr -d "=+/" | cut -c1-25)

# Create database and user
sudo -u postgres psql -c "SELECT 1 FROM pg_roles WHERE rolname='omni'" | grep -q 1 || \
    sudo -u postgres psql -c "CREATE USER omni WITH PASSWORD '$DB_PASSWORD';"

sudo -u postgres psql -c "SELECT 1 FROM pg_database WHERE datname='OmniCloud'" | grep -q 1 || \
    sudo -u postgres psql -c "CREATE DATABASE OmniCloud OWNER omni;"

sudo -u postgres psql -d OmniCloud -c "CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\";"
sudo -u postgres psql -d OmniCloud -c "GRANT ALL PRIVILEGES ON DATABASE OmniCloud TO omni;"

echo -e "${GREEN}✓ Database 'OmniCloud' created${NC}"

# Run migrations
echo ""
echo "[6/12] Running database migrations..."
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for migration in "$SCRIPT_DIR"/migrations/*.sql; do
    if [ -f "$migration" ]; then
        echo "  Running: $(basename $migration)"
        PGPASSWORD="$DB_PASSWORD" psql -h localhost -U omni -d OmniCloud -f "$migration" -q
    fi
done
echo -e "${GREEN}✓ Migrations completed${NC}"

# Copy binary
echo ""
echo "[7/12] Installing binary..."
cp "$SCRIPT_DIR/omnicloud" /opt/omnicloud/bin/omnicloud
chmod +x /opt/omnicloud/bin/omnicloud
chown omnicloud:omnicloud /opt/omnicloud/bin/omnicloud
echo -e "${GREEN}✓ Binary installed to /opt/omnicloud/bin/omnicloud${NC}"

# Generate config
echo ""
echo "[8/12] Generating configuration..."

# Registration key: use env (from main server admin) or generate (user must set on main server or edit config)
if [ -n "${REGISTRATION_KEY:-}" ]; then
    REG_KEY="$REGISTRATION_KEY"
    echo -e "${GREEN}Using registration key from REGISTRATION_KEY${NC}"
else
    REG_KEY=$(openssl rand -hex 32)
    echo -e "${YELLOW}Generated random registration key. For client mode, get the key from your main server admin and set in /etc/omnicloud/auth.config or re-run with REGISTRATION_KEY=...${NC}"
fi

# Main server URL: use env or placeholder
MAIN_URL="${MAIN_SERVER_URL:-http://mainserver.example.com:10858}"

# Get MAC address
MAC_ADDRESS=$(ip link show | awk '/ether/ {print $2; exit}')

# Get hostname
HOSTNAME=$(hostname -f)

cat > /etc/omnicloud/auth.config << EOF
# OmniCloud Configuration
# Generated: $(date)

# Database connection
host=localhost
port=5432
database=OmniCloud
user=omni
password=$DB_PASSWORD

# Server configuration
server_mode=client
# Options: "main" for main server, "client" for site servers

# API port
api_port=10858

# Registration key (must match main server's key for client registration)
registration_key=$REG_KEY

# Main server URL
main_server_url=$MAIN_URL

# DCP archive path (update this with your DCP storage path)
scan_path=/path/to/dcp/library

# Periodic scan interval in hours
scan_interval=12

# Server identification
server_name=$HOSTNAME
server_location=Unknown Location

# Torrent generation workers
max_torrent_generation_workers=4
EOF

chown omnicloud:omnicloud /etc/omnicloud/auth.config
chmod 600 /etc/omnicloud/auth.config
echo -e "${GREEN}✓ Configuration created at /etc/omnicloud/auth.config${NC}"
echo -e "${YELLOW}⚠  Please edit /etc/omnicloud/auth.config with your settings${NC}"

# Install systemd service
echo ""
echo "[9/12] Installing systemd service..."
cp "$SCRIPT_DIR/omnicloud.service" /etc/systemd/system/omnicloud.service
systemctl daemon-reload
echo -e "${GREEN}✓ Systemd service installed${NC}"

# Enable service (but don't start yet - user needs to configure)
echo ""
echo "[10/12] Enabling service..."
systemctl enable omnicloud
echo -e "${GREEN}✓ Service enabled${NC}"

# Setup log rotation
echo ""
echo "[11/12] Configuring log rotation..."
cat > /etc/logrotate.d/omnicloud << EOF
/var/log/omnicloud/*.log {
    daily
    rotate 14
    compress
    delaycompress
    notifempty
    missingok
    create 0640 omnicloud omnicloud
    sharedscripts
    postrotate
        systemctl reload omnicloud > /dev/null 2>&1 || true
    endscript
}
EOF
echo -e "${GREEN}✓ Log rotation configured${NC}"

# Final instructions
echo ""
echo "[12/12] Installation complete!"
echo ""
echo "=========================================="
echo -e "${GREEN}✓ OmniCloud installed successfully!${NC}"
echo "=========================================="
echo ""
echo "Next steps:"
echo "1. Edit configuration: vi /etc/omnicloud/auth.config"
echo "   - Set main_server_url to your main server (if not set via MAIN_SERVER_URL)"
echo "   - Set registration_key to your main server's key (if not set via REGISTRATION_KEY)"
echo "   - Set scan_path to your DCP library"
echo "   - Update server_name and server_location"
echo ""
echo "2. Start the service:"
echo "   systemctl start omnicloud"
echo ""
echo "3. Check status:"
echo "   systemctl status omnicloud"
echo ""
echo "4. View logs:"
echo "   journalctl -u omnicloud -f"
echo "   or"
echo "   tail -f /var/log/omnicloud/omnicloud.log"
echo ""
echo "5. Test API (after starting):"
echo "   curl http://localhost:10858/api/v1/health"
echo ""
echo "Database credentials:"
echo "  Database: OmniCloud"
echo "  User: omni"
echo "  Password: $DB_PASSWORD"
echo ""
echo "Registration key: $REG_KEY"
echo "(This is already in /etc/omnicloud/auth.config)"
echo "=========================================="
