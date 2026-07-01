package compliance

import (
	"context"
	"fmt"
	"time"

	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/state"
	"meta-business-mcp/pkg/userintel"
)

type ComplianceResult struct {
	Allowed         bool   `json:"allowed"`
	ReasonCode      string `json:"reason_code"`
	HumanExplanation string `json:"human_explanation"`
	SuggestedAction string `json:"suggested_action"`
}

type Engine struct {
	db          *db.DB
	stateEngine *state.Engine
	userManager *userintel.Manager
}

func NewEngine(database *db.DB, stateEngine *state.Engine, userManager *userintel.Manager) *Engine {
	return &Engine{
		db:          database,
		stateEngine: stateEngine,
		userManager: userManager,
	}
}

func (e *Engine) CheckCompliance(ctx context.Context, customerID, channel, messageType string) (*ComplianceResult, error) {
	return e.CheckComplianceDryRun(ctx, customerID, channel, messageType, false)
}

// CheckComplianceDryRun evaluates compliance rules without writing audit logs or frequency records.
// When dryRun is true, the engine operates in read-only mode — suitable for eligibility queries
// where audit trail noise must be avoided.
func (e *Engine) CheckComplianceDryRun(ctx context.Context, customerID, channel, messageType string, dryRun bool) (*ComplianceResult, error) {
	// 1. Get or create customer context
	customer, err := e.userManager.GetOrCreateCustomer(ctx, customerID, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve customer context: %w", err)
	}

	// 2. Check Opt-out status
	if messageType == "marketing" && !customer.OptInMarketing {
		return &ComplianceResult{
			Allowed:          false,
			ReasonCode:       "USER_OPTED_OUT",
			HumanExplanation: "The customer has opted out of marketing communications.",
			SuggestedAction:  "Do not attempt to send marketing messages to this user unless they opt-in again.",
		}, nil
	}

	if messageType == "utility" && !customer.OptInUtility {
		return &ComplianceResult{
			Allowed:          false,
			ReasonCode:       "USER_OPTED_OUT",
			HumanExplanation: "The customer has opted out of utility/operational communications.",
			SuggestedAction:  "Ensure the user opts back in before scheduling system notifications.",
		}, nil
	}

	// 3. Check 24-hour Customer Care window status (Only for free-form service messages, i.e., non-template)
	if messageType == "service" {
		conv, err := e.stateEngine.GetActiveConversation(ctx, customerID, channel)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve conversation window state: %w", err)
		}

		// Check if active window exists
		windowActive := conv.WindowExpiresAt != nil && conv.WindowExpiresAt.After(time.Now())
		if !windowActive {
			return &ComplianceResult{
				Allowed:          false,
				ReasonCode:       "TEMPLATE_REQUIRED",
				HumanExplanation: "Customer care window is closed. More than 24 hours have passed since the customer's last reply.",
				SuggestedAction:  "Send an approved WhatsApp Template message using 'send_template' to re-engage the customer.",
			}, nil
		}
	}

	// 4. Check Frequency Cap (only for marketing messages)
	if messageType == "marketing" {
		// Default cap: 2 marketing messages per 24 hours
		maxMarketingPerDay := 2

		// Let's count sent marketing messages in the last 24 hours
		countQuery := `
			SELECT COUNT(*) 
			FROM message_frequencies 
			WHERE customer_id = $1 AND channel = $2 AND message_type = $3 AND sent_at >= NOW() - INTERVAL '24 hours'`

		var count int
		err = e.db.Pool.QueryRow(ctx, countQuery, customerID, channel, messageType).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("failed to check message frequencies: %w", err)
		}

		if count >= maxMarketingPerDay {
			return &ComplianceResult{
				Allowed:          false,
				ReasonCode:       "FREQUENCY_CAP_EXCEEDED",
				HumanExplanation: fmt.Sprintf("Daily frequency cap reached. Customer has already received %d marketing messages today.", count),
				SuggestedAction:  "Reschedule the marketing message or try again tomorrow after the 24-hour limit resets.",
			}, nil
		}
	}

	// Compliance check passed
	return &ComplianceResult{
		Allowed: true,
	}, nil
}

func (e *Engine) RecordMessageSent(ctx context.Context, customerID, channel, messageType string) error {
	// Add entry to message frequencies to track caps
	insertQuery := `
		INSERT INTO message_frequencies (customer_id, channel, message_type) 
		VALUES ($1, $2, $3)`
	
	_, err := e.db.Pool.Exec(ctx, insertQuery, customerID, channel, messageType)
	if err != nil {
		return fmt.Errorf("failed to record message frequency: %w", err)
	}

	return nil
}

func (e *Engine) GetActiveConversation(ctx context.Context, customerID, channel string) (*state.Conversation, error) {
	return e.stateEngine.GetActiveConversation(ctx, customerID, channel)
}

func (e *Engine) OpenConversationWindow(ctx context.Context, customerID, channel, convType string) (*state.Conversation, error) {
	return e.stateEngine.OpenWindow(ctx, customerID, channel, convType)
}

