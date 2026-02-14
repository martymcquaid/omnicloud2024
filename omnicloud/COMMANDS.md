# OmniCloud – Command reference

All commands below are run from the **omnicloud** directory: `/home/appbox/DCPCLOUDAPP/omnicloud` (or `scripts/` parent).

---

## Service script (do it all)

One script for build, release package, and service control:

```bash
./scripts/omnicloud-service.sh <command>
```

| Command | Description |
|--------|-------------|
| `build` | Build binary (`make build`). |
| `release` | Create upgrade package (`scripts/build-release.sh`). |
| `start` | Build if needed, then start OmniCloud (restart loop, log to `omnicloud.log`). |
| `stop` | Stop OmniCloud. |
| `restart` | Stop, build, start. |
| `status` | Show if running and API health. |
| `logs` | Tail `omnicloud.log`. |
| `all` | **Do it all:** stop → build → release (create package) → start. |

Examples:

```bash
# Full deploy: build, create package, restart
./scripts/omnicloud-service.sh all

# Just build and start
./scripts/omnicloud-service.sh start

# Restart after code changes
./scripts/omnicloud-service.sh restart
```

---

## Build and run

| Command | Description |
|--------|-------------|
| `make build` | Build binary to `bin/omnicloud` (version from timestamp unless `VERSION` is set). |
| `make run` | Build then run `./bin/omnicloud`. |
| `make build && ./bin/omnicloud` | Same as `make run`. |

Optional version:

```bash
make build VERSION=20260214-120000
./bin/omnicloud
```

---

## Rebuild and restart (when already running)

Stop any running instance, rebuild, then start:

```bash
cd /home/appbox/DCPCLOUDAPP/omnicloud
pkill -f bin/omnicloud 2>/dev/null
make build && ./bin/omnicloud
```

One-liner:

```bash
cd /home/appbox/DCPCLOUDAPP/omnicloud && pkill -f bin/omnicloud 2>/dev/null; make build && ./bin/omnicloud
```

---

## Create upgrade package

Build a versioned tarball for self-upgrade and remote agents, and register it with the API (if the server is running):

```bash
cd /home/appbox/DCPCLOUDAPP/omnicloud
./scripts/build-release.sh
```

Output: `releases/omnicloud-<VERSION>-linux-amd64.tar.gz` and updated `releases/manifest.json`. The main server serves files from `releases/` at `/releases/`.

With a specific version:

```bash
VERSION=20260214-120000 ./scripts/build-release.sh
```

Custom API URL for registration (default `http://localhost:10858`):

```bash
OMNICLOUD_API_URL=http://127.0.0.1:10858 ./scripts/build-release.sh
```

---

## Makefile targets

| Target | Description |
|--------|-------------|
| `make build` | Build to `bin/omnicloud` (no CGO). |
| `make release` | Build with CGO for release binary. |
| `make run` | `make build` then run `./bin/omnicloud`. |
| `make clean` | Remove `bin/` and run `go clean`. |
| `make test` | Run tests: `go test -v ./...`. |
| `make deps` | `go mod download` and `go mod tidy`. |
| `make install` | `go install` for `cmd/omnicloud`. |
| `make migrate` | Run DB migrations via `psql` (hardcoded DB credentials). |

Version override (for any build target):

```bash
make build VERSION=1.0.0
```

---

## Initial install (client site) – download everything from main server

The **install script by itself does not download anything**. It expects to be run from an **already extracted** release tarball (binary, migrations, systemd file must be next to `install.sh`).

To **download everything from your main server** (e.g. dcp1.omniplex.services) and install in one go, use the **bootstrap** script. It fetches the latest release from the main server API, extracts it, and runs `install.sh` with the right settings.

**On the new machine (as root or with sudo):**

Copy `scripts/bootstrap-install.sh` to the machine (or clone the repo), then:

```bash
MAIN_SERVER_URL=http://dcp1.omniplex.services:10858 ./bootstrap-install.sh
```

With the main server’s registration key (get it from the main server admin / auth.config):

```bash
MAIN_SERVER_URL=http://dcp1.omniplex.services:10858 REGISTRATION_KEY=your-main-server-key ./scripts/bootstrap-install.sh
```

The bootstrap script will:

1. Call `GET $MAIN_SERVER_URL/api/v1/versions/latest` to get the latest version and `download_url`.
2. Download the tarball from `$MAIN_SERVER_URL/releases/omnicloud-<version>-linux-amd64.tar.gz`.
3. Verify SHA256 if the API returns a checksum.
4. Extract and run `install.sh` with `MAIN_SERVER_URL` and `REGISTRATION_KEY` set.

**Requirements:**

- Main server (e.g. http://dcp1.omniplex.services:10858) is reachable.
- At least one version is registered on the main server (run `./scripts/build-release.sh` there once) and the server serves `/releases/` (it does by default).
- Client uses the **same** `registration_key` as the main server so registration succeeds; then authorize the server in the main UI if needed.

**Manual alternative:** download the tarball from the main server, extract, then run install with env vars:

```bash
curl -sSfL -o omnicloud.tar.gz "http://dcp1.omniplex.services:10858/releases/omnicloud-$(curl -sS "http://dcp1.omniplex.services:10858/api/v1/versions/latest" | grep -o '"version":"[^"]*"' | cut -d'"' -f4)-linux-amd64.tar.gz"
tar -xzf omnicloud.tar.gz && cd omnicloud-*-linux-amd64
sudo MAIN_SERVER_URL=http://dcp1.omniplex.services:10858 REGISTRATION_KEY=your-key ./install.sh
```

---

## Quick reference

| Goal | Command |
|------|--------|
| Build and run | `make run` |
| Rebuild and restart (kill existing first) | `pkill -f bin/omnicloud 2>/dev/null; make build && ./bin/omnicloud` |
| Create upgrade package | `./scripts/build-release.sh` |
| Create package with version | `VERSION=20260214-120000 ./scripts/build-release.sh` |
| Clean build artifacts | `make clean` |
| Run tests | `make test` |
