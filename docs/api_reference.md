# API & MCP Tools Reference

This document describes the HTTP endpoints and Model Context Protocol (MCP) tools provided by the Meta Business MCP platform. All tools are exposed to AI Agents over the standard I/O channel.

## HTTP Endpoints

The Go application hosts an HTTP server on the port defined by `SERVER_HTTP_PORT` (default: `8080`).

### 1. Webhook Verification
Verify the Webhook subscription from the Meta Developer Dashboard.

- **URL**: `/webhook`
- **Method**: `GET`
- **Query Parameters**:
  - `hub.mode` (must be `subscribe`)
  - `hub.verify_token` (must match configured token)
  - `hub.challenge` (random string sent by Meta)
- **Response**: Status `200 OK` with the exact `hub.challenge` string.

### 2. Webhook Event Callback
Receives real-time webhook events from Meta (messages, status updates, template approval changes).

- **URL**: `/webhook`
- **Method**: `POST`
- **Content-Type**: `application/json`
- **Headers**:
  - `X-Hub-Signature-256` (Meta signature for request authenticity validation)
- **Response**: Status `200 OK` with body `"EVENT_RECEIVED"`.

### 3. Health Check
- **URL**: `/health`
- **Method**: `GET`
- **Response**: Status `200 OK` with body `"OK"`.

### 4. Prometheus Metrics
- **URL**: `/metrics`
- **Method**: `GET`
- **Response**: Status `200 OK` with standard Prometheus scrape payload.

---

## v1 Tools (Implemented — Phase 1)

These 4 tools were the original MCP tool set.

---

### 1. `check_compliance`

Evaluate whether sending a planned outbound message to a customer is currently allowed under Meta WABA rules and local business policies.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `customer_id` | string | ✅ | Customer phone number with country code (e.g. `"+628119989630"`) |
  | `message_type` | string | ✅ | Category: `"marketing"`, `"utility"`, `"template"`, or `"service"` |
  | `channel` | string | ❌ | Default: `"whatsapp"` |

- **Response (success)**:
  ```json
  {
    "allowed": false,
    "reason_code": "TEMPLATE_REQUIRED",
    "human_explanation": "Customer care window is closed.",
    "suggested_action": "Send an approved WhatsApp Template message using 'send_template'."
  }
  ```

- **Error codes**: `TEMPLATE_REQUIRED`, `USER_OPTED_OUT`, `FREQUENCY_CAP_EXCEEDED`, `POLICY_RESTRICTION`
- **Tier**: OSS
- **Side effects**: None (read-only)
- **Example**: customer_id="+628119989630", message_type="service"

---

### 2. `send_message`

Send a free-form service text message to a customer. Only permitted when the 24-hour care window is active.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `to` | string | ✅ | Recipient phone number with country code |
  | `text` | string | ✅ | Message body text |
  | `channel` | string | ❌ | Default: `"whatsapp"` |

- **Response (success)**:
  ```json
  {
    "status": "queued",
    "message_id": "993a4661-bc4a-4a56-82ff-852dbbfa34a6"
  }
  ```

- **Error codes**: `TEMPLATE_REQUIRED`, `USER_OPTED_OUT`, `POLICY_RESTRICTION`
- **Tier**: OSS
- **Side effects**: Writes message to DB, publishes to NATS delivery stream, writes audit log on failure
- **Example**: to="+628119989630", text="Hello, how can I help?"

---

### 3. `send_template`

Send an approved WhatsApp Message Template to a customer. Bypasses the 24-hour care window.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `to` | string | ✅ | Recipient phone number with country code |
  | `template_name` | string | ✅ | Name of the approved template stored in the database |
  | `locale` | string | ❌ | Language code. Default: `"en"` |
  | `variables` | string | ❌ | JSON array of substitution parameters (e.g. `'["John", "$50"]'`) |
  | `channel` | string | ❌ | Default: `"whatsapp"` |

- **Response (success)**:
  ```json
  {
    "status": "queued",
    "message_id": "a918a562-7f1b-4ea9-a562-6bbed1ae50b8"
  }
  ```

- **Error codes**: `TEMPLATE_REQUIRED`, `USER_OPTED_OUT`, `POLICY_RESTRICTION`
- **Tier**: OSS
- **Side effects**: Writes message to DB, publishes to NATS delivery stream
- **Example**: to="+628119989630", template_name="sample_flight_confirmation", variables='["GA-123"]'

---

### 4. `explain_error`

Translate a numeric Meta Cloud API error code into a categorized, actionable explanation.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `code` | string | ✅ | Meta API numeric error code (e.g. `"131047"`) |

- **Response (success)**:
  ```json
  {
    "code": 131047,
    "category": "user_related",
    "can_retry": false,
    "human_explanation": "Re-engagement message failed because user is outside the 24-hour window.",
    "suggested_action": "Send an approved WhatsApp template instead of a free-form message."
  }
  ```

- **Error codes**: Returns fallback explanation for unmapped codes
- **Tier**: OSS
- **Side effects**: None (read-only)
- **Example**: code="131047"

---

## v2 Tools — Read-Only Intelligence (Group A)

---

### 5. `check_conversation`

Query the conversation state for a customer: 24-hour window status, time remaining, conversation type, and interaction timestamps. Use this to determine if a customer is in an active care window before attempting to send a free-form message.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `customer_id` | string | ✅ | Customer phone number with country code |
  | `channel` | string | ❌ | Default: `"whatsapp"` |

- **Response (success)**:
  ```json
  {
    "customer_id": "628119989630",
    "channel": "whatsapp",
    "window_open": true,
    "time_remaining_secs": 82345,
    "conversation_type": "service",
    "last_inbound_at": "2025-06-25T08:30:00Z",
    "last_outbound_at": "2025-06-25T09:15:00Z",
    "window_expires_at": "2025-06-26T08:30:00Z",
    "marketing_eligibility": "eligible",
    "utility_eligibility": "eligible"
  }
  ```

- **Tier**: OSS
- **Side effects**: None (read-only, uses Redis cache with PostgreSQL fallback)
- **Example**: customer_id="+628119989630"

---

### 6. `check_frequency_cap`

Check whether a customer is currently under Meta's frequency cap for marketing messages. Returns cap status, reset timing, and reason if capped.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `customer_id` | string | ✅ | Customer phone number with country code |
  | `channel` | string | ❌ | Default: `"whatsapp"` |

- **Response (success — capped)**:
  ```json
  {
    "customer_id": "628119989630",
    "channel": "whatsapp",
    "is_capped": true,
    "reason": "Daily frequency cap reached. Customer has already received 2 marketing messages today.",
    "suggested_action": "Reschedule the marketing message or try again tomorrow.",
    "reset_hint": "Frequency cap resets 24 hours after the first marketing message was sent today."
  }
  ```

- **Response (success — not capped)**:
  ```json
  {
    "customer_id": "628119989630",
    "channel": "whatsapp",
    "is_capped": false,
    "reason": "",
    "suggested_action": ""
  }
  ```

- **Tier**: OSS
- **Side effects**: None (read-only, calls Compliance Engine as black box)
- **Example**: customer_id="+628119989630"

---

### 7. `get_customer_context`

Return the full communication profile of a customer: opt-in/opt-out status, interaction timeline, segment tags, and eligibility summary for all message types. Uses dry-run compliance evaluation — no audit logs are written.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `customer_id` | string | ✅ | Customer phone number with country code |
  | `channel` | string | ❌ | Default: `"whatsapp"` |

- **Response (success)**:
  ```json
  {
    "customer_id": "628119989630",
    "channel": "whatsapp",
    "opt_in": { "marketing": true, "utility": true },
    "tags": ["vip"],
    "engagement_score": 1.0,
    "last_interaction": "2025-06-25T08:30:00Z",
    "conversation": {
      "window_open": true,
      "conversation_type": "service",
      "last_inbound": "2025-06-25T08:30:00Z",
      "last_outbound": "2025-06-25T09:15:00Z",
      "window_expires": "2025-06-26T08:30:00Z"
    },
    "eligibility": {
      "marketing": { "eligible": false, "reason_code": "POLICY_RESTRICTION", "human_explanation": "...", "suggested_action": "..." },
      "utility": { "eligible": true },
      "service": { "eligible": true }
    }
  }
  ```

- **Tier**: OSS
- **Side effects**: None (read-only, dry-run compliance — no audit log writes)
- **Example**: customer_id="+628119989630"

---

### 8. `get_delivery_status`

Return the current delivery status of a message: sent, delivered, read, or failed. Includes error code and explanation via Error Intelligence if the message failed.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `message_id` | string | ✅ | The message ID returned when the message was queued |

- **Response (success — delivered)**:
  ```json
  {
    "message_id": "wamid.HBgM...",
    "customer_id": "628119989630",
    "direction": "outbound",
    "message_type": "text",
    "status": "delivered",
    "retry_count": 0,
    "created_at": "2025-06-25T09:00:00Z",
    "updated_at": "2025-06-25T09:00:05Z"
  }
  ```

- **Response (success — failed)**:
  ```json
  {
    "message_id": "wamid.HBgM...",
    "status": "failed",
    "retry_count": 2,
    "error": {
      "code": 131047,
      "category": "user_related",
      "can_retry": false,
      "human_explanation": "Re-engagement message failed...",
      "suggested_action": "Send an approved template..."
    }
  }
  ```

- **Tier**: OSS
- **Side effects**: None (read-only)
- **Example**: message_id="wamid.HBgM123456"

---

### 9. `get_rate_limit`

Return the current state of the rate limiter: messages per second capacity, tokens consumed, tokens remaining, and last update timestamp.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `channel` | string | ❌ | Default: `"whatsapp"` |

- **Response (success)**:
  ```json
  {
    "channel": "whatsapp",
    "capacity_mps": 20,
    "tokens_remaining": 18,
    "tokens_used": 2,
    "last_update_ms": 1719308400000,
    "last_update_at": "2025-06-25T09:00:00Z"
  }
  ```

- **Tier**: OSS
- **Side effects**: None (read-only, reads from Redis token bucket)
- **Example**: channel="whatsapp"

---

### 10. `list_conversations`

Return a paginated list of conversations with their current state. The `expiring_soon` filter selects windows closing within the next 2 hours — this is a derived filter computed at query time, not a stored column.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `channel` | string | ❌ | Default: `"whatsapp"` |
  | `status_filter` | string | ❌ | `"open"`, `"closed"`, `"expiring_soon"`. Empty = all |
  | `limit` | number | ❌ | Default: 50 |
  | `offset` | number | ❌ | Default: 0 |

- **Response (success)**:
  ```json
  {
    "conversations": [
      {
        "id": "uuid-...",
        "customer_id": "628119989630",
        "channel": "whatsapp",
        "window_open": true,
        "time_remaining_secs": 82345,
        "conversation_type": "service",
        "last_inbound_at": "2025-06-25T08:30:00Z",
        "last_outbound_at": null,
        "window_expires_at": "2025-06-26T08:30:00Z"
      }
    ],
    "total": 42,
    "limit": 50,
    "offset": 0
  }
  ```

- **Tier**: OSS
- **Side effects**: None (read-only, paginated at DB level)
- **Example**: channel="whatsapp", status_filter="expiring_soon", limit=10

---

### 11. `list_templates`

List WhatsApp message templates with optional filters for status, category, and locale. Results are paginated at the database level.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `status_filter` | string | ❌ | e.g. `"APPROVED"`, `"PENDING"`, `"REJECTED"` |
  | `category_filter` | string | ❌ | e.g. `"marketing"`, `"utility"` |
  | `locale_filter` | string | ❌ | e.g. `"en"`, `"id"` |
  | `limit` | number | ❌ | Default: 50 |
  | `offset` | number | ❌ | Default: 0 |

- **Response (success)**:
  ```json
  {
    "templates": [
      {
        "name": "sample_flight_confirmation",
        "locale": "en",
        "category": "utility",
        "status": "APPROVED",
        "body_text": "Your flight has been confirmed. Details: {{1}}"
      }
    ],
    "total": 5,
    "limit": 50,
    "offset": 0
  }
  ```

- **Tier**: OSS
- **Side effects**: None (read-only)
- **Example**: status_filter="APPROVED", category_filter="utility"

---

## v2 Tools — Action Operations (Group B)

---

### 12. `reply_customer`

Send a free-form reply within an active conversation context. Requires an open 24-hour care window — if the window is closed, returns a clear error directing the caller to use `send_template` instead. Traverses the full Compliance → Policy → Orchestrator send chain.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `customer_id` | string | ✅ | Customer phone number with country code |
  | `message_text` | string | ✅ | Body text content of the reply |
  | `channel` | string | ❌ | Default: `"whatsapp"` |

- **Response (success)**:
  ```json
  { "status": "queued", "message_id": "uuid-..." }
  ```

- **Error codes**: `WINDOW_CLOSED` (use send_template), `USER_OPTED_OUT`, `POLICY_RESTRICTION`
- **Tier**: OSS
- **Side effects**: Writes message to DB, publishes to NATS, writes audit log on NATS failure
- **Example**: customer_id="+628119989630", message_text="Thanks for your message!"

---

### 13. `retry_failed_messages`

Trigger manual retry for one or more failed messages. Validates each message is actually `failed`, checks retryability via Error Intelligence (non-retryable permanent failures are skipped), re-runs compliance check at retry time, and re-queues retryable messages to NATS.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `message_ids` | string | ✅ | JSON array of message ID strings, e.g. `'["msg_123", "msg_456"]'` |

- **Response (success)**:
  ```json
  [
    { "message_id": "msg_123", "status": "retried" },
    { "message_id": "msg_456", "status": "skipped", "reason": "error 131049 is not retryable: Frequency cap reached." }
  ]
  ```

- **Tier**: OSS
- **Side effects**: Updates message status in DB, publishes to NATS, writes audit log
- **Example**: message_ids='["wamid.HBgM123", "wamid.HBgM456"]'

---

### 14. `sync_template_status`

Syncs the approval status of a specific template from Meta's API. Pulls the latest status from Meta and updates the local database. Does not grant or expedite approval — template approval is Meta's decision. Use this when webhook delivery may have been missed or delayed.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `template_name` | string | ✅ | Name of the template to check |
  | `locale` | string | ❌ | Default: `"en"` |

- **Response (success)**:
  ```json
  {
    "template_name": "sample_flight_confirmation",
    "locale": "en",
    "status": "APPROVED",
    "category": "utility"
  }
  ```

- **Response (rejected)**:
  ```json
  {
    "template_name": "my_template",
    "locale": "en",
    "status": "REJECTED",
    "category": "marketing",
    "reason": "Template was rejected by Meta. Review the template content and resubmit.",
    "suggested_action": "Check Meta Business Manager for specific rejection feedback."
  }
  ```

- **Tier**: OSS
- **Side effects**: Makes live API call to Meta, updates local DB via SyncTemplates
- **Example**: template_name="sample_flight_confirmation", locale="en"

---

## v2 Tools — Scheduling & Campaign (Group C)

---

### 15. `schedule_message`

Schedule a single message for future delivery. Compliance check runs at scheduling time (opt-out only) and again at delivery time (full pipeline including 24-hour window state). The message is persisted with `status=scheduled` and picked up by the scheduler.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `customer_id` | string | ✅ | Customer phone number with country code |
  | `message_type` | string | ✅ | `"marketing"`, `"utility"`, `"service"`, `"template"` |
  | `content` | string | ✅ | JSON message payload, e.g. `'{"type":"text","text":{"body":"Hello"}}'` |
  | `deliver_at` | string | ✅ | RFC3339 timestamp, e.g. `"2025-06-26T14:00:00Z"`. Must be in the future. |
  | `channel` | string | ❌ | Default: `"whatsapp"` |

- **Response (success)**:
  ```json
  {
    "message_id": "sch_uuid-...",
    "status": "scheduled",
    "deliver_at": "2025-06-26T14:00:00Z",
    "customer_id": "628119989630"
  }
  ```

- **Error codes**: `USER_OPTED_OUT` (blocked at scheduling time)
- **Tier**: OSS
- **Side effects**: Writes to messages table (status=scheduled), writes audit log. Full compliance check runs again at dispatch time by the scheduler.
- **Example**: customer_id="+628119989630", message_type="marketing", content='{"type":"text","text":{"body":"Sale starts!"}}', deliver_at="2025-06-26T09:00:00Z"

---

### 16. `schedule_campaign`

**[Pro Tier]** Schedule a broadcast campaign. Validates template is approved, deliver_at is in the future. Returns campaign ID. The scheduler transitions the campaign to `running` at delivery time and enqueues individual messages per recipient.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `name` | string | ✅ | Campaign name |
  | `type` | string | ✅ | `"broadcast"`, `"marketing"`, `"utility"` |
  | `template_name` | string | ✅ | Name of the approved template |
  | `deliver_at` | string | ✅ | RFC3339 timestamp. Must be in the future. |
  | `locale` | string | ❌ | Default: `"en"` |
  | `variables` | string | ❌ | JSON object of template variables, e.g. `'{"1":"John"}'` |
  | `audience_filter` | string | ❌ | JSON object for audience segmentation |

- **Response (success)**:
  ```json
  {
    "campaign_id": "uuid-...",
    "status": "scheduled",
    "name": "Summer Sale",
    "type": "marketing",
    "deliver_at": "2025-06-26T09:00:00Z"
  }
  ```

- **Error codes**: `FEATURE_NOT_AVAILABLE` (OSS tier), `TEMPLATE_NOT_APPROVED`
- **Tier**: Pro+
- **OSS upgrade message**: `"This feature requires the pro tier. Your current tier is oss. Upgrade to pro or higher to access this feature."`
- **Side effects**: Writes to campaigns table, writes audit log
- **Example**: name="Summer Sale", type="marketing", template_name="sample_purchase_feedback", deliver_at="2025-06-26T09:00:00Z"

---

### 17. `cancel_campaign`

**[Pro Tier]** Cancel a campaign in any non-terminal state (scheduled, running, paused). Completed, failed, or already-cancelled campaigns cannot be cancelled.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `campaign_id` | string | ✅ | The campaign ID to cancel |
  | `reason` | string | ❌ | Cancellation reason for audit trail |

- **Response (success)**:
  ```json
  {
    "campaign_id": "uuid-...",
    "status": "cancelled",
    "from_status": "scheduled"
  }
  ```

- **Error codes**: `FEATURE_NOT_AVAILABLE` (OSS), `CAMPAIGN_TERMINAL_STATE`
- **Tier**: Pro+
- **Side effects**: Updates campaign status in DB, writes audit log. Messages already dispatched are not recalled.
- **Example**: campaign_id="uuid-...", reason="Customer request"

---

### 18. `pause_campaign`

**[Pro Tier]** Pause an actively running or scheduled campaign. Preserves progress counters (sent_count, delivered_count, failed_count) and paused_at timestamp for future resume. Only `running`, `sending`, or `scheduled` campaigns can be paused.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `campaign_id` | string | ✅ | The campaign ID to pause |

- **Response (success)**:
  ```json
  {
    "campaign_id": "uuid-...",
    "status": "paused",
    "from_status": "running",
    "note": "Campaign paused. Progress counters preserved for future resume."
  }
  ```

- **Error codes**: `FEATURE_NOT_AVAILABLE` (OSS), `INVALID_STATE_TRANSITION`
- **Tier**: Pro+
- **Side effects**: Updates campaign status and paused_at in DB, writes audit log
- **Example**: campaign_id="uuid-..."

---

## v2 Tools — Account & Cost Intelligence (Group D)

---

### 19. `get_account_quality`

Retrieve the current WABA quality score and messaging limit tier from Meta. This is a live API call to Meta's Business Management endpoint — not a cached value.

- **Parameters**: None

- **Response (success)**:
  ```json
  {
    "waba_id": "2926170004382000",
    "name": "My Business",
    "quality_tier": "green",
    "quality_rating": "HIGH",
    "current_limit": "1000",
    "limit_tier": "TIER_1K"
  }
  ```

- **Tier**: OSS
- **Side effects**: Makes live API call to Meta
- **Example**: (no parameters)

---

### 20. `estimate_cost`

Estimate the conversation cost for a planned message batch before sending. Computes cost based on the current Meta conversation pricing model stored in the system. No API calls to Meta.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `message_type` | string | ✅ | `"marketing"`, `"utility"`, `"authentication"`, `"service"` |
  | `recipient_country` | string | ❌ | ISO country code (e.g. `"ID"`, `"US"`). Default: `"DEFAULT"` |
  | `quantity` | number | ❌ | Number of messages. Default: 1 |

- **Response (success)**:
  ```json
  {
    "message_type": "marketing",
    "recipient_country": "ID",
    "quantity": 100,
    "cost_per_conversation": "0.0440",
    "total_estimated_cost": "4.4000",
    "currency": "USD",
    "pricing_category": "marketing",
    "pricing_model_version": "Meta Conversation-Based Pricing (2024)"
  }
  ```

- **Tier**: OSS
- **Side effects**: None (read-only, DB lookup)
- **Example**: message_type="marketing", recipient_country="ID", quantity=100

---

### 21. `estimate_pricing`

Return the current conversation pricing tier for a specific country and conversation type combination. Lookup against the pricing knowledge base.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `conversation_type` | string | ✅ | `"marketing"`, `"utility"`, `"authentication"`, `"service"` |
  | `recipient_country` | string | ❌ | ISO country code. Default: `"DEFAULT"` |

- **Response (success)**:
  ```json
  {
    "country": "ID",
    "conversation_type": "marketing",
    "cost_per_conversation": 0.0440,
    "currency": "USD",
    "pricing_category": "marketing",
    "effective_date": "2025-06-25"
  }
  ```

- **Tier**: OSS
- **Side effects**: None (read-only, DB lookup)
- **Example**: conversation_type="marketing", recipient_country="ID"

---

## v2 Tools — Operational (Group E)

---

### 22. `create_template`

Submit a new template to Meta for approval. Validates template configuration locally before submitting (name length, body length, valid category). On success, persists the template locally with `status=PENDING`. Approval arrives asynchronously via webhook.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `name` | string | ✅ | Template name (max 512 chars) |
  | `category` | string | ✅ | `"UTILITY"`, `"MARKETING"`, `"AUTHENTICATION"` |
  | `language` | string | ✅ | Language code (e.g. `"en"`, `"id"`) |
  | `body_text` | string | ✅ | Template body text (max 1024 chars) |

- **Response (success)**:
  ```json
  {
    "template_name": "order_update_v2",
    "locale": "en",
    "category": "UTILITY",
    "status": "PENDING",
    "meta_id": "mock-template-id-123",
    "note": "Template submitted to Meta for approval. Approval will arrive via webhook."
  }
  ```

- **Tier**: OSS
- **Side effects**: Makes live API call to Meta, writes to templates table, writes audit log
- **Example**: name="order_update_v2", category="UTILITY", language="en", body_text="Your order {{1}} has been shipped."

---

### 23. `sync_webhooks`

Trigger a manual re-sync of template statuses and pending webhook events from Meta. Use for recovery scenarios where webhooks may have been missed. Operation is idempotent.

- **Parameters**: None

- **Response (success)**:
  ```json
  {
    "template_synced": true,
    "messages_reconciled": 3
  }
  ```

- **Tier**: OSS
- **Side effects**: Makes live API call to Meta (template sync), updates templates table, writes audit log
- **Example**: (no parameters)

---

### 24. `archive_chat`

Transition a conversation to `archived` state. Only conversations with expired windows (or no window) can be archived. Active conversations cannot be archived. Archived conversations are excluded from `list_conversations` by default.

- **Parameters**:

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `conversation_id` | string | ✅ | The conversation UUID to archive |

- **Response (success)**:
  ```json
  {
    "conversation_id": "uuid-...",
    "status": "archived",
    "from_type": "service"
  }
  ```

- **Error codes**: `CONVERSATION_ACTIVE` (cannot archive active window)
- **Tier**: OSS
- **Side effects**: Updates conversations table, writes audit log
- **Example**: conversation_id="uuid-..."