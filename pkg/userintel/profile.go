package userintel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"meta-business-mcp/pkg/db"
)

type Customer struct {
	ID                 string     `json:"id"`
	Channel            string     `json:"channel"`
	OptInMarketing     bool       `json:"opt_in_marketing"`
	OptInUtility       bool       `json:"opt_in_utility"`
	LastInteractionAt  *time.Time `json:"last_interaction_at"`
	EngagementScore    float64    `json:"engagement_score"`
	Tags               []string   `json:"tags"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type Manager struct {
	db *db.DB
}

func NewManager(database *db.DB) *Manager {
	return &Manager{db: database}
}

func (m *Manager) GetOrCreateCustomer(ctx context.Context, id, channel string) (*Customer, error) {
	// First, try to query
	query := `
		SELECT id, channel, opt_in_marketing, opt_in_utility, last_interaction_at, engagement_score, tags, created_at, updated_at 
		FROM customers 
		WHERE id = $1 AND channel = $2`

	var c Customer
	err := m.db.Pool.QueryRow(ctx, query, id, channel).Scan(
		&c.ID, &c.Channel, &c.OptInMarketing, &c.OptInUtility, &c.LastInteractionAt, &c.EngagementScore, &c.Tags, &c.CreatedAt, &c.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Create new customer
			insertQuery := `
				INSERT INTO customers (id, channel, opt_in_marketing, opt_in_utility, engagement_score, tags, last_interaction_at) 
				VALUES ($1, $2, TRUE, TRUE, 1.0, ARRAY[]::TEXT[], NOW()) 
				RETURNING id, channel, opt_in_marketing, opt_in_utility, last_interaction_at, engagement_score, tags, created_at, updated_at`

			err = m.db.Pool.QueryRow(ctx, insertQuery, id, channel).Scan(
				&c.ID, &c.Channel, &c.OptInMarketing, &c.OptInUtility, &c.LastInteractionAt, &c.EngagementScore, &c.Tags, &c.CreatedAt, &c.UpdatedAt,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to create customer: %w", err)
			}
			return &c, nil
		}
		return nil, fmt.Errorf("failed to query customer: %w", err)
	}

	return &c, nil
}

func (m *Manager) UpdateOptIn(ctx context.Context, id, channel string, marketing, utility bool) error {
	query := `
		UPDATE customers 
		SET opt_in_marketing = $3, opt_in_utility = $4, updated_at = NOW() 
		WHERE id = $1 AND channel = $2`

	_, err := m.db.Pool.Exec(ctx, query, id, channel, marketing, utility)
	if err != nil {
		return fmt.Errorf("failed to update customer opt-in: %w", err)
	}
	return nil
}

func (m *Manager) UpdateTags(ctx context.Context, id, channel string, tags []string) error {
	query := `
		UPDATE customers 
		SET tags = $3, updated_at = NOW() 
		WHERE id = $1 AND channel = $2`

	_, err := m.db.Pool.Exec(ctx, query, id, channel, tags)
	if err != nil {
		return fmt.Errorf("failed to update customer tags: %w", err)
	}
	return nil
}

func (m *Manager) RecordInteraction(ctx context.Context, id, channel string) error {
	query := `
		UPDATE customers 
		SET last_interaction_at = NOW(), updated_at = NOW() 
		WHERE id = $1 AND channel = $2`

	_, err := m.db.Pool.Exec(ctx, query, id, channel)
	if err != nil {
		return fmt.Errorf("failed to record interaction: %w", err)
	}
	return nil
}

