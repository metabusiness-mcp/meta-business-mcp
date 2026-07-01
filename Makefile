.PHONY: build dev dashboard-build dashboard-dev clean test test-infra-only test-verbose test-cover docker-build docker-up docker-down

# Production build: build dashboard, copy to embed location, build Go binary
build: dashboard-build
	rm -rf cmd/server/dashboard_out/*
	cp -r dashboard/out/* cmd/server/dashboard_out/
	go build -o bin/meta-business-mcp ./cmd/server

# Build dashboard static files
dashboard-build:
	cd dashboard && npm install && npm run build

# Development: run Go server
dev:
	go run ./cmd/server

# Run dashboard dev server only (with API proxy to localhost:8080)
dashboard-dev:
	cd dashboard && npm run dev

# Run all tests (stops app container first to free ports, restarts after)
test:
	docker compose stop app
	go test ./... -count=1 -p 1
	docker compose start app

# Run tests excluding flaky scheduler tests
test-infra-only:
	go test ./... -count=1 -p 1 \
	  --ignore pkg/delivery

# Run tests with verbose output
test-verbose:
	go test -count=1 -v -p 1 ./...

# Run tests with coverage
test-cover:
	go test -cover ./...

# Clean build artifacts
clean:
	rm -rf bin/ dashboard/out/ dashboard/.next/ cmd/server/dashboard_out/* 

# Build Docker image
docker-build:
	docker compose build

# Start all services
docker-up:
	docker compose up -d

# Stop all services
docker-down:
	docker compose down
