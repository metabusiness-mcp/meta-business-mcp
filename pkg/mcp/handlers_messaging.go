package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
)

// ─── Existing v1 Tools ───────────────────────────────────────────────────────

func (s *Server) handleCheckCompliance(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	customerID, err := req.RequireString("customer_id")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: customer_id"), nil
	}
	customerID = normalizePhone(customerID)

	msgType, err := req.RequireString("message_type")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: message_type"), nil
	}

	channel := req.GetString("channel", "whatsapp")

	// 1. Run compliance engine
	comp, err := s.complianceEng.CheckCompliance(ctx, customerID, channel, msgType)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("compliance check error: %v", err)), nil
	}

	if !comp.Allowed {
		resJSON, _ := json.Marshal(comp)
		return mcp.NewToolResultText(string(resJSON)), nil
	}

	// 2. Run policy engine
	customer, _ := s.userManager.GetOrCreateCustomer(ctx, customerID, channel)
	policyRes, err := s.policyEng.EvaluatePolicies(ctx, customerID, channel, msgType, customer.Tags)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("policy engine error: %v", err)), nil
	}

	resJSON, _ := json.Marshal(policyRes)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handleSendMessage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	to, err := req.RequireString("to")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: to"), nil
	}
	to = normalizePhone(to)

	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: text"), nil
	}

	channel := req.GetString("channel", "whatsapp")

	// 1. Compliance check (for free-form service messaging)
	comp, err := s.complianceEng.CheckCompliance(ctx, to, channel, "service")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("compliance evaluation failed: %v", err)), nil
	}
	if !comp.Allowed {
		return mcp.NewToolResultText(fmt.Sprintf("COMPLIANCE_DENIED: %s. Action: %s", comp.HumanExplanation, comp.SuggestedAction)), nil
	}

	// 2. Policy check
	customer, _ := s.userManager.GetOrCreateCustomer(ctx, to, channel)
	policyRes, err := s.policyEng.EvaluatePolicies(ctx, to, channel, "service", customer.Tags)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("policy evaluation failed: %v", err)), nil
	}
	if !policyRes.Allowed {
		return mcp.NewToolResultText(fmt.Sprintf("POLICY_DENIED: %s. Action: %s", policyRes.HumanExplanation, policyRes.SuggestedAction)), nil
	}

	// 3. Retrieve conversation context to link message
	conv, err := s.getActiveConv(ctx, to, channel)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get active conversation state: %v", err)), nil
	}

	msgUUID := uuid.New().String()
	contentBytes, _ := json.Marshal(map[string]string{"body": text})

	// 4. Save message in Postgres as queued
	var convIDVal *string
	if conv != nil && conv.ID != "" {
		convIDVal = &conv.ID
	}

	_, err = s.db.Pool.Exec(ctx,
		`INSERT INTO messages (id, conversation_id, customer_id, direction, message_type, content, status) 
		 VALUES ($1, $2, $3, 'outbound', 'text', $4, 'queued')`,
		msgUUID, convIDVal, to, contentBytes)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to record message: %v", err)), nil
	}

	// 5. Publish to NATS stream
	err = s.orchestrator.PublishOutboundMessage(ctx, msgUUID, to, "service", map[string]any{
		"type": "text",
		"text": map[string]string{"body": text},
	})
	if err != nil {
		// Update DB status to failed
		_, _ = s.db.Pool.Exec(ctx, "UPDATE messages SET status = 'failed' WHERE id = $1", msgUUID)
		return mcp.NewToolResultError(fmt.Sprintf("failed to queue message to NATS: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(`{"status": "queued", "message_id": "%s"}`, msgUUID)), nil
}

func (s *Server) handleSendTemplate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	to, err := req.RequireString("to")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: to"), nil
	}
	to = normalizePhone(to)

	templateName, err := req.RequireString("template_name")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: template_name"), nil
	}

	locale := req.GetString("locale", "en")
	variablesStr := req.GetString("variables", "[]")
	channel := req.GetString("channel", "whatsapp")

	// Parse variables array
	var variables []string
	if err := json.Unmarshal([]byte(variablesStr), &variables); err != nil {
		return mcp.NewToolResultError("invalid variables format: must be a JSON array of strings"), nil
	}

	// 1. Get Template from DB
	tmpl, err := s.templateMgr.GetTemplate(ctx, templateName, locale)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("template lookup failed: %v", err)), nil
	}

	// 2. Compliance check (for templates, we evaluate template messaging eligibility)
	comp, err := s.complianceEng.CheckCompliance(ctx, to, channel, tmpl.Category)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("compliance evaluation failed: %v", err)), nil
	}
	if !comp.Allowed {
		return mcp.NewToolResultText(fmt.Sprintf("COMPLIANCE_DENIED: %s. Action: %s", comp.HumanExplanation, comp.SuggestedAction)), nil
	}

	// 3. Policy check
	customer, _ := s.userManager.GetOrCreateCustomer(ctx, to, channel)
	policyRes, err := s.policyEng.EvaluatePolicies(ctx, to, channel, tmpl.Category, customer.Tags)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("policy evaluation failed: %v", err)), nil
	}
	if !policyRes.Allowed {
		return mcp.NewToolResultText(fmt.Sprintf("POLICY_DENIED: %s. Action: %s", policyRes.HumanExplanation, policyRes.SuggestedAction)), nil
	}

	// 4. Retrieve or open conversation context to link message
	conv, err := s.getActiveConv(ctx, to, channel)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get active conversation state: %v", err)), nil
	}

	// Open window if template starts/resumes a conversation
	if conv == nil || conv.ID == "" {
		conv, err = s.complianceEng.OpenConversationWindow(ctx, to, channel, tmpl.Category)
		if err != nil {
			fmt.Printf("[MCP] Warning: failed to pre-open conversation window in DB: %v\n", err)
		}
	}

	msgUUID := uuid.New().String()

	// Map components parameters matching Meta structure
	params := make([]map[string]any, len(variables))
	for i, v := range variables {
		params[i] = map[string]any{
			"type": "text",
			"text": v,
		}
	}

	contentPayload := map[string]any{
		"type": "template",
		"template": map[string]any{
			"name": templateName,
			"language": map[string]string{
				"code": locale,
			},
			"components": []map[string]any{
				{
					"type":       "body",
					"parameters": params,
				},
			},
		},
	}

	contentBytes, _ := json.Marshal(contentPayload)

	// 5. Save message in Postgres as queued
	var convIDVal *string
	if conv != nil && conv.ID != "" {
		convIDVal = &conv.ID
	}

	_, err = s.db.Pool.Exec(ctx,
		`INSERT INTO messages (id, conversation_id, customer_id, direction, message_type, content, status) 
		 VALUES ($1, $2, $3, 'outbound', 'template', $4, 'queued')`,
		msgUUID, convIDVal, to, contentBytes)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to record template message: %v", err)), nil
	}

	// 6. Publish to NATS stream
	err = s.orchestrator.PublishOutboundMessage(ctx, msgUUID, to, tmpl.Category, contentPayload)
	if err != nil {
		// Update DB status to failed
		_, _ = s.db.Pool.Exec(ctx, "UPDATE messages SET status = 'failed' WHERE id = $1", msgUUID)
		return mcp.NewToolResultError(fmt.Sprintf("failed to queue template message to NATS: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(`{"status": "queued", "message_id": "%s"}`, msgUUID)), nil
}

func (s *Server) handleExplainError(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	codeStr, err := req.RequireString("code")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: code"), nil
	}

	code, err := strconv.Atoi(codeStr)
	if err != nil {
		return mcp.NewToolResultError("invalid code format: must be an integer"), nil
	}

	intel, err := s.errorIntel.ExplainError(ctx, code)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to explain error: %v", err)), nil
	}

	intelJSON, _ := json.Marshal(intel)
	return mcp.NewToolResultText(string(intelJSON)), nil
}

// ─── Sprint 2: Group B Action Tools ─────────────────────────────────────────

func (s *Server) handleReplyCustomer(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	customerID, err := req.RequireString("customer_id")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: customer_id"), nil
	}
	customerID = normalizePhone(customerID)

	messageText, err := req.RequireString("message_text")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: message_text"), nil
	}

	channel := req.GetString("channel", "whatsapp")

	// 1. Explicitly verify active 24-hour window (this is the key distinction from send_message)
	conv, err := s.complianceEng.GetActiveConversation(ctx, customerID, channel)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to check conversation state: %v", err)), nil
	}
	if conv == nil || conv.WindowExpiresAt == nil || !conv.WindowExpiresAt.After(time.Now()) {
		return toolError("WINDOW_CLOSED", "No active 24-hour conversation window for this customer.",
			"Use send_template to re-engage the customer with an approved template message.")
	}

	// 2. Full compliance chain
	comp, err := s.complianceEng.CheckCompliance(ctx, customerID, channel, "service")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("compliance evaluation failed: %v", err)), nil
	}
	if !comp.Allowed {
		return toolError(comp.ReasonCode, comp.HumanExplanation, comp.SuggestedAction)
	}

	customer, _ := s.userManager.GetOrCreateCustomer(ctx, customerID, channel)
	policyRes, err := s.policyEng.EvaluatePolicies(ctx, customerID, channel, "service", customer.Tags)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("policy evaluation failed: %v", err)), nil
	}
	if !policyRes.Allowed {
		return toolError(policyRes.ReasonCode, policyRes.HumanExplanation, policyRes.SuggestedAction)
	}

	// 3. Save and dispatch
	msgUUID := uuid.New().String()
	contentBytes, _ := json.Marshal(map[string]string{"body": messageText})

	var convIDVal *string
	if conv.ID != "" {
		convIDVal = &conv.ID
	}

	_, err = s.db.Pool.Exec(ctx,
		`INSERT INTO messages (id, conversation_id, customer_id, direction, message_type, content, status) 
		 VALUES ($1, $2, $3, 'outbound', 'text', $4, 'queued')`,
		msgUUID, convIDVal, customerID, contentBytes)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to record message: %v", err)), nil
	}

	err = s.orchestrator.PublishOutboundMessage(ctx, msgUUID, customerID, "service", map[string]any{
		"type": "text",
		"text": map[string]string{"body": messageText},
	})
	if err != nil {
		_, _ = s.db.Pool.Exec(ctx, "UPDATE messages SET status = 'failed' WHERE id = $1", msgUUID)
		return mcp.NewToolResultError(fmt.Sprintf("failed to queue message to NATS: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(`{"status": "queued", "message_id": "%s"}`, msgUUID)), nil
}

func (s *Server) handleRetryFailedMessages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	idsStr, err := req.RequireString("message_ids")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: message_ids (JSON array of message ID strings)"), nil
	}

	var messageIDs []string
	if err := json.Unmarshal([]byte(idsStr), &messageIDs); err != nil {
		return mcp.NewToolResultError("invalid message_ids format: must be a JSON array of strings"), nil
	}

	if len(messageIDs) == 0 {
		return mcp.NewToolResultError("message_ids must contain at least one message ID"), nil
	}

	type retryResult struct {
		MessageID string `json:"message_id"`
		Status    string `json:"status"` // "retried" or "skipped"
		Reason    string `json:"reason,omitempty"`
	}

	var results []retryResult
	retriedCount := 0

	for _, msgID := range messageIDs {
		// 1. Fetch current message state
		var status string
		var errorCode *int
		var customerID, messageType string
		var content json.RawMessage

		err := s.db.Pool.QueryRow(ctx,
			`SELECT status, error_code, customer_id, message_type, content FROM messages WHERE id = $1`, msgID,
		).Scan(&status, &errorCode, &customerID, &messageType, &content)

		if err != nil {
			results = append(results, retryResult{MessageID: msgID, Status: "skipped", Reason: "message not found"})
			continue
		}

		// 2. Verify message is actually failed
		if status != "failed" {
			results = append(results, retryResult{MessageID: msgID, Status: "skipped", Reason: fmt.Sprintf("message status is '%s', not 'failed'", status)})
			continue
		}

		// 3. Check if failure is retryable via Error Intelligence
		if errorCode != nil {
			intel, err := s.errorIntel.ExplainError(ctx, *errorCode)
			if err == nil && !intel.CanRetry {
				results = append(results, retryResult{MessageID: msgID, Status: "skipped", Reason: fmt.Sprintf("error %d is not retryable: %s", *errorCode, intel.HumanExplanation)})
				continue
			}
		}

		// 4. Re-run compliance check (state may have changed since original failure)
		comp, err := s.complianceEng.CheckCompliance(ctx, customerID, "whatsapp", messageType)
		if err != nil {
			results = append(results, retryResult{MessageID: msgID, Status: "skipped", Reason: fmt.Sprintf("compliance check failed: %v", err)})
			continue
		}
		if !comp.Allowed {
			results = append(results, retryResult{MessageID: msgID, Status: "skipped", Reason: fmt.Sprintf("compliance denied: %s", comp.HumanExplanation)})
			continue
		}

		// 5. Re-queue to NATS
		var contentMap map[string]any
		_ = json.Unmarshal(content, &contentMap)

		err = s.orchestrator.PublishOutboundMessage(ctx, msgID, customerID, messageType, contentMap)
		if err != nil {
			results = append(results, retryResult{MessageID: msgID, Status: "skipped", Reason: fmt.Sprintf("failed to publish to NATS: %v", err)})
			continue
		}

		// 6. Update message status to queued
		_, _ = s.db.Pool.Exec(ctx, `UPDATE messages SET status = 'queued', retry_count = retry_count + 1, error_code = NULL, error_message = NULL, updated_at = NOW() WHERE id = $1`, msgID)

		results = append(results, retryResult{MessageID: msgID, Status: "retried"})
		retriedCount++
	}

	// Audit log the retry operation
	auditDetails, _ := json.Marshal(map[string]any{
		"total_requested": len(messageIDs),
		"total_retried":   retriedCount,
		"results":         results,
	})
	_, _ = s.db.Pool.Exec(ctx, "INSERT INTO audit_logs (action, details) VALUES ('retry_failed_messages', $1)", auditDetails)

	resJSON, _ := json.Marshal(results)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handleSyncTemplateStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	templateName, err := req.RequireString("template_name")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: template_name"), nil
	}

	locale := req.GetString("locale", "en")

	// Sync all templates from Meta (this is a pull operation)
	if err := s.templateMgr.SyncTemplates(ctx); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to sync templates from Meta: %v", err)), nil
	}

	// Now read the updated status from local DB
	tmpl, err := s.templateMgr.GetTemplate(ctx, templateName, locale)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("template '%s' (%s) not found after sync: %v", templateName, locale, err)), nil
	}

	result := map[string]any{
		"template_name": tmpl.Name,
		"locale":        tmpl.Locale,
		"status":        tmpl.Status,
		"category":      tmpl.Category,
	}

	// If rejected, surface rejection reason via Error Intelligence pattern
	if tmpl.Status == "REJECTED" {
		result["reason"] = "Template was rejected by Meta. Review the template content and resubmit."
		result["suggested_action"] = "Check Meta Business Manager for specific rejection feedback, modify the template, and resubmit."
	}

	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}