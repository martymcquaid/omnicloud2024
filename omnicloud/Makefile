.PHONY: build run install migrate test clean release

# Version can be overridden: make build VERSION=1.0.0
VERSION ?= $(shell date -u +%Y%m%d-%H%M%S)
LDFLAGS := -ldflags "-X main.Version=$(VERSION)"

build:
	@echo "Building OmniCloud v$(VERSION)..."
	@mkdir -p bin
	go build $(LDFLAGS) -o bin/omnicloud cmd/omnicloud/main.go

release:
	@echo "Building release for OmniCloud v$(VERSION)..."
	@mkdir -p bin
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/omnicloud cmd/omnicloud/main.go

run: build
	@echo "Starting OmniCloud..."
	./bin/omnicloud

install:
	@echo "Installing OmniCloud..."
	go install cmd/omnicloud

migrate:
	@echo "Running database migrations..."
	@PGPASSWORD=Random93555342 psql -h localhost -U omni -d OmniCloud -f internal/db/migrations/001_create_servers.sql
	@PGPASSWORD=Random93555342 psql -h localhost -U omni -d OmniCloud -f internal/db/migrations/002_create_dcp_packages.sql
	@PGPASSWORD=Random93555342 psql -h localhost -U omni -d OmniCloud -f internal/db/migrations/003_create_dcp_compositions.sql
	@PGPASSWORD=Random93555342 psql -h localhost -U omni -d OmniCloud -f internal/db/migrations/004_create_dcp_assets.sql
	@PGPASSWORD=Random93555342 psql -h localhost -U omni -d OmniCloud -f internal/db/migrations/005_create_inventory.sql
	@PGPASSWORD=Random93555342 psql -h localhost -U omni -d OmniCloud -f internal/db/migrations/006_add_mac_address.sql
	@PGPASSWORD=Random93555342 psql -h localhost -U omni -d OmniCloud -f internal/db/migrations/007_create_torrent_tables.sql
	@PGPASSWORD=Random93555342 psql -h localhost -U omni -d OmniCloud -f internal/db/migrations/008_add_torrent_queue_total_size.sql
	@PGPASSWORD=Random93555342 psql -h localhost -U omni -d OmniCloud -f internal/db/migrations/009_torrent_queue_total_size_bigint.sql
	@echo "Migrations complete!"

test:
	@echo "Running tests..."
	go test -v ./...

clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	go clean

deps:
	@echo "Installing dependencies..."
	go mod download
	go mod tidy
