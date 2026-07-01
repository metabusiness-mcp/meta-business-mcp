package dashboard

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
)

// jsonWrite writes a JSON response with the given status code.
func jsonWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// jsonError writes a JSON error response.
func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonWrite(w, status, map[string]string{"error": msg})
}

// --- Auth Handlers ---

// handleLogin authenticates a user and creates a session.
func handleLogin(cfg *config.Config, store *SessionStore) http.HandlerFunc {
	type request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		if req.Username != cfg.Dashboard.Username {
			jsonError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}

		if !VerifyPassword(req.Password, cfg.Dashboard.PasswordHash) {
			jsonError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}

		token := store.Create()

		http.SetCookie(w, &http.Cookie{
			Name:     SessionCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int((24 * time.Hour).Seconds()),
		})

		jsonWrite(w, http.StatusOK, map[string]string{"token": token})
	}
}

// handleLogout clears the session cookie.
func handleLogout(store *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err == nil {
			store.Delete(cookie.Value)
		}

		http.SetCookie(w, &http.Cookie{
			Name:     SessionCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
		})

		jsonWrite(w, http.StatusOK, map[string]string{"status": "logged_out"})
	}
}

// handleAuthCheck returns whether the current session is valid.
func handleAuthCheck(store *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil || !store.Validate(cookie.Value) {
			jsonWrite(w, http.StatusOK, map[string]bool{"authenticated": false})
			return
		}
		jsonWrite(w, http.StatusOK, map[string]bool{"authenticated": true})
	}
}

// --- Data Handlers ---

// handleGetMessages returns messages with pagination and filters.
func handleGetMessages(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		limit := queryInt(r, "limit", 50)
		offset := queryInt(r, "offset", 0)
		status := r.URL.Query().Get("status")
		direction := r.URL.Query().Get("direction")
		customerID := r.URL.Query().Get("customer_id")

		where, args := buildWhere(map[string]string{
			"status":      status,
			"direction":   direction,
			"customer_id": customerID,
		})

		// Count total
		var total int
		countQuery := "SELECT COUNT(*) FROM messages" + where
		if err := database.Pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
			log.Printf("[Dashboard] messages count error: %v", err)
			jsonError(w, http.StatusInternalServerError, "failed to query messages")
			return
		}

		// Fetch rows
		dataQuery := `SELECT id, conversation_id, customer_id, direction, message_type, 
			status, error_code, error_message, retry_count, next_retry_at, created_at, updated_at
			FROM messages` + where + ` ORDER BY created_at DESC LIMIT $` + itoa(len(args)+1) + ` OFFSET $` + itoa(len(args)+2)
		args = append(args, limit, offset)

		rows, err := database.Pool.Query(ctx, dataQuery, args...)
		if err != nil {
			log.Printf("[Dashboard] messages query error: %v", err)
			jsonError(w, http.StatusInternalServerError, "failed to query messages")
			return
		}
		defer rows.Close()

		messages := make([]map[string]any, 0)
		for rows.Next() {
			var id, customerID, direction, messageType, status string
			var conversationID *string
			var errorCode *int
			var errorMessage *string
			var retryCount int
			var nextRetryAt, createdAt, updatedAt *time.Time

			if err := rows.Scan(&id, &conversationID, &customerID, &direction, &messageType,
				&status, &errorCode, &errorMessage, &retryCount, &nextRetryAt, &createdAt, &updatedAt); err != nil {
				log.Printf("[Dashboard] messages scan error: %v", err)
				continue
			}

			msg := map[string]any{
				"id":           id,
				"customer_id":  customerID,
				"direction":    direction,
				"message_type": messageType,
				"status":       status,
				"retry_count":  retryCount,
				"created_at":   createdAt,
				"updated_at":   updatedAt,
			}
			if conversationID != nil {
				msg["conversation_id"] = *conversationID
			}
			if errorCode != nil {
				msg["error_code"] = *errorCode
			}
			if errorMessage != nil {
				msg["error_message"] = *errorMessage
			}
			if nextRetryAt != nil {
				msg["next_retry_at"] = *nextRetryAt
			}
			messages = append(messages, msg)
		}

		jsonWrite(w, http.StatusOK, map[string]any{
			"messages": messages,
			"total":    total,
			"limit":    limit,
			"offset":   offset,
		})
	}
}

// handleGetConversations returns conversations with pagination and filters.
func handleGetConversations(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		limit := queryInt(r, "limit", 50)
		offset := queryInt(r, "offset", 0)
		status := r.URL.Query().Get("status")
		expiringSoon := r.URL.Query().Get("expiring_soon")

		where, args := buildWhere(map[string]string{
			"status": status,
		})

		// Handle expiring_soon filter: conversations expiring within 1 hour
		if expiringSoon == "true" {
			clause := "window_expires_at IS NOT NULL AND window_expires_at <= NOW() + INTERVAL '1 hour' AND window_expires_at > NOW()"
			if where == "" {
				where = " WHERE " + clause
			} else {
				where += " AND " + clause
			}
		}

		var total int
		countQuery := "SELECT COUNT(*) FROM conversations" + where
		if err := database.Pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
			log.Printf("[Dashboard] conversations count error: %v", err)
			jsonError(w, http.StatusInternalServerError, "failed to query conversations")
			return
		}

		dataQuery := `SELECT id, customer_id, channel, conversation_type, last_inbound_at, 
			window_expires_at, status, created_at, updated_at
			FROM conversations` + where + ` ORDER BY updated_at DESC LIMIT $` + itoa(len(args)+1) + ` OFFSET $` + itoa(len(args)+2)
		args = append(args, limit, offset)

		rows, err := database.Pool.Query(ctx, dataQuery, args...)
		if err != nil {
			log.Printf("[Dashboard] conversations query error: %v", err)
			jsonError(w, http.StatusInternalServerError, "failed to query conversations")
			return
		}
		defer rows.Close()

		conversations := make([]map[string]any, 0)
		for rows.Next() {
			var id, customerID, channel, convType, status string
			var lastInboundAt, windowExpiresAt, createdAt, updatedAt *time.Time

			if err := rows.Scan(&id, &customerID, &channel, &convType, &lastInboundAt,
				&windowExpiresAt, &status, &createdAt, &updatedAt); err != nil {
				log.Printf("[Dashboard] conversations scan error: %v", err)
				continue
			}

			conv := map[string]any{
				"id":                id,
				"customer_id":       customerID,
				"channel":           channel,
				"conversation_type": convType,
				"status":            status,
				"created_at":        createdAt,
				"updated_at":        updatedAt,
			}
			if lastInboundAt != nil {
				conv["last_inbound_at"] = *lastInboundAt
			}
			if windowExpiresAt != nil {
				conv["window_expires_at"] = *windowExpiresAt
			}
			conversations = append(conversations, conv)
		}

		jsonWrite(w, http.StatusOK, map[string]any{
			"conversations": conversations,
			"total":         total,
			"limit":         limit,
			"offset":        offset,
		})
	}
}

// handleGetComplianceEvents returns audit log entries for compliance checks.
func handleGetComplianceEvents(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		limit := queryInt(r, "limit", 100)
		offset := queryInt(r, "offset", 0)

		var total int
		countQuery := "SELECT COUNT(*) FROM audit_logs WHERE action = 'compliance_check'"
		if err := database.Pool.QueryRow(ctx, countQuery).Scan(&total); err != nil {
			log.Printf("[Dashboard] compliance events count error: %v", err)
			jsonError(w, http.StatusInternalServerError, "failed to query compliance events")
			return
		}

		dataQuery := `SELECT id, action, details, created_at FROM audit_logs 
			WHERE action = 'compliance_check' ORDER BY created_at DESC LIMIT $1 OFFSET $2`

		rows, err := database.Pool.Query(ctx, dataQuery, limit, offset)
		if err != nil {
			log.Printf("[Dashboard] compliance events query error: %v", err)
			jsonError(w, http.StatusInternalServerError, "failed to query compliance events")
			return
		}
		defer rows.Close()

		events := make([]map[string]any, 0)
		for rows.Next() {
			var id, action string
			var details []byte
			var createdAt *time.Time

			if err := rows.Scan(&id, &action, &details, &createdAt); err != nil {
				log.Printf("[Dashboard] compliance events scan error: %v", err)
				continue
			}

			var detailsMap map[string]any
			if details != nil {
				_ = json.Unmarshal(details, &detailsMap)
			}

			event := map[string]any{
				"id":         id,
				"action":     action,
				"details":    detailsMap,
				"created_at": createdAt,
			}
			events = append(events, event)
		}

		jsonWrite(w, http.StatusOK, map[string]any{
			"events": events,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		})
	}
}

// handleGetTemplates returns templates with optional filters.
func handleGetTemplates(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		status := r.URL.Query().Get("status")
		category := r.URL.Query().Get("category")

		where, args := buildWhere(map[string]string{
			"status":   status,
			"category": category,
		})

		var total int
		countQuery := "SELECT COUNT(*) FROM templates" + where
		if err := database.Pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
			log.Printf("[Dashboard] templates count error: %v", err)
			jsonError(w, http.StatusInternalServerError, "failed to query templates")
			return
		}

		dataQuery := `SELECT name, locale, category, status, body_text, variables, created_at, updated_at
			FROM templates` + where + ` ORDER BY name ASC`

		rows, err := database.Pool.Query(ctx, dataQuery, args...)
		if err != nil {
			log.Printf("[Dashboard] templates query error: %v", err)
			jsonError(w, http.StatusInternalServerError, "failed to query templates")
			return
		}
		defer rows.Close()

		templates := make([]map[string]any, 0)
		for rows.Next() {
			var name, locale, category, status, bodyText string
			var variables []byte
			var createdAt, updatedAt *time.Time

			if err := rows.Scan(&name, &locale, &category, &status, &bodyText, &variables, &createdAt, &updatedAt); err != nil {
				log.Printf("[Dashboard] templates scan error: %v", err)
				continue
			}

			var variablesJSON any
			if variables != nil {
				_ = json.Unmarshal(variables, &variablesJSON)
			}

			tmpl := map[string]any{
				"name":       name,
				"locale":     locale,
				"category":   category,
				"status":     status,
				"body_text":  bodyText,
				"variables":  variablesJSON,
				"created_at": createdAt,
				"updated_at": updatedAt,
			}
			templates = append(templates, tmpl)
		}

		jsonWrite(w, http.StatusOK, map[string]any{
			"templates": templates,
			"total":     total,
		})
	}
}

// handleGetMetricsSummary returns aggregated metrics for the dashboard.
func handleGetMetricsSummary(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		summary := map[string]any{
			"messages_sent_today":     0,
			"messages_delivered":      0,
			"messages_failed":         0,
			"active_conversations":    0,
			"compliance_checks_today": 0,
			"compliance_pass_rate":    0.0,
			"templates_approved":      0,
			"templates_pending":       0,
			"templates_rejected":      0,
		}

		// Messages sent today
		var messagesSentToday int
		err := database.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM messages WHERE direction = 'outbound' AND created_at >= CURRENT_DATE").Scan(&messagesSentToday)
		if err == nil {
			summary["messages_sent_today"] = messagesSentToday
		}

		// Messages delivered (status = delivered or read)
		var messagesDelivered int
		err = database.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM messages WHERE status IN ('delivered', 'read') AND created_at >= CURRENT_DATE").Scan(&messagesDelivered)
		if err == nil {
			summary["messages_delivered"] = messagesDelivered
		}

		// Messages failed today
		var messagesFailed int
		err = database.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM messages WHERE status = 'failed' AND created_at >= CURRENT_DATE").Scan(&messagesFailed)
		if err == nil {
			summary["messages_failed"] = messagesFailed
		}

		// Active conversations
		var activeConversations int
		err = database.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM conversations WHERE status = 'active'").Scan(&activeConversations)
		if err == nil {
			summary["active_conversations"] = activeConversations
		}

		// Compliance checks today
		var complianceChecksToday int
		err = database.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM audit_logs WHERE action = 'compliance_check' AND created_at >= CURRENT_DATE").Scan(&complianceChecksToday)
		if err == nil {
			summary["compliance_checks_today"] = complianceChecksToday
		}

		// Compliance pass rate today
		var compliancePass int
		err = database.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM audit_logs WHERE action = 'compliance_check' AND created_at >= CURRENT_DATE 
			 AND details->>'allowed' = 'true'`).Scan(&compliancePass)
		if err == nil && complianceChecksToday > 0 {
			summary["compliance_pass_rate"] = float64(compliancePass) / float64(complianceChecksToday)
		}

		// Template counts by status
		var approved, pending, rejected int
		_ = database.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM templates WHERE status = 'approved'").Scan(&approved)
		_ = database.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM templates WHERE status = 'PENDING'").Scan(&pending)
		_ = database.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM templates WHERE status = 'REJECTED'").Scan(&rejected)
		summary["templates_approved"] = approved
		summary["templates_pending"] = pending
		summary["templates_rejected"] = rejected

		jsonWrite(w, http.StatusOK, summary)
	}
}

// handleGetWebhookConfig returns masked webhook configuration.
func handleGetWebhookConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		webhookURL := "https://your-domain.com/webhook"
		if cfg.Meta.WebhookVerifyToken != "" {
			webhookURL = "https://your-domain.com/webhook"
		}

		jsonWrite(w, http.StatusOK, map[string]any{
			"webhook_url":          webhookURL,
			"verify_token":         maskString(cfg.Meta.WebhookVerifyToken),
			"signature_validation": true,
		})
	}
}

// handleGetMetaConfig returns masked Meta API configuration.
func handleGetMetaConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonWrite(w, http.StatusOK, map[string]any{
			"phone_number_id": maskString(cfg.Meta.PhoneNumberID),
			"waba_id":         maskString(cfg.Meta.WABAID),
			"api_url":         cfg.Meta.APIURL,
			"tier":            cfg.Tier,
		})
	}
}

// --- Helper Functions ---

// queryInt parses an integer query parameter with a default value.
func queryInt(r *http.Request, key string, defaultVal int) int {
	val := r.URL.Query().Get(key)
	if val == "" {
		return defaultVal
	}
	var n int
	for _, c := range val {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			return defaultVal
		}
	}
	if n == 0 {
		return defaultVal
	}
	return n
}

// buildWhere builds a WHERE clause from a map of column=value filters.
func buildWhere(filters map[string]string) (string, []any) {
	var clauses []string
	var args []any
	argIdx := 1

	for col, val := range filters {
		if val == "" {
			continue
		}
		clauses = append(clauses, col+" = $"+itoa(argIdx))
		args = append(args, val)
		argIdx++
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// maskString masks a string showing only the first and last 4 characters.
func maskString(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}

// itoa is a simple int-to-string conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// Unused but required to avoid import warnings.
var _ = context.Background