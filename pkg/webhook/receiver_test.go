package webhook

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/state"
)

func TestReceiverVerify(t *testing.T) {
	cfg := &config.Config{
		Meta: config.MetaConfig{
			WebhookVerifyToken: "my-secret-verify-token",
		},
	}

	receiver := NewReceiver(nil, cfg, nil)

	// Case 1: Valid Verification
	req := httptest.NewRequest("GET", "/webhook?hub.mode=subscribe&hub.verify_token=my-secret-verify-token&hub.challenge=test_challenge_abc", nil)
	w := httptest.NewRecorder()
	receiver.Verify(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Read body
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if buf.String() != "test_challenge_abc" {
		t.Errorf("Expected challenge 'test_challenge_abc', got '%s'", buf.String())
	}

	// Case 2: Invalid Verification
	req = httptest.NewRequest("GET", "/webhook?hub.mode=subscribe&hub.verify_token=wrong-token&hub.challenge=test_challenge", nil)
	w = httptest.NewRecorder()
	receiver.Verify(w, req)

	resp = w.Result()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected status 403, got %d", resp.StatusCode)
	}
}

func TestReceiverHandleEvent(t *testing.T) {
	ctx := context.Background()

	// Connect to postgres/redis
	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "password",
			DBName:   "meta_mcp",
			SSLMode:  "disable",
		},
		Redis: config.RedisConfig{
			Addr: "localhost:6379",
		},
		PoliciesPath: "policies.yaml",
	}

	database, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Run migration
	_ = db.Migrate(ctx, database)

	stateEngine := state.NewEngine(database)
	receiver := NewReceiver(database, cfg, stateEngine)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	t.Run("Invalid body", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/webhook", bytes.NewBufferString("{invalid-json"))
		w := httptest.NewRecorder()
		receiver.HandleEvent(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", w.Code)
		}
	})

	t.Run("Inbound Message opens care window and inserts message", func(t *testing.T) {
		customerID := fmt.Sprintf("+1777%07d", rng.Intn(10000000))
		msgID := fmt.Sprintf("wamid.test_inbound_%d", rng.Int63())

		payload := fmt.Sprintf(`{
			"object": "whatsapp_business_account",
			"entry": [{
				"id": "mock-waba-id",
				"changes": [{
					"value": {
						"messaging_product": "whatsapp",
						"metadata": {
							"display_phone_number": "15555555555",
							"phone_number_id": "mock-phone-id"
						},
						"contacts": [{
							"profile": {
								"name": "Test User"
							},
							"wa_id": "%s"
						}],
						"messages": [{
							"from": "%s",
							"id": "%s",
							"timestamp": "1678901234",
							"text": {
								"body": "Hello receiver"
							},
							"type": "text"
						}]
					},
					"field": "messages"
				}]
			}]
		}`, customerID, customerID, msgID)

		req := httptest.NewRequest("POST", "/webhook", bytes.NewBufferString(payload))
		w := httptest.NewRecorder()
		receiver.HandleEvent(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", w.Code)
		}

		time.Sleep(100 * time.Millisecond)

		// Assert conversation state shows open window in DB/Cache
		cleanCustomerID := customerID
		if len(cleanCustomerID) > 0 && cleanCustomerID[0] == '+' {
			cleanCustomerID = cleanCustomerID[1:]
		}
		conv, err := stateEngine.GetActiveConversation(ctx, cleanCustomerID, "whatsapp")
		if err != nil {
			t.Fatalf("Failed to get active conversation: %v", err)
		}
		if conv.WindowExpiresAt == nil || conv.WindowExpiresAt.Before(time.Now()) {
			t.Errorf("Expected active care window")
		}

		// Assert message is recorded in Postgres
		var dbStatus string
		var dbDir string
		err = database.Pool.QueryRow(ctx, "SELECT status, direction FROM messages WHERE id = $1", msgID).Scan(&dbStatus, &dbDir)
		if err != nil {
			t.Fatalf("Message not found in DB: %v", err)
		}
		if dbStatus != "read" {
			t.Errorf("Expected status 'read', got '%s'", dbStatus)
		}
		if dbDir != "inbound" {
			t.Errorf("Expected direction 'inbound', got '%s'", dbDir)
		}
	})

	t.Run("Outbound message status update", func(t *testing.T) {
		customerID := fmt.Sprintf("+1777%07d", rng.Intn(10000000))
		msgID := fmt.Sprintf("wamid.test_outbound_%d", rng.Int63())

		// First pre-insert message in DB with 'queued' status
		_, err = database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status) 
			 VALUES ($1, $2, 'outbound', 'text', '{}'::JSONB, 'queued')`,
			msgID, customerID)
		if err != nil {
			t.Fatalf("Failed to insert mock message: %v", err)
		}

		// Update to delivered
		payloadDelivered := fmt.Sprintf(`{
			"object": "whatsapp_business_account",
			"entry": [{
				"id": "mock-waba-id",
				"changes": [{
					"value": {
						"messaging_product": "whatsapp",
						"statuses": [{
							"id": "%s",
							"status": "delivered",
							"timestamp": "1678901235",
							"recipient_id": "%s"
						}]
					},
					"field": "messages"
				}]
			}]
		}`, msgID, customerID)

		req := httptest.NewRequest("POST", "/webhook", bytes.NewBufferString(payloadDelivered))
		w := httptest.NewRecorder()
		receiver.HandleEvent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", w.Code)
		}

		time.Sleep(50 * time.Millisecond)

		var dbStatus string
		_ = database.Pool.QueryRow(ctx, "SELECT status FROM messages WHERE id = $1", msgID).Scan(&dbStatus)
		if dbStatus != "delivered" {
			t.Errorf("Expected status 'delivered', got '%s'", dbStatus)
		}

		// Update to failed with error code
		payloadFailed := fmt.Sprintf(`{
			"object": "whatsapp_business_account",
			"entry": [{
				"id": "mock-waba-id",
				"changes": [{
					"value": {
						"messaging_product": "whatsapp",
						"statuses": [{
							"id": "%s",
							"status": "failed",
							"timestamp": "1678901236",
							"recipient_id": "%s",
							"error_code": 131049,
							"error_message": "Frequency cap reached"
						}]
					},
					"field": "messages"
				}]
			}]
		}`, msgID, customerID)

		req = httptest.NewRequest("POST", "/webhook", bytes.NewBufferString(payloadFailed))
		w = httptest.NewRecorder()
		receiver.HandleEvent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", w.Code)
		}

		time.Sleep(50 * time.Millisecond)

		var dbErrCode *int
		var dbErrMsg *string
		err = database.Pool.QueryRow(ctx, "SELECT status, error_code, error_message FROM messages WHERE id = $1", msgID).Scan(&dbStatus, &dbErrCode, &dbErrMsg)
		if err != nil {
			t.Fatalf("Failed to query updated status: %v", err)
		}
		if dbStatus != "failed" {
			t.Errorf("Expected status 'failed', got '%s'", dbStatus)
		}
		if dbErrCode == nil || *dbErrCode != 131049 {
			t.Errorf("Expected error code 131049, got %v", dbErrCode)
		}
		if dbErrMsg == nil || *dbErrMsg != "Frequency cap reached" {
			t.Errorf("Expected error message 'Frequency cap reached', got %v", dbErrMsg)
		}
	})

	t.Run("Message template status update", func(t *testing.T) {
		templateName := fmt.Sprintf("sample_temp_%d", rng.Intn(100000))
		locale := "id"

		// Pre-insert template in DB
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO templates (name, locale, category, status, body_text, variables) 
			 VALUES ($1, $2, 'marketing', 'PENDING', 'Halo', '{}'::JSONB)`,
			templateName, locale)
		if err != nil {
			t.Fatalf("Failed to insert template: %v", err)
		}

		payload := fmt.Sprintf(`{
			"object": "whatsapp_business_account",
			"entry": [{
				"id": "mock-waba-id",
				"changes": [{
					"value": {
						"event": "TEMPLATE_STATUS_UPDATE",
						"message_template_id": "mock-tmpl-1234",
						"message_template_name": "%s",
						"message_template_language": "%s",
						"event_type": "APPROVED",
						"timestamp": 1678901235
					},
					"field": "message_template_status_update"
				}]
			}]
		}`, templateName, locale)

		req := httptest.NewRequest("POST", "/webhook", bytes.NewBufferString(payload))
		w := httptest.NewRecorder()
		receiver.HandleEvent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", w.Code)
		}

		time.Sleep(50 * time.Millisecond)

		var dbStatus string
		err = database.Pool.QueryRow(ctx, "SELECT status FROM templates WHERE name = $1 AND locale = $2", templateName, locale).Scan(&dbStatus)
		if err != nil {
			t.Fatalf("Template not found: %v", err)
		}
		if dbStatus != "APPROVED" {
			t.Errorf("Expected template status APPROVED, got '%s'", dbStatus)
		}
	})
}
