package template

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
)

type Template struct {
	Name      string         `json:"name"`
	Locale    string         `json:"locale"`
	Category  string         `json:"category"`
	Status    string         `json:"status"`
	BodyText  string         `json:"body_text"`
	Variables map[string]any `json:"variables"` // list of param indexes, e.g. {"1": true, "2": true}
}

type Manager struct {
	db  *db.DB
	cfg *config.Config
}

type MetaTemplatesResponse struct {
	Data []struct {
		Name       string `json:"name"`
		Category   string `json:"category"`
		Language   string `json:"language"`
		Status     string `json:"status"`
		Components []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"components"`
	} `json:"data"`
}

func NewManager(database *db.DB, cfg *config.Config) *Manager {
	return &Manager{
		db:  database,
		cfg: cfg,
	}
}

func (m *Manager) SyncTemplates(ctx context.Context) error {
	url := fmt.Sprintf("%s/v20.0/%s/message_templates", m.cfg.Meta.APIURL, m.cfg.Meta.WABAID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create sync request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+m.cfg.Meta.AccessToken)
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call Meta template sync: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("meta sync status error %d: %s", resp.StatusCode, string(body))
	}

	var metaResp MetaTemplatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&metaResp); err != nil {
		return fmt.Errorf("failed to decode meta templates response: %w", err)
	}

	for _, t := range metaResp.Data {
		var bodyText string
		for _, comp := range t.Components {
			if comp.Type == "BODY" {
				bodyText = comp.Text
			}
		}

		// Save/Update in PostgreSQL
		query := `
			INSERT INTO templates (name, locale, category, status, body_text, variables, updated_at) 
			VALUES ($1, $2, $3, $4, $5, '{}'::JSONB, NOW()) 
			ON CONFLICT (name, locale) 
			DO UPDATE SET category = $3, status = $4, body_text = $5, updated_at = NOW()`
		
		_, err = m.db.Pool.Exec(ctx, query, t.Name, t.Language, t.Category, t.Status, bodyText)
		if err != nil {
			return fmt.Errorf("failed to upsert template %s: %w", t.Name, err)
		}
	}

	return nil
}

// ListTemplates returns templates filtered by status, category, and locale with pagination.
// Empty filter strings mean "match all". Limit defaults to 50 if <= 0. Offset defaults to 0 if negative.
func (m *Manager) ListTemplates(ctx context.Context, statusFilter, categoryFilter, localeFilter string, limit, offset int) ([]Template, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	// Build dynamic WHERE clause
	where := "WHERE 1=1"
	args := []any{}
	argIdx := 1

	if statusFilter != "" {
		where += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, statusFilter)
		argIdx++
	}
	if categoryFilter != "" {
		where += fmt.Sprintf(" AND category = $%d", argIdx)
		args = append(args, categoryFilter)
		argIdx++
	}
	if localeFilter != "" {
		where += fmt.Sprintf(" AND locale = $%d", argIdx)
		args = append(args, localeFilter)
		argIdx++
	}

	// Count total matching rows
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM templates %s", where)
	var total int
	if err := m.db.Pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count templates: %w", err)
	}

	// Fetch page
	query := fmt.Sprintf(`
		SELECT name, locale, category, status, body_text 
		FROM templates %s 
		ORDER BY name ASC, locale ASC 
		LIMIT $%d OFFSET $%d`, where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := m.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query templates: %w", err)
	}
	defer rows.Close()

	var templates []Template
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.Name, &t.Locale, &t.Category, &t.Status, &t.BodyText); err != nil {
			return nil, 0, fmt.Errorf("failed to scan template: %w", err)
		}
		templates = append(templates, t)
	}

	return templates, total, nil
}

func (m *Manager) GetTemplate(ctx context.Context, name, locale string) (*Template, error) {
	query := `
		SELECT name, locale, category, status, body_text 
		FROM templates 
		WHERE name = $1 AND locale = $2`

	var t Template
	err := m.db.Pool.QueryRow(ctx, query, name, locale).Scan(&t.Name, &t.Locale, &t.Category, &t.Status, &t.BodyText)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("template '%s' (%s) not found in DB", name, locale)
		}
		return nil, fmt.Errorf("failed to get template: %w", err)
	}

	return &t, nil
}
