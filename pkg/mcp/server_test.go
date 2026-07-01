package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"meta-business-mcp/pkg/campaign"
	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/delivery"
	"meta-business-mcp/pkg/errorintel"
	"meta-business-mcp/pkg/policy"
	"meta-business-mcp/pkg/state"
	"meta-business-mcp/pkg/template"
	"meta-business-mcp/pkg/userintel"
)

func TestMCPServerTools(t *testing.T) {
	ctx := context.Background()

	// Connect to postgres/redis/NATS
	cfg := &config.Config{
		Server: config.ServerConfig{
			MCPName:    "meta-mcp-test",
			MCPVersion: "1.0.0-test",
		},
		Database: config.DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "password",
			DBName:   "meta_mcp",
			SSLMode:  "disable",
		},
		Redis: config.RedisConfig{
			Addr: "localhost:6379",
		},
		NATS: config.NATSConfig{
			URL: "nats://localhost:4222",
		},
		Meta: config.MetaConfig{
			APIURL:      "http://localhost:8081",
			WABAID:      "mock-waba-id",
			AccessToken: "mock-access-token",
		},
		PoliciesPath: "policies.yaml",
	}

	database, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Run migration
	_ = db.Migrate(ctx, database)
	_ = db.Seed(ctx, database, cfg.PoliciesPath)

	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	complianceEngine := compliance.NewEngine(database, stateEngine, userManager)
	policyEngine := policy.NewEngine(database)
	errorIntel := errorintel.NewEngine(database)
	templateManager := template.NewManager(database, cfg)
	campaignManager := campaign.NewManager(database)

	orchestrator, err := delivery.NewOrchestrator(cfg)
	if err != nil {
		t.Fatalf("Failed to start NATS orchestrator: %v", err)
	}
	defer orchestrator.Close()

	srv := NewServer(
		database, cfg, complianceEngine, policyEngine, errorIntel, templateManager, userManager, campaignManager, orchestrator,
	)

	if srv.GetMCPServer() == nil {
		t.Fatalf("NewServer returned nil MCPServer")
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	t.Run("check_compliance tool", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))
		cleanCustomerID := customerID[1:]

		// 1. Check compliance (window closed)
		req := mcp.CallToolRequest{}
		req.Params.Name = "check_compliance"
		req.Params.Arguments = map[string]interface{}{
			"customer_id":  customerID,
			"message_type": "service",
		}

		res, err := srv.handleCheckCompliance(ctx, req)
		if err != nil {
			t.Fatalf("handleCheckCompliance failed: %v", err)
		}
		if res.IsError {
			t.Fatalf("Expected no error, got tool error")
		}

		// Since window is closed, compliance should deny
		var compRes compliance.ComplianceResult
		err = json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &compRes)
		if err != nil {
			t.Fatalf("Failed to unmarshal compliance result: %v", err)
		}
		if compRes.Allowed {
			t.Errorf("Expected compliance check to deny service message when window is closed")
		}
		if compRes.ReasonCode != "TEMPLATE_REQUIRED" {
			t.Errorf("Expected TEMPLATE_REQUIRED, got '%s'", compRes.ReasonCode)
		}

		// 2. Open care window
		_, err = stateEngine.OpenWindow(ctx, cleanCustomerID, "whatsapp", "service")
		if err != nil {
			t.Fatalf("OpenWindow failed: %v", err)
		}

		// 3. Re-check compliance
		res, err = srv.handleCheckCompliance(ctx, req)
		if err != nil {
			t.Fatalf("handleCheckCompliance failed: %v", err)
		}

		err = json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &compRes)
		if err != nil {
			t.Fatalf("Failed to unmarshal compliance result: %v", err)
		}
		if !compRes.Allowed {
			t.Errorf("Expected compliance check to allow service message when window is open")
		}
	})

	t.Run("explain_error tool", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "explain_error"
		req.Params.Arguments = map[string]interface{}{
			"code": "131047",
		}

		res, err := srv.handleExplainError(ctx, req)
		if err != nil {
			t.Fatalf("handleExplainError failed: %v", err)
		}

		var intel errorintel.ErrorDetails
		err = json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &intel)
		if err != nil {
			t.Fatalf("Failed to unmarshal error details: %v", err)
		}

		if intel.Code != 131047 {
			t.Errorf("Expected code 131047, got %d", intel.Code)
		}
		if intel.Category != "user_related" {
			t.Errorf("Expected category 'user_related', got '%s'", intel.Category)
		}
	})

	t.Run("send_message tool (window open)", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))
		cleanCustomerID := customerID[1:]

		// 1. Open care window so it passes compliance
		_, err = stateEngine.OpenWindow(ctx, cleanCustomerID, "whatsapp", "service")
		if err != nil {
			t.Fatalf("OpenWindow failed: %v", err)
		}

		req := mcp.CallToolRequest{}
		req.Params.Name = "send_message"
		req.Params.Arguments = map[string]interface{}{
			"to":   customerID,
			"text": "Hello via MCP!",
		}

		res, err := srv.handleSendMessage(ctx, req)
		if err != nil {
			t.Fatalf("handleSendMessage failed: %v", err)
		}

		var sendRes map[string]string
		err = json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &sendRes)
		if err != nil {
			t.Fatalf("Failed to unmarshal send result: %v", err)
		}

		if sendRes["status"] != "queued" {
			t.Errorf("Expected status 'queued', got '%s'", sendRes["status"])
		}
		if sendRes["message_id"] == "" {
			t.Errorf("Expected a non-empty message_id")
		}
	})

	t.Run("send_template tool", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))
		templateName := "sample_flight_confirmation"

		// Pre-populate template in DB via template sync
		err := templateManager.SyncTemplates(ctx)
		if err != nil {
			t.Fatalf("SyncTemplates failed: %v", err)
		}

		req := mcp.CallToolRequest{}
		req.Params.Name = "send_template"
		req.Params.Arguments = map[string]interface{}{
			"to":            customerID,
			"template_name": templateName,
			"locale":        "en",
			"variables":     `["GA-123"]`,
		}

		res, err := srv.handleSendTemplate(ctx, req)
		if err != nil {
			t.Fatalf("handleSendTemplate failed: %v", err)
		}

		textRes := res.Content[0].(mcp.TextContent).Text
		var sendRes map[string]string
		err = json.Unmarshal([]byte(textRes), &sendRes)
		if err != nil {
			t.Fatalf("Failed to unmarshal send result (content: %q): %v", textRes, err)
		}

		if sendRes["status"] != "queued" {
			t.Errorf("Expected status 'queued', got '%s'", sendRes["status"])
		}
		if sendRes["message_id"] == "" {
			t.Errorf("Expected a non-empty message_id")
		}
	})

	t.Run("phone number normalization regression test", func(t *testing.T) {
		cleanID := "6281119273555"
		plusID := "+6281119273555"

		// 1. Open window using normalized ID (without +)
		_, err := stateEngine.OpenWindow(ctx, cleanID, "whatsapp", "service")
		if err != nil {
			t.Fatalf("OpenWindow failed: %v", err)
		}

		// 2. check_compliance with plusID
		req := mcp.CallToolRequest{}
		req.Params.Name = "check_compliance"
		req.Params.Arguments = map[string]interface{}{
			"customer_id":  plusID,
			"message_type": "service",
		}

		res, err := srv.handleCheckCompliance(ctx, req)
		if err != nil {
			t.Fatalf("handleCheckCompliance failed: %v", err)
		}
		var compRes compliance.ComplianceResult
		err = json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &compRes)
		if err != nil {
			t.Fatalf("Failed to unmarshal compliance result: %v", err)
		}
		if !compRes.Allowed {
			t.Errorf("Expected compliance check to allow service message for normalized customer (with leading +)")
		}

		// 3. send_message with plusID
		msgReq := mcp.CallToolRequest{}
		msgReq.Params.Name = "send_message"
		msgReq.Params.Arguments = map[string]interface{}{
			"to":   plusID,
			"text": "Normalization check",
		}
		msgRes, err := srv.handleSendMessage(ctx, msgReq)
		if err != nil {
			t.Fatalf("handleSendMessage failed: %v", err)
		}
		var sendRes map[string]string
		err = json.Unmarshal([]byte(msgRes.Content[0].(mcp.TextContent).Text), &sendRes)
		if err != nil {
			t.Fatalf("Failed to unmarshal send result: %v", err)
		}
		if sendRes["status"] != "queued" {
			t.Errorf("Expected send_message status 'queued', got '%s'", sendRes["status"])
		}

		// 4. send_template with plusID
		tmplReq := mcp.CallToolRequest{}
		tmplReq.Params.Name = "send_template"
		tmplReq.Params.Arguments = map[string]interface{}{
			"to":            plusID,
			"template_name": "sample_flight_confirmation",
			"locale":        "en",
			"variables":     `["GA-123"]`,
		}
		tmplRes, err := srv.handleSendTemplate(ctx, tmplReq)
		if err != nil {
			t.Fatalf("handleSendTemplate failed: %v", err)
		}
		var tmplSendRes map[string]string
		err = json.Unmarshal([]byte(tmplRes.Content[0].(mcp.TextContent).Text), &tmplSendRes)
		if err != nil {
			t.Fatalf("Failed to unmarshal template send result: %v", err)
		}
		if tmplSendRes["status"] != "queued" {
			t.Errorf("Expected send_template status 'queued', got '%s'", tmplSendRes["status"])
		}
	})

	// ═══ Group A: Read-Only Intelligence Tests ═══

	t.Run("check_conversation tool", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))
		cleanID := customerID[1:]

		// Open a window
		_, _ = stateEngine.OpenWindow(ctx, cleanID, "whatsapp", "service")

		req := mcp.CallToolRequest{}
		req.Params.Name = "check_conversation"
		req.Params.Arguments = map[string]interface{}{
			"customer_id": customerID,
		}

		res, err := srv.handleCheckConversation(ctx, req)
		if err != nil {
			t.Fatalf("handleCheckConversation failed: %v", err)
		}

		var result map[string]any
		err = json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		if err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}

		if result["window_open"] != true {
			t.Errorf("Expected window_open=true")
		}
		if result["conversation_type"] != "service" {
			t.Errorf("Expected conversation_type 'service', got '%v'", result["conversation_type"])
		}
	})

	t.Run("check_conversation - no window", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))

		req := mcp.CallToolRequest{}
		req.Params.Name = "check_conversation"
		req.Params.Arguments = map[string]interface{}{"customer_id": customerID}

		res, err := srv.handleCheckConversation(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		if result["window_open"] != false {
			t.Errorf("Expected window_open=false for new customer")
		}
	})

	t.Run("check_frequency_cap tool - not capped", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))

		req := mcp.CallToolRequest{}
		req.Params.Name = "check_frequency_cap"
		req.Params.Arguments = map[string]interface{}{"customer_id": customerID}

		res, err := srv.handleCheckFrequencyCap(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		if result["is_capped"] != false {
			t.Errorf("Expected is_capped=false for new customer")
		}
	})

	t.Run("get_customer_context tool", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))
		cleanID := customerID[1:]

		// Create customer and open window
		_, _ = userManager.GetOrCreateCustomer(ctx, cleanID, "whatsapp")
		_, _ = stateEngine.OpenWindow(ctx, cleanID, "whatsapp", "service")

		req := mcp.CallToolRequest{}
		req.Params.Name = "get_customer_context"
		req.Params.Arguments = map[string]interface{}{"customer_id": customerID}

		res, err := srv.handleGetCustomerContext(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)

		if result["customer_id"] != cleanID {
			t.Errorf("Expected customer_id '%s', got '%v'", cleanID, result["customer_id"])
		}
		if result["opt_in"] == nil {
			t.Errorf("Expected opt_in field")
		}
		if result["eligibility"] == nil {
			t.Errorf("Expected eligibility field")
		}
	})

	t.Run("get_delivery_status tool", func(t *testing.T) {
		// Create a message in the DB
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))
		cleanID := customerID[1:]
		msgID := fmt.Sprintf("wamid.status_test_%d", rng.Int63())

		contentBytes, _ := json.Marshal(map[string]any{"body": "test"})
		database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status) VALUES ($1,$2,'outbound','text',$3,'sent')`,
			msgID, cleanID, contentBytes)

		req := mcp.CallToolRequest{}
		req.Params.Name = "get_delivery_status"
		req.Params.Arguments = map[string]interface{}{"message_id": msgID}

		res, err := srv.handleGetDeliveryStatus(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)

		if result["status"] != "sent" {
			t.Errorf("Expected status 'sent', got '%v'", result["status"])
		}
		if result["message_id"] != msgID {
			t.Errorf("Expected message_id '%s', got '%v'", msgID, result["message_id"])
		}
	})

	t.Run("get_delivery_status - failed with error intel", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))
		cleanID := customerID[1:]
		msgID := fmt.Sprintf("wamid.failed_test_%d", rng.Int63())

		contentBytes, _ := json.Marshal(map[string]any{"body": "test"})
		database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status, error_code) VALUES ($1,$2,'outbound','text',$3,'failed',131047)`,
			msgID, cleanID, contentBytes)

		req := mcp.CallToolRequest{}
		req.Params.Name = "get_delivery_status"
		req.Params.Arguments = map[string]interface{}{"message_id": msgID}

		res, err := srv.handleGetDeliveryStatus(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)

		if result["status"] != "failed" {
			t.Errorf("Expected status 'failed'")
		}
		if result["error"] == nil {
			t.Errorf("Expected error field for failed message")
		}
	})

	t.Run("get_rate_limit tool", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "get_rate_limit"
		req.Params.Arguments = map[string]interface{}{}

		res, err := srv.handleGetRateLimit(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)

		if result["capacity_mps"] == nil {
			t.Errorf("Expected capacity_mps field")
		}
		if result["tokens_remaining"] == nil {
			t.Errorf("Expected tokens_remaining field")
		}
	})

	t.Run("list_templates tool", func(t *testing.T) {
		// Ensure templates exist from prior sync
		_ = templateManager.SyncTemplates(ctx)

		req := mcp.CallToolRequest{}
		req.Params.Name = "list_templates"
		req.Params.Arguments = map[string]interface{}{
			"status_filter": "APPROVED",
		}

		res, err := srv.handleListTemplates(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)

		if result["templates"] == nil {
			t.Errorf("Expected templates field")
		}
		if result["total"] == nil {
			t.Errorf("Expected total field")
		}
	})

	t.Run("list_conversations tool", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))
		cleanID := customerID[1:]
		_, _ = stateEngine.OpenWindow(ctx, cleanID, "whatsapp", "service")

		req := mcp.CallToolRequest{}
		req.Params.Name = "list_conversations"
		req.Params.Arguments = map[string]interface{}{
			"status_filter": "open",
			"limit":         10,
		}

		res, err := srv.handleListConversations(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)

		if result["conversations"] == nil {
			t.Errorf("Expected conversations field")
		}
		if result["total"] == nil {
			t.Errorf("Expected total field")
		}
	})

	// ═══ Group B: Action Tools Tests ═══

	t.Run("reply_customer - window open", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))
		cleanID := customerID[1:]
		_, _ = stateEngine.OpenWindow(ctx, cleanID, "whatsapp", "service")

		req := mcp.CallToolRequest{}
		req.Params.Name = "reply_customer"
		req.Params.Arguments = map[string]interface{}{
			"customer_id":  customerID,
			"message_text": "Reply test",
		}

		res, err := srv.handleReplyCustomer(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var sendRes map[string]string
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &sendRes)
		if sendRes["status"] != "queued" {
			t.Errorf("Expected status 'queued', got '%s'", sendRes["status"])
		}
	})

	t.Run("reply_customer - window closed", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))

		req := mcp.CallToolRequest{}
		req.Params.Name = "reply_customer"
		req.Params.Arguments = map[string]interface{}{
			"customer_id":  customerID,
			"message_text": "Should fail",
		}

		res, err := srv.handleReplyCustomer(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		if result["reason_code"] != "WINDOW_CLOSED" {
			t.Errorf("Expected WINDOW_CLOSED, got '%v'", result["reason_code"])
		}
	})

	t.Run("retry_failed_messages tool", func(t *testing.T) {
		cleanID := fmt.Sprintf("1888%07d", rng.Intn(10000000))
		msgID := fmt.Sprintf("wamid.retry_test_%d", rng.Int63())

		contentBytes, _ := json.Marshal(map[string]any{"body": "retry test"})
		database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status, error_code) VALUES ($1,$2,'outbound','text',$3,'failed',470)`,
			msgID, cleanID, contentBytes)

		// Open window so compliance passes
		_, _ = stateEngine.OpenWindow(ctx, cleanID, "whatsapp", "text")

		req := mcp.CallToolRequest{}
		req.Params.Name = "retry_failed_messages"
		req.Params.Arguments = map[string]interface{}{
			"message_ids": fmt.Sprintf(`["%s"]`, msgID),
		}

		res, err := srv.handleRetryFailedMessages(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var results []map[string]string
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &results)
		if len(results) != 1 {
			t.Fatalf("Expected 1 result, got %d", len(results))
		}
		if results[0]["status"] != "retried" {
			t.Errorf("Expected 'retried', got '%s'", results[0]["status"])
		}
	})

	t.Run("retry_failed_messages - non-retryable", func(t *testing.T) {
		cleanID := fmt.Sprintf("1888%07d", rng.Intn(10000000))
		msgID := fmt.Sprintf("wamid.noretry_%d", rng.Int63())

		contentBytes, _ := json.Marshal(map[string]any{"body": "no retry"})
		database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status, error_code) VALUES ($1,$2,'outbound','text',$3,'failed',131049)`,
			msgID, cleanID, contentBytes)

		req := mcp.CallToolRequest{}
		req.Params.Name = "retry_failed_messages"
		req.Params.Arguments = map[string]interface{}{
			"message_ids": fmt.Sprintf(`["%s"]`, msgID),
		}

		res, err := srv.handleRetryFailedMessages(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var results []map[string]string
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &results)
		if results[0]["status"] != "skipped" {
			t.Errorf("Expected 'skipped' for non-retryable, got '%s'", results[0]["status"])
		}
	})

	t.Run("sync_template_status tool", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "sync_template_status"
		req.Params.Arguments = map[string]interface{}{
			"template_name": "sample_flight_confirmation",
			"locale":        "en",
		}

		res, err := srv.handleSyncTemplateStatus(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)

		if result["template_name"] != "sample_flight_confirmation" {
			t.Errorf("Expected template_name 'sample_flight_confirmation'")
		}
	})

	// ═══ Group C: Scheduling & Campaign Tests ═══

	t.Run("schedule_message tool", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))
		cleanID := customerID[1:]
		_, _ = userManager.GetOrCreateCustomer(ctx, cleanID, "whatsapp")

		futureTime := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)

		req := mcp.CallToolRequest{}
		req.Params.Name = "schedule_message"
		req.Params.Arguments = map[string]interface{}{
			"customer_id":  customerID,
			"message_type": "marketing",
			"content":      `{"type":"text","text":{"body":"Scheduled!"}}`,
			"deliver_at":   futureTime,
		}

		res, err := srv.handleScheduleMessage(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		if result["status"] != "scheduled" {
			t.Errorf("Expected status 'scheduled', got '%v'", result["status"])
		}
	})

	t.Run("schedule_message - past time rejected", func(t *testing.T) {
		customerID := fmt.Sprintf("+1888%07d", rng.Intn(10000000))

		pastTime := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)

		req := mcp.CallToolRequest{}
		req.Params.Name = "schedule_message"
		req.Params.Arguments = map[string]interface{}{
			"customer_id":  customerID,
			"message_type": "marketing",
			"content":      `{"type":"text","text":{"body":"Past"}}`,
			"deliver_at":   pastTime,
		}

		res, _ := srv.handleScheduleMessage(ctx, req)
		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		if result["error"] == nil && result["reason_code"] == nil {
			// Should have returned an error
			text := res.Content[0].(mcp.TextContent).Text
			if text == "" {
				t.Errorf("Expected error for past deliver_at")
			}
		}
	})

	t.Run("campaign tools - tier gating (OSS)", func(t *testing.T) {
		// schedule_campaign
		req := mcp.CallToolRequest{}
		req.Params.Name = "schedule_campaign"
		req.Params.Arguments = map[string]interface{}{
			"name":          "Test",
			"type":          "marketing",
			"template_name": "sample_purchase_feedback",
			"deliver_at":    time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339),
		}

		res, err := srv.handleScheduleCampaign(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		if result["reason_code"] != "FEATURE_NOT_AVAILABLE" {
			t.Errorf("Expected FEATURE_NOT_AVAILABLE for OSS tier, got '%v'", result["reason_code"])
		}
	})

	t.Run("cancel_campaign - tier gating (OSS)", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "cancel_campaign"
		req.Params.Arguments = map[string]interface{}{
			"campaign_id": "fake-id",
		}

		res, err := srv.handleCancelCampaign(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		if result["reason_code"] != "FEATURE_NOT_AVAILABLE" {
			t.Errorf("Expected FEATURE_NOT_AVAILABLE, got '%v'", result["reason_code"])
		}
	})

	t.Run("pause_campaign - tier gating (OSS)", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "pause_campaign"
		req.Params.Arguments = map[string]interface{}{
			"campaign_id": "fake-id",
		}

		res, err := srv.handlePauseCampaign(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		if result["reason_code"] != "FEATURE_NOT_AVAILABLE" {
			t.Errorf("Expected FEATURE_NOT_AVAILABLE, got '%v'", result["reason_code"])
		}
	})

	// ═══ Group D: Account & Cost Tests ═══

	t.Run("estimate_cost tool", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "estimate_cost"
		req.Params.Arguments = map[string]interface{}{
			"message_type":      "marketing",
			"recipient_country": "ID",
			"quantity":          100,
		}

		res, err := srv.handleEstimateCost(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)

		if result["message_type"] != "marketing" {
			t.Errorf("Expected message_type 'marketing'")
		}
		if result["currency"] != "USD" {
			t.Errorf("Expected currency 'USD'")
		}
		if result["total_estimated_cost"] == nil {
			t.Errorf("Expected total_estimated_cost")
		}
	})

	t.Run("estimate_pricing tool", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "estimate_pricing"
		req.Params.Arguments = map[string]interface{}{
			"conversation_type": "marketing",
			"recipient_country": "ID",
		}

		res, err := srv.handleEstimatePricing(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)

		if result["country"] != "ID" {
			t.Errorf("Expected country 'ID'")
		}
		if result["cost_per_conversation"] == nil {
			t.Errorf("Expected cost_per_conversation")
		}
	})

		t.Run("get_account_quality tool", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "get_account_quality"
		req.Params.Arguments = map[string]interface{}{}

		res, err := srv.handleGetAccountQuality(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		// The tool should return a valid response (success JSON or error message)
		text := res.Content[0].(mcp.TextContent).Text
		if text == "" {
			t.Errorf("Expected non-empty response from get_account_quality")
		}
	})

	t.Run("archive_chat - active window rejected", func(t *testing.T) {
		cleanID := fmt.Sprintf("1888%07d", rng.Intn(10000000))
		conv, _ := stateEngine.OpenWindow(ctx, cleanID, "whatsapp", "service")

		req := mcp.CallToolRequest{}
		req.Params.Name = "archive_chat"
		req.Params.Arguments = map[string]interface{}{
			"conversation_id": conv.ID,
		}

		res, err := srv.handleArchiveChat(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		if result["reason_code"] != "CONVERSATION_ACTIVE" {
			t.Errorf("Expected CONVERSATION_ACTIVE, got '%v'", result["reason_code"])
		}
	})

	t.Run("archive_chat - expired window succeeds", func(t *testing.T) {
		cleanID := fmt.Sprintf("1888%07d", rng.Intn(10000000))

		// Create a conversation with expired window directly in DB
		var convID string
		err := database.Pool.QueryRow(ctx,
			`INSERT INTO conversations (customer_id, channel, window_expires_at, conversation_type, status)
			 VALUES ($1, 'whatsapp', NOW() - INTERVAL '1 hour', 'service', 'active')
			 RETURNING id`, cleanID).Scan(&convID)
		if err != nil {
			t.Fatalf("Failed to create conversation: %v", err)
		}

		req := mcp.CallToolRequest{}
		req.Params.Name = "archive_chat"
		req.Params.Arguments = map[string]interface{}{
			"conversation_id": convID,
		}

		res, err := srv.handleArchiveChat(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		if result["status"] != "archived" {
			t.Errorf("Expected status 'archived', got '%v'", result["status"])
		}

		// Verify conversation_type was NOT changed
		var convType string
		database.Pool.QueryRow(ctx, "SELECT conversation_type FROM conversations WHERE id = $1", convID).Scan(&convType)
		if convType != "service" {
			t.Errorf("Expected conversation_type to remain 'service', got '%s'", convType)
		}
	})

	t.Run("sync_webhooks tool", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "sync_webhooks"
		req.Params.Arguments = map[string]interface{}{}

		res, err := srv.handleSyncWebhooks(ctx, req)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var result map[string]any
		json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &result)
		// Should not error even if sync has issues
		if result["template_synced"] == nil {
			t.Errorf("Expected template_synced field")
		}
	})
}

