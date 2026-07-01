package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/errorintel"
	"meta-business-mcp/pkg/ratelimit"
	"meta-business-mcp/pkg/state"
	"meta-business-mcp/pkg/userintel"
)

// Mock NATS Msg for testing worker actions
type mockNatsMsg struct {
	jetstream.Msg
	ackCalled         bool
	nakWithDelayValue time.Duration
}

func (m *mockNatsMsg) Data() []byte {
	return []byte(`{}`)
}

func (m *mockNatsMsg) Ack() error {
	m.ackCalled = true
	return nil
}

func (m *mockNatsMsg) NakWithDelay(delay time.Duration) error {
	m.nakWithDelayValue = delay
	return nil
}

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

	// Run migrations and seeds
	_ = db.Migrate(ctx, database)
	_ = db.Seed(ctx, database, "")

	cleanup := func() {
		database.Close()
	}

	return database, ctx, cleanup
}

func uniqueMsgID() string {
	return fmt.Sprintf("msg_%d_%d", time.Now().UnixNano(), rand.Intn(100000))
}

func uniquePhone() string {
	return fmt.Sprintf("+1%010d", rand.Int63n(10000000000))
}

func TestWorkerBackoffRetries(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	cfg := &config.Config{}
	limiter := ratelimit.NewLimiter(database.Redis)
	stateEng := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	compEng := compliance.NewEngine(database, stateEng, userManager)
	errorIntel := errorintel.NewEngine(database)

	worker := &Worker{
		db:            database,
		cfg:           cfg,
		limiter:       limiter,
		complianceEng: compEng,
		stateEng:      stateEng,
		errorIntel:    errorIntel,
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}

	t.Run("Attempt 1 failure plans retry with 1s backoff", func(t *testing.T) {
		msgID := uniqueMsgID()
		customerID := uniquePhone()

		// Save message in DB as queued
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, status, retry_count) 
			 VALUES ($1, $2, 'outbound', 'text', 'queued', 0)`, msgID, customerID)
		if err != nil {
			t.Fatalf("failed to insert test message: %v", err)
		}

		mockMsg := &mockNatsMsg{}
		worker.handleDeliveryError(ctx, mockMsg, msgID, customerID, "text", 470, "Transient error message", 0)

		// Assert NATS Msg was NAK'd with 1s delay
		if mockMsg.ackCalled {
			t.Errorf("Expected msg NOT to be ACKed")
		}
		if mockMsg.nakWithDelayValue != 1*time.Second {
			t.Errorf("Expected NakWithDelay with 1s, got %v", mockMsg.nakWithDelayValue)
		}

		// Assert DB status is 'retry' and retry count is 1
		var status string
		var retryCount int
		err = database.Pool.QueryRow(ctx, "SELECT status, retry_count FROM messages WHERE id = $1", msgID).Scan(&status, &retryCount)
		if err != nil {
			t.Fatalf("failed to select message status: %v", err)
		}
		if status != "retry" {
			t.Errorf("Expected status 'retry', got '%s'", status)
		}
		if retryCount != 1 {
			t.Errorf("Expected retry_count 1, got %d", retryCount)
		}
	})

	t.Run("Attempt 2 failure plans retry with 5s backoff", func(t *testing.T) {
		msgID := uniqueMsgID()
		customerID := uniquePhone()

		_, err := database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, status, retry_count) 
			 VALUES ($1, $2, 'outbound', 'text', 'queued', 1)`, msgID, customerID)
		if err != nil {
			t.Fatalf("failed to insert test message: %v", err)
		}

		mockMsg := &mockNatsMsg{}
		worker.handleDeliveryError(ctx, mockMsg, msgID, customerID, "text", 470, "Transient error message", 1)

		if mockMsg.nakWithDelayValue != 5*time.Second {
			t.Errorf("Expected NakWithDelay with 5s, got %v", mockMsg.nakWithDelayValue)
		}

		var retryCount int
		err = database.Pool.QueryRow(ctx, "SELECT retry_count FROM messages WHERE id = $1", msgID).Scan(&retryCount)
		if err != nil {
			t.Fatalf("failed to query retry count: %v", err)
		}
		if retryCount != 2 {
			t.Errorf("Expected retry_count 2, got %d", retryCount)
		}
	})
}

func TestWorkerRetryExhaustion(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	cfg := &config.Config{}
	limiter := ratelimit.NewLimiter(database.Redis)
	stateEng := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	compEng := compliance.NewEngine(database, stateEng, userManager)
	errorIntel := errorintel.NewEngine(database)

	worker := &Worker{
		db:            database,
		cfg:           cfg,
		limiter:       limiter,
		complianceEng: compEng,
		stateEng:      stateEng,
		errorIntel:    errorIntel,
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}

	t.Run("Attempt 3 failure triggers retry exhaustion and permanent failure", func(t *testing.T) {
		msgID := uniqueMsgID()
		customerID := uniquePhone()

		_, err := database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, status, retry_count) 
			 VALUES ($1, $2, 'outbound', 'text', 'queued', 2)`, msgID, customerID)
		if err != nil {
			t.Fatalf("failed to insert test message: %v", err)
		}

		mockMsg := &mockNatsMsg{}
		worker.handleDeliveryError(ctx, mockMsg, msgID, customerID, "text", 470, "Transient error message", 2)

		// Assert NATS Msg was ACKed (removed from NATS stream)
		if !mockMsg.ackCalled {
			t.Errorf("Expected msg to be ACKed upon retry exhaustion")
		}

		// Assert DB status is 'failed'
		var status string
		var errMsg string
		err = database.Pool.QueryRow(ctx, "SELECT status, error_message FROM messages WHERE id = $1", msgID).Scan(&status, &errMsg)
		if err != nil {
			t.Fatalf("failed to select message status: %v", err)
		}
		if status != "failed" {
			t.Errorf("Expected status 'failed', got '%s'", status)
		}

		// Assert audit log exists
		var auditCount int
		err = database.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_logs WHERE action = 'message_delivery_failed'").Scan(&auditCount)
		if err != nil {
			t.Fatalf("failed to count audit logs: %v", err)
		}
		if auditCount == 0 {
			t.Errorf("Expected audit log to be created upon delivery failure")
		}
	})
}

func TestWorkerImmediatePermanentFailure(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	cfg := &config.Config{}
	limiter := ratelimit.NewLimiter(database.Redis)
	stateEng := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	compEng := compliance.NewEngine(database, stateEng, userManager)
	errorIntel := errorintel.NewEngine(database)

	worker := &Worker{
		db:            database,
		cfg:           cfg,
		limiter:       limiter,
		complianceEng: compEng,
		stateEng:      stateEng,
		errorIntel:    errorIntel,
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}

	t.Run("Error 131049 results in immediate permanent failure and no retry", func(t *testing.T) {
		msgID := uniqueMsgID()
		customerID := uniquePhone()

		_, err := database.Pool.Exec(ctx,
			`INSERT INTO messages (id, customer_id, direction, message_type, status, retry_count) 
			 VALUES ($1, $2, 'outbound', 'marketing', 'queued', 0)`, msgID, customerID)
		if err != nil {
			t.Fatalf("failed to insert test message: %v", err)
		}

		mockMsg := &mockNatsMsg{}
		// Error code 131049 passed
		worker.handleDeliveryError(ctx, mockMsg, msgID, customerID, "marketing", 131049, "Frequency cap reached", 0)

		// Assert NATS Msg was ACKed (to drop it)
		if !mockMsg.ackCalled {
			t.Errorf("Expected msg to be ACKed to stop retries")
		}

		// Assert DB status is 'failed'
		var status string
		err = database.Pool.QueryRow(ctx, "SELECT status FROM messages WHERE id = $1", msgID).Scan(&status)
		if err != nil {
			t.Fatalf("failed to select message status: %v", err)
		}
		if status != "failed" {
			t.Errorf("Expected status 'failed', got '%s'", status)
		}
	})
}

type mockPayloadMsg struct {
	jetstream.Msg
	data              []byte
	ackCalled         bool
	nakWithDelayValue time.Duration
}

func (m *mockPayloadMsg) Data() []byte {
	return m.data
}

func (m *mockPayloadMsg) Ack() error {
	m.ackCalled = true
	return nil
}

func (m *mockPayloadMsg) NakWithDelay(delay time.Duration) error {
	m.nakWithDelayValue = delay
	return nil
}

func TestWorkerProcessMessageSuccess(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	metaAPIURL := os.Getenv("META_API_URL")
	if metaAPIURL == "" {
		metaAPIURL = "http://localhost:8081"
	}

	cfg := &config.Config{
		Meta: config.MetaConfig{
			APIURL:        metaAPIURL,
			PhoneNumberID: "mock-phone-id",
			AccessToken:   "mock-access-token",
		},
	}
	limiter := ratelimit.NewLimiter(database.Redis)
	stateEng := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	compEng := compliance.NewEngine(database, stateEng, userManager)
	errorIntel := errorintel.NewEngine(database)

	worker := &Worker{
		db:            database,
		cfg:           cfg,
		limiter:       limiter,
		complianceEng: compEng,
		stateEng:      stateEng,
		errorIntel:    errorIntel,
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}

	msgID := uniqueMsgID()
	customerID := uniquePhone()

	// Open customer care window
	_, err := stateEng.OpenWindow(ctx, customerID, "whatsapp", "service")
	if err != nil {
		t.Fatalf("failed to open window: %v", err)
	}

	// Save message in DB as queued
	content := `{"text":{"body":"Hello Success Test"}}`
	_, err = database.Pool.Exec(ctx,
		`INSERT INTO messages (id, customer_id, direction, message_type, content, status, retry_count) 
		 VALUES ($1, $2, 'outbound', 'service', $3, 'queued', 0)`, msgID, customerID, []byte(content))
	if err != nil {
		t.Fatalf("failed to insert test message: %v", err)
	}

	// Marshall NATS MessageEnvelope payload
	env := MessageEnvelope{
		MessageID:   msgID,
		CustomerID:  customerID,
		MessageType: "service",
		Content:     content,
	}
	envBytes, _ := json.Marshal(env)

	mockMsg := &mockPayloadMsg{
		data: envBytes,
	}

	// Execute processMessage
	worker.processMessage(ctx, mockMsg)

	// Assert NATS Msg was ACKed
	if !mockMsg.ackCalled {
		t.Errorf("Expected message to be ACKed upon successful Meta dispatch")
	}

	// Assert DB status is 'sent'
	var status string
	err = database.Pool.QueryRow(ctx, "SELECT status FROM messages WHERE customer_id = $1", customerID).Scan(&status)
	if err != nil {
		t.Fatalf("failed to select message status: %v", err)
	}
	if status != "sent" {
		t.Errorf("Expected status 'sent', got '%s'", status)
	}
}

func TestWorkerStartLoopSuccess(t *testing.T) {
	database, ctx, cleanup := getTestDB(t)
	defer cleanup()

	metaAPIURL := os.Getenv("META_API_URL")
	if metaAPIURL == "" {
		metaAPIURL = "http://localhost:8081"
	}
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	cfg := &config.Config{
		Meta: config.MetaConfig{
			APIURL:        metaAPIURL,
			PhoneNumberID: "mock-phone-id",
			AccessToken:   "mock-access-token",
		},
		NATS: config.NATSConfig{
			URL: natsURL,
		},
	}

	orch, err := NewOrchestrator(cfg)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}
	defer orch.Close()

	limiter := ratelimit.NewLimiter(database.Redis)
	stateEng := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	compEng := compliance.NewEngine(database, stateEng, userManager)
	errorIntel := errorintel.NewEngine(database)

	worker := NewWorker(database, cfg, orch, limiter, compEng, stateEng, errorIntel)

	workerCtx, workerCancel := context.WithCancel(ctx)
	defer func() {
		workerCancel()
		time.Sleep(1 * time.Second) // Wait for worker loop to exit
	}()

	// Start worker
	err = worker.Start(workerCtx)
	if err != nil {
		t.Fatalf("failed to start worker: %v", err)
	}

	msgID := uniqueMsgID()
	customerID := uniquePhone()

	// Open customer care window
	_, _ = stateEng.OpenWindow(ctx, customerID, "whatsapp", "service")

	// Insert message as queued
	content := `{"text":{"body":"Hello Worker Start Loop"}}`
	_, err = database.Pool.Exec(ctx,
		`INSERT INTO messages (id, customer_id, direction, message_type, content, status, retry_count) 
		 VALUES ($1, $2, 'outbound', 'service', $3, 'queued', 0)`, msgID, customerID, []byte(content))
	if err != nil {
		t.Fatalf("failed to insert message: %v", err)
	}

	// Publish message to NATS via orchestrator
	payload := map[string]any{"text": map[string]string{"body": "Hello Worker Start Loop"}}
	err = orch.PublishOutboundMessage(ctx, msgID, customerID, "service", payload)
	if err != nil {
		t.Fatalf("failed to publish outbound: %v", err)
	}

	// Wait for worker loop to pick up and process
	time.Sleep(1 * time.Second)

	// Verify the status in database changed to 'sent'
	var status string
	err = database.Pool.QueryRow(ctx, "SELECT status FROM messages WHERE customer_id = $1", customerID).Scan(&status)
	if err != nil {
		t.Fatalf("failed to query status: %v", err)
	}
	if status != "sent" {
		t.Errorf("Expected message to be processed and marked 'sent', got '%s'", status)
	}
}
