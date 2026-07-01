package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"meta-business-mcp/pkg/config"
)

func TestOrchestratorLifecycle(t *testing.T) {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	cfg := &config.Config{
		NATS: config.NATSConfig{
			URL: natsURL,
		},
	}

	// 1. Create Orchestrator
	orch, err := NewOrchestrator(cfg)
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	defer orch.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a dedicated isolated stream for this test to avoid WorkQueue consumer conflicts
	streamName := fmt.Sprintf("TEST_ORCH_STREAM_%d", rand.Intn(100000))
	subject := fmt.Sprintf("test.messages.outbound.%d", rand.Intn(100000))

	_, err = orch.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      streamName,
		Subjects:  []string{subject},
		Retention: jetstream.WorkQueuePolicy,
	})
	if err != nil {
		t.Fatalf("Failed to create isolated stream: %v", err)
	}
	defer func() {
		_ = orch.js.DeleteStream(ctx, streamName)
	}()

	// 2. Publish a message to the isolated stream
	msgID := fmt.Sprintf("orch_msg_%d", rand.Intn(1000000))
	customerID := fmt.Sprintf("+1%010d", rand.Int63n(10000000000))
	env := MessageEnvelope{
		MessageID:   msgID,
		CustomerID:  customerID,
		MessageType: "service",
		Content:     `{"text":{"body":"Orchestrator test message"}}`,
	}
	payload, _ := json.Marshal(env)

	_, err = orch.js.Publish(ctx, subject, payload)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// 3. Consume the message directly from the isolated stream
	cons, err := orch.js.CreateOrUpdateConsumer(ctx, streamName, jetstream.ConsumerConfig{
		Durable:       "test-orch-consumer",
		FilterSubject: ">",
	})
	if err != nil {
		t.Fatalf("Failed to create test consumer: %v", err)
	}

	msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	var found bool
	for msg := range msgs.Messages() {
		found = true
		var fetchedEnv MessageEnvelope
		err = json.Unmarshal(msg.Data(), &fetchedEnv)
		if err != nil {
			t.Fatalf("Failed to unmarshal envelope: %v", err)
		}
		if fetchedEnv.MessageID != msgID {
			t.Errorf("Expected message ID %s, got %s", msgID, fetchedEnv.MessageID)
		}
		if fetchedEnv.CustomerID != customerID {
			t.Errorf("Expected customer ID %s, got %s", customerID, fetchedEnv.CustomerID)
		}
		_ = msg.Ack()
	}

	if !found {
		t.Errorf("No message consumed from isolated stream, expected 1 message")
	}
}
