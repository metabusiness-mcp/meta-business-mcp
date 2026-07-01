package ratelimit

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"
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

func uniqueKey() string {
	return fmt.Sprintf("phone_%d_%d", time.Now().UnixNano(), rand.Intn(100000))
}

func TestRateLimitingBasic(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	limiter := NewLimiter(database.Redis)
	key := uniqueKey()

	t.Run("Allow first request when bucket is full", func(t *testing.T) {
		allowed, err := limiter.Allow(ctx, key, 5.0) // 5.0 MPS
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if !allowed {
			t.Errorf("Expected request to be allowed")
		}
	})

	t.Run("Deny requests exceeding rate limit", func(t *testing.T) {
		limitKey := uniqueKey()

		// Allow first request
		allowed, _ := limiter.Allow(ctx, limitKey, 1.0) // 1.0 MPS
		if !allowed {
			t.Fatalf("Expected first request to be allowed")
		}

		// Immediate second request - should be blocked
		allowed, err := limiter.Allow(ctx, limitKey, 1.0)
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if allowed {
			t.Errorf("Expected second request to be blocked")
		}
	})

	t.Run("Replenish tokens over time", func(t *testing.T) {
		limitKey := uniqueKey()

		// Exhaust limit
		_, _ = limiter.Allow(ctx, limitKey, 1.0)
		blocked, _ := limiter.Allow(ctx, limitKey, 1.0)
		if blocked {
			t.Fatalf("Expected second request to be blocked")
		}

		// Wait for replenishment (1 second at 1.0 MPS adds 1 token)
		time.Sleep(1100 * time.Millisecond)

		// Third request should now be allowed
		allowed, err := limiter.Allow(ctx, limitKey, 1.0)
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if !allowed {
			t.Errorf("Expected request to be allowed after replenishment delay")
		}
	})
}

func TestGetStatus(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	limiter := NewLimiter(database.Redis)
	key := uniqueKey()
	mps := 10.0

	t.Run("GetStatus returns full capacity when bucket is empty", func(t *testing.T) {
		// Ensure no existing key
		database.Redis.Del(ctx, fmt.Sprintf("rate:%s", key))

		status, err := limiter.GetStatus(ctx, key, mps)
		if err != nil {
			t.Fatalf("GetStatus failed: %v", err)
		}
		if status.Capacity != mps {
			t.Errorf("Expected capacity %v, got %v", mps, status.Capacity)
		}
		if status.TokensLeft != mps {
			t.Errorf("Expected tokens_left %v (full), got %v", mps, status.TokensLeft)
		}
		if status.TokensUsed != 0 {
			t.Errorf("Expected tokens_used 0, got %v", status.TokensUsed)
		}
	})

	t.Run("GetStatus reflects consumed tokens after Allow", func(t *testing.T) {
		statusKey := uniqueKey()

		// Consume 3 tokens
		for i := 0; i < 3; i++ {
			_, _ = limiter.Allow(ctx, statusKey, mps)
		}

		status, err := limiter.GetStatus(ctx, statusKey, mps)
		if err != nil {
			t.Fatalf("GetStatus failed: %v", err)
		}
		if status.TokensLeft > mps-2 {
			t.Errorf("Expected tokens_left <= %v after 3 consumes, got %v", mps-2, status.TokensLeft)
		}
		if status.LastUpdateMs == 0 {
			t.Errorf("Expected last_update_ms to be set")
		}
	})

	t.Run("GetStatus computes replenishment correctly", func(t *testing.T) {
		statusKey := uniqueKey()

		// Consume 1 token to initialize
		_, _ = limiter.Allow(ctx, statusKey, 1.0)

		// Wait 1.1 seconds for replenishment at 1.0 MPS
		time.Sleep(1100 * time.Millisecond)

		status, err := limiter.GetStatus(ctx, statusKey, 1.0)
		if err != nil {
			t.Fatalf("GetStatus failed: %v", err)
		}
		// After 1.1s at 1.0 MPS, token should be replenished to ~1.0
		if status.TokensLeft < 0.8 {
			t.Errorf("Expected tokens_left >= 0.8 after replenishment, got %v", status.TokensLeft)
		}
	})
}

func TestRateLimitingConcurrency(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	limiter := NewLimiter(database.Redis)
	limitKey := uniqueKey()
	mps := 10.0 // 10 MPS limit

	var wg sync.WaitGroup
	allowedCount := 0
	var mu sync.Mutex

	// Fire 30 concurrent requests
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, err := limiter.Allow(ctx, limitKey, mps)
			if err == nil && allowed {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Since MPS is 10.0, at most 10 requests should have been allowed concurrently
	if allowedCount > 10 {
		t.Errorf("Rate limiter allowed %d requests, exceeding limit of 10", allowedCount)
	}
	if allowedCount == 0 {
		t.Errorf("Rate limiter allowed 0 requests, expected up to 10")
	}
}
