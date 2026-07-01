package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/state"
)

// ─── Sprint 2: Group A — Read-Only Intelligence Tools ────────────────────────

func (s *Server) handleCheckConversation(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	customerID, err := req.RequireString("customer_id")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: customer_id"), nil
	}
	customerID = normalizePhone(customerID)

	channel := req.GetString("channel", "whatsapp")

	conv, err := s.stateEng.GetActiveConversation(ctx, customerID, channel)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query conversation state: %v", err)), nil
	}

	windowOpen := conv.WindowExpiresAt != nil && conv.WindowExpiresAt.After(time.Now())
	var timeRemaining int64
	if windowOpen {
		timeRemaining = int64(time.Until(*conv.WindowExpiresAt).Seconds())
		if timeRemaining < 0 {
			timeRemaining = 0
		}
	}

	result := map[string]any{
		"customer_id":          customerID,
		"channel":              channel,
		"window_open":          windowOpen,
		"time_remaining_secs":  timeRemaining,
		"conversation_type":    conv.ConversationType,
		"last_inbound_at":      conv.LastInboundAt,
		"last_outbound_at":     conv.LastOutboundAt,
		"window_expires_at":    conv.WindowExpiresAt,
		"marketing_eligibility": conv.MarketingEligibility,
		"utility_eligibility":  conv.UtilityEligibility,
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handleCheckFrequencyCap(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	customerID, err := req.RequireString("customer_id")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: customer_id"), nil
	}
	customerID = normalizePhone(customerID)

	channel := req.GetString("channel", "whatsapp")

	// Call compliance engine as a black box for marketing messages
	comp, err := s.complianceEng.CheckCompliance(ctx, customerID, channel, "marketing")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("compliance check failed: %v", err)), nil
	}

	isCapped := !comp.Allowed && comp.ReasonCode == "FREQUENCY_CAP_EXCEEDED"

	result := map[string]any{
		"customer_id": customerID,
		"channel":     channel,
		"is_capped":   isCapped,
	}

	if isCapped {
		result["reason"] = comp.HumanExplanation
		result["suggested_action"] = comp.SuggestedAction
		// Frequency cap resets at midnight UTC + 24h from first message
		result["reset_hint"] = "Frequency cap resets 24 hours after the first marketing message was sent today."
	} else if !comp.Allowed {
		// Not capped but compliance failed for another reason
		result["is_capped"] = false
		result["compliance_note"] = fmt.Sprintf("Not frequency-capped, but compliance check returned: %s (%s)", comp.ReasonCode, comp.HumanExplanation)
	} else {
		result["reason"] = ""
		result["suggested_action"] = ""
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handleGetCustomerContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	customerID, err := req.RequireString("customer_id")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: customer_id"), nil
	}
	customerID = normalizePhone(customerID)

	channel := req.GetString("channel", "whatsapp")

	// 1. Get customer profile
	customer, err := s.userManager.GetOrCreateCustomer(ctx, customerID, channel)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to retrieve customer: %v", err)), nil
	}

	// 2. Get conversation state
	conv, err := s.stateEng.GetActiveConversation(ctx, customerID, channel)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to retrieve conversation: %v", err)), nil
	}

	// 3. Compute eligibility via dry-run compliance (no audit log writes)
	marketingComp, _ := s.complianceEng.CheckComplianceDryRun(ctx, customerID, channel, "marketing", true)
	utilityComp, _ := s.complianceEng.CheckComplianceDryRun(ctx, customerID, channel, "utility", true)
	serviceComp, _ := s.complianceEng.CheckComplianceDryRun(ctx, customerID, channel, "service", true)

	windowOpen := conv.WindowExpiresAt != nil && conv.WindowExpiresAt.After(time.Now())

	result := map[string]any{
		"customer_id": customerID,
		"channel":     channel,
		"opt_in": map[string]bool{
			"marketing": customer.OptInMarketing,
			"utility":   customer.OptInUtility,
		},
		"tags":             customer.Tags,
		"engagement_score": customer.EngagementScore,
		"last_interaction": customer.LastInteractionAt,
		"conversation": map[string]any{
			"window_open":       windowOpen,
			"conversation_type": conv.ConversationType,
			"last_inbound":      conv.LastInboundAt,
			"last_outbound":     conv.LastOutboundAt,
			"window_expires":    conv.WindowExpiresAt,
		},
		"eligibility": map[string]any{
			"marketing": eligibilitySummary(marketingComp),
			"utility":   eligibilitySummary(utilityComp),
			"service":   eligibilitySummary(serviceComp),
		},
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func eligibilitySummary(comp *compliance.ComplianceResult) map[string]any {
	if comp == nil {
		return map[string]any{"eligible": false, "reason": "compliance check unavailable"}
	}
	if comp.Allowed {
		return map[string]any{"eligible": true}
	}
	return map[string]any{
		"eligible":         false,
		"reason_code":      comp.ReasonCode,
		"human_explanation": comp.HumanExplanation,
		"suggested_action": comp.SuggestedAction,
	}
}

func (s *Server) handleGetDeliveryStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	messageID, err := req.RequireString("message_id")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: message_id"), nil
	}

	var msg struct {
		ID           string  `json:"id"`
		CustomerID   string  `json:"customer_id"`
		Direction    string  `json:"direction"`
		MessageType  string  `json:"message_type"`
		Status       string  `json:"status"`
		ErrorCode    *int    `json:"error_code"`
		ErrorMessage *string `json:"error_message"`
		RetryCount   int     `json:"retry_count"`
		CreatedAt    string  `json:"created_at"`
		UpdatedAt    string  `json:"updated_at"`
	}

	err = s.db.Pool.QueryRow(ctx,
		`SELECT id, customer_id, direction, message_type, status, error_code, error_message, 
		        retry_count, created_at::TEXT, updated_at::TEXT 
		 FROM messages WHERE id = $1`, messageID,
	).Scan(&msg.ID, &msg.CustomerID, &msg.Direction, &msg.MessageType, &msg.Status,
		&msg.ErrorCode, &msg.ErrorMessage, &msg.RetryCount, &msg.CreatedAt, &msg.UpdatedAt)

	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("message '%s' not found: %v", messageID, err)), nil
	}

	result := map[string]any{
		"message_id":   msg.ID,
		"customer_id":  msg.CustomerID,
		"direction":    msg.Direction,
		"message_type": msg.MessageType,
		"status":       msg.Status,
		"retry_count":  msg.RetryCount,
		"created_at":   msg.CreatedAt,
		"updated_at":   msg.UpdatedAt,
	}

	// Enrich with error intelligence if failed
	if msg.Status == "failed" && msg.ErrorCode != nil {
		intel, err := s.errorIntel.ExplainError(ctx, *msg.ErrorCode)
		if err == nil {
			result["error"] = map[string]any{
				"code":              intel.Code,
				"category":          intel.Category,
				"can_retry":         intel.CanRetry,
				"human_explanation": intel.HumanExplanation,
				"suggested_action":  intel.SuggestedAction,
			}
		} else {
			result["error"] = map[string]any{
				"code":     msg.ErrorCode,
				"message":  msg.ErrorMessage,
				"can_retry": false,
			}
		}
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handleGetRateLimit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	channel := req.GetString("channel", "whatsapp")
	_ = channel // Rate limiting is per phone number, channel is for future use

	// Use the same key source as the worker: cfg.Meta.PhoneNumberID
	defaultMPS := 20.0 // Same as worker default
	status, err := s.rateLimiter.GetStatus(ctx, s.cfg.Meta.PhoneNumberID, defaultMPS)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query rate limit status: %v", err)), nil
	}

	result := map[string]any{
		"channel":           channel,
		"capacity_mps":      status.Capacity,
		"tokens_remaining":  int(status.TokensLeft),
		"tokens_used":       int(status.TokensUsed),
		"last_update_ms":    status.LastUpdateMs,
	}

	if status.LastUpdateMs > 0 {
		lastUpdate := time.UnixMilli(status.LastUpdateMs)
		result["last_update_at"] = lastUpdate.Format(time.RFC3339)
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handleListConversations(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	channel := req.GetString("channel", "whatsapp")
	statusFilter := req.GetString("status_filter", "")
	limit := req.GetInt("limit", 50)
	offset := req.GetInt("offset", 0)

	convs, total, err := s.stateEng.ListConversations(ctx, channel, statusFilter, limit, offset)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list conversations: %v", err)), nil
	}

	// Enrich with window status
	type enrichedConv struct {
		state.Conversation
		WindowOpen         bool `json:"window_open"`
		TimeRemainingSecs  int64 `json:"time_remaining_secs"`
	}

	enriched := make([]enrichedConv, len(convs))
	for i, c := range convs {
		enriched[i].Conversation = c
		enriched[i].WindowOpen = c.WindowExpiresAt != nil && c.WindowExpiresAt.After(time.Now())
		if enriched[i].WindowOpen {
			secs := int64(time.Until(*c.WindowExpiresAt).Seconds())
			if secs < 0 {
				secs = 0
			}
			enriched[i].TimeRemainingSecs = secs
		}
	}

	result := map[string]any{
		"conversations": enriched,
		"total":         total,
		"limit":         limit,
		"offset":        offset,
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handleListTemplates(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	statusFilter := req.GetString("status_filter", "")
	categoryFilter := req.GetString("category_filter", "")
	localeFilter := req.GetString("locale_filter", "")
	limit := req.GetInt("limit", 50)
	offset := req.GetInt("offset", 0)

	templates, total, err := s.templateMgr.ListTemplates(ctx, statusFilter, categoryFilter, localeFilter, limit, offset)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list templates: %v", err)), nil
	}

	result := map[string]any{
		"templates": templates,
		"total":     total,
		"limit":     limit,
		"offset":    offset,
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}