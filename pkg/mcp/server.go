package mcp

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"meta-business-mcp/pkg/campaign"
	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/delivery"
	"meta-business-mcp/pkg/errorintel"
	"meta-business-mcp/pkg/policy"
	"meta-business-mcp/pkg/ratelimit"
	"meta-business-mcp/pkg/state"
	"meta-business-mcp/pkg/template"
	"meta-business-mcp/pkg/userintel"
)

type Server struct {
	mcpServer     *server.MCPServer
	db            *db.DB
	cfg           *config.Config
	complianceEng *compliance.Engine
	policyEng     *policy.Engine
	errorIntel    *errorintel.Engine
	templateMgr   *template.Manager
	userManager   *userintel.Manager
	campaignMgr   *campaign.Manager
	orchestrator  *delivery.Orchestrator
	stateEng      *state.Engine
	rateLimiter   *ratelimit.Limiter
}

func NewServer(
	database *db.DB,
	cfg *config.Config,
	compEng *compliance.Engine,
	polEng *policy.Engine,
	errIntel *errorintel.Engine,
	tempMgr *template.Manager,
	userMgr *userintel.Manager,
	campMgr *campaign.Manager,
	orch *delivery.Orchestrator,
) *Server {
	s := server.NewMCPServer(
		cfg.Server.MCPName,
		cfg.Server.MCPVersion,
		server.WithToolCapabilities(true),
	)

	// Create a state engine and rate limiter from the database for use in tool handlers
	stateEng := state.NewEngine(database)
	limiter := ratelimit.NewLimiter(database.Redis)

	srv := &Server{
		mcpServer:     s,
		db:            database,
		cfg:           cfg,
		complianceEng: compEng,
		policyEng:     polEng,
		errorIntel:    errIntel,
		templateMgr:   tempMgr,
		userManager:   userMgr,
		campaignMgr:   campMgr,
		orchestrator:  orch,
		stateEng:      stateEng,
		rateLimiter:   limiter,
	}

	srv.registerTools()

	return srv
}

func (s *Server) GetMCPServer() *server.MCPServer {
	return s.mcpServer
}

func (s *Server) registerTools() {
	// ═══ Group A: Read-Only Intelligence ═══

	// 5. check_conversation
	s.mcpServer.AddTool(mcp.NewTool("check_conversation",
		mcp.WithDescription("Query the conversation state for a customer: 24-hour window status, time remaining, conversation type, and last interaction timestamps."),
		mcp.WithString("customer_id", mcp.Required(), mcp.Description("Customer phone number including country code (e.g. +628119989630)")),
		mcp.WithString("channel", mcp.Description("Target channel: 'whatsapp' (default)")),
	), s.handleCheckConversation)

	// 6. check_frequency_cap
	s.mcpServer.AddTool(mcp.NewTool("check_frequency_cap",
		mcp.WithDescription("Check whether a customer is currently under Meta's frequency cap for marketing messages. Returns cap status, reset timing, and reason if capped."),
		mcp.WithString("customer_id", mcp.Required(), mcp.Description("Customer phone number including country code")),
		mcp.WithString("channel", mcp.Description("Target channel: 'whatsapp' (default)")),
	), s.handleCheckFrequencyCap)

	// 7. get_customer_context
	s.mcpServer.AddTool(mcp.NewTool("get_customer_context",
		mcp.WithDescription("Return the full communication profile of a customer: opt-in/opt-out status, interaction timeline, segment tags, and eligibility summary for marketing, utility, and service messages."),
		mcp.WithString("customer_id", mcp.Required(), mcp.Description("Customer phone number including country code")),
		mcp.WithString("channel", mcp.Description("Target channel: 'whatsapp' (default)")),
	), s.handleGetCustomerContext)

	// 8. get_delivery_status
	s.mcpServer.AddTool(mcp.NewTool("get_delivery_status",
		mcp.WithDescription("Return the current delivery status of a message: sent, delivered, read, or failed. Includes error code and explanation if failed."),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The message ID returned when the message was queued")),
	), s.handleGetDeliveryStatus)

	// 9. get_rate_limit
	s.mcpServer.AddTool(mcp.NewTool("get_rate_limit",
		mcp.WithDescription("Return the current state of the rate limiter: messages per second capacity, tokens consumed in the current window, tokens remaining, and last update timestamp."),
		mcp.WithString("channel", mcp.Description("Target channel: 'whatsapp' (default)")),
	), s.handleGetRateLimit)

	// 10. list_conversations
	s.mcpServer.AddTool(mcp.NewTool("list_conversations",
		mcp.WithDescription("Return a paginated list of conversations with their current state. Filter by status: 'open', 'closed', or 'expiring_soon' (window closes within 2 hours)."),
		mcp.WithString("channel", mcp.Description("Target channel: 'whatsapp' (default)")),
		mcp.WithString("status_filter", mcp.Description("Filter by status: 'open', 'closed', 'expiring_soon'")),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default: 50)")),
		mcp.WithNumber("offset", mcp.Description("Number of results to skip (default: 0)")),
	), s.handleListConversations)

	// 11. list_templates
	s.mcpServer.AddTool(mcp.NewTool("list_templates",
		mcp.WithDescription("List WhatsApp message templates with optional filters for status, category, and locale. Results are paginated."),
		mcp.WithString("status_filter", mcp.Description("Filter by template status (e.g. 'APPROVED', 'PENDING', 'REJECTED')")),
		mcp.WithString("category_filter", mcp.Description("Filter by category (e.g. 'marketing', 'utility')")),
		mcp.WithString("locale_filter", mcp.Description("Filter by locale (e.g. 'en', 'id')")),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default: 50)")),
		mcp.WithNumber("offset", mcp.Description("Number of results to skip (default: 0)")),
	), s.handleListTemplates)

	// ═══ Group B: Action Operations ═══

	// 1. check_compliance (existing v1)
	s.mcpServer.AddTool(mcp.NewTool("check_compliance",
		mcp.WithDescription("Evaluate if a message is compliant with Meta Business rules and policy regulations"),
		mcp.WithString("customer_id", mcp.Required(), mcp.Description("Customer phone number including country code (e.g. +628119989630)")),
		mcp.WithString("message_type", mcp.Required(), mcp.Description("Type of message being sent: 'marketing', 'utility', 'template', or 'service' (free-text)")),
		mcp.WithString("channel", mcp.Description("Target channel: 'whatsapp' (default)")),
	), s.handleCheckCompliance)

	// 2. send_message (existing v1)
	s.mcpServer.AddTool(mcp.NewTool("send_message",
		mcp.WithDescription("Send a free-form text message to a customer (requires an active 24-hour customer care window)"),
		mcp.WithString("to", mcp.Required(), mcp.Description("Recipient phone number including country code (e.g. +628119989630)")),
		mcp.WithString("text", mcp.Required(), mcp.Description("Body text content of the message")),
		mcp.WithString("channel", mcp.Description("Target channel: 'whatsapp' (default)")),
	), s.handleSendMessage)

	// 3. send_template (existing v1)
	s.mcpServer.AddTool(mcp.NewTool("send_template",
		mcp.WithDescription("Send a pre-approved Meta message template to a customer"),
		mcp.WithString("to", mcp.Required(), mcp.Description("Recipient phone number including country code")),
		mcp.WithString("template_name", mcp.Required(), mcp.Description("Name of the approved template")),
		mcp.WithString("locale", mcp.Description("Locale code (default: 'en')")),
		mcp.WithString("variables", mcp.Description("JSON array of string parameters to map, e.g. '[\"John\", \"$50\"]'")),
		mcp.WithString("channel", mcp.Description("Target channel: 'whatsapp' (default)")),
	), s.handleSendTemplate)

	// 4. explain_error (existing v1)
	s.mcpServer.AddTool(mcp.NewTool("explain_error",
		mcp.WithDescription("Translate Meta Cloud API error codes into human-friendly explanations and actionable steps"),
		mcp.WithString("code", mcp.Required(), mcp.Description("Meta API numeric error code (e.g. 131047)")),
	), s.handleExplainError)

	// 12. reply_customer
	s.mcpServer.AddTool(mcp.NewTool("reply_customer",
		mcp.WithDescription("Send a free-form reply within an active conversation context. Requires an open 24-hour care window. Returns a clear error directing to send_template if the window is closed."),
		mcp.WithString("customer_id", mcp.Required(), mcp.Description("Customer phone number including country code")),
		mcp.WithString("message_text", mcp.Required(), mcp.Description("Body text content of the reply")),
		mcp.WithString("channel", mcp.Description("Target channel: 'whatsapp' (default)")),
	), s.handleReplyCustomer)

	// 13. retry_failed_messages
	s.mcpServer.AddTool(mcp.NewTool("retry_failed_messages",
		mcp.WithDescription("Trigger manual retry for one or more failed messages. Validates each message is failed and retryable. Non-retryable permanent failures are skipped with an explanation."),
		mcp.WithString("message_ids", mcp.Required(), mcp.Description("JSON array of message ID strings to retry, e.g. '[\"msg_123\", \"msg_456\"]'")),
	), s.handleRetryFailedMessages)

	// 14. sync_template_status
	s.mcpServer.AddTool(mcp.NewTool("sync_template_status",
		mcp.WithDescription("Syncs the approval status of a specific template from Meta's API. Pulls the latest status from Meta and updates the local database. Does not grant or expedite approval — template approval is Meta's decision. Use this when webhook delivery may have been missed or delayed."),
		mcp.WithString("template_name", mcp.Required(), mcp.Description("Name of the template to check")),
		mcp.WithString("locale", mcp.Description("Locale code (default: 'en')")),
	), s.handleSyncTemplateStatus)

	// ═══ Group C: Scheduling & Campaign ═══

	// 15. schedule_message
	s.mcpServer.AddTool(mcp.NewTool("schedule_message",
		mcp.WithDescription("Schedule a single message for future delivery. Compliance check runs at scheduling time (opt-out) and again at delivery time (full pipeline)."),
		mcp.WithString("customer_id", mcp.Required(), mcp.Description("Customer phone number including country code")),
		mcp.WithString("message_type", mcp.Required(), mcp.Description("Message type: 'marketing', 'utility', 'service', 'template'")),
		mcp.WithString("content", mcp.Required(), mcp.Description("JSON message payload, e.g. '{\"type\":\"text\",\"text\":{\"body\":\"Hello\"}}'")),
		mcp.WithString("deliver_at", mcp.Required(), mcp.Description("RFC3339 timestamp for delivery, e.g. '2025-01-15T14:00:00Z'")),
		mcp.WithString("channel", mcp.Description("Target channel: 'whatsapp' (default)")),
	), s.handleScheduleMessage)

	// 16. schedule_campaign
	s.mcpServer.AddTool(mcp.NewTool("schedule_campaign",
		mcp.WithDescription("[Pro Tier] Schedule a broadcast campaign. Validates template is approved, audience is non-empty, and deliver_at is in the future. Returns campaign ID."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Campaign name")),
		mcp.WithString("type", mcp.Required(), mcp.Description("Campaign type: 'broadcast', 'marketing', 'utility'")),
		mcp.WithString("template_name", mcp.Required(), mcp.Description("Name of the approved template to use")),
		mcp.WithString("deliver_at", mcp.Required(), mcp.Description("RFC3339 timestamp for campaign delivery")),
		mcp.WithString("locale", mcp.Description("Locale code (default: 'en')")),
		mcp.WithString("variables", mcp.Description("JSON object of template variables, e.g. '{\"1\":\"John\"}'")),
		mcp.WithString("audience_filter", mcp.Description("JSON object for audience segmentation")),
	), s.handleScheduleCampaign)

	// 17. cancel_campaign
	s.mcpServer.AddTool(mcp.NewTool("cancel_campaign",
		mcp.WithDescription("[Pro Tier] Cancel a campaign in any non-terminal state (scheduled, running, paused). Completed or failed campaigns cannot be cancelled."),
		mcp.WithString("campaign_id", mcp.Required(), mcp.Description("The campaign ID to cancel")),
		mcp.WithString("reason", mcp.Description("Cancellation reason for audit trail")),
	), s.handleCancelCampaign)

	// 18. pause_campaign
	s.mcpServer.AddTool(mcp.NewTool("pause_campaign",
		mcp.WithDescription("[Pro Tier] Pause an actively running or scheduled campaign. Preserves progress counters for future resume. Only running, sending, or scheduled campaigns can be paused."),
		mcp.WithString("campaign_id", mcp.Required(), mcp.Description("The campaign ID to pause")),
	), s.handlePauseCampaign)

	// ═══ Group D: Account & Cost Intelligence ═══

	// 19. get_account_quality
	s.mcpServer.AddTool(mcp.NewTool("get_account_quality",
		mcp.WithDescription("Retrieve the current WABA quality score and messaging limit tier from Meta. Returns quality tier (green/yellow/red), current messaging limit, and any quality-based restrictions."),
	), s.handleGetAccountQuality)

	// 20. estimate_cost
	s.mcpServer.AddTool(mcp.NewTool("estimate_cost",
		mcp.WithDescription("Estimate the conversation cost for a planned message batch before sending. Computes cost based on the current Meta conversation pricing model stored in the system."),
		mcp.WithString("message_type", mcp.Required(), mcp.Description("Message type: 'marketing', 'utility', 'authentication', 'service'")),
		mcp.WithString("recipient_country", mcp.Description("ISO country code (e.g. 'ID', 'US'). Default: 'DEFAULT'")),
		mcp.WithNumber("quantity", mcp.Description("Number of messages to estimate (default: 1)")),
	), s.handleEstimateCost)

	// 21. estimate_pricing
	s.mcpServer.AddTool(mcp.NewTool("estimate_pricing",
		mcp.WithDescription("Return the current conversation pricing tier for a specific country and conversation type combination."),
		mcp.WithString("conversation_type", mcp.Required(), mcp.Description("Conversation type: 'marketing', 'utility', 'authentication', 'service'")),
		mcp.WithString("recipient_country", mcp.Description("ISO country code (e.g. 'ID', 'US'). Default: 'DEFAULT'")),
	), s.handleEstimatePricing)

	// ═══ Group E: Operational ═══

	// 22. create_template
	s.mcpServer.AddTool(mcp.NewTool("create_template",
		mcp.WithDescription("Submit a new template to Meta for approval. Validates template configuration locally before submitting. On success, persists the template locally with status=pending. Approval arrives via webhook."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Template name (alphanumeric and underscores only)")),
		mcp.WithString("category", mcp.Required(), mcp.Description("Template category: 'UTILITY', 'MARKETING', 'AUTHENTICATION'")),
		mcp.WithString("language", mcp.Required(), mcp.Description("Language code (e.g. 'en', 'id')")),
		mcp.WithString("body_text", mcp.Required(), mcp.Description("Template body text (max 1024 characters)")),
	), s.handleCreateTemplate)

	// 23. sync_webhooks
	s.mcpServer.AddTool(mcp.NewTool("sync_webhooks",
		mcp.WithDescription("Trigger a manual re-sync of template statuses and pending webhook events from Meta. Use for recovery scenarios where webhooks may have been missed. Operation is idempotent."),
	), s.handleSyncWebhooks)

	// 24. archive_chat
	s.mcpServer.AddTool(mcp.NewTool("archive_chat",
		mcp.WithDescription("Transition a conversation to archived state. Only conversations with expired windows or in terminal state can be archived. Active conversations cannot be archived."),
		mcp.WithString("conversation_id", mcp.Required(), mcp.Description("The conversation UUID to archive")),
	), s.handleArchiveChat)

	log.Printf("[MCP] Registered %d tools", 24)
}

// ─── Helper Functions ────────────────────────────────────────────────────────

func normalizePhone(phone string) string {
	if len(phone) > 0 && phone[0] == '+' {
		return phone[1:]
	}
	return phone
}

// getActiveConv is a private helper to query active conversation state
func (s *Server) getActiveConv(ctx context.Context, customerID, channel string) (*state.Conversation, error) {
	return s.complianceEng.GetActiveConversation(ctx, customerID, channel)
}

// requireTier returns a standard FEATURE_NOT_AVAILABLE error if the current tier
// is below the required tier. The tier hierarchy is: oss < pro < enterprise.
func (s *Server) requireTier(requiredTier string) (*mcp.CallToolResult, error) {
	currentTier := strings.ToLower(s.cfg.Tier)
	required := strings.ToLower(requiredTier)

	tierLevel := map[string]int{
		"oss":        0,
		"pro":        1,
		"enterprise": 2,
	}

	currentLevel, ok := tierLevel[currentTier]
	if !ok {
		currentLevel = 0
	}
	requiredLevel, ok := tierLevel[required]
	if !ok {
		requiredLevel = 1
	}

	if currentLevel < requiredLevel {
		return toolError("FEATURE_NOT_AVAILABLE",
			fmt.Sprintf("This feature requires the %s tier. Your current tier is %s.", requiredTier, s.cfg.Tier),
			fmt.Sprintf("Upgrade to %s or higher to access this feature.", requiredTier))
	}

	return nil, nil
}

// toolError creates a structured error response with the standard error contract:
// reason_code, human_explanation, suggested_action.
func toolError(reasonCode, humanExplanation, suggestedAction string) (*mcp.CallToolResult, error) {
	result := fmt.Sprintf(`{"reason_code":"%s","human_explanation":"%s","suggested_action":"%s"}`,
		reasonCode, humanExplanation, suggestedAction)
	return mcp.NewToolResultText(result), nil
}

// newUUID generates a new UUID string
func newUUID() string {
	return uuid.New().String()
}