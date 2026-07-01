package tests

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/errorintel"
	"meta-business-mcp/pkg/policy"
	"meta-business-mcp/pkg/state"
	"meta-business-mcp/pkg/userintel"
	"meta-business-mcp/pkg/webhook"
)

func TestE2EIntegrationFlow(t *testing.T) {
	// Set test environment variables or load defaults
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost" // Fallback to localhost if running directly outside docker
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	metaAPIURL := os.Getenv("META_API_URL")
	if metaAPIURL == "" {
		metaAPIURL = "http://localhost:8081" // Local mock meta server
	}

	cfg := &config.Config{
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
		Meta: config.MetaConfig{
			APIURL:             metaAPIURL,
			WABAID:             "mock-waba-id",
			PhoneNumberID:      "mock-phone-id",
			AccessToken:        "mock-access-token",
			WebhookVerifyToken: "mock-verify-token",
		},
		PoliciesPath: "policies.yaml",
	}

	ctx := context.Background()

	// 1. Connect to Database & Redis
	database, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Ensure migrations are run
	if err := db.Migrate(ctx, database); err != nil {
		t.Fatalf("Migrations failed: %v", err)
	}
	if err := db.Seed(ctx, database, cfg.PoliciesPath); err != nil {
		t.Fatalf("Seeding failed: %v", err)
	}

	// Clean up database tables for fresh test run
	_, _ = database.Pool.Exec(ctx, "TRUNCATE conversations, customers, message_frequencies, messages, audit_logs CASCADE")
	_ = database.Redis.FlushAll(ctx).Err()

	// 2. Initialize Engines
	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	complianceEngine := compliance.NewEngine(database, stateEngine, userManager)
	policyEngine := policy.NewEngine(database)
	errorIntel := errorintel.NewEngine(database)

	// --- STEP 1: Verification of Inbound Webhook Trigger ---
	t.Run("Inbound Webhook Opens 24h Care Window", func(t *testing.T) {
		customerID := "12345678900"
		receiver := webhook.NewReceiver(database, cfg, stateEngine)

		payload := `{
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
							"wa_id": "` + customerID + `"
						}],
						"messages": [{
							"from": "` + customerID + `",
							"id": "wamid.inbound12345",
							"timestamp": "1678901234",
							"text": {
								"body": "Hello, I need help with my account"
							},
							"type": "text"
						}]
					},
					"field": "messages"
				}]
			}]
		}`

		req := httptest.NewRequest("POST", "/webhook", bytes.NewBufferString(payload))
		w := httptest.NewRecorder()
		receiver.HandleEvent(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", resp.StatusCode)
		}

		// Wait slightly for webhook processing
		time.Sleep(100 * time.Millisecond)

		// Assert conversation state shows open window in DB/Cache
		conv, err := stateEngine.GetActiveConversation(ctx, customerID, "whatsapp")
		if err != nil {
			t.Fatalf("Failed to fetch active conversation: %v", err)
		}

		if conv.WindowExpiresAt == nil || conv.WindowExpiresAt.Before(time.Now()) {
			t.Errorf("Expected conversation care window to be active, but it was closed or missing")
		}

		if conv.ConversationType != "service" {
			t.Errorf("Expected conversation type to be 'service', got '%s'", conv.ConversationType)
		}
	})

	// --- STEP 2: Verification of Compliance Engine ---
	t.Run("Compliance Rule Assertions", func(t *testing.T) {
		// Customer 12345678900 has an active window
		res, err := complianceEngine.CheckCompliance(ctx, "12345678900", "whatsapp", "service")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if !res.Allowed {
			t.Errorf("Expected free-text service message compliance to be allowed, but it was denied: %s", res.HumanExplanation)
		}

		// Customer 12345678901 does NOT have an active window
		res, err = complianceEngine.CheckCompliance(ctx, "12345678901", "whatsapp", "service")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if res.Allowed {
			t.Errorf("Expected compliance to deny service message for out-of-window customer")
		}
		if res.ReasonCode != "TEMPLATE_REQUIRED" {
			t.Errorf("Expected TEMPLATE_REQUIRED reason, got '%s'", res.ReasonCode)
		}

		// Customer 12345678901 can receive templates
		res, err = complianceEngine.CheckCompliance(ctx, "12345678901", "whatsapp", "template")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if !res.Allowed {
			t.Errorf("Expected template compliance to be allowed for out-of-window customer")
		}
	})

	// --- STEP 3: Verification of Policy Engine (Business Policies) ---
	t.Run("Business Policy Assertions", func(t *testing.T) {
		customerID := "12345678900" // Has active window

		// Verify VIP tag exclusion
		tags := []string{"vip"}
		res, err := policyEngine.EvaluatePolicies(ctx, customerID, "whatsapp", "marketing", tags)
		if err != nil {
			t.Fatalf("EvaluatePolicies failed: %v", err)
		}
		if res.Allowed {
			t.Errorf("Expected policy to deny marketing message for customer with tag 'vip'")
		}
		if res.ReasonCode != "POLICY_RESTRICTION" {
			t.Errorf("Expected POLICY_RESTRICTION reason, got '%s'", res.ReasonCode)
		}
	})

	// --- STEP 4: Verification of Error Intelligence ---
	t.Run("Error Intelligence Explanation", func(t *testing.T) {
		intel, err := errorIntel.ExplainError(ctx, 131047)
		if err != nil {
			t.Fatalf("ExplainError failed: %v", err)
		}
		if intel.Code != 131047 {
			t.Errorf("Expected code 131047, got %d", intel.Code)
		}
		if intel.Category != "user_related" {
			t.Errorf("Expected category 'user_related', got '%s'", intel.Category)
		}
		if intel.CanRetry {
			t.Errorf("Expected can_retry to be false for window expiration error")
		}
	})
}
