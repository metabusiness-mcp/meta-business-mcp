package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"meta-business-mcp/pkg/db"
)

type Campaign struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Type         string     `json:"type"` // 'broadcast', 'marketing', 'utility', 'authentication'
	Status       string     `json:"status"` // 'draft', 'scheduled', 'sending', 'paused', 'completed', 'cancelled'
	TemplateName string     `json:"template_name"`
	Locale       string     `json:"locale"`
	Variables    any        `json:"variables"`
	AudienceFilter any      `json:"audience_filter"`
	ScheduledAt  *time.Time `json:"scheduled_at"`
	SentCount    int        `json:"sent_count"`
	DeliveredCount int      `json:"delivered_count"`
	FailedCount  int        `json:"failed_count"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type Manager struct {
	db *db.DB
}

func NewManager(database *db.DB) *Manager {
	return &Manager{db: database}
}

func (m *Manager) CreateCampaign(ctx context.Context, name, cType, templateName, locale string, variables map[string]any, scheduledAt *time.Time) (*Campaign, error) {
	query := `
		INSERT INTO campaigns (name, type, status, template_name, locale, variables, scheduled_at, audience_filter) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, '{}'::JSONB) 
		RETURNING id, name, type, status, template_name, locale, variables, scheduled_at, sent_count, delivered_count, failed_count, created_at, updated_at`

	varsJSON, err := json.Marshal(variables)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal campaign variables: %w", err)
	}

	status := "draft"
	if scheduledAt != nil {
		status = "scheduled"
	}

	var c Campaign
	var varsRaw []byte
	err = m.db.Pool.QueryRow(ctx, query, name, cType, status, templateName, locale, varsJSON, scheduledAt).Scan(
		&c.ID, &c.Name, &c.Type, &c.Status, &c.TemplateName, &c.Locale, &varsRaw, &c.ScheduledAt,
		&c.SentCount, &c.DeliveredCount, &c.FailedCount, &c.CreatedAt, &c.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create campaign: %w", err)
	}

	_ = json.Unmarshal(varsRaw, &c.Variables)
	return &c, nil
}

func (m *Manager) GetCampaign(ctx context.Context, campaignID string) (*Campaign, error) {
	query := `
		SELECT id, name, type, status, template_name, locale, variables, scheduled_at, 
		       sent_count, delivered_count, failed_count, created_at, updated_at 
		FROM campaigns 
		WHERE id = $1`

	var c Campaign
	var varsRaw []byte
	err := m.db.Pool.QueryRow(ctx, query, campaignID).Scan(
		&c.ID, &c.Name, &c.Type, &c.Status, &c.TemplateName, &c.Locale, &varsRaw, &c.ScheduledAt,
		&c.SentCount, &c.DeliveredCount, &c.FailedCount, &c.CreatedAt, &c.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("campaign %s not found", campaignID)
		}
		return nil, fmt.Errorf("failed to get campaign: %w", err)
	}

	_ = json.Unmarshal(varsRaw, &c.Variables)
	return &c, nil
}

func (m *Manager) UpdateCampaignStatus(ctx context.Context, campaignID, status string) error {
	query := `
		UPDATE campaigns 
		SET status = $2, updated_at = NOW() 
		WHERE id = $1`

	_, err := m.db.Pool.Exec(ctx, query, campaignID, status)
	if err != nil {
		return fmt.Errorf("failed to update campaign status: %w", err)
	}
	return nil
}

func (m *Manager) UpdateCampaignProgress(ctx context.Context, campaignID string, sent, delivered, failed int) error {
	query := `
		UPDATE campaigns 
		SET sent_count = sent_count + $2, delivered_count = delivered_count + $3, failed_count = failed_count + $4, updated_at = NOW() 
		WHERE id = $1`

	_, err := m.db.Pool.Exec(ctx, query, campaignID, sent, delivered, failed)
	if err != nil {
		return fmt.Errorf("failed to update campaign progress: %w", err)
	}
	return nil
}
