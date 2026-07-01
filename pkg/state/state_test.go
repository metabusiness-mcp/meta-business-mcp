package state

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

func TestGetActiveConversationCacheFlow(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	engine := NewEngine(database)
	customerID := uniquePhone()
	redisKey := fmt.Sprintf("conv:%s:whatsapp", customerID)

	database.Redis.Del(ctx, redisKey)

	t.Run("Cache Miss queries DB and Populates Cache with correct TTL", func(t *testing.T) {
		conv, err := engine.OpenWindow(ctx, customerID, "whatsapp", "service")
		if err != nil {
			t.Fatalf("OpenWindow failed: %v", err)
		}

		database.Redis.Del(ctx, redisKey)

		fetched, err := engine.GetActiveConversation(ctx, customerID, "whatsapp")
		if err != nil {
			t.Fatalf("GetActiveConversation failed: %v", err)
		}
		if fetched.ID != conv.ID {
			t.Errorf("Expected fetched ID %s, got %s", conv.ID, fetched.ID)
		}

		cachedVal, err := database.Redis.Get(ctx, redisKey).Result()
		if err != nil {
			t.Fatalf("Expected key %s to exist in Redis: %v", redisKey, err)
		}
		if cachedVal == "" {
			t.Errorf("Expected cached value to not be empty")
		}

		ttl, err := database.Redis.TTL(ctx, redisKey).Result()
		if err != nil {
			t.Fatalf("failed to get TTL: %v", err)
		}
		if ttl <= 0 || ttl > 24*time.Hour {
			t.Errorf("Expected TTL to be between 0 and 24 hours, got %v", ttl)
		}
	})

	t.Run("Cache Hit reads from Redis directly", func(t *testing.T) {
		fakeCachedVal := `{"id":"fake-id","customer_id":"fake-customer","channel":"whatsapp","conversation_type":"marketing"}`
		err := database.Redis.Set(ctx, redisKey, fakeCachedVal, 10*time.Second).Err()
		if err != nil {
			t.Fatalf("failed to set fake cache: %v", err)
		}

		fetched, err := engine.GetActiveConversation(ctx, customerID, "whatsapp")
		if err != nil {
			t.Fatalf("GetActiveConversation failed: %v", err)
		}
		if fetched.ID != "fake-id" {
			t.Errorf("Expected cache hit ID 'fake-id', got '%s'", fetched.ID)
		}
		if fetched.CustomerID != "fake-customer" {
			t.Errorf("Expected customer 'fake-customer', got '%s'", fetched.CustomerID)
		}
	})
}

func TestCacheInvalidationOnOutbound(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	engine := NewEngine(database)
	customerID := uniquePhone()
	redisKey := fmt.Sprintf("conv:%s:whatsapp", customerID)

	_, err := engine.OpenWindow(ctx, customerID, "whatsapp", "service")
	if err != nil {
		t.Fatalf("OpenWindow failed: %v", err)
	}

	_, err = engine.GetActiveConversation(ctx, customerID, "whatsapp")
	if err != nil {
		t.Fatalf("GetActiveConversation failed: %v", err)
	}

	exists, _ := database.Redis.Exists(ctx, redisKey).Result()
	if exists != 1 {
		t.Fatalf("Expected key to exist in Redis cache")
	}

	err = engine.UpdateLastOutbound(ctx, customerID, "whatsapp")
	if err != nil {
		t.Fatalf("UpdateLastOutbound failed: %v", err)
	}

	exists, _ = database.Redis.Exists(ctx, redisKey).Result()
	if exists != 0 {
		t.Errorf("Expected Redis cache key to be deleted (invalidated) after outbound update")
	}
}

func TestListConversations(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	engine := NewEngine(database)
	cleanID := uniquePhone()[1:]

	_, err := engine.OpenWindow(ctx, cleanID, "whatsapp", "service")
	if err != nil {
		t.Fatalf("OpenWindow failed: %v", err)
	}

	t.Run("Filter open returns active conversations", func(t *testing.T) {
		convs, total, err := engine.ListConversations(ctx, "whatsapp", "open", 50, 0)
		if err != nil {
			t.Fatalf("ListConversations failed: %v", err)
		}
		if total < 1 {
			t.Errorf("Expected at least 1 open conversation, got %d", total)
		}
		for _, c := range convs {
			if c.WindowExpiresAt == nil || c.WindowExpiresAt.Before(time.Now()) {
				t.Errorf("Expected all open conversations to have active window, got ID=%s", c.ID)
			}
		}
	})

	t.Run("Filter closed returns expired conversations", func(t *testing.T) {
		convs, _, err := engine.ListConversations(ctx, "whatsapp", "closed", 50, 0)
		if err != nil {
			t.Fatalf("ListConversations failed: %v", err)
		}
		for _, c := range convs {
			if c.WindowExpiresAt != nil && c.WindowExpiresAt.After(time.Now()) {
				t.Errorf("Expected closed conversation to have expired window, got ID=%s", c.ID)
			}
		}
	})

	t.Run("Filter expiring_soon returns windows closing within 2 hours", func(t *testing.T) {
		convs, _, err := engine.ListConversations(ctx, "whatsapp", "expiring_soon", 50, 0)
		if err != nil {
			t.Fatalf("ListConversations failed: %v", err)
		}
		for _, c := range convs {
			if c.WindowExpiresAt == nil {
				t.Errorf("Expected expiring_soon conversation to have a window expiry, got ID=%s", c.ID)
				continue
			}
			if c.WindowExpiresAt.Before(time.Now()) {
				t.Errorf("Expected expiring_soon window to be in the future, got ID=%s", c.ID)
			}
			if c.WindowExpiresAt.After(time.Now().Add(2*time.Hour + 1*time.Minute)) {
				t.Errorf("Expected expiring_soon window to close within 2 hours, got ID=%s expires=%v", c.ID, c.WindowExpiresAt)
			}
		}
	})

	t.Run("Archived conversations excluded by default", func(t *testing.T) {
		archiveID := uniquePhone()[1:]
		var convID string
		err := database.Pool.QueryRow(ctx,
			`INSERT INTO conversations (customer_id, channel, window_expires_at, conversation_type, status)
			 VALUES ($1, 'whatsapp', NOW() - INTERVAL '1 hour', 'service', 'archived')
			 RETURNING id`, archiveID).Scan(&convID)
		if err != nil {
			t.Fatalf("Failed to create archived conversation: %v", err)
		}

		convs, _, err := engine.ListConversations(ctx, "whatsapp", "", 50, 0)
		if err != nil {
			t.Fatalf("ListConversations failed: %v", err)
		}
		for _, c := range convs {
			if c.ID == convID {
				t.Errorf("Archived conversation should be excluded from default listing")
			}
		}

		archivedConvs, archivedTotal, err := engine.ListConversations(ctx, "whatsapp", "archived", 50, 0)
		if err != nil {
			t.Fatalf("ListConversations archived failed: %v", err)
		}
		found := false
		for _, c := range archivedConvs {
			if c.ID == convID {
				found = true
			}
		}
		if !found && archivedTotal > 0 {
			t.Errorf("Archived conversation should be included when filter is 'archived'")
		}
	})

	t.Run("Pagination works", func(t *testing.T) {
		_, total1, err := engine.ListConversations(ctx, "whatsapp", "", 1, 0)
		if err != nil {
			t.Fatalf("ListConversations page1 failed: %v", err)
		}
		_, total2, err := engine.ListConversations(ctx, "whatsapp", "", 1, 1)
		if err != nil {
			t.Fatalf("ListConversations page2 failed: %v", err)
		}
		if total1 != total2 {
			t.Errorf("Expected total to be consistent across pages: %d vs %d", total1, total2)
		}
	})
}