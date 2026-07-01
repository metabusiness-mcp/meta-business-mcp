# Environment Variables Reference

This document lists all environment variables used to configure the Meta Business MCP platform.

## Configuration Variables

| Variable Name | Description | Default Value | Example Value |
|---|---|---|---|
| `SERVER_HTTP_PORT` | Port for the HTTP web server (Webhook receiver, metrics, health) | `8080` | `8080` |
| `SERVER_MCP_NAME` | Name identifier for the MCP Server | `"meta-business-mcp"` | `"meta-mcp-prod"` |
| `SERVER_MCP_VERSION` | Version of the MCP Server | `"1.0.0"` | `"1.2.0"` |
| `DB_HOST` | PostgreSQL Host Address | `"localhost"` | `"postgres"` |
| `DB_PORT` | PostgreSQL Port | `5432` | `5432` |
| `DB_USER` | PostgreSQL Username | `"postgres"` | `"postgres"` |
| `DB_PASSWORD` | PostgreSQL Password | `"password"` | `"secret_db_pass"` |
| `DB_NAME` | PostgreSQL Database Name | `"meta_mcp"` | `"meta_mcp"` |
| `DB_SSLMODE` | PostgreSQL SSL Mode (`disable`, `require`, `verify-ca`) | `"disable"` | `"require"` |
| `REDIS_ADDR` | Redis connection address | `"localhost:6379"` | `"redis:6379"` |
| `REDIS_PASSWORD` | Redis connection password | `""` | `"secret_redis_pass"` |
| `REDIS_DB` | Redis database index | `0` | `1` |
| `NATS_URL` | NATS connection URL | `"nats://localhost:4222"` | `"nats://nats:4222"` |
| `META_API_URL` | Base URL of Meta Graph Cloud API | `"https://graph.facebook.com"` | `"http://mock-meta:8081"` |
| `META_ACCESS_TOKEN` | Meta API OAuth access token | *Required* | `"EAAG..."` |
| `META_PHONE_NUMBER_ID` | WhatsApp Business Phone Number ID | *Required* | `"106555123456"` |
| `META_WABA_ID` | WhatsApp Business Account ID (WABA ID) | *Required* | `"204555123456"` |
| `META_WEBHOOK_VERIFY_TOKEN` | Token sent by Meta to verify webhook challenge | *Required* | `"my-verify-token"` |
| `POLICIES_PATH` | Path to the business policy seed file (YAML) | `"policies.yaml"` | `"/app/policies.yaml"` |
