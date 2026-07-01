package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"meta-business-mcp/pkg/config"
)

type MessageEnvelope struct {
	MessageID   string `json:"message_id"`
	CustomerID  string `json:"customer_id"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"` // JSON serialized message payload
}

type Orchestrator struct {
	nc *nats.Conn
	js jetstream.JetStream
}

func NewOrchestrator(cfg *config.Config) (*Orchestrator, error) {
	nc, err := nats.Connect(cfg.NATS.URL, nats.Timeout(10*time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to initialize JetStream: %w", err)
	}

	o := &Orchestrator{
		nc: nc,
		js: js,
	}

	// Declare streams
	if err := o.declareStreams(); err != nil {
		o.Close()
		return nil, err
	}

	return o, nil
}

func (o *Orchestrator) declareStreams() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Meta MCP Delivery Stream (Outbound queues)
	_, err := o.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      "META_MCP_DELIVERY",
		Subjects:  []string{"whatsapp.messages.outbound", "whatsapp.messages.retry"},
		Retention: jetstream.WorkQueuePolicy,
	})
	if err != nil {
		return fmt.Errorf("failed to create/update delivery stream: %w", err)
	}

	// 2. Meta MCP Campaign Stream (Campaign triggers)
	_, err = o.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      "META_MCP_CAMPAIGN",
		Subjects:  []string{"whatsapp.campaigns.trigger"},
		Retention: jetstream.WorkQueuePolicy,
	})
	if err != nil {
		return fmt.Errorf("failed to create/update campaign stream: %w", err)
	}

	log.Println("[NATS] JetStream streams initialized successfully")
	return nil
}

func (o *Orchestrator) PublishOutboundMessage(ctx context.Context, msgID string, customerID string, msgType string, content map[string]any) error {
	contentBytes, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("failed to marshal outbound payload: %w", err)
	}

	env := MessageEnvelope{
		MessageID:   msgID,
		CustomerID:  customerID,
		MessageType: msgType,
		Content:     string(contentBytes),
	}

	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("failed to marshal outbound envelope: %w", err)
	}

	// Publish to subject
	_, err = o.js.Publish(ctx, "whatsapp.messages.outbound", payload)
	if err != nil {
		return fmt.Errorf("failed to publish outbound message: %w", err)
	}

	return nil
}

func (o *Orchestrator) Close() {
	o.nc.Close()
}
