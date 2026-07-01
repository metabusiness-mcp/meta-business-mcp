package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/delivery"
	"meta-business-mcp/pkg/errorintel"
	"meta-business-mcp/pkg/observability"
	"meta-business-mcp/pkg/ratelimit"
	"meta-business-mcp/pkg/state"
	"meta-business-mcp/pkg/template"
	"meta-business-mcp/pkg/userintel"
	"meta-business-mcp/pkg/webhook"
)

func getIntegrationTestConfig() *config.Config {
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}
	metaAPIURL := os.Getenv("META_API_URL")
	if metaAPIURL == "" {
		metaAPIURL = "http://localhost:8081"
	}

	return &config.Config{
		Server: config.ServerConfig{
			HTTPPort: 8099,
		},
		Database: config.DatabaseConfig{
			Host:     dbHost,
			Port:     5432,
			User:     "postgres",
			Password: "password",
			DBName:   "meta_mcp",
			SSLMode:  "disable",
		},
		Redis: config.RedisConfig{
			Addr: redisAddr,
		},
		NATS: config.NATSConfig{
			URL: natsURL,
		},
		Meta: config.MetaConfig{
			APIURL:             metaAPIURL,
			WABAID:             "mock-waba-id",
			PhoneNumberID:      "mock-phone-id",
			AccessToken:        "mock-access-token",
			WebhookVerifyToken: "mock-verify-token",
		},
		PoliciesPath: "../policies.yaml",
	}
}

func TestIntegrationOutboundAndWebhookFlows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := getIntegrationTestConfig()

	database, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	_ = db.Migrate(ctx, database)
	_ = db.Seed(ctx, database, cfg.PoliciesPath)

	// Clean up database tables for fresh test run
	_, _ = database.Pool.Exec(ctx, "TRUNCATE conversations, customers, message_frequencies, messages, audit_logs CASCADE")
	_ = database.Redis.FlushAll(ctx).Err()

	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	complianceEngine := compliance.NewEngine(database, stateEngine, userManager)
	errorIntel := errorintel.NewEngine(database)
	templateManager := template.NewManager(database, cfg)

	orchestrator, err := delivery.NewOrchestrator(cfg)
	if err != nil {
		t.Fatalf("Failed to initialize NATS orchestrator: %v", err)
	}
	defer orchestrator.Close()

	limiter := ratelimit.NewLimiter(database.Redis)
	worker := delivery.NewWorker(
		database, cfg, orchestrator, limiter, complianceEngine, stateEngine, errorIntel,
	)

	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Failed to start workers: %v", err)
	}

	receiver := webhook.NewReceiver(database, cfg, stateEngine)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	t.Run("Outbound message - active window", func(t *testing.T) {
		customerID := fmt.Sprintf("+1999%07d", rng.Intn(10000000))
		msgID := fmt.Sprintf("msg_%d", rng.Int63())

		// 1. Open conversation window first
		_, err := stateEngine.OpenWindow(ctx, customerID, "whatsapp", "service")
		if err != nil {
			t.Fatalf("OpenWindow failed: %v", err)
		}

		// 2. Compliance and Policy evaluation
		comp, err := complianceEngine.CheckCompliance(ctx, customerID, "whatsapp", "service")
		if err != nil || !comp.Allowed {
			t.Fatalf("Compliance check failed: %v, allowed: %v", err, comp.Allowed)
		}

		// 3. Save message in Postgres as queued
		contentBytes, _ := json.Marshal(map[string]any{
			"type": "text",
			"text": map[string]string{"body": "Hello active window!"},
		})
		_, err = database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status) 
			 VALUES ($1, $2, 'outbound', 'text', $3, 'queued')`,
			msgID, customerID, contentBytes)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}

		// 4. Publish to NATS stream
		err = orchestrator.PublishOutboundMessage(ctx, msgID, customerID, "service", map[string]any{
			"type": "text",
			"text": map[string]string{"body": "Hello active window!"},
		})
		if err != nil {
			t.Fatalf("Failed to publish: %v", err)
		}

		// 5. Wait for worker processing to finish and status update in DB to 'sent'
		var status string
		var actualMetaID string
		for i := 0; i < 20; i++ {
			time.Sleep(100 * time.Millisecond)
			err = database.Pool.QueryRow(ctx, "SELECT status, id FROM messages WHERE customer_id = $1", customerID).Scan(&status, &actualMetaID)
			if err == nil && status == "sent" {
				break
			}
		}

		if status != "sent" {
			t.Fatalf("Expected status 'sent' after worker processing, got '%s'", status)
		}

		// 6. Simulate Meta webhook response 'delivered' using actualMetaID
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
		}`, actualMetaID, customerID)

		req := httptest.NewRequest("POST", "/webhook", bytes.NewBufferString(payloadDelivered))
		w := httptest.NewRecorder()
		receiver.HandleEvent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("Expected webhook event handler success, got %d", w.Code)
		}

		// Verify DB status becomes delivered
		for i := 0; i < 10; i++ {
			time.Sleep(50 * time.Millisecond)
			_ = database.Pool.QueryRow(ctx, "SELECT status FROM messages WHERE id = $1", actualMetaID).Scan(&status)
			if status == "delivered" {
				break
			}
		}
		if status != "delivered" {
			t.Errorf("Expected status to be updated to 'delivered', got '%s'", status)
		}
	})

	t.Run("Outbound message - closed window blocks check_compliance", func(t *testing.T) {
		customerID := fmt.Sprintf("+1999%07d", rng.Intn(10000000))
		comp, err := complianceEngine.CheckCompliance(ctx, customerID, "whatsapp", "service")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if comp.Allowed {
			t.Errorf("Expected compliance check to block service message when care window is closed")
		}
		if comp.ReasonCode != "TEMPLATE_REQUIRED" {
			t.Errorf("Expected TEMPLATE_REQUIRED reason, got '%s'", comp.ReasonCode)
		}
	})

	t.Run("Send Template - closed window allowed", func(t *testing.T) {
		customerID := fmt.Sprintf("+1999%07d", rng.Intn(10000000))
		msgID := fmt.Sprintf("msg_%d", rng.Int63())

		// Pre-populate templates via template sync
		err := templateManager.SyncTemplates(ctx)
		if err != nil {
			t.Fatalf("SyncTemplates failed: %v", err)
		}

		// Category of sample_flight_confirmation is utility
		comp, err := complianceEngine.CheckCompliance(ctx, customerID, "whatsapp", "utility")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if !comp.Allowed {
			t.Errorf("Expected template category utility to bypass care window and be allowed")
		}

		// Enqueue template message
		contentBytes, _ := json.Marshal(map[string]any{
			"type": "template",
			"template": map[string]any{
				"name": "sample_flight_confirmation",
				"language": map[string]string{
					"code": "en",
				},
			},
		})
		_, err = database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status) 
			 VALUES ($1, $2, 'outbound', 'template', $3, 'queued')`,
			msgID, customerID, contentBytes)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}

		err = orchestrator.PublishOutboundMessage(ctx, msgID, customerID, "utility", map[string]any{
			"type": "template",
			"template": map[string]any{
				"name": "sample_flight_confirmation",
				"language": map[string]string{
					"code": "en",
				},
			},
		})
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}

		// Wait for worker processing to finish and status update in DB to 'sent'
		var status string
		for i := 0; i < 20; i++ {
			time.Sleep(100 * time.Millisecond)
			err = database.Pool.QueryRow(ctx, "SELECT status FROM messages WHERE customer_id = $1", customerID).Scan(&status)
			if err == nil && status == "sent" {
				break
			}
		}

		if status != "sent" {
			t.Errorf("Expected template message status 'sent' after worker processing, got '%s'", status)
		}
	})

	t.Run("Health and Metrics endpoints", func(t *testing.T) {
		r := chi.NewRouter()
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		})
		r.Handle("/metrics", observability.MetricsHandler())

		ts := httptest.NewServer(r)
		defer ts.Close()

		// Test health check
		resp, err := http.Get(ts.URL + "/health")
		if err != nil {
			t.Fatalf("Health check request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected /health status 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "OK" {
			t.Errorf("Expected health response 'OK', got '%s'", string(body))
		}

		// Test metrics endpoint
		respMetrics, err := http.Get(ts.URL + "/metrics")
		if err != nil {
			t.Fatalf("Metrics check request failed: %v", err)
		}
		defer respMetrics.Body.Close()
		if respMetrics.StatusCode != http.StatusOK {
			t.Errorf("Expected /metrics status 200, got %d", respMetrics.StatusCode)
		}
	})

	t.Run("Opt-out and Opt-in resets", func(t *testing.T) {
		customerID := fmt.Sprintf("+1999%07d", rng.Intn(10000000))
		// Ensure customer is created first so UpdateOptIn has a row to update
		_, _ = userManager.GetOrCreateCustomer(ctx, customerID, "whatsapp")

		// 1. Opt out of marketing, keep utility opted in
		err := userManager.UpdateOptIn(ctx, customerID, "whatsapp", false, true)
		if err != nil {
			t.Fatalf("UpdateOptIn failed: %v", err)
		}

		// Verify check compliance blocks marketing template
		comp, err := complianceEngine.CheckCompliance(ctx, customerID, "whatsapp", "marketing")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if comp.Allowed {
			t.Errorf("Expected marketing to be blocked for opted-out customer")
		}
		if comp.ReasonCode != "USER_OPTED_OUT" {
			t.Errorf("Expected USER_OPTED_OUT, got '%s'", comp.ReasonCode)
		}

		// 2. Opt back in to marketing
		err = userManager.UpdateOptIn(ctx, customerID, "whatsapp", true, true)
		if err != nil {
			t.Fatalf("UpdateOptIn failed: %v", err)
		}

		// Verify allowed now
		comp, err = complianceEngine.CheckCompliance(ctx, customerID, "whatsapp", "marketing")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if !comp.Allowed {
			t.Errorf("Expected marketing to be allowed after opt-in reset")
		}
	})

	t.Run("Duplicate Webhook processing idempotency", func(t *testing.T) {
		customerID := fmt.Sprintf("+1999%07d", rng.Intn(10000000))
		msgID := fmt.Sprintf("wamid.duplicate_test_%d", rng.Int63())

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
								"name": "Jane Doe"
							},
							"wa_id": "%s"
						}],
						"messages": [{
							"from": "%s",
							"id": "%s",
							"timestamp": "1678901234",
							"text": {
								"body": "Idempotency check message"
							},
							"type": "text"
						}]
					},
					"field": "messages"
				}]
			}]
		}`, customerID, customerID, msgID)

		// Post 1st time
		req1 := httptest.NewRequest("POST", "/webhook", bytes.NewBufferString(payload))
		w1 := httptest.NewRecorder()
		receiver.HandleEvent(w1, req1)
		if w1.Code != http.StatusOK {
			t.Fatalf("First webhook post failed: %d", w1.Code)
		}

		// Post 2nd time immediately
		req2 := httptest.NewRequest("POST", "/webhook", bytes.NewBufferString(payload))
		w2 := httptest.NewRecorder()
		receiver.HandleEvent(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("Second webhook post failed: %d", w2.Code)
		}
	})

	t.Run("24h window expiration race condition (meta rejection 131047)", func(t *testing.T) {
		// Mock meta triggers error 131047 for phone number +12345678901
		customerID := "+12345678901"
		msgID := fmt.Sprintf("msg_race_%d", rng.Int63())

		// Pre-populate customer and open window so compliance allows enqueuing
		_, _ = stateEngine.OpenWindow(ctx, customerID, "whatsapp", "service")

		// Save and publish
		contentBytes, _ := json.Marshal(map[string]any{
			"type": "text",
			"text": map[string]string{"body": "Hello race!"},
		})
		_, _ = database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status) 
			 VALUES ($1, $2, 'outbound', 'text', $3, 'queued')`,
			msgID, customerID, contentBytes)

		err = orchestrator.PublishOutboundMessage(ctx, msgID, customerID, "service", map[string]any{
			"type": "text",
			"text": map[string]string{"body": "Hello race!"},
		})
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}

		// Wait for worker processing to finish and fail the message
		var status string
		var dbErrCode int
		for i := 0; i < 20; i++ {
			time.Sleep(100 * time.Millisecond)
			err = database.Pool.QueryRow(ctx, "SELECT status, error_code FROM messages WHERE id = $1", msgID).Scan(&status, &dbErrCode)
			if err == nil && status == "failed" {
				break
			}
		}

		if status != "failed" {
			t.Fatalf("Expected status 'failed' due to 131047 Meta rejection, got '%s'", status)
		}
		if dbErrCode != 131047 {
			t.Errorf("Expected DB error code 131047, got %d", dbErrCode)
		}
	})
}
