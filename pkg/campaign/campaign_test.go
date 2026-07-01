package campaign

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

	cleanup := func() {
		database.Close()
	}

	return database, ctx, cleanup
}

func TestCampaignLifecycle(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	manager := NewManager(database)
	campaignName := fmt.Sprintf("Promo_%d", rand.Intn(1000000))

	var camp *Campaign
	t.Run("Create Campaign", func(t *testing.T) {
		variables := map[string]any{"promo_code": "DISCOUNT20"}
		var err error
		camp, err = manager.CreateCampaign(ctx, campaignName, "marketing", "welcome_template", "en", variables, nil)
		if err != nil {
			t.Fatalf("CreateCampaign failed: %v", err)
		}
		if camp.Name != campaignName {
			t.Errorf("Expected campaign name %s, got %s", campaignName, camp.Name)
		}
		if camp.Status != "draft" {
			t.Errorf("Expected default status 'draft', got '%s'", camp.Status)
		}
	})

	t.Run("Get Campaign", func(t *testing.T) {
		fetched, err := manager.GetCampaign(ctx, camp.ID)
		if err != nil {
			t.Fatalf("GetCampaign failed: %v", err)
		}
		if fetched.ID != camp.ID {
			t.Errorf("Expected fetched ID %s, got %s", camp.ID, fetched.ID)
		}
	})

	t.Run("Update Status", func(t *testing.T) {
		err := manager.UpdateCampaignStatus(ctx, camp.ID, "sending")
		if err != nil {
			t.Fatalf("UpdateCampaignStatus failed: %v", err)
		}

		fetched, _ := manager.GetCampaign(ctx, camp.ID)
		if fetched.Status != "sending" {
			t.Errorf("Expected status to be updated to 'sending', got '%s'", fetched.Status)
		}
	})

	t.Run("Update Progress", func(t *testing.T) {
		err := manager.UpdateCampaignProgress(ctx, camp.ID, 10, 8, 2)
		if err != nil {
			t.Fatalf("UpdateCampaignProgress failed: %v", err)
		}

		fetched, _ := manager.GetCampaign(ctx, camp.ID)
		if fetched.SentCount != 10 {
			t.Errorf("Expected SentCount 10, got %d", fetched.SentCount)
		}
		if fetched.DeliveredCount != 8 {
			t.Errorf("Expected DeliveredCount 8, got %d", fetched.DeliveredCount)
		}
		if fetched.FailedCount != 2 {
			t.Errorf("Expected FailedCount 2, got %d", fetched.FailedCount)
		}
	})
}
