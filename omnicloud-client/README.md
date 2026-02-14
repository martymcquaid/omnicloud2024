# OmniCloud Test Client Instance

This is a local test client instance for testing client-main server communication on the same machine.

## Purpose

- Test client registration with main server
- Test inventory sync from client to main
- Test torrent status reporting
- Test upgrade/restart functionality
- Debug client-server communication issues

## Configuration

| Setting | Value |
|---------|-------|
| Mode | `client` |
| API Port | `10859` (different from main's 10858) |
| Main Server URL | `http://localhost:10858` |
| Server Name | `test-client-local` |
| Location | `Test-Local` |
| Scan Path | Same as main (`/APPBOX_DATA/storage/DCP/TESTLIBRARY/`) |

## Usage

```bash
# Start the client
./run.sh start

# Stop the client
./run.sh stop

# Restart
./run.sh restart

# Check status
./run.sh status

# Follow logs
./run.sh logs
```

## Log Files

- `omnicloud-client.log` - Application log
- `omnicloud-client.pid` - Process ID file

## Testing Scenarios

### 1. Client Registration
Client automatically registers with main server on startup. Check main server's `/api/v1/servers` endpoint.

### 2. Inventory Sync
Client syncs its DCP inventory to main server every 5 minutes. Check main server database for inventory entries.

### 3. Torrent Status
Client reports torrent/hashing status every 10 seconds. Check main server's Hashing page.

### 4. Upgrade Flow
Trigger upgrade from main server UI - client's update agent polls for pending actions.

## Ports

- Main Server: `10858`
- Test Client: `10859`

Both share the same database but have separate server identities.
