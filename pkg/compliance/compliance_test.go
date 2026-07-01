package compliance

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/state"
	"meta-business-mcp/pkg/userintel"
)

func getTestDB(t testing.TB) (*db.DB, context.Context, func()) {
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
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
	}

	ctx := context.Background()
	database, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}

	cleanup := func() {
		database.Close()
	}

	return database, ctx, cleanup
}

func uniquePhone() string {
	return fmt.Sprintf("+1%010d", rand.Int63n(10000000000))
}

func TestCheckComplianceOptOut(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	engine := NewEngine(database, stateEngine, userManager)

	t.Run("Marketing Opt-Out Blocks Marketing Messages", func(t *testing.T) {
		customerID := uniquePhone()

		// Create customer opted out of marketing
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO customers (id, channel, opt_in_marketing, opt_in_utility) 
			 VALUES ($1, 'whatsapp', FALSE, TRUE)`, customerID)
		if err != nil {
			t.Fatalf("failed to insert customer: %v", err)
		}

		res, err := engine.CheckCompliance(ctx, customerID, "whatsapp", "marketing")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if res.Allowed {
			t.Errorf("Expected marketing to be blocked for opted-out customer")
		}
		if res.ReasonCode != "USER_OPTED_OUT" {
			t.Errorf("Expected USER_OPTED_OUT reason, got %s", res.ReasonCode)
		}
	})

	t.Run("Utility Opt-Out Blocks Utility Messages", func(t *testing.T) {
		customerID := uniquePhone()

		// Create customer opted out of utility
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO customers (id, channel, opt_in_marketing, opt_in_utility) 
			 VALUES ($1, 'whatsapp', TRUE, FALSE)`, customerID)
		if err != nil {
			t.Fatalf("failed to insert customer: %v", err)
		}

		res, err := engine.CheckCompliance(ctx, customerID, "whatsapp", "utility")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if res.Allowed {
			t.Errorf("Expected utility to be blocked for opted-out customer")
		}
		if res.ReasonCode != "USER_OPTED_OUT" {
			t.Errorf("Expected USER_OPTED_OUT reason, got %s", res.ReasonCode)
		}
	})
}

func TestCheckComplianceWindow(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	engine := NewEngine(database, stateEngine, userManager)

	t.Run("Free-form text message blocks when care window is closed", func(t *testing.T) {
		customerID := uniquePhone()

		// Ensure customer is created
		_, _ = userManager.GetOrCreateCustomer(ctx, customerID, "whatsapp")

		// No active window exists, check compliance for free-form
		res, err := engine.CheckCompliance(ctx, customerID, "whatsapp", "service")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if res.Allowed {
			t.Errorf("Expected free-form message to be blocked without active window")
		}
		if res.ReasonCode != "TEMPLATE_REQUIRED" {
			t.Errorf("Expected TEMPLATE_REQUIRED reason, got %s", res.ReasonCode)
		}
	})

	t.Run("Free-form text message allowed when care window is active", func(t *testing.T) {
		customerID := uniquePhone()

		// Open window
		_, err := stateEngine.OpenWindow(ctx, customerID, "whatsapp", "service")
		if err != nil {
			t.Fatalf("failed to open window: %v", err)
		}

		res, err := engine.CheckCompliance(ctx, customerID, "whatsapp", "service")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if !res.Allowed {
			t.Errorf("Expected free-form message to be allowed with active window, but got blocked: %s", res.HumanExplanation)
		}
	})
}

func TestCheckComplianceFrequencyCap(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	engine := NewEngine(database, stateEngine, userManager)

	t.Run("Frequency cap blocks marketing after max messages", func(t *testing.T) {
		customerID := uniquePhone()

		// Open window first so care window check passes
		_, err := stateEngine.OpenWindow(ctx, customerID, "whatsapp", "service")
		if err != nil {
			t.Fatalf("failed to open window: %v", err)
		}

		// 1. Record 1st message sent
		err = engine.RecordMessageSent(ctx, customerID, "whatsapp", "marketing")
		if err != nil {
			t.Fatalf("failed to record message: %v", err)
		}

		// Check compliance - should be allowed (1 sent, cap is 2)
		res, err := engine.CheckCompliance(ctx, customerID, "whatsapp", "marketing")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if !res.Allowed {
			t.Errorf("Expected marketing to be allowed after 1 message")
		}

		// 2. Record 2nd message sent
		err = engine.RecordMessageSent(ctx, customerID, "whatsapp", "marketing")
		if err != nil {
			t.Fatalf("failed to record message: %v", err)
		}

		// Check compliance - should be blocked (2 sent, cap is 2)
		res, err = engine.CheckCompliance(ctx, customerID, "whatsapp", "marketing")
		if err != nil {
			t.Fatalf("CheckCompliance failed: %v", err)
		}
		if res.Allowed {
			t.Errorf("Expected marketing to be blocked after 2 messages")
		}
		if res.ReasonCode != "FREQUENCY_CAP_EXCEEDED" {
			t.Errorf("Expected FREQUENCY_CAP_EXCEEDED reason, got %s", res.ReasonCode)
		}
	})
}

func TestAuxiliaryMethods(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	engine := NewEngine(database, stateEngine, userManager)

	customerID := uniquePhone()

	conv, err := engine.OpenConversationWindow(ctx, customerID, "whatsapp", "service")
	if err != nil {
		t.Fatalf("OpenConversationWindow failed: %v", err)
	}
	if conv.CustomerID != customerID {
		t.Errorf("Expected customer ID %s, got %s", customerID, conv.CustomerID)
	}

	activeConv, err := engine.GetActiveConversation(ctx, customerID, "whatsapp")
	if err != nil {
		t.Fatalf("GetActiveConversation failed: %v", err)
	}
	if activeConv.ID != conv.ID {
		t.Errorf("Expected active conversation ID %s, got %s", conv.ID, activeConv.ID)
	}
}

func BenchmarkCheckCompliance(b *testing.B) {
	database, ctx, cleanup := getTestDB(b)
	defer cleanup()

	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	engine := NewEngine(database, stateEngine, userManager)

	customerID := uniquePhone()
	_, _ = stateEngine.OpenWindow(ctx, customerID, "whatsapp", "service")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.CheckCompliance(ctx, customerID, "whatsapp", "service")
		if err != nil {
			b.Fatalf("CheckCompliance failed: %v", err)
		}
	}
}

func TestCheckComplianceErrorPaths(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	engine := NewEngine(database, stateEngine, userManager)

	customerID := uniquePhone()

	t.Run("Context cancelled triggers GetOrCreateCustomer error", func(t *testing.T) {
		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, err := engine.CheckCompliance(cancelledCtx, customerID, "whatsapp", "marketing")
		if err == nil {
			t.Errorf("Expected CheckCompliance to fail with cancelled context")
		}
	})

	t.Run("Context cancelled triggers GetActiveConversation error", func(t *testing.T) {
		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, err := engine.CheckCompliance(cancelledCtx, customerID, "whatsapp", "service")
		if err == nil {
			t.Errorf("Expected CheckCompliance to fail with cancelled context")
		}
	})

	t.Run("Context cancelled triggers message_frequencies query error", func(t *testing.T) {
		_, _ = stateEngine.OpenWindow(ctx, customerID, "whatsapp", "service")

		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, err := engine.CheckCompliance(cancelledCtx, customerID, "whatsapp", "marketing")
		if err == nil {
			t.Errorf("Expected CheckCompliance to fail with cancelled context")
		}
	})

	t.Run("RecordMessageSent fails with cancelled context", func(t *testing.T) {
		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		err := engine.RecordMessageSent(cancelledCtx, customerID, "whatsapp", "marketing")
		if err == nil {
			t.Errorf("Expected RecordMessageSent to fail with cancelled context")
		}
	})
}
