# Production Deployment Guide

This document describes how to deploy the Meta Business MCP system in a production environment.

## Prerequisites

- **Docker & Docker Compose** (version 20.10+ / Compose V2)
- **PostgreSQL 16** (or AWS RDS PostgreSQL)
- **Redis 7** (or AWS ElastiCache Redis)
- **NATS Server** (with JetStream enabled)
- **WhatsApp Business API Account Credentials** (from Meta Developer Console)

---

## 1. Environment Configuration

Create a production `.env` configuration file in your project root:

```env
# Server settings
SERVER_HTTP_PORT=8080
SERVER_MCP_NAME=meta-business-mcp
SERVER_MCP_VERSION=1.0.0

# Database connections
DB_HOST=postgres.prod.internal
DB_PORT=5432
DB_USER=mcp_user
DB_PASSWORD=production_secure_postgres_pass
DB_NAME=meta_mcp
DB_SSLMODE=require

# Cache connection
REDIS_ADDR=redis.prod.internal:6379
REDIS_PASSWORD=production_redis_auth_pass
REDIS_DB=0

# Message broker connection
NATS_URL=nats://nats.prod.internal:4222

# Meta Cloud API credentials
META_API_URL=https://graph.facebook.com
META_ACCESS_TOKEN=EAAG...production_long_lived_system_user_token...
META_PHONE_NUMBER_ID=106555123456789
META_WABA_ID=204555123456789
META_WEBHOOK_VERIFY_TOKEN=production_webhook_verification_passphrase

# Policies file path
POLICIES_PATH=/app/policies.yaml
```

---

## 2. Docker Compose Deployment

A standard production `docker-compose.prod.yaml`:

```yaml
version: '3.8'

services:
  postgres:
    image: postgres:16-alpine
    container_name: mcp_postgres
    restart: always
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: production_secure_postgres_pass
      POSTGRES_DB: meta_mcp
    volumes:
      - pgdata:/var/lib/postgresql/data
    ports:
      - "5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres -d meta_mcp"]
      interval: 10s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    container_name: mcp_redis
    restart: always
    command: redis-server --requirepass production_redis_auth_pass
    ports:
      - "6379:6379"
    volumes:
      - redisdata:/data
    healthcheck:
      test: ["CMD", "redis-cli", "-a", "production_redis_auth_pass", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

  nats:
    image: nats:alpine
    container_name: mcp_nats
    restart: always
    command: "--jetstream -m 8222"
    ports:
      - "4222:4222"
      - "8222:8222"
    volumes:
      - natsdata:/data
    healthcheck:
      test: ["CMD", "nc", "-z", "localhost", "4222"]
      interval: 10s
      timeout: 5s
      retries: 5

  app:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: mcp_app
    restart: always
    ports:
      - "8080:8080"
    env_file:
      - .env
    volumes:
      - ./policies.yaml:/app/policies.yaml
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      nats:
        condition: service_healthy

volumes:
  pgdata:
  redisdata:
  natsdata:
```

### Launch Services
```bash
docker compose -f docker-compose.prod.yaml up -d --build
```

---

## 3. Database Migrations & Seeding

The application automatically executes database schema migrations and seeds policy rules from `policies.yaml` on startup. No external migration runner is required.

If you modify `policies.yaml` and want to hot-reload the changes into the database:
- Simply restart the `mcp_app` container. The system runs an idempotent seed script on startup (`ON CONFLICT (id) DO UPDATE`) which updates policy configurations dynamically.

---

## 4. Health Checks & Verification

To verify that the deployment is healthy, issue a curl request to the HTTP server:

```bash
# Verify health check
curl http://localhost:8080/health
# Response: OK

# Verify Prometheus metrics
curl http://localhost:8080/metrics
# Response: Prometheus metric scrape output
```
