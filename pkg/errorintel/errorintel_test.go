package errorintel

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
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

	// Make sure DB seeds are loaded (if not already seeded by the server)
	// Migrate/Seed can run safely multiple times
	_ = db.Migrate(ctx, database)
	_ = db.Seed(ctx, database, "")

	cleanup := func() {
		database.Close()
	}

	return database, ctx, cleanup
}

func TestExplainErrorSeededCodes(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	engine := NewEngine(database)

	seededCodes := []struct {
		Code             int
		ExpectedCategory string
		ExpectedCanRetry bool
	}{
		{131047, "user_related", false},
		{131048, "policy", true},
		{131049, "policy", false}, // verify our updated seed makes 131049 permanent
		{470, "transient", true},
		{10, "policy", false},
		{100, "user_related", false},
		{200, "policy", false},
	}

	for _, tc := range seededCodes {
		t.Run(fmt.Sprintf("Code %d mapping verification", tc.Code), func(t *testing.T) {
			details, err := engine.ExplainError(ctx, tc.Code)
			if err != nil {
				t.Fatalf("ExplainError failed for code %d: %v", tc.Code, err)
			}
			if details.Code != tc.Code {
				t.Errorf("Expected code %d, got %d", tc.Code, details.Code)
			}
			if details.Category != tc.ExpectedCategory {
				t.Errorf("Expected category %s, got %s for code %d", tc.ExpectedCategory, details.Category, tc.Code)
			}
			if details.CanRetry != tc.ExpectedCanRetry {
				t.Errorf("Expected can_retry %t, got %t for code %d", tc.ExpectedCanRetry, details.CanRetry, tc.Code)
			}
			if details.HumanExplanation == "" {
				t.Errorf("Expected explanation for code %d to not be empty", tc.Code)
			}
			if details.SuggestedAction == "" {
				t.Errorf("Expected action for code %d to not be empty", tc.Code)
			}
		})
	}
}

func TestExplainErrorFallback(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	engine := NewEngine(database)

	t.Run("Unmapped error code returns default fallback details", func(t *testing.T) {
		// Use a random code that is guaranteed not to exist in error_knowledge_base
		unmappedCode := 999990 + rand.Intn(1000)

		details, err := engine.ExplainError(ctx, unmappedCode)
		if err != nil {
			t.Fatalf("ExplainError failed: %v", err)
		}
		if details.Code != unmappedCode {
			t.Errorf("Expected code %d, got %d", unmappedCode, details.Code)
		}
		if details.Category != "unexpected" {
			t.Errorf("Expected fallback category 'unexpected', got '%s'", details.Category)
		}
		if details.CanRetry {
			t.Errorf("Expected fallback can_retry to be false")
		}
		if details.HumanExplanation == "" {
			t.Errorf("Expected fallback explanation to not be empty")
		}
	})
}

func TestExplainErrorDBError(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	engine := NewEngine(database)

	t.Run("Context cancelled triggers query error", func(t *testing.T) {
		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, err := engine.ExplainError(cancelledCtx, 470)
		if err == nil {
			t.Errorf("Expected ExplainError to fail with cancelled context")
		}
	})
}
