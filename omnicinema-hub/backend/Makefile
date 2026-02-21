.PHONY: build run install migrate test clean

build:
	@echo "Building OmniCloud..."
	@mkdir -p bin
	go build -o bin/omnicloud cmd/omnicloud/main.go

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
