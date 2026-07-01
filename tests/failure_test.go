package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/delivery"
	"meta-business-mcp/pkg/errorintel"
	"meta-business-mcp/pkg/ratelimit"
	"meta-business-mcp/pkg/state"
	"meta-business-mcp/pkg/userintel"
)

func TestFailureSimulations(t *testing.T) {
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

	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	complianceEngine := compliance.NewEngine(database, stateEngine, userManager)
	errorIntel := errorintel.NewEngine(database)

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
		t.Fatalf("Failed to start worker: %v", err)
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	t.Run("Redis Lost - Graceful Fallback to Postgres", func(t *testing.T) {
		customerID := fmt.Sprintf("+1666%07d", rng.Intn(10000000))

		// 1. Create active conversation in PostgreSQL
		conv, err := stateEngine.OpenWindow(ctx, customerID, "whatsapp", "service")
		if err != nil {
			t.Fatalf("OpenWindow failed: %v", err)
		}

		// 2. Verify cache hit (to ensure it was cached)
		cachedVal, err := database.Redis.Get(ctx, fmt.Sprintf("conv:%s:whatsapp", customerID)).Result()
		if err != nil || cachedVal == "" {
			t.Fatalf("Expected conversation to be cached in Redis")
		}

		// 3. Simulate Redis failure by closing the client
		// We instantiate a new Engine with a custom DB structure containing a CLOSED Redis client
		closedRedis := redis.NewClient(&redis.Options{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		closedRedis.Close() // Close it immediately

		fallbackDB := &db.DB{
			Pool:  database.Pool,
			Redis: closedRedis,
		}
		fallbackStateEngine := state.NewEngine(fallbackDB)

		// 4. Retrieve active conversation. Should fallback to Postgres and succeed!
		convFallback, err := fallbackStateEngine.GetActiveConversation(ctx, customerID, "whatsapp")
		if err != nil {
			t.Fatalf("GetActiveConversation failed under simulated Redis outage: %v", err)
		}

		if convFallback.ID != conv.ID {
			t.Errorf("Expected conversation ID %s, got %s", conv.ID, convFallback.ID)
		}
	})

	t.Run("NATS Lost - Outbound publishing fail-fast", func(t *testing.T) {
		// Instantiate orchestrator with closed connection
		brokenOrchestrator, err := delivery.NewOrchestrator(cfg)
		if err != nil {
			t.Fatalf("NewOrchestrator failed: %v", err)
		}
		brokenOrchestrator.Close() // Close it to simulate NATS down

		err = brokenOrchestrator.PublishOutboundMessage(ctx, "msg-123", "+12345", "service", map[string]any{"body": "test"})
		if err == nil {
			t.Errorf("Expected PublishOutboundMessage to fail when NATS connection is closed")
		}
	})

	t.Run("Mock Meta HTTP 500 - Transient Error & Retry Trigger", func(t *testing.T) {
		customerID := "+12345678904" // Triggers HTTP 500 / Error 470 in Mock Meta
		msgID := fmt.Sprintf("msg_fail_%d", rng.Int63())

		// Open care window
		_, _ = stateEngine.OpenWindow(ctx, customerID, "whatsapp", "service")

		// Insert message as queued
		contentBytes, _ := json.Marshal(map[string]any{
			"type": "text",
			"text": map[string]string{"body": "Hello HTTP 500!"},
		})
		_, _ = database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status, retry_count) 
			 VALUES ($1, $2, 'outbound', 'text', $3, 'queued', 0)`,
			msgID, customerID, contentBytes)

		// Enqueue
		err = orchestrator.PublishOutboundMessage(ctx, msgID, customerID, "service", map[string]any{
			"type": "text",
			"text": map[string]string{"body": "Hello HTTP 500!"},
		})
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}

		// Wait for worker processing to finish and update status in DB to 'retry'
		var status string
		var retryCount int
		for i := 0; i < 20; i++ {
			time.Sleep(100 * time.Millisecond)
			err = database.Pool.QueryRow(ctx, "SELECT status, retry_count FROM messages WHERE id = $1", msgID).Scan(&status, &retryCount)
			if err == nil && status == "retry" {
				break
			}
		}

		if status != "retry" {
			t.Errorf("Expected status 'retry' due to transient Meta error, got '%s'", status)
		}
		if retryCount != 1 {
			t.Errorf("Expected retry count to be incremented to 1, got %d", retryCount)
		}
	})

	t.Run("Mock Meta 131049 - Permanent Failure (No Retry)", func(t *testing.T) {
		customerID := "+12345678903" // Triggers 131049 in Mock Meta
		msgID := fmt.Sprintf("msg_fail_%d", rng.Int63())

		// Open care window
		_, _ = stateEngine.OpenWindow(ctx, customerID, "whatsapp", "service")

		// Insert message as queued
		contentBytes, _ := json.Marshal(map[string]any{
			"type": "text",
			"text": map[string]string{"body": "Hello 131049!"},
		})
		_, _ = database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status, retry_count) 
			 VALUES ($1, $2, 'outbound', 'text', $3, 'queued', 0)`,
			msgID, customerID, contentBytes)

		// Enqueue
		err = orchestrator.PublishOutboundMessage(ctx, msgID, customerID, "service", map[string]any{
			"type": "text",
			"text": map[string]string{"body": "Hello 131049!"},
		})
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}

		// Wait for worker processing to finish and fail permanently
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
			t.Fatalf("Expected status 'failed' due to 131049 Meta rejection, got '%s'", status)
		}
		if dbErrCode != 131049 {
			t.Errorf("Expected DB error code 131049, got %d", dbErrCode)
		}

		// Assert audit log exists
		var auditCount int
		err = database.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_logs WHERE action = 'message_delivery_failed'").Scan(&auditCount)
		if err != nil || auditCount == 0 {
			t.Errorf("Expected message_delivery_failed audit log entry, got count: %d", auditCount)
		}
	})
}
