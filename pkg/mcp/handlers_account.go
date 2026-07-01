package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ─── Sprint 2: Group D — Account & Cost Intelligence Tools ───────────────────

func (s *Server) handleGetAccountQuality(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Live API call to Meta Business Management endpoint
	url := fmt.Sprintf("%s/v20.0/%s", s.cfg.Meta.APIURL, s.cfg.Meta.WABAID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create request: %v", err)), nil
	}

	httpReq.Header.Set("Authorization", "Bearer "+s.cfg.Meta.AccessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to call Meta API: %v", err)), nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return mcp.NewToolResultError(fmt.Sprintf("Meta API returned status %d: %s", resp.StatusCode, string(body))), nil
	}

	// Parse Meta response for quality and messaging limit info
	var metaResp struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		MessagingLimitTier string `json:"messaging_limit_tier"`
		QualityRating      string `json:"quality_rating"`
		AccountMode        string `json:"account_mode"`
	}

	if err := json.Unmarshal(body, &metaResp); err != nil {
		// If the mock doesn't return these fields, return a best-effort response
		result := map[string]any{
			"waba_id":       s.cfg.Meta.WABAID,
			"raw_response":  string(body),
			"quality_tier":  "unknown",
			"current_limit": "unknown",
			"limit_tier":    "unknown",
			"note":          "Quality score fields not available from this API response.",
		}
		resJSON, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(resJSON)), nil
	}

	// Map quality rating to tier
	qualityTier := "green"
	switch metaResp.QualityRating {
	case "LOW":
		qualityTier = "red"
	case "MEDIUM":
		qualityTier = "yellow"
	case "HIGH":
		qualityTier = "green"
	}

	// Map messaging limit tier to numeric MPS
	currentLimit := "1000"
	switch metaResp.MessagingLimitTier {
	case "TIER_50":
		currentLimit = "50"
	case "TIER_250":
		currentLimit = "250"
	case "TIER_1K":
		currentLimit = "1000"
	case "TIER_10K":
		currentLimit = "10000"
	case "TIER_100K":
		currentLimit = "100000"
	case "TIER_UNLIMITED":
		currentLimit = "unlimited"
	}

	result := map[string]any{
		"waba_id":       metaResp.ID,
		"name":          metaResp.Name,
		"quality_tier":  qualityTier,
		"quality_rating": metaResp.QualityRating,
		"current_limit": currentLimit,
		"limit_tier":    metaResp.MessagingLimitTier,
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handleEstimateCost(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	messageType, err := req.RequireString("message_type")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: message_type (marketing, utility, authentication, service)"), nil
	}

	recipientCountry := req.GetString("recipient_country", "DEFAULT")
	quantity := req.GetInt("quantity", 1)

	if quantity < 1 {
		return mcp.NewToolResultError("quantity must be at least 1"), nil
	}

	// Look up pricing from conversation_pricing table
	var costPerConv float64
	var currency string
	var pricingCategory string

	err = s.db.Pool.QueryRow(ctx,
		`SELECT cost_per_conversation, currency, pricing_category 
		 FROM conversation_pricing 
		 WHERE country = $1 AND conversation_type = $2 
		 ORDER BY effective_date DESC LIMIT 1`,
		recipientCountry, messageType,
	).Scan(&costPerConv, &currency, &pricingCategory)

	if err != nil {
		// Fall back to DEFAULT pricing
		err = s.db.Pool.QueryRow(ctx,
			`SELECT cost_per_conversation, currency, pricing_category 
			 FROM conversation_pricing 
			 WHERE country = 'DEFAULT' AND conversation_type = $1 
			 ORDER BY effective_date DESC LIMIT 1`,
			messageType,
		).Scan(&costPerConv, &currency, &pricingCategory)

		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("no pricing data found for '%s' in '%s'", messageType, recipientCountry)), nil
		}
		recipientCountry = "DEFAULT (fallback)"
	}

	totalCost := costPerConv * float64(quantity)

	result := map[string]any{
		"message_type":         messageType,
		"recipient_country":    recipientCountry,
		"quantity":             quantity,
		"cost_per_conversation": costPerConv,
		"total_estimated_cost":  fmt.Sprintf("%.4f", totalCost),
		"currency":              currency,
		"pricing_category":      pricingCategory,
		"pricing_model_version": "Meta Conversation-Based Pricing (2024)",
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}

func (s *Server) handleEstimatePricing(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	recipientCountry := req.GetString("recipient_country", "DEFAULT")
	conversationType, err := req.RequireString("conversation_type")
	if err != nil {
		return mcp.NewToolResultError("missing parameter: conversation_type (marketing, utility, authentication, service)"), nil
	}

	// Look up exact pricing
	var costPerConv float64
	var currency string
	var pricingCategory string
	var effectiveDate string

	err = s.db.Pool.QueryRow(ctx,
		`SELECT cost_per_conversation, currency, pricing_category, effective_date::TEXT 
		 FROM conversation_pricing 
		 WHERE country = $1 AND conversation_type = $2 
		 ORDER BY effective_date DESC LIMIT 1`,
		recipientCountry, conversationType,
	).Scan(&costPerConv, &currency, &pricingCategory, &effectiveDate)

	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("no pricing data for '%s' conversations in '%s'", conversationType, recipientCountry)), nil
	}

	result := map[string]any{
		"country":              recipientCountry,
		"conversation_type":    conversationType,
		"cost_per_conversation": costPerConv,
		"currency":              currency,
		"pricing_category":      pricingCategory,
		"effective_date":        effectiveDate,
	}

	resJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resJSON)), nil
}