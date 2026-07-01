package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/db"
)

type Policy struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Channel     string `json:"channel"`
	MessageType string `json:"message_type"`
	IsEnabled   bool   `json:"is_enabled"`
	Rules       string `json:"rules"` // JSON String
}

type Engine struct {
	db *db.DB
}

func NewEngine(database *db.DB) *Engine {
	return &Engine{db: database}
}

func (e *Engine) EvaluatePolicies(ctx context.Context, customerID, channel, messageType string, customerTags []string) (*compliance.ComplianceResult, error) {
	// Query active policies for this channel and message type
	query := `
		SELECT id, name, type, rules 
		FROM policies 
		WHERE is_enabled = TRUE AND channel = $1 AND (message_type = $2 OR message_type = 'all')`

	rows, err := e.db.Pool.Query(ctx, query, channel, messageType)
	if err != nil {
		return nil, fmt.Errorf("failed to query policies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var pID, pName, pType string
		var rulesRaw []byte
		if err := rows.Scan(&pID, &pName, &pType, &rulesRaw); err != nil {
			return nil, fmt.Errorf("failed to scan policy: %w", err)
		}

		var rules map[string]any
		if err := json.Unmarshal(rulesRaw, &rules); err != nil {
			continue // skip malformed policy rules
		}

		// Evaluate policy type
		switch pType {
		case "time_restriction":
			// Check if message is sent during restricted hours
			denyAfterStr, ok := rules["deny_after"].(string)
			if !ok {
				continue
			}
			timezoneStr, ok := rules["timezone"].(string)
			if !ok {
				timezoneStr = "UTC"
			}

			loc, err := time.LoadLocation(timezoneStr)
			if err != nil {
				loc = time.UTC
			}

			nowLocal := time.Now().In(loc)
			denyTime, err := time.ParseInLocation("15:04", denyAfterStr, loc)
			if err != nil {
				continue
			}

			// Compare hour and minute
			nowHour, nowMin, _ := nowLocal.Clock()
			denyHour, denyMin, _ := denyTime.Clock()

			if nowHour > denyHour || (nowHour == denyHour && nowMin >= denyMin) {
				return &compliance.ComplianceResult{
					Allowed:          false,
					ReasonCode:       "POLICY_RESTRICTION",
					HumanExplanation: fmt.Sprintf("Message blocked by policy '%s': Sending marketing messages is restricted after %s in %s.", pName, denyAfterStr, timezoneStr),
					SuggestedAction:  "Queue this message for next-day dispatch or send it tomorrow morning.",
				}, nil
			}

		case "segment_exclusion":
			// Check if customer possesses an excluded tag
			excludeTag, ok := rules["exclude_tag"].(string)
			if !ok {
				continue
			}

			for _, tag := range customerTags {
				if tag == excludeTag {
					return &compliance.ComplianceResult{
						Allowed:          false,
						ReasonCode:       "POLICY_RESTRICTION",
						HumanExplanation: fmt.Sprintf("Message blocked by policy '%s': Customer has tag '%s' which is excluded from marketing campaigns.", pName, excludeTag),
						SuggestedAction:  "Excluding VIP/Sensitive customers from generic marketing broadcasts is required by policy.",
					}, nil
				}
			}
		}
	}

	return &compliance.ComplianceResult{
		Allowed: true,
	}, nil
}
