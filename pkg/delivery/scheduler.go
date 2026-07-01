package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/policy"
	"meta-business-mcp/pkg/userintel"
)

// Clock abstracts time for testability.
type Clock interface {
	Now() time.Time
}

// RealClock uses the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// Scheduler polls the database for due scheduled messages and campaigns,
// atomically claims them, runs dispatch-time compliance checks, and enqueues
// them to the NATS delivery stream.
type Scheduler struct {
	db            *db.DB
	cfg           *config.Config
	orchestrator  *Orchestrator
	complianceEng *compliance.Engine
	policyEng     *policy.Engine
	userManager   *userintel.Manager
	clock         Clock
	pollInterval  time.Duration
}

// NewScheduler creates a new Scheduler. Pass RealClock{} for production,
// or a FakeClock for tests.
func NewScheduler(
	database *db.DB,
	cfg *config.Config,
	orch *Orchestrator,
	compEng *compliance.Engine,
	polEng *policy.Engine,
	userMgr *userintel.Manager,
	clock Clock,
) (*Scheduler, error) {
	interval, err := time.ParseDuration(cfg.SchedPollInterval)
	if err != nil {
		interval = 30 * time.Second
	}

	return &Scheduler{
		db:            database,
		cfg:           cfg,
		orchestrator:  orch,
		complianceEng: compEng,
		policyEng:     polEng,
		userManager:   userMgr,
		clock:         clock,
		pollInterval:  interval,
	}, nil
}

// Start begins the poll loop. It runs until ctx is cancelled.
func (sc *Scheduler) Start(ctx context.Context) error {
	log.Printf("[Scheduler] Starting with poll interval %s", sc.pollInterval)
	ticker := time.NewTicker(sc.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Scheduler] Shutting down")
			return nil
		case <-ticker.C:
			sc.poll(ctx)
		}
	}
}

// poll runs one cycle of the scheduler.
func (sc *Scheduler) poll(ctx context.Context) {
	msgCount := sc.pollScheduledMessages(ctx)
	campCount := sc.pollScheduledCampaigns(ctx)

	if msgCount > 0 || campCount > 0 {
		log.Printf("[Scheduler] Poll cycle complete: %d messages dispatched, %d campaigns triggered", msgCount, campCount)
	}
}

// pollScheduledMessages finds and dispatches due scheduled messages.
func (sc *Scheduler) pollScheduledMessages(ctx context.Context) int {
	now := sc.clock.Now()

	rows, err := sc.db.Pool.Query(ctx,
		`SELECT id FROM messages WHERE status = 'scheduled' AND deliver_at <= $1 LIMIT 100`, now)
	if err != nil {
		log.Printf("[Scheduler] Error querying scheduled messages: %v", err)
		return 0
	}
	defer rows.Close()

	dispatched := 0
	for rows.Next() {
		var msgID string
		if err := rows.Scan(&msgID); err != nil {
			continue
		}

		if sc.claimAndDispatchMessage(ctx, msgID) {
			dispatched++
		}
	}
	return dispatched
}

// claimAndDispatchMessage atomically claims a scheduled message and dispatches it.
func (sc *Scheduler) claimAndDispatchMessage(ctx context.Context, msgID string) bool {
	// Atomic claim: only the instance that successfully updates the row owns the dispatch
	var claimedID string
	err := sc.db.Pool.QueryRow(ctx,
		`UPDATE messages SET status = 'queued', updated_at = NOW() 
		 WHERE id = $1 AND status = 'scheduled' 
		 RETURNING id`, msgID).Scan(&claimedID)
	if err != nil {
		// Another instance claimed it, or it no longer exists
		return false
	}

	// Load message details for compliance check and NATS publishing
	var customerID, messageType string
	var content json.RawMessage
	err = sc.db.Pool.QueryRow(ctx,
		`SELECT customer_id, message_type, content FROM messages WHERE id = $1`, claimedID,
	).Scan(&customerID, &messageType, &content)
	if err != nil {
		// Transition back to failed
		_, _ = sc.db.Pool.Exec(ctx,
			`UPDATE messages SET status = 'failed', error_message = $2, updated_at = NOW() WHERE id = $1`,
			claimedID, fmt.Sprintf("scheduler: failed to load message details: %v", err))
		return false
	}

	// Dispatch-time compliance check (the safety net)
	comp, err := sc.complianceEng.CheckCompliance(ctx, customerID, "whatsapp", messageType)
	if err != nil {
		sc.failMessage(ctx, claimedID, customerID, messageType, 0, fmt.Sprintf("compliance error: %v", err))
		return false
	}
	if !comp.Allowed {
		sc.failMessage(ctx, claimedID, customerID, messageType, 0,
			fmt.Sprintf("compliance denied at dispatch: %s (%s)", comp.ReasonCode, comp.HumanExplanation))
		return false
	}

	// Policy check at dispatch time
	customer, _ := sc.userManager.GetOrCreateCustomer(ctx, customerID, "whatsapp")
	policyRes, err := sc.policyEng.EvaluatePolicies(ctx, customerID, "whatsapp", messageType, customer.Tags)
	if err != nil {
		sc.failMessage(ctx, claimedID, customerID, messageType, 0, fmt.Sprintf("policy error: %v", err))
		return false
	}
	if !policyRes.Allowed {
		sc.failMessage(ctx, claimedID, customerID, messageType, 0,
			fmt.Sprintf("policy denied at dispatch: %s (%s)", policyRes.ReasonCode, policyRes.HumanExplanation))
		return false
	}

	// Parse content and enqueue to NATS
	var contentMap map[string]any
	if err := json.Unmarshal(content, &contentMap); err != nil {
		sc.failMessage(ctx, claimedID, customerID, messageType, 0, "failed to parse message content")
		return false
	}

	if err := sc.orchestrator.PublishOutboundMessage(ctx, claimedID, customerID, messageType, contentMap); err != nil {
		sc.failMessage(ctx, claimedID, customerID, messageType, 0, fmt.Sprintf("NATS publish failed: %v", err))
		return false
	}

	return true
}

// pollScheduledCampaigns finds and triggers due scheduled campaigns.
func (sc *Scheduler) pollScheduledCampaigns(ctx context.Context) int {
	now := sc.clock.Now()

	rows, err := sc.db.Pool.Query(ctx,
		`SELECT id FROM campaigns WHERE status = 'scheduled' AND scheduled_at <= $1 LIMIT 10`, now)
	if err != nil {
		log.Printf("[Scheduler] Error querying scheduled campaigns: %v", err)
		return 0
	}
	defer rows.Close()

	triggered := 0
	for rows.Next() {
		var campID string
		if err := rows.Scan(&campID); err != nil {
			continue
		}

		if sc.claimAndTriggerCampaign(ctx, campID) {
			triggered++
		}
	}
	return triggered
}

// claimAndTriggerCampaign atomically claims a campaign and enqueues individual messages.
func (sc *Scheduler) claimAndTriggerCampaign(ctx context.Context, campID string) bool {
	// Atomic claim: transition scheduled → running
	var claimedID string
	err := sc.db.Pool.QueryRow(ctx,
		`UPDATE campaigns SET status = 'running', updated_at = NOW() 
		 WHERE id = $1 AND status = 'scheduled' 
		 RETURNING id`, campID).Scan(&claimedID)
	if err != nil {
		return false
	}

	// Load campaign details
	var templateName, locale string
	var audienceFilterRaw json.RawMessage
	err = sc.db.Pool.QueryRow(ctx,
		`SELECT template_name, locale, audience_filter FROM campaigns WHERE id = $1`, claimedID,
	).Scan(&templateName, &locale, &audienceFilterRaw)
	if err != nil {
		_, _ = sc.db.Pool.Exec(ctx, `UPDATE campaigns SET status = 'failed', updated_at = NOW() WHERE id = $1`, claimedID)
		return false
	}

	// Parse audience filter to get recipient list
	var audienceFilter map[string]any
	_ = json.Unmarshal(audienceFilterRaw, &audienceFilter)

	// Load audience: customers matching the filter criteria
	recipients := sc.loadCampaignAudience(ctx, audienceFilter)
	if len(recipients) == 0 {
		_, _ = sc.db.Pool.Exec(ctx, `UPDATE campaigns SET status = 'completed', updated_at = NOW() WHERE id = $1`, claimedID)
		return false
	}

	// Enqueue individual messages for each recipient
	enqueued := 0
	for _, recipientID := range recipients {
		msgID := fmt.Sprintf("camp_%s_%s", claimedID[:8], newMsgID())

		contentPayload := map[string]any{
			"type": "template",
			"template": map[string]any{
				"name": templateName,
				"language": map[string]string{"code": locale},
			},
		}
		contentBytes, _ := json.Marshal(contentPayload)

		// Insert message as queued
		_, err := sc.db.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status) 
			 VALUES ($1, $2, 'outbound', 'template', $3, 'queued')`,
			msgID, recipientID, contentBytes)
		if err != nil {
			continue
		}

		// Publish to NATS
		if err := sc.orchestrator.PublishOutboundMessage(ctx, msgID, recipientID, "marketing", contentPayload); err != nil {
			_, _ = sc.db.Pool.Exec(ctx, `UPDATE messages SET status = 'failed', updated_at = NOW() WHERE id = $1`, msgID)
			continue
		}

		enqueued++
	}

	log.Printf("[Scheduler] Campaign %s: enqueued %d/%d messages", claimedID, enqueued, len(recipients))
	return true
}

// loadCampaignAudience loads recipients based on audience filter.
// For v1, this loads all customers matching basic filter criteria.
func (sc *Scheduler) loadCampaignAudience(ctx context.Context, filter map[string]any) []string {
	// Build query based on audience filter
	query := `SELECT id FROM customers WHERE 1=1`
	args := []any{}
	argIdx := 1

	if excludeTag, ok := filter["exclude_tag"].(string); ok && excludeTag != "" {
		query += fmt.Sprintf(" AND NOT ($%d = ANY(tags))", argIdx)
		args = append(args, excludeTag)
		argIdx++
	}

	if requireOptIn, ok := filter["require_opt_in"].(string); ok {
		switch requireOptIn {
		case "marketing":
			query += " AND opt_in_marketing = TRUE"
		case "utility":
			query += " AND opt_in_utility = TRUE"
		}
	}

	if channel, ok := filter["channel"].(string); ok && channel != "" {
		query += fmt.Sprintf(" AND channel = $%d", argIdx)
		args = append(args, channel)
		argIdx++
	}

	rows, err := sc.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var recipients []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			recipients = append(recipients, id)
		}
	}
	return recipients
}

// failMessage transitions a message to failed status and writes an audit log.
func (sc *Scheduler) failMessage(ctx context.Context, msgID, customerID, msgType string, errCode int, reason string) {
	_, _ = sc.db.Pool.Exec(ctx,
		`UPDATE messages SET status = 'failed', error_code = $2, error_message = $3, updated_at = NOW() WHERE id = $1`,
		msgID, errCode, reason)

	auditDetails, _ := json.Marshal(map[string]any{
		"message_id":   msgID,
		"customer_id":  customerID,
		"message_type": msgType,
		"error_code":   errCode,
		"reason":       reason,
	})
	_, _ = sc.db.Pool.Exec(ctx, "INSERT INTO audit_logs (action, details) VALUES ('scheduler_dispatch_failed', $1)", auditDetails)
}

// newMsgID generates a short unique ID for campaign messages.
func newMsgID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}