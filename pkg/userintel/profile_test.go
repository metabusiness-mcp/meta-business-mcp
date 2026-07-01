package userintel

import (
	"context"
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

func TestGetOrCreateCustomer(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	manager := NewManager(database)
	customerID := uniquePhone()

	t.Run("Create new customer when not exists", func(t *testing.T) {
		cust, err := manager.GetOrCreateCustomer(ctx, customerID, "whatsapp")
		if err != nil {
			t.Fatalf("GetOrCreateCustomer failed: %v", err)
		}
		if cust.ID != customerID {
			t.Errorf("Expected customer ID %s, got %s", customerID, cust.ID)
		}
		if !cust.OptInMarketing || !cust.OptInUtility {
			t.Errorf("Expected new customer to default to opt-in")
		}
	})

	t.Run("Retrieve existing customer", func(t *testing.T) {
		cust, err := manager.GetOrCreateCustomer(ctx, customerID, "whatsapp")
		if err != nil {
			t.Fatalf("GetOrCreateCustomer failed: %v", err)
		}
		if cust.ID != customerID {
			t.Errorf("Expected customer ID %s, got %s", customerID, cust.ID)
		}
	})
}

func TestUpdateOptIn(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	manager := NewManager(database)
	customerID := uniquePhone()

	_, _ = manager.GetOrCreateCustomer(ctx, customerID, "whatsapp")

	err := manager.UpdateOptIn(ctx, customerID, "whatsapp", false, false)
	if err != nil {
		t.Fatalf("UpdateOptIn failed: %v", err)
	}

	cust, _ := manager.GetOrCreateCustomer(ctx, customerID, "whatsapp")
	if cust.OptInMarketing || cust.OptInUtility {
		t.Errorf("Expected opt-ins to be updated to false")
	}
}

func TestUpdateTagsAndRecordInteraction(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	manager := NewManager(database)
	customerID := uniquePhone()

	_, _ = manager.GetOrCreateCustomer(ctx, customerID, "whatsapp")

	err := manager.UpdateTags(ctx, customerID, "whatsapp", []string{"vip", "loyal"})
	if err != nil {
		t.Fatalf("UpdateTags failed: %v", err)
	}

	cust, _ := manager.GetOrCreateCustomer(ctx, customerID, "whatsapp")
	if len(cust.Tags) != 2 || cust.Tags[0] != "vip" || cust.Tags[1] != "loyal" {
		t.Errorf("Expected tags ['vip', 'loyal'], got %v", cust.Tags)
	}

	err = manager.RecordInteraction(ctx, customerID, "whatsapp")
	if err != nil {
		t.Fatalf("RecordInteraction failed: %v", err)
	}

	cust, _ = manager.GetOrCreateCustomer(ctx, customerID, "whatsapp")
	if cust.LastInteractionAt == nil || time.Since(*cust.LastInteractionAt) > 10*time.Second {
		t.Errorf("Expected LastInteractionAt to be updated to now")
	}
}
