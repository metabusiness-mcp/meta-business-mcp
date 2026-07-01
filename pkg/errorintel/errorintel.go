package errorintel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"meta-business-mcp/pkg/db"
)

type ErrorDetails struct {
	Code             int    `json:"code"`
	Category         string `json:"category"` // 'transient', 'permanent', 'user_related', 'policy'
	CanRetry         bool   `json:"can_retry"`
	HumanExplanation string `json:"human_explanation"`
	SuggestedAction  string `json:"suggested_action"`
}

type Engine struct {
	db *db.DB
}

func NewEngine(database *db.DB) *Engine {
	return &Engine{db: database}
}

func (e *Engine) ExplainError(ctx context.Context, code int) (*ErrorDetails, error) {
	query := `
		SELECT code, category, can_retry, human_explanation, suggested_action 
		FROM error_knowledge_base 
		WHERE code = $1`

	var d ErrorDetails
	err := e.db.Pool.QueryRow(ctx, query, code).Scan(
		&d.Code, &d.Category, &d.CanRetry, &d.HumanExplanation, &d.SuggestedAction,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			// Fallback default error details for unmapped codes
			return &ErrorDetails{
				Code:             code,
				Category:         "unexpected",
				CanRetry:         false,
				HumanExplanation: fmt.Sprintf("Meta returned an unclassified error (Code %d).", code),
				SuggestedAction:  "Examine application logs for details or consult Meta Cloud API reference.",
			}, nil
		}
		return nil, fmt.Errorf("failed to query error intel: %w", err)
	}

	return &d, nil
}
