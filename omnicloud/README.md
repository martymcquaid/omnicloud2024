# OmniCloud DCP Manager

OmniCloud is a centralized Digital Cinema Package (DCP) library management system built in Go. It provides real-time monitoring, comprehensive metadata indexing, and multi-server inventory tracking for DCP archives.

## Features

- **Real-time Filesystem Monitoring**: Watches DCP archive directories for changes
- **Full Metadata Parsing**: Extracts all details from ASSETMAP, CPL, and PKL files
- **PostgreSQL Database**: Comprehensive schema for DCP packages, compositions, reels, and assets
- **REST API**: Multi-server communication for distributed DCP inventory
- **Periodic Verification**: Scheduled full scans to ensure database consistency
- **Audit Trail**: Complete logging of all scan operations

## Architecture

- **Scanner**: Discovers and parses DCP packages from filesystem
- **Watcher**: Real-time filesystem event monitoring with debouncing
- **Parser**: XML parsing for ASSETMAP, CPL (Composition Playlist), and PKL (Packing List)
- **API Server**: RESTful endpoints for server registration and inventory queries
- **Database**: PostgreSQL with 8 tables tracking servers, packages, compositions, reels, assets

## Requirements

- Go 1.19 or higher
- PostgreSQL 12 or higher
- Linux (tested on Ubuntu 20.04+)

## Installation

1. Clone the repository
2. Configure database in `auth.config`:
   ```
   host=localhost
   port=5432
   database=OmniCloud
   user=omni
   password=YOUR_PASSWORD
   ```

3. Build the application:
   ```bash
   make build
   ```

4. Run database migrations:
   ```bash
   make migrate
   ```

5. Start OmniCloud:
   ```bash
   ./bin/omnicloud
   ```

## Configuration

Edit `auth.config` in the project root or set environment variables:

- `DB_HOST` - PostgreSQL host (default: localhost)
- `DB_PORT` - PostgreSQL port (default: 5432)
- `DB_NAME` - Database name (default: OmniCloud)
- `DB_USER` - Database user
- `DB_PASSWORD` - Database password
- `SCAN_PATH` - DCP archive path (default: /APPBOX_DATA/storage/DCP/Archive/)
- `API_PORT` - API server port (default: 8080)
- `SCAN_INTERVAL` - Periodic scan interval in hours (default: 12)

## API Endpoints

- `GET /api/v1/health` - Health check
- `GET /api/v1/dcps` - List all DCPs
- `GET /api/v1/dcps/{uuid}` - Get DCP details
- `GET /api/v1/servers` - List all servers
- `POST /api/v1/servers/register` - Register a new server
- `POST /api/v1/servers/{id}/inventory` - Update server inventory

## Database Schema

- `servers` - Server registry
- `dcp_packages` - Main DCP package records
- `dcp_compositions` - CPL metadata (playlists)
- `dcp_reels` - Individual reel information
- `dcp_assets` - MXF and asset files
- `dcp_packing_lists` - PKL metadata
- `server_dcp_inventory` - Server-to-DCP mapping
- `scan_logs` - Audit trail

## License

Proprietary - All rights reserved
