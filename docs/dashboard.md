# Dashboard Documentation

## Overview

The Operational Dashboard is a web-based interface for monitoring Meta Business MCP. It provides visibility into message delivery, compliance status, templates, and system configuration.

**Access:** `http://localhost:8080` (or your deployed URL)  
**Authentication:** Username/password configured in `config.yaml`

---

## Pages

### Dashboard Home (`/`)

Overview of platform activity:
- **Metrics Cards:** Messages Sent Today, Delivery Rate, Active Conversations, Compliance Pass Rate
- **Charts:** Message Volume (7 days), Message Status Breakdown
- **Recent Activity:** Last 10 messages, Last 5 compliance events

### Messages (`/messages`)

Table of all messages with:
- **Columns:** ID, Customer, Direction, Type, Status, Error, Timestamp
- **Filters:** Status (queued/sent/delivered/read/failed), Direction (inbound/outbound)
- **Expand:** Click row to see full ID, conversation ID, error message, retry count

### Conversations (`/conversations`)

Table of all conversations with:
- **Columns:** Customer ID, Channel, Type, Last Inbound, Window Expires, Status
- **Filters:** Status (active/archived), Expiring Soon toggle
- **Color coding:** Green (window open), Yellow (expiring <1h), Red (window closed)

### Templates (`/templates`)

Table of all templates with:
- **Columns:** Name, Locale, Category, Status, Body Preview
- **Filters:** Status (approved/pending/rejected), Category (marketing/utility/authentication)
- **Expand:** Click row to see full template body and variables

### Compliance (`/compliance`)

Audit log of compliance checks:
- **Columns:** Action, Customer ID, Result (Allowed/Blocked), Reason Code, Timestamp
- **Color coding:** Green (allowed), Red (blocked)

### Settings (`/settings`)

Read-only display of:
- Webhook URL, Verify Token, Signature Validation
- Phone Number ID, WABA ID, API URL, Current Tier

---

## Authentication

### Login

1. Navigate to `http://localhost:8080`
2. Enter username and password (configured in `config.yaml`)
3. Session cookie valid for 24 hours

### Default Credentials

```
Username: admin
Password: admin
```

**Change before production deployment.**

### Generate New Password Hash

```go
// Save as /tmp/gen_hash.go, run with: go run /tmp/gen_hash.go
package main

import (
    "fmt"
    "golang.org/x/crypto/bcrypt"
)

func main() {
    hash, _ := bcrypt.GenerateFromPassword([]byte("your-new-password"), 10)
    fmt.Println(string(hash))
}
```

Update `config.yaml`:
```yaml
dashboard:
  username: "admin"
  password_hash: "$2a$10$..."  # New hash
  session_key: "random-32-byte-string"
```

---

## API Endpoints

All endpoints return JSON. Protected endpoints require session cookie.

### Auth (Public)

| Endpoint | Method | Body | Response |
|---|---|---|---|
| `/api/auth/login` | POST | `{"username":"...","password":"..."}` | `{"token":"..."}` + Set-Cookie |
| `/api/auth/logout` | POST | — | `{"status":"logged_out"}` |
| `/api/auth/check` | GET | — | `{"authenticated":true/false}` |

### Data (Protected)

| Endpoint | Method | Query Params | Response |
|---|---|---|---|
| `/api/messages` | GET | `limit`, `offset`, `status`, `direction`, `customer_id` | `{"messages":[...],"total":N}` |
| `/api/conversations` | GET | `limit`, `offset`, `status`, `expiring_soon` | `{"conversations":[...],"total":N}` |
| `/api/compliance/events` | GET | `limit`, `offset` | `{"events":[...],"total":N}` |
| `/api/templates` | GET | `status`, `category` | `{"templates":[...],"total":N}` |
| `/api/metrics/summary` | GET | — | `{"messages_sent_today":N,...}` |
| `/api/config/webhook` | GET | — | `{"webhook_url":"...","verify_token":"***"}` |
| `/api/config/meta` | GET | — | `{"phone_number_id":"***","waba_id":"***"}` |

---

## Build Pipeline

### Production Build

```bash
make build
# Equivalent to:
# 1. cd dashboard && npm install && npm run build
# 2. cp -r dashboard/out/* cmd/server/dashboard_out/
# 3. go build -o bin/meta-business-mcp ./cmd/server
```

### Development

```bash
# Terminal 1: Go server
make dev

# Terminal 2: Next.js dev server (with API proxy)
make dashboard-dev
```

### Docker

```bash
docker compose build
docker compose up -d
```

---

## Architecture

```
Browser → Go HTTP Server (port 8080)
              │
              ├── /api/* → Dashboard handlers → PostgreSQL
              ├── /webhook → Webhook receiver
              ├── /health → Health check
              ├── /metrics → Prometheus
              └── /* → Embedded static files (Next.js export)
```

**Single binary:** Dashboard HTML/CSS/JS embedded via `go:embed`. No separate service, no CORS, no Node.js runtime in production.
