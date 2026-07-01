#!/bin/bash
# Meta Business MCP — 24 Tools Test (Real WABA)
# Each test spawns a fresh server that reads from mounted config.yaml

DOCKER="/mnt/c/Program Files/Docker/Docker/resources/bin/docker.exe"
WIN_CONFIG='Z:\01 ADAM\00 DOKUMENTASI PROJECT\Meta Business MCP\config.yaml'
WIN_POLICIES='Z:\01 ADAM\00 DOKUMENTASI PROJECT\Meta Business MCP\policies.yaml'
RESULTS_DIR="/mnt/z/01 ADAM/00 DOKUMENTASI PROJECT/Meta Business MCP/tests/results"
mkdir -p "$RESULTS_DIR"

PHONE="+6281119273555"

run_mcp() {
    local label="$1"
    local requests="$2"
    local outfile="$RESULTS_DIR/$label.txt"
    
    printf "%-50s" "  Testing: $label"
    
    local result
    result=$(echo "$requests" | "$DOCKER" run --rm -i \
        --network metabusinessmcp_default \
        -e DB_HOST=mcp_postgres_real \
        -e DB_PORT=5432 \
        -e DB_USER=postgres \
        -e DB_NAME=meta_mcp \
        -e DB_SSLMODE=disable \
        -e REDIS_ADDR=mcp_redis_real:6379 \
        -e NATS_URL=nats://mcp_nats_real:4222 \
        -e SERVER_HTTP_PORT=9999 \
        -e TIER=pro \
        -v "$WIN_CONFIG:/app/config.yaml:ro" \
        -v "$WIN_POLICIES:/app/policies.yaml:ro" \
        metabusinessmcp-app:latest \
        /app/server 2>/dev/null)
    
    echo "$result" > "$outfile"
    
    # Extract the tool call response (last JSON line)
    local tool_resp
    tool_resp=$(echo "$result" | grep '"id":10' || echo "$result" | tail -1)
    
    if echo "$tool_resp" | grep -q '"isError"'; then
        if echo "$tool_resp" | grep -q '"isError":true'; then
            echo "FAIL (error response)"
        else
            echo "PASS"
        fi
    elif echo "$tool_resp" | grep -q '"result"'; then
        echo "PASS"
    elif echo "$tool_resp" | grep -q '"error"'; then
        echo "FAIL (json-rpc error)"
    else
        echo "WARN (no response)"
    fi
}

build_req() {
    # Build: init + notification + tool call
    local tool="$1"
    local params="$2"
    printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}\n{"jsonrpc":"2.0","method":"notifications/initialized"}\n{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"%s","arguments":%s}}\n' "$tool" "$params"
}

echo "=========================================================="
echo "  META BUSINESS MCP — 24 TOOLS INTEGRATION TEST"
echo "  Target: Real WABA (2926170004382000)"
echo "  Phone:  $PHONE"
echo "  Time:   $(date -Iseconds)"
echo "=========================================================="
echo ""

# ═══ GROUP A: Read-Only Intelligence ═══
echo "═══ GROUP A: Read-Only Intelligence (7 tools) ═══"

run_mcp "A01_check_conversation" \
    "$(build_req "check_conversation" "{\"customer_id\":\"$PHONE\",\"channel\":\"whatsapp\"}")"

run_mcp "A02_check_frequency_cap" \
    "$(build_req "check_frequency_cap" "{\"customer_id\":\"$PHONE\",\"channel\":\"whatsapp\"}")"

run_mcp "A03_get_customer_context" \
    "$(build_req "get_customer_context" "{\"customer_id\":\"$PHONE\",\"channel\":\"whatsapp\"}")"

run_mcp "A04_get_delivery_status" \
    "$(build_req "get_delivery_status" "{\"message_id\":\"test-nonexistent-msg\"}")"

run_mcp "A05_get_rate_limit" \
    "$(build_req "get_rate_limit" "{\"channel\":\"whatsapp\"}")"

run_mcp "A06_list_conversations" \
    "$(build_req "list_conversations" "{\"channel\":\"whatsapp\",\"limit\":10}")"

run_mcp "A07_list_templates" \
    "$(build_req "list_templates" "{\"limit\":20}")"

# ═══ GROUP B: Action Operations ═══
echo ""
echo "═══ GROUP B: Action Operations (7 tools) ═══"

run_mcp "B01_check_compliance" \
    "$(build_req "check_compliance" "{\"customer_id\":\"$PHONE\",\"message_type\":\"marketing\",\"channel\":\"whatsapp\"}")"

run_mcp "B02_send_message" \
    "$(build_req "send_message" "{\"to\":\"$PHONE\",\"text\":\"MCP Integration Test — 24 tools testing from Meta Business MCP\",\"channel\":\"whatsapp\"}")"

run_mcp "B03_send_template" \
    "$(build_req "send_template" "{\"to\":\"$PHONE\",\"template_name\":\"hello_world\",\"locale\":\"en_US\"}")"

run_mcp "B04_explain_error" \
    "$(build_req "explain_error" "{\"code\":\"131047\"}")"

run_mcp "B05_reply_customer" \
    "$(build_req "reply_customer" "{\"customer_id\":\"$PHONE\",\"message_text\":\"MCP Test Reply in care window\",\"channel\":\"whatsapp\"}")"

run_mcp "B06_retry_failed_messages" \
    "$(build_req "retry_failed_messages" "{\"message_ids\":\"[\\\"nonexistent-msg-id\\\"]\"}")"

run_mcp "B07_sync_template_status" \
    "$(build_req "sync_template_status" "{\"template_name\":\"hello_world\",\"locale\":\"en_US\"}")"

# ═══ GROUP C: Scheduling & Campaign ═══
echo ""
echo "═══ GROUP C: Scheduling & Campaign (4 tools) ═══"

run_mcp "C01_schedule_message" \
    "$(build_req "schedule_message" "{\"customer_id\":\"$PHONE\",\"message_type\":\"marketing\",\"content\":\"{\\\"type\\\":\\\"text\\\",\\\"text\\\":{\\\"body\\\":\\\"Scheduled test message\\\"}}\",\"deliver_at\":\"2026-06-26T10:00:00Z\",\"channel\":\"whatsapp\"}")"

run_mcp "C02_schedule_campaign" \
    "$(build_req "schedule_campaign" "{\"name\":\"MCP Test Campaign\",\"type\":\"marketing\",\"template_name\":\"hello_world\",\"deliver_at\":\"2026-06-26T14:00:00Z\",\"locale\":\"en_US\"}")"

run_mcp "C03_cancel_campaign" \
    "$(build_req "cancel_campaign" "{\"campaign_id\":\"00000000-0000-0000-0000-000000000000\",\"reason\":\"test\"}")"

run_mcp "C04_pause_campaign" \
    "$(build_req "pause_campaign" "{\"campaign_id\":\"00000000-0000-0000-0000-000000000000\"}")"

# ═══ GROUP D: Account & Cost Intelligence ═══
echo ""
echo "═══ GROUP D: Account & Cost Intelligence (3 tools) ═══"

run_mcp "D01_get_account_quality" \
    "$(build_req "get_account_quality" "{}")"

run_mcp "D02_estimate_cost" \
    "$(build_req "estimate_cost" "{\"message_type\":\"marketing\",\"recipient_country\":\"ID\",\"quantity\":100}")"

run_mcp "D03_estimate_pricing" \
    "$(build_req "estimate_pricing" "{\"conversation_type\":\"marketing\",\"recipient_country\":\"ID\"}")"

# ═══ GROUP E: Operational ═══
echo ""
echo "═══ GROUP E: Operational (3 tools) ═══"

run_mcp "E01_create_template" \
    "$(build_req "create_template" "{\"name\":\"mcp_test_$(date +%s)\",\"category\":\"UTILITY\",\"language\":\"en\",\"body_text\":\"Your order {{1}} is ready for pickup.\"}")"

run_mcp "E02_sync_webhooks" \
    "$(build_req "sync_webhooks" "{}")"

run_mcp "E03_archive_chat" \
    "$(build_req "archive_chat" "{\"conversation_id\":\"00000000-0000-0000-0000-000000000000\"}")"

echo ""
echo "=========================================================="
echo "  ALL 24 TOOLS TESTED"
echo "  Results: $RESULTS_DIR/"
echo "  Time: $(date -Iseconds)"
echo "=========================================================="
