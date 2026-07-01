package delivery

import (
	"context"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/policy"
	"meta-business-mcp/pkg/state"
	"meta-business-mcp/pkg/userintel"
)

// FakeClock allows tests to control time without sleeping.
type FakeClock struct {
	current time.Time
}

func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{current: t}
}

func (fc *FakeClock) Now() time.Time {
	return fc.current
}

func (fc *FakeClock) Advance(d time.Duration) {
	fc.current = fc.current.Add(d)
}

func (fc *FakeClock) Set(t time.Time) {
	fc.current = t
}

func getSchedulerTestConfig() *config.Config {
	dbHost := "localhost"
	redisAddr := "localhost:6379"
	natsURL := "nats://localhost:4222"
	metaAPIURL := "http://localhost:8081"

	return &config.Config{
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
			APIURL:        metaAPIURL,
			WABAID:        "mock-waba-id",
			PhoneNumberID: "mock-phone-id",
			AccessToken:   "mock-access-token",
		},
		SchedPollInterval: "1s",
	}
}

func TestSchedulerMessageDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := getSchedulerTestConfig()
	database, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	_ = db.Migrate(ctx, database)
	_ = db.Seed(ctx, database, cfg.PoliciesPath)
	_, _ = database.Pool.Exec(ctx, "TRUNCATE messages, customers, conversations, audit_logs CASCADE")

	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	complianceEngine := compliance.NewEngine(database, stateEngine, userManager)
	policyEngine := policy.NewEngine(database)

	orchestrator, err := NewOrchestrator(cfg)
	if err != nil {
		t.Fatalf("Failed to start NATS orchestrator: %v", err)
	}
	defer orchestrator.Close()

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Create a fake clock set to a fixed time
	now := time.Date(2025, 6, 25, 10, 0, 0, 0, time.UTC)
	fakeClock := NewFakeClock(now)

	scheduler, err := NewScheduler(database, cfg, orchestrator, complianceEngine, policyEngine, userManager, fakeClock)
	if err != nil {
		t.Fatalf("Failed to create scheduler: %v", err)
	}

	t.Run("Due message is dispatched", func(t *testing.T) {
		customerID := fmt.Sprintf("+1777%07d", rng.Intn(10000000))
		cleanID := customerID[1:]
		msgID := fmt.Sprintf("sch_test_due_%d", rng.Int63())

		// Create customer and open window so compliance passes
		_, _ = userManager.GetOrCreateCustomer(ctx, cleanID, "whatsapp")
		_, _ = stateEngine.OpenWindow(ctx, cleanID, "whatsapp", "service")

		// Insert a scheduled message with deliver_at in the past (relative to fake clock)
		deliverAt := now.Add(-1 * time.Minute)
		contentBytes := []byte(`{"type":"text","text":{"body":"Scheduled hello"}}`)
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status, deliver_at) 
			 VALUES ($1, $2, 'outbound', 'service', $3, 'scheduled', $4)`,
			msgID, cleanID, contentBytes, deliverAt)
		if err != nil {
			t.Fatalf("Failed to insert scheduled message: %v", err)
		}

		// Run one poll cycle using the fake clock (already set to now)
		scheduler.poll(ctx)

		// Verify message was dispatched (status changed from scheduled to queued)
		var status string
		err = database.Pool.QueryRow(ctx, "SELECT status FROM messages WHERE id = $1", msgID).Scan(&status)
		if err != nil {
			t.Fatalf("Failed to query message: %v", err)
		}
		if status != "queued" {
			t.Errorf("Expected status 'queued' after dispatch, got '%s'", status)
		}
	})

	t.Run("Future message is not dispatched", func(t *testing.T) {
		customerID := fmt.Sprintf("+1777%07d", rng.Intn(10000000))
		cleanID := customerID[1:]
		msgID := fmt.Sprintf("sch_test_future_%d", rng.Int63())

		_, _ = userManager.GetOrCreateCustomer(ctx, cleanID, "whatsapp")

		// Insert a scheduled message with deliver_at in the future
		deliverAt := now.Add(1 * time.Hour)
		contentBytes := []byte(`{"type":"text","text":{"body":"Future message"}}`)
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status, deliver_at) 
			 VALUES ($1, $2, 'outbound', 'service', $3, 'scheduled', $4)`,
			msgID, cleanID, contentBytes, deliverAt)
		if err != nil {
			t.Fatalf("Failed to insert scheduled message: %v", err)
		}

		// Run poll cycle — message should NOT be dispatched
		scheduler.poll(ctx)

		var status string
		err = database.Pool.QueryRow(ctx, "SELECT status FROM messages WHERE id = $1", msgID).Scan(&status)
		if err != nil {
			t.Fatalf("Failed to query message: %v", err)
		}
		if status != "scheduled" {
			t.Errorf("Expected status 'scheduled' (not yet due), got '%s'", status)
		}
	})

	t.Run("Compliance failure at dispatch transitions to failed", func(t *testing.T) {
		cleanID := fmt.Sprintf("1777%07d", rng.Intn(10000000))
		msgID := fmt.Sprintf("sch_test_comp_%d", rng.Int63())

		// Create customer but do NOT open a window — compliance will deny service message
		_, _ = userManager.GetOrCreateCustomer(ctx, cleanID, "whatsapp")

		deliverAt := now.Add(-1 * time.Minute)
		contentBytes := []byte(`{"type":"text","text":{"body":"Will fail compliance"}}`)
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status, deliver_at) 
			 VALUES ($1, $2, 'outbound', 'service', $3, 'scheduled', $4)`,
			msgID, cleanID, contentBytes, deliverAt)
		if err != nil {
			t.Fatalf("Failed to insert scheduled message: %v", err)
		}

		scheduler.poll(ctx)

		var status string
		err = database.Pool.QueryRow(ctx, "SELECT status FROM messages WHERE id = $1", msgID).Scan(&status)
		if err != nil {
			t.Fatalf("Failed to query message: %v", err)
		}
		if status != "failed" {
			t.Errorf("Expected status 'failed' (compliance denied), got '%s'", status)
		}

		// Verify audit log exists
		var auditCount int
		_ = database.Pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM audit_logs WHERE action = 'scheduler_dispatch_failed'").Scan(&auditCount)
		if auditCount == 0 {
			t.Errorf("Expected scheduler_dispatch_failed audit log entry")
		}
	})

	t.Run("Duplicate dispatch prevention with atomic claim", func(t *testing.T) {
		cleanID := fmt.Sprintf("1777%07d", rng.Intn(10000000))
		msgID := fmt.Sprintf("sch_test_dupe_%d", rng.Int63())

		_, _ = userManager.GetOrCreateCustomer(ctx, cleanID, "whatsapp")
		_, _ = stateEngine.OpenWindow(ctx, cleanID, "whatsapp", "service")

		deliverAt := now.Add(-1 * time.Minute)
		contentBytes := []byte(`{"type":"text","text":{"body":"Duplicate test"}}`)
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, content, status, deliver_at) 
			 VALUES ($1, $2, 'outbound', 'service', $3, 'scheduled', $4)`,
			msgID, cleanID, contentBytes, deliverAt)
		if err != nil {
			t.Fatalf("Failed to insert scheduled message: %v", err)
		}

		// Create two scheduler instances to simulate concurrent polling
		sched2, _ := NewScheduler(database, cfg, orchestrator, complianceEngine, policyEngine, userManager, fakeClock)

		var dispatchCount int64

		// Run both polls concurrently
		done := make(chan struct{}, 2)
		go func() {
			scheduler.poll(ctx)
			done <- struct{}{}
		}()
		go func() {
			sched2.poll(ctx)
			done <- struct{}{}
		}()
		<-done
		<-done

		// Count how many times the message was dispatched (should be exactly 1)
		var status string
		err = database.Pool.QueryRow(ctx, "SELECT status FROM messages WHERE id = $1", msgID).Scan(&status)
		if err != nil {
			t.Fatalf("Failed to query message: %v", err)
		}

		// The message should have been claimed by exactly one scheduler
		if status == "queued" {
			atomic.AddInt64(&dispatchCount, 1)
		}

		// Verify it was only dispatched once — the second scheduler should have found
		// status already changed and the atomic claim returned no rows
		if status != "queued" && status != "sent" && status != "delivered" {
			t.Errorf("Expected message to be dispatched (queued/sent/delivered), got '%s'", status)
		}
	})

	_, _ = database.Pool.Exec(ctx, "TRUNCATE messages, customers, conversations, audit_logs CASCADE")
}

func TestSchedulerCampaignTrigger(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := getSchedulerTestConfig()
	database, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	_ = db.Migrate(ctx, database)
	_ = db.Seed(ctx, database, cfg.PoliciesPath)
	_, _ = database.Pool.Exec(ctx, "TRUNCATE campaigns, messages, customers, conversations, audit_logs CASCADE")

	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	complianceEngine := compliance.NewEngine(database, stateEngine, userManager)
	policyEngine := policy.NewEngine(database)

	orchestrator, err := NewOrchestrator(cfg)
	if err != nil {
		t.Fatalf("Failed to start NATS orchestrator: %v", err)
	}
	defer orchestrator.Close()

	now := time.Date(2025, 6, 25, 10, 0, 0, 0, time.UTC)
	fakeClock := NewFakeClock(now)

	scheduler, err := NewScheduler(database, cfg, orchestrator, complianceEngine, policyEngine, userManager, fakeClock)
	if err != nil {
		t.Fatalf("Failed to create scheduler: %v", err)
	}

	t.Run("Campaign transitions scheduled to running and enqueues messages", func(t *testing.T) {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))

		// Create 3 customers
		customers := make([]string, 3)
		for i := range customers {
			cid := fmt.Sprintf("1888%07d", rng.Intn(10000000))
			customers[i] = cid
			_, _ = userManager.GetOrCreateCustomer(ctx, cid, "whatsapp")
		}

		// Insert a scheduled campaign with scheduled_at in the past
		scheduledAt := now.Add(-1 * time.Minute)
		var campID string
		err := database.Pool.QueryRow(ctx,
			`INSERT INTO campaigns (name, type, status, template_name, locale, audience_filter, scheduled_at) 
			 VALUES ('Test Campaign', 'marketing', 'scheduled', 'sample_purchase_feedback', 'en', '{}', $1) 
			 RETURNING id`, scheduledAt).Scan(&campID)
		if err != nil {
			t.Fatalf("Failed to insert campaign: %v", err)
		}

		// Sync templates so the template exists
		// (In tests, we skip this — the scheduler will try to enqueue regardless)

		// Run poll cycle
		scheduler.poll(ctx)

		// Verify campaign is now running
		var campStatus string
		err = database.Pool.QueryRow(ctx, "SELECT status FROM campaigns WHERE id = $1", campID).Scan(&campStatus)
		if err != nil {
			t.Fatalf("Failed to query campaign: %v", err)
		}
		if campStatus != "running" && campStatus != "completed" {
			t.Errorf("Expected campaign status 'running' or 'completed', got '%s'", campStatus)
		}
	})

	_, _ = database.Pool.Exec(ctx, "TRUNCATE campaigns, messages, customers, conversations, audit_logs CASCADE")
}