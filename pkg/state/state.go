package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"meta-business-mcp/pkg/db"
)

type Conversation struct {
	ID                   string     `json:"id"`
	CustomerID           string     `json:"customer_id"`
	Channel              string     `json:"channel"`
	LastInboundAt        *time.Time `json:"last_inbound_at"`
	LastOutboundAt       *time.Time `json:"last_outbound_at"`
	WindowExpiresAt      *time.Time `json:"window_expires_at"`
	ConversationType     string     `json:"conversation_type"`
	ActiveTemplateID     *string    `json:"active_template_id"`
	MarketingEligibility string     `json:"marketing_eligibility"`
	UtilityEligibility   string     `json:"utility_eligibility"`
}

type Engine struct {
	db *db.DB
}

func NewEngine(database *db.DB) *Engine {
	return &Engine{db: database}
}

func (e *Engine) GetActiveConversation(ctx context.Context, customerID, channel string) (*Conversation, error) {
	redisKey := fmt.Sprintf("conv:%s:%s", customerID, channel)

	// 1. Try Redis Cache
	cachedVal, err := e.db.Redis.Get(ctx, redisKey).Result()
	if err == nil {
		var conv Conversation
		if err := json.Unmarshal([]byte(cachedVal), &conv); err == nil {
			return &conv, nil
		}
	}

	// 2. Query Postgres
	query := `
		SELECT id, customer_id, channel, last_inbound_at, last_outbound_at, window_expires_at, 
		       conversation_type, active_template_id, marketing_eligibility, utility_eligibility 
		FROM conversations 
		WHERE customer_id = $1 AND channel = $2 AND window_expires_at > NOW() 
		LIMIT 1`

	var conv Conversation
	err = e.db.Pool.QueryRow(ctx, query, customerID, channel).Scan(
		&conv.ID, &conv.CustomerID, &conv.Channel, &conv.LastInboundAt, &conv.LastOutboundAt, 
		&conv.WindowExpiresAt, &conv.ConversationType, &conv.ActiveTemplateID, 
		&conv.MarketingEligibility, &conv.UtilityEligibility,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No active conversation window
			return &Conversation{
				CustomerID:           customerID,
				Channel:              channel,
				ConversationType:     "service",
				MarketingEligibility: "eligible",
				UtilityEligibility:   "eligible",
			}, nil
		}
		return nil, fmt.Errorf("failed to query conversation: %w", err)
	}

	// 3. Cache in Redis
	if conv.WindowExpiresAt != nil {
		ttl := time.Until(*conv.WindowExpiresAt)
		if ttl > 0 {
			convJSON, _ := json.Marshal(conv)
			_ = e.db.Redis.Set(ctx, redisKey, convJSON, ttl).Err()
		}
	}

	return &conv, nil
}

func (e *Engine) OpenWindow(ctx context.Context, customerID, channel, convType string) (*Conversation, error) {
	expiresAt := time.Now().Add(24 * time.Hour)
	now := time.Now()

	// 1. Try to update active conversation if exists
	updateQuery := `
		UPDATE conversations 
		SET last_inbound_at = $3, window_expires_at = $4, conversation_type = $5, updated_at = NOW() 
		WHERE customer_id = $1 AND channel = $2 AND window_expires_at > NOW() 
		RETURNING id, customer_id, channel, last_inbound_at, last_outbound_at, window_expires_at, 
		          conversation_type, active_template_id, marketing_eligibility, utility_eligibility`

	var conv Conversation
	err := e.db.Pool.QueryRow(ctx, updateQuery, customerID, channel, now, expiresAt, convType).Scan(
		&conv.ID, &conv.CustomerID, &conv.Channel, &conv.LastInboundAt, &conv.LastOutboundAt, 
		&conv.WindowExpiresAt, &conv.ConversationType, &conv.ActiveTemplateID, 
		&conv.MarketingEligibility, &conv.UtilityEligibility,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No active conversation to update, insert a new one
			insertQuery := `
				INSERT INTO conversations (customer_id, channel, last_inbound_at, window_expires_at, conversation_type) 
				VALUES ($1, $2, $3, $4, $5) 
				RETURNING id, customer_id, channel, last_inbound_at, last_outbound_at, window_expires_at, 
				          conversation_type, active_template_id, marketing_eligibility, utility_eligibility`
			
			err = e.db.Pool.QueryRow(ctx, insertQuery, customerID, channel, now, expiresAt, convType).Scan(
				&conv.ID, &conv.CustomerID, &conv.Channel, &conv.LastInboundAt, &conv.LastOutboundAt, 
				&conv.WindowExpiresAt, &conv.ConversationType, &conv.ActiveTemplateID, 
				&conv.MarketingEligibility, &conv.UtilityEligibility,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to insert new conversation: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to update active conversation: %w", err)
		}
	}

	// 2. Cache in Redis
	redisKey := fmt.Sprintf("conv:%s:%s", customerID, channel)
	convJSON, _ := json.Marshal(conv)
	ttl := time.Until(expiresAt)
	_ = e.db.Redis.Set(ctx, redisKey, convJSON, ttl).Err()

	return &conv, nil
}

// ListConversations returns conversations with pagination and optional status filter.
// Status filter values: "open" (window active), "closed" (no active window), "expiring_soon" (window closes within 2 hours).
func (e *Engine) ListConversations(ctx context.Context, channel, statusFilter string, limit, offset int) ([]Conversation, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	// Build WHERE clause based on status filter
	where := "WHERE channel = $1"
	args := []any{channel}
	argIdx := 2

	switch statusFilter {
	case "open":
		where += " AND window_expires_at > NOW() AND status != 'archived'"
	case "closed":
		where += " AND (window_expires_at IS NULL OR window_expires_at <= NOW()) AND status != 'archived'"
	case "expiring_soon":
		where += " AND window_expires_at > NOW() AND window_expires_at <= NOW() + INTERVAL '2 hours' AND status != 'archived'"
	case "archived":
		where += " AND status = 'archived'"
	default:
		// empty string means all active (non-archived)
		where += " AND status != 'archived'"
	}

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM conversations %s", where)
	var total int
	if err := e.db.Pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count conversations: %w", err)
	}

	// Fetch page
	query := fmt.Sprintf(`
		SELECT id, customer_id, channel, last_inbound_at, last_outbound_at, window_expires_at,
		       conversation_type, active_template_id, marketing_eligibility, utility_eligibility
		FROM conversations %s
		ORDER BY updated_at DESC
		LIMIT $%d OFFSET $%d`, where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := e.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query conversations: %w", err)
	}
	defer rows.Close()

	var convs []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.CustomerID, &c.Channel, &c.LastInboundAt, &c.LastOutboundAt,
			&c.WindowExpiresAt, &c.ConversationType, &c.ActiveTemplateID,
			&c.MarketingEligibility, &c.UtilityEligibility); err != nil {
			return nil, 0, fmt.Errorf("failed to scan conversation: %w", err)
		}
		convs = append(convs, c)
	}

	return convs, total, nil
}

func (e *Engine) UpdateLastOutbound(ctx context.Context, customerID, channel string) error {
	now := time.Now()
	query := `
		UPDATE conversations 
		SET last_outbound_at = $3, updated_at = NOW() 
		WHERE customer_id = $1 AND channel = $2 AND window_expires_at > NOW() 
		RETURNING id`

	var id string
	err := e.db.Pool.QueryRow(ctx, query, customerID, channel, now).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No active window to update (might be sending message out-of-window using a template)
			return nil
		}
		return fmt.Errorf("failed to update last outbound timestamp: %w", err)
	}

	// Invalidate Redis cache to refresh values next query
	redisKey := fmt.Sprintf("conv:%s:%s", customerID, channel)
	_ = e.db.Redis.Del(ctx, redisKey).Err()

	return nil
}

