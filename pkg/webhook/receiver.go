package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"

	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/observability"
	"meta-business-mcp/pkg/state"
)

type Receiver struct {
	db          *db.DB
	cfg         *config.Config
	stateEngine *state.Engine
}

func NewReceiver(database *db.DB, cfg *config.Config, stateEngine *state.Engine) *Receiver {
	return &Receiver{
		db:          database,
		cfg:         cfg,
		stateEngine: stateEngine,
	}
}

// Meta Webhook Verification (GET /webhook)
func (rc *Receiver) Verify(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token == rc.cfg.Meta.WebhookVerifyToken {
		log.Println("[Webhook] Meta verification successful")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(challenge))
		return
	}

	log.Printf("[Webhook] Meta verification failed. Received token: %s", token)
	w.WriteHeader(http.StatusForbidden)
}

type WebhookPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Changes []struct {
			Field string          `json:"field"`
			Value json.RawMessage `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

// Meta Webhook Event Processing (POST /webhook)
func (rc *Receiver) HandleEvent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	for _, entry := range payload.Entry {
		for _, change := range changeList(entry.Changes) {
			rc.processChange(r.Context(), change.Field, change.Value)
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("EVENT_RECEIVED"))
}

// Helper to make sure we work around Go compiler issues on anonymous structs
type changeItem struct {
	Field string
	Value json.RawMessage
}

func changeList(changes []struct {
	Field string          `json:"field"`
	Value json.RawMessage `json:"value"`
}) []changeItem {
	list := make([]changeItem, len(changes))
	for i, c := range changes {
		list[i] = changeItem{Field: c.Field, Value: c.Value}
	}
	return list
}

func (rc *Receiver) processChange(ctx context.Context, field string, value json.RawMessage) {
	observability.WebhookEventsReceived.WithLabelValues(field, "processing").Inc()

	switch field {
	case "messages":
		var msgVal struct {
			Messages []struct {
				From      string `json:"from"`
				ID        string `json:"id"`
				Timestamp string `json:"timestamp"`
				Type      string `json:"type"`
				Text      *struct {
					Body string `json:"body"`
				} `json:"text,omitempty"`
			} `json:"messages,omitempty"`
			Statuses []struct {
				ID           string `json:"id"`
				Status       string `json:"status"`
				RecipientID  string `json:"recipient_id"`
				ErrorCode    int    `json:"error_code,omitempty"`
				ErrorMessage string `json:"error_message,omitempty"`
			} `json:"statuses,omitempty"`
		}

		if err := json.Unmarshal(value, &msgVal); err != nil {
			log.Printf("[Webhook] Failed to parse messages change value: %v", err)
			return
		}

		// Handle Inbound Messages
		for _, m := range msgVal.Messages {
			from := normalizePhone(m.From)
			log.Printf("[Webhook] Received inbound message from %s. Body: %v", from, m.Text)
			// Open/extend active conversation care window
			_, err := rc.stateEngine.OpenWindow(ctx, from, "whatsapp", "service")
			if err != nil {
				log.Printf("[Webhook] Failed to open conversation window: %v", err)
			}

			// Store inbound message log in Postgres
			var contentJSON []byte
			if m.Text != nil {
				contentJSON, _ = json.Marshal(map[string]string{"body": m.Text.Body})
			} else {
				contentJSON = []byte(`{}`)
			}

			_, _ = rc.db.Pool.Exec(ctx,
				`INSERT INTO messages (id, customer_id, direction, message_type, content, status) 
				 VALUES ($1, $2, 'inbound', $3, $4, 'read') 
				 ON CONFLICT (id) DO NOTHING`,
				m.ID, from, m.Type, contentJSON)
		}

		// Handle Message Status updates (delivered, read, failed)
		for _, s := range msgVal.Statuses {
			log.Printf("[Webhook] Status update for message %s: %s", s.ID, s.Status)
			
			var errCodeVal *int
			var errMsgVal *string
			if s.Status == "failed" {
				errCodeVal = &s.ErrorCode
				errMsgVal = &s.ErrorMessage
			}

			_, err := rc.db.Pool.Exec(ctx,
				`UPDATE messages 
				 SET status = $2, error_code = $3, error_message = $4, updated_at = NOW() 
				 WHERE id = $1`, s.ID, s.Status, errCodeVal, errMsgVal)
			if err != nil {
				log.Printf("[Webhook] Failed to update message status: %v", err)
			}
		}

	case "message_template_status_update":
		var templateUpdate struct {
			Event                 string `json:"event"`
			MessageTemplateID     string `json:"message_template_id"`
			MessageTemplateName   string `json:"message_template_name"`
			MessageTemplateLanguage string `json:"message_template_language"`
			EventType             string `json:"event_type"` // e.g. APPROVED, REJECTED
		}

		if err := json.Unmarshal(value, &templateUpdate); err != nil {
			log.Printf("[Webhook] Failed to parse template status update: %v", err)
			return
		}

		log.Printf("[Webhook] Template status update for %s: %s", templateUpdate.MessageTemplateName, templateUpdate.EventType)
		_, err := rc.db.Pool.Exec(ctx,
			`UPDATE templates 
			 SET status = $3, updated_at = NOW() 
			 WHERE name = $1 AND locale = $2`,
			templateUpdate.MessageTemplateName, templateUpdate.MessageTemplateLanguage, templateUpdate.EventType)
		if err != nil {
			log.Printf("[Webhook] Failed to update template status: %v", err)
		}
	}
}

func normalizePhone(phone string) string {
	if len(phone) > 0 && phone[0] == '+' {
		return phone[1:]
	}
	return phone
}
