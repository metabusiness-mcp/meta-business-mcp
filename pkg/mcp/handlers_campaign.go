package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ─── Sprint 2: Group C — Scheduling & Campaign Tools ─────────────────────────

func (s *Server) handleScheduleMessage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	customerID, err := req.RequireString("customer_id")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: customer_id"), nil
	}
	customerID = normalizePhone(customerID)

	channel := req.GetString("channel", "whatsapp")
	messageType, err := req.RequireString("message_type")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: message_type"), nil
	}

	contentStr, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: content (JSON message payload)"), nil
	}

	var contentMap map[string]any
	if err := json.Unmarshal([]byte(contentStr), &contentMap); err != nil {
		return mcp.NewToolResultError("invalid content format: must be a valid JSON object"), nil
	}

	deliverAtStr, err := req.RequireString("deliver_at")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: deliver_at (RFC3339 timestamp)"), nil
	}

	deliverAt, err := time.Parse(time.RFC3339, deliverAtStr)
	if err != nil {
		return mcp.NewToolResultError("invalid deliver_at format: must be RFC3339 (e.g. 2025-01-15T14:00:00Z)"), nil
	}

	if !deliverAt.After(time.Now()) {
		return mcp.NewToolResultError("deliver_at must be in the future"), nil
	}

	// Compliance check at scheduling time (opt-out check)
	comp, err := s.complianceEng.CheckCompliance(ctx, customerID, channel, messageType)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("compliance check failed: %v", err)), nil
	}
	if !comp.Allowed && comp.ReasonCode == "USER_OPTED_OUT" {
		return toolError(comp.ReasonCode, comp.HumanExplanation, comp.SuggestedAction)
	}

	// Persist to messages table as scheduled
	msgUUID := fmt.Sprintf("sch_%s", newUUID())
	contentBytes, _ := json.Marshal(contentMap)

	_, err = s.db.Pool.Exec(ctx,
		`INSERT INTO messages (id, customer_id, direction, message_type, content, status, deliver_at) 
		 VALUES ($1, $2, 'outbound', $3, $4, 'scheduled', $5)`,
		msgUUID, customerID, messageType, contentBytes, deliverAt)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to schedule message: %v", err)), nil
	}

	// Audit log
	auditDetails, _ := json.Marshal(map[string]any{
		"message_id":   msgUUID,
		"customer_id":  customerID,
		"channel":      channel,
		"message_type": messageType,
		"deliver_at":   deliverAtStr,
		"action":       "scheduled",
	})
	_, _ = s.db.Pool.Exec(ctx, "INSERT INTO audit_logs (action, details) VALUES ('schedule_message', $1)", auditDetails)

	result := map[string]any{
		"message_id":  msgUUID,
		"status":      "scheduled",
		"deliver_at":  deliverAtStr,
		"customer_id": customerID,
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handleScheduleCampaign(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := s.requireTier("pro"); res != nil {
		return res, nil
	}

	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: name"), nil
	}

	campaignType, err := req.RequireString("type")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: type (broadcast, marketing, utility)"), nil
	}

	templateName, err := req.RequireString("template_name")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: template_name"), nil
	}

	locale := req.GetString("locale", "en")
	variablesStr := req.GetString("variables", "{}")
	audienceStr := req.GetString("audience_filter", "{}")

	deliverAtStr, err := req.RequireString("deliver_at")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: deliver_at (RFC3339 timestamp)"), nil
	}

	deliverAt, err := time.Parse(time.RFC3339, deliverAtStr)
	if err != nil {
		return mcp.NewToolResultError("invalid deliver_at format: must be RFC3339"), nil
	}

	if !deliverAt.After(time.Now()) {
		return mcp.NewToolResultError("deliver_at must be in the future"), nil
	}

	// Validate template exists and is approved
	tmpl, err := s.templateMgr.GetTemplate(ctx, templateName, locale)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("template '%s' (%s) not found: %v", templateName, locale, err)), nil
	}
	if tmpl.Status != "APPROVED" && tmpl.Status != "approved" {
		return toolError("TEMPLATE_NOT_APPROVED",
			fmt.Sprintf("Template '%s' status is '%s', not approved.", templateName, tmpl.Status),
			"Wait for template approval or use an approved template.")
	}

	// Parse variables and audience filter
	var variables map[string]any
	if err := json.Unmarshal([]byte(variablesStr), &variables); err != nil {
		return mcp.NewToolResultError("invalid variables format: must be a JSON object"), nil
	}

	var audienceFilter map[string]any
	if err := json.Unmarshal([]byte(audienceStr), &audienceFilter); err != nil {
		return mcp.NewToolResultError("invalid audience_filter format: must be a JSON object"), nil
	}

	// Create campaign
	campaign, err := s.campaignMgr.CreateCampaign(ctx, name, campaignType, templateName, locale, variables, &deliverAt)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create campaign: %v", err)), nil
	}

	// Set audience filter
	audienceJSON, _ := json.Marshal(audienceFilter)
	_, _ = s.db.Pool.Exec(ctx, `UPDATE campaigns SET audience_filter = $2 WHERE id = $1`, campaign.ID, audienceJSON)

	// Audit log
	auditDetails, _ := json.Marshal(map[string]any{
		"campaign_id":   campaign.ID,
		"name":          name,
		"type":          campaignType,
		"template_name": templateName,
		"deliver_at":    deliverAtStr,
		"action":        "scheduled",
	})
	_, _ = s.db.Pool.Exec(ctx, "INSERT INTO audit_logs (action, details) VALUES ('schedule_campaign', $1)", auditDetails)

	result := map[string]any{
		"campaign_id": campaign.ID,
		"status":      "scheduled",
		"name":        name,
		"type":        campaignType,
		"deliver_at":  deliverAtStr,
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handleCancelCampaign(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := s.requireTier("pro"); res != nil {
		return res, nil
	}

	campaignID, err := req.RequireString("campaign_id")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: campaign_id"), nil
	}

	reason := req.GetString("reason", "cancelled by user")

	// Get campaign and validate state
	campaign, err := s.campaignMgr.GetCampaign(ctx, campaignID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("campaign not found: %v", err)), nil
	}

	// Validate non-terminal state
	terminalStates := map[string]bool{"completed": true, "failed": true, "cancelled": true}
	if terminalStates[campaign.Status] {
		return toolError("CAMPAIGN_TERMINAL_STATE",
			fmt.Sprintf("Campaign '%s' is in terminal state '%s' and cannot be cancelled.", campaignID, campaign.Status),
			"No action needed — this campaign has already reached a final state.")
	}

	// Cancel campaign
	if err := s.campaignMgr.UpdateCampaignStatus(ctx, campaignID, "cancelled"); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to cancel campaign: %v", err)), nil
	}

	// Audit log
	auditDetails, _ := json.Marshal(map[string]any{
		"campaign_id": campaignID,
		"from_status": campaign.Status,
		"to_status":   "cancelled",
		"reason":      reason,
	})
	_, _ = s.db.Pool.Exec(ctx, "INSERT INTO audit_logs (action, details) VALUES ('cancel_campaign', $1)", auditDetails)

	result := map[string]any{
		"campaign_id": campaignID,
		"status":      "cancelled",
		"from_status": campaign.Status,
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handlePauseCampaign(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if res, _ := s.requireTier("pro"); res != nil {
		return res, nil
	}

	campaignID, err := req.RequireString("campaign_id")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: campaign_id"), nil
	}

	// Get campaign and validate state
	campaign, err := s.campaignMgr.GetCampaign(ctx, campaignID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("campaign not found: %v", err)), nil
	}

	// Validate valid transition: running → paused, scheduled → paused
	validFromStates := map[string]bool{"running": true, "sending": true, "scheduled": true}
	if !validFromStates[campaign.Status] {
		return toolError("INVALID_STATE_TRANSITION",
			fmt.Sprintf("Cannot pause campaign in state '%s'. Only 'running', 'sending', or 'scheduled' campaigns can be paused.", campaign.Status),
			"Check campaign status before attempting to pause.")
	}

	// Pause: update status and set paused_at timestamp
	_, err = s.db.Pool.Exec(ctx,
		`UPDATE campaigns SET status = 'paused', paused_at = NOW(), updated_at = NOW() WHERE id = $1`, campaignID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to pause campaign: %v", err)), nil
	}

	// Audit log (preserves sent_count, delivered_count, failed_count for future resume)
	auditDetails, _ := json.Marshal(map[string]any{
		"campaign_id":    campaignID,
		"from_status":    campaign.Status,
		"to_status":      "paused",
		"sent_count":     campaign.SentCount,
		"delivered_count": campaign.DeliveredCount,
		"failed_count":   campaign.FailedCount,
	})
	_, _ = s.db.Pool.Exec(ctx, "INSERT INTO audit_logs (action, details) VALUES ('pause_campaign', $1)", auditDetails)

	result := map[string]any{
		"campaign_id": campaignID,
		"status":      "paused",
		"from_status": campaign.Status,
		"note":        "Campaign paused. Progress counters preserved for future resume.",
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}