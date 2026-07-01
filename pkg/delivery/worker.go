package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/errorintel"
	"meta-business-mcp/pkg/ratelimit"
	"meta-business-mcp/pkg/state"
)

type Worker struct {
	db             *db.DB
	cfg            *config.Config
	js             jetstream.JetStream
	limiter        *ratelimit.Limiter
	complianceEng  *compliance.Engine
	stateEng       *state.Engine
	errorIntel     *errorintel.Engine
	httpClient     *http.Client
}

func NewWorker(
	database *db.DB,
	cfg *config.Config,
	orchestrator *Orchestrator,
	limiter *ratelimit.Limiter,
	compEng *compliance.Engine,
	stateEng *state.Engine,
	errorIntel *errorintel.Engine,
) *Worker {
	return &Worker{
		db:            database,
		cfg:           cfg,
		js:            orchestrator.js,
		limiter:       limiter,
		complianceEng: compEng,
		stateEng:      stateEng,
		errorIntel:    errorIntel,
		httpClient:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (w *Worker) Start(ctx context.Context) error {
	// Create consumer on the stream
	cons, err := w.js.CreateOrUpdateConsumer(ctx, "META_MCP_DELIVERY", jetstream.ConsumerConfig{
		Durable:       "delivery-workers",
		FilterSubject: ">",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    3,
	})
	if err != nil {
		return fmt.Errorf("failed to create NATS consumer: %w", err)
	}

	go func() {
		log.Println("[Worker] Message delivery worker pool started")
		for {
			select {
			case <-ctx.Done():
				log.Println("[Worker] Message delivery worker stopped")
				return
			default:
				// Pull messages from NATS
				msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(500*time.Millisecond))
				if err != nil {
					continue
				}

				for msg := range msgs.Messages() {
					w.processMessage(ctx, msg)
				}
			}
		}
	}()

	return nil
}

func (w *Worker) processMessage(ctx context.Context, msg jetstream.Msg) {
	var env MessageEnvelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil {
		log.Printf("[Worker] Failed to unmarshal envelope: %v. Message ACKed/Dropped.", err)
		_ = msg.Ack()
		return
	}

	// 1. Enforce Rate Limiting (default 20 MPS limit for WABA accounts or similar)
	allowed, err := w.limiter.Allow(ctx, w.cfg.Meta.PhoneNumberID, 20.0)
	if err != nil || !allowed {
		// Rate limited, NAK with short delay to try again
		_ = msg.NakWithDelay(500 * time.Millisecond)
		return
	}

	log.Printf("[Worker] Dispatching message ID %s to customer %s", env.MessageID, env.CustomerID)

	// 2. Load message details to check current retry count in DB
	var retryCount int
	err = w.db.Pool.QueryRow(ctx, "SELECT retry_count FROM messages WHERE id = $1", env.MessageID).Scan(&retryCount)
	if err != nil {
		// If not found in DB, assume retry count is 0
		retryCount = 0
	}

	// 3. Make HTTP request to Meta Cloud API (or Mock server)
	metaURL := fmt.Sprintf("%s/v20.0/%s/messages", w.cfg.Meta.APIURL, w.cfg.Meta.PhoneNumberID)
	
	// Construct the exact Meta body payload
	var rawContent map[string]any
	_ = json.Unmarshal([]byte(env.Content), &rawContent)

	metaPayload := map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                env.CustomerID,
	}
	for k, v := range rawContent {
		metaPayload[k] = v
	}

	bodyBytes, _ := json.Marshal(metaPayload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, metaURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		w.handleDeliveryError(ctx, msg, env.MessageID, env.CustomerID, env.MessageType, 500, "Internal request construction error", retryCount)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.cfg.Meta.AccessToken)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		w.handleDeliveryError(ctx, msg, env.MessageID, env.CustomerID, env.MessageType, 503, err.Error(), retryCount)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Parse Meta API error payload
		var metaErr struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(respBody, &metaErr)

		errCode := metaErr.Error.Code
		if errCode == 0 {
			errCode = resp.StatusCode
		}

		w.handleDeliveryError(ctx, msg, env.MessageID, env.CustomerID, env.MessageType, errCode, metaErr.Error.Message, retryCount)
		return
	}

	// Parsing success response containing Meta Message ID
	var successResp struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(respBody, &successResp)

	metaMsgID := env.MessageID
	if len(successResp.Messages) > 0 {
		metaMsgID = successResp.Messages[0].ID
	}

	// 4. Update status in Database
	_, err = w.db.Pool.Exec(ctx, 
		`UPDATE messages 
		 SET id = $2, status = 'sent', updated_at = NOW() 
		 WHERE id = $1`, env.MessageID, metaMsgID)
	if err != nil {
		log.Printf("[Worker] Database update failed for message %s: %v", env.MessageID, err)
	}

	// 5. Update Conversation state last outbound timestamp & record sending frequency
	_ = w.stateEng.UpdateLastOutbound(ctx, env.CustomerID, "whatsapp")
	_ = w.complianceEng.RecordMessageSent(ctx, env.CustomerID, "whatsapp", env.MessageType)

	// Acknowledge NATS queue message
	_ = msg.Ack()
	log.Printf("[Worker] Message %s successfully sent to Meta. MetaMsgID: %s", env.MessageID, metaMsgID)
}

func (w *Worker) handleDeliveryError(ctx context.Context, msg jetstream.Msg, msgID, customerID, msgType string, errCode int, errMsg string, retryCount int) {
	log.Printf("[Worker] Delivery failed for message %s. Meta Code: %d, Message: %s", msgID, errCode, errMsg)

	// Fetch Error Intelligence translation
	intel, err := w.errorIntel.ExplainError(ctx, errCode)
	if err != nil {
		// Fallback
		intel = &errorintel.ErrorDetails{
			Code:             errCode,
			Category:         "unexpected",
			CanRetry:         false,
			HumanExplanation: errMsg,
			SuggestedAction:  "Investigate error logs.",
		}
	}

	if intel.CanRetry {
		nextRetryCount := retryCount + 1
		if nextRetryCount < 3 {
			// Apply backoff retry delay (1s -> 5s -> 30s)
			backoff := 1 * time.Second
			if nextRetryCount == 2 {
				backoff = 5 * time.Second
			}

			// Update retry state in DB
			nextRetryAt := time.Now().Add(backoff)
			_, _ = w.db.Pool.Exec(ctx,
				`UPDATE messages 
				 SET status = 'retry', retry_count = $2, next_retry_at = $3, error_code = $4, error_message = $5, updated_at = NOW() 
				 WHERE id = $1`, msgID, nextRetryCount, nextRetryAt, errCode, intel.HumanExplanation)

			_ = msg.NakWithDelay(backoff)
			log.Printf("[Worker] Message %s scheduled for retry in %s (Attempt %d)", msgID, backoff, nextRetryCount)
			return
		}
	}

	// Permanent failure or max retries exceeded
	_, _ = w.db.Pool.Exec(ctx,
		`UPDATE messages 
		 SET status = 'failed', error_code = $2, error_message = $3, updated_at = NOW() 
		 WHERE id = $1`, msgID, errCode, fmt.Sprintf("%s (%s)", intel.HumanExplanation, intel.SuggestedAction))

	// Record failure audit log
	auditDetails, _ := json.Marshal(map[string]any{
		"message_id":       msgID,
		"customer_id":      customerID,
		"message_type":     msgType,
		"error_code":       errCode,
		"explanation":      intel.HumanExplanation,
		"suggested_action": intel.SuggestedAction,
	})
	_, _ = w.db.Pool.Exec(ctx, "INSERT INTO audit_logs (action, details) VALUES ('message_delivery_failed', $1)", auditDetails)

	// ACK message to drop from NATS stream
	_ = msg.Ack()
	log.Printf("[Worker] Message %s marked as failed permanently", msgID)
}
