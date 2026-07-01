package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

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

	cleanup := func() {
		database.Close()
	}

	return database, ctx, cleanup
}

func uniquePhone() string {
	return fmt.Sprintf("+1%010d", rand.Int63n(10000000000))
}

func TestEvaluatePoliciesTimeRestriction(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	engine := NewEngine(database)

	t.Run("Time restriction denys sending during restricted hours", func(t *testing.T) {
		customerID := uniquePhone()

		// Indonesian Jakarta timezone is UTC+7
		loc, err := time.LoadLocation("Asia/Jakarta")
		if err != nil {
			t.Fatalf("failed to load timezone: %v", err)
		}

		// Calculate a time that is "after" the restricted time in Jakarta
		nowLocal := time.Now().In(loc)
		// We set deny_after to 1 hour ago so that nowLocal is definitely after it
		denyTime := nowLocal.Add(-1 * time.Hour)
		denyAfterStr := denyTime.Format("15:04")

		policyID := fmt.Sprintf("policy_time_%d", rand.Int())
		rules := map[string]any{
			"deny_after": denyAfterStr,
			"timezone":   "Asia/Jakarta",
		}
		rulesJSON, _ := json.Marshal(rules)

		// Insert policy
		_, err = database.Pool.Exec(ctx,
			`INSERT INTO policies (id, name, type, channel, message_type, is_enabled, rules) 
			 VALUES ($1, 'Late Night Restrict', 'time_restriction', 'whatsapp', 'marketing', TRUE, $2)`,
			policyID, rulesJSON)
		if err != nil {
			t.Fatalf("failed to insert policy: %v", err)
		}
		defer func() {
			_, _ = database.Pool.Exec(ctx, "DELETE FROM policies WHERE id = $1", policyID)
		}()

		// Evaluate policy
		res, err := engine.EvaluatePolicies(ctx, customerID, "whatsapp", "marketing", []string{})
		if err != nil {
			t.Fatalf("EvaluatePolicies failed: %v", err)
		}

		if res.Allowed {
			t.Errorf("Expected marketing to be blocked after %s Jakarta time, but it was allowed. Local Jakarta time: %s", denyAfterStr, nowLocal.Format("15:04"))
		}
		if res.ReasonCode != "POLICY_RESTRICTION" {
			t.Errorf("Expected ReasonCode POLICY_RESTRICTION, got %s", res.ReasonCode)
		}
	})

	t.Run("Time restriction allows sending before restricted hours", func(t *testing.T) {
		customerID := uniquePhone()

		loc, err := time.LoadLocation("Asia/Jakarta")
		if err != nil {
			t.Fatalf("failed to load timezone: %v", err)
		}

		nowLocal := time.Now().In(loc)
		// We set deny_after to 2 hours from now so that nowLocal is definitely before it
		denyTime := nowLocal.Add(2 * time.Hour)
		denyAfterStr := denyTime.Format("15:04")

		policyID := fmt.Sprintf("policy_time_%d", rand.Int())
		rules := map[string]any{
			"deny_after": denyAfterStr,
			"timezone":   "Asia/Jakarta",
		}
		rulesJSON, _ := json.Marshal(rules)

		// Insert policy
		_, err = database.Pool.Exec(ctx,
			`INSERT INTO policies (id, name, type, channel, message_type, is_enabled, rules) 
			 VALUES ($1, 'Future Restrict', 'time_restriction', 'whatsapp', 'marketing', TRUE, $2)`,
			policyID, rulesJSON)
		if err != nil {
			t.Fatalf("failed to insert policy: %v", err)
		}
		defer func() {
			_, _ = database.Pool.Exec(ctx, "DELETE FROM policies WHERE id = $1", policyID)
		}()

		// Evaluate policy
		res, err := engine.EvaluatePolicies(ctx, customerID, "whatsapp", "marketing", []string{})
		if err != nil {
			t.Fatalf("EvaluatePolicies failed: %v", err)
		}

		if !res.Allowed {
			t.Errorf("Expected marketing to be allowed before %s Jakarta time, but got blocked: %s", denyAfterStr, res.HumanExplanation)
		}
	})
}

func TestEvaluatePoliciesSegmentExclusion(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	engine := NewEngine(database)

	policyID := fmt.Sprintf("policy_exclude_%d", rand.Int())
	rules := map[string]any{
		"exclude_tag": "vip",
	}
	rulesJSON, _ := json.Marshal(rules)

	// Insert policy
	_, err := database.Pool.Exec(ctx,
		`INSERT INTO policies (id, name, type, channel, message_type, is_enabled, rules) 
		 VALUES ($1, 'Skip VIPs', 'segment_exclusion', 'whatsapp', 'marketing', TRUE, $2)`,
		policyID, rulesJSON)
	if err != nil {
		t.Fatalf("failed to insert policy: %v", err)
	}
	defer func() {
		_, _ = database.Pool.Exec(ctx, "DELETE FROM policies WHERE id = $1", policyID)
	}()

	t.Run("Excludes VIP users from marketing", func(t *testing.T) {
		customerID := uniquePhone()

		// Tag contains "vip"
		res, err := engine.EvaluatePolicies(ctx, customerID, "whatsapp", "marketing", []string{"vip", "active"})
		if err != nil {
			t.Fatalf("EvaluatePolicies failed: %v", err)
		}
		if res.Allowed {
			t.Errorf("Expected marketing to be blocked for VIP customer")
		}
		if res.ReasonCode != "POLICY_RESTRICTION" {
			t.Errorf("Expected reason POLICY_RESTRICTION, got %s", res.ReasonCode)
		}
	})

	t.Run("Allows non-VIP users for marketing", func(t *testing.T) {
		customerID := uniquePhone()

		// Tag does not contain "vip"
		res, err := engine.EvaluatePolicies(ctx, customerID, "whatsapp", "marketing", []string{"regular", "active"})
		if err != nil {
			t.Fatalf("EvaluatePolicies failed: %v", err)
		}
		if !res.Allowed {
			t.Errorf("Expected marketing to be allowed for non-VIP customer, but got blocked: %s", res.HumanExplanation)
		}
	})
}
