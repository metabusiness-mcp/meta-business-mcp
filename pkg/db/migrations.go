package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

type PolicySeed struct {
	ID          string         `yaml:"id"`
	Name        string         `yaml:"name"`
	Type        string         `yaml:"type"`
	Channel     string         `yaml:"channel"`
	MessageType string         `yaml:"message_type"`
	IsEnabled   bool           `yaml:"is_enabled"`
	Rules       map[string]any `yaml:"rules"`
}

type PolicySeedWrapper struct {
	Policies []PolicySeed `yaml:"policies"`
}

func Migrate(ctx context.Context, db *DB) error {
	queries := []string{
		// 1. conversations
		`CREATE TABLE IF NOT EXISTS conversations (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			customer_id VARCHAR(100) NOT NULL,
			channel VARCHAR(50) NOT NULL DEFAULT 'whatsapp',
			last_inbound_at TIMESTAMP WITH TIME ZONE,
			last_outbound_at TIMESTAMP WITH TIME ZONE,
			window_expires_at TIMESTAMP WITH TIME ZONE,
			conversation_type VARCHAR(50) NOT NULL DEFAULT 'service',
			active_template_id VARCHAR(100),
			marketing_eligibility VARCHAR(50) NOT NULL DEFAULT 'eligible',
			utility_eligibility VARCHAR(50) NOT NULL DEFAULT 'eligible',
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);`,

		`CREATE INDEX IF NOT EXISTS idx_conversations_expires ON conversations(window_expires_at);`,

		// 2. customers
		`CREATE TABLE IF NOT EXISTS customers (
			id VARCHAR(100) PRIMARY KEY,
			channel VARCHAR(50) DEFAULT 'whatsapp',
			opt_in_marketing BOOLEAN DEFAULT TRUE,
			opt_in_utility BOOLEAN DEFAULT TRUE,
			last_interaction_at TIMESTAMP WITH TIME ZONE,
			engagement_score DOUBLE PRECISION DEFAULT 1.0,
			tags TEXT[],
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);`,

		// 3. message_frequencies
		`CREATE TABLE IF NOT EXISTS message_frequencies (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			customer_id VARCHAR(100) NOT NULL,
			channel VARCHAR(50) NOT NULL DEFAULT 'whatsapp',
			message_type VARCHAR(50) NOT NULL,
			sent_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);`,
		`CREATE INDEX IF NOT EXISTS idx_msg_freq_lookup ON message_frequencies(customer_id, channel, message_type, sent_at);`,

		// 4. templates
		`CREATE TABLE IF NOT EXISTS templates (
			name VARCHAR(150),
			locale VARCHAR(10) DEFAULT 'en',
			category VARCHAR(50) NOT NULL,
			status VARCHAR(50) DEFAULT 'approved',
			body_text TEXT NOT NULL,
			variables JSONB,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			PRIMARY KEY (name, locale)
		);`,

		// 5. policies
		`CREATE TABLE IF NOT EXISTS policies (
			id VARCHAR(100) PRIMARY KEY,
			name VARCHAR(150) NOT NULL,
			type VARCHAR(50) NOT NULL,
			channel VARCHAR(50) NOT NULL DEFAULT 'whatsapp',
			message_type VARCHAR(50) NOT NULL,
			is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
			rules JSONB NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);`,

		// 6. campaigns
		`CREATE TABLE IF NOT EXISTS campaigns (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(150) NOT NULL,
			type VARCHAR(50) NOT NULL,
			status VARCHAR(50) NOT NULL DEFAULT 'draft',
			template_name VARCHAR(150),
			locale VARCHAR(10) DEFAULT 'en',
			variables JSONB,
			audience_filter JSONB,
			scheduled_at TIMESTAMP WITH TIME ZONE,
			sent_count INTEGER DEFAULT 0,
			delivered_count INTEGER DEFAULT 0,
			failed_count INTEGER DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);`,

		// 7. error_knowledge_base
		`CREATE TABLE IF NOT EXISTS error_knowledge_base (
			code INTEGER PRIMARY KEY,
			category VARCHAR(50) NOT NULL,
			can_retry BOOLEAN NOT NULL DEFAULT FALSE,
			human_explanation TEXT NOT NULL,
			suggested_action TEXT NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);`,

		// 8. messages
		`CREATE TABLE IF NOT EXISTS messages (
			id VARCHAR(100) PRIMARY KEY,
			conversation_id UUID REFERENCES conversations(id),
			customer_id VARCHAR(100) NOT NULL,
			direction VARCHAR(20) NOT NULL,
			message_type VARCHAR(50) NOT NULL,
			content JSONB,
			status VARCHAR(50) DEFAULT 'queued',
			error_code INTEGER,
			error_message TEXT,
			retry_count INTEGER DEFAULT 0,
			next_retry_at TIMESTAMP WITH TIME ZONE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_status ON messages(status);`,

		// 9. audit_logs
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			action VARCHAR(100) NOT NULL,
			details JSONB,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);`,

		// 10. conversation_pricing (Meta conversation pricing knowledge base)
		`CREATE TABLE IF NOT EXISTS conversation_pricing (
			country VARCHAR(10) NOT NULL,
			conversation_type VARCHAR(50) NOT NULL,
			cost_per_conversation NUMERIC(10,4) NOT NULL,
			currency VARCHAR(10) NOT NULL DEFAULT 'USD',
			pricing_category VARCHAR(50) NOT NULL,
			effective_date DATE NOT NULL DEFAULT CURRENT_DATE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			PRIMARY KEY (country, conversation_type, effective_date)
		);`,

		// 11. Add deliver_at column to messages (for scheduled message support)
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS deliver_at TIMESTAMP WITH TIME ZONE;`,
		`CREATE INDEX IF NOT EXISTS idx_messages_scheduled ON messages(status, deliver_at) WHERE status = 'scheduled';`,

		// 12. Add paused_at column to campaigns (for pause/resume support)
		`ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS paused_at TIMESTAMP WITH TIME ZONE;`,

		// 13. Add status column to conversations (for archive lifecycle)
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS status VARCHAR(50) NOT NULL DEFAULT 'active';`,
	}

	for _, query := range queries {
		if _, err := db.Pool.Exec(ctx, query); err != nil {
			return fmt.Errorf("migration failed for query [%s]: %w", query, err)
		}
	}

	log.Println("Database migrations applied successfully")
	return nil
}

func Seed(ctx context.Context, db *DB, policiesPath string) error {
	// 1. Seed error_knowledge_base
	errorsToSeed := []struct {
		Code        int
		Category    string
		CanRetry    bool
		Explanation string
		Action      string
	}{
		{131047, "user_related", false, "Re-engagement message failed because user is outside the 24-hour window.", "Send an approved WhatsApp template instead of a free-form message."},
		{131048, "policy", true, "Spam rate limit hit. Account sending speed has exceeded threshold limits.", "Reduce messaging velocity and wait for at least 1 hour before retrying."},
		{131049, "policy", false, "Frequency cap reached. Customer has received too many marketing messages.", "Retry delivery tomorrow when the daily sending window resets."},
		{131030, "user_related", false, "Recipient phone number not in allowed list. In Sandbox mode, you can only send messages to pre-registered, verified phone numbers.", "Add the recipient phone number to the allowed list in your Meta Developer Dashboard, or verify the number is active."},
		{470, "transient", true, "Message failed due to an unknown Meta error.", "Automatic retry will attempt to send this message again."},
		{10, "policy", false, "Permission denied. WABA account setup is incomplete or invalid.", "Verify your WABA credentials, phone number ID, and access token permissions."},
		{100, "user_related", false, "Invalid parameter. Re-check the phone number or request format.", "Validate the phone number format (must include country code) and template variables."},
		{200, "policy", false, "Permission error. App has insufficient permissions to access the WABA account.", "Check Meta App settings and regenerate your System User Access Token."},
	}

	for _, e := range errorsToSeed {
		_, err := db.Pool.Exec(ctx,
			`INSERT INTO error_knowledge_base (code, category, can_retry, human_explanation, suggested_action, updated_at) 
			 VALUES ($1, $2, $3, $4, $5, NOW())
			 ON CONFLICT (code) DO UPDATE 
			 SET category = EXCLUDED.category, 
			     can_retry = EXCLUDED.can_retry, 
			     human_explanation = EXCLUDED.human_explanation, 
			     suggested_action = EXCLUDED.suggested_action,
			     updated_at = NOW()`,
			e.Code, e.Category, e.CanRetry, e.Explanation, e.Action)
		if err != nil {
			return fmt.Errorf("failed to seed error %d: %w", e.Code, err)
		}
	}
	log.Println("Seeded/Updated error_knowledge_base with standard Meta error codes")

	// 2. Seed conversation_pricing (Meta pricing model)
	pricingToSeed := []struct {
		Country          string
		ConversationType string
		Cost             float64
		Currency         string
		Category         string
	}{
		{"ID", "marketing", 0.0440, "USD", "marketing"},
		{"ID", "utility", 0.0220, "USD", "utility"},
		{"ID", "authentication", 0.0220, "USD", "authentication"},
		{"ID", "service", 0.0000, "USD", "service"},
		{"US", "marketing", 0.0147, "USD", "marketing"},
		{"US", "utility", 0.0040, "USD", "utility"},
		{"US", "authentication", 0.0147, "USD", "authentication"},
		{"US", "service", 0.0000, "USD", "service"},
		{"IN", "marketing", 0.0099, "USD", "marketing"},
		{"IN", "utility", 0.0014, "USD", "utility"},
		{"IN", "authentication", 0.0099, "USD", "authentication"},
		{"IN", "service", 0.0000, "USD", "service"},
		{"BR", "marketing", 0.0625, "USD", "marketing"},
		{"BR", "utility", 0.0080, "USD", "utility"},
		{"BR", "authentication", 0.0325, "USD", "authentication"},
		{"BR", "service", 0.0000, "USD", "service"},
		{"GB", "marketing", 0.0528, "USD", "marketing"},
		{"GB", "utility", 0.0192, "USD", "utility"},
		{"GB", "authentication", 0.0313, "USD", "authentication"},
		{"GB", "service", 0.0000, "USD", "service"},
		{"DEFAULT", "marketing", 0.0250, "USD", "marketing"},
		{"DEFAULT", "utility", 0.0100, "USD", "utility"},
		{"DEFAULT", "authentication", 0.0200, "USD", "authentication"},
		{"DEFAULT", "service", 0.0000, "USD", "service"},
	}

	for _, p := range pricingToSeed {
		_, err := db.Pool.Exec(ctx,
			`INSERT INTO conversation_pricing (country, conversation_type, cost_per_conversation, currency, pricing_category, effective_date, updated_at) 
			 VALUES ($1, $2, $3, $4, $5, CURRENT_DATE, NOW())
			 ON CONFLICT (country, conversation_type, effective_date) DO UPDATE 
			 SET cost_per_conversation = EXCLUDED.cost_per_conversation, 
			     currency = EXCLUDED.currency, 
			     pricing_category = EXCLUDED.pricing_category,
			     updated_at = NOW()`,
			p.Country, p.ConversationType, p.Cost, p.Currency, p.Category)
		if err != nil {
			return fmt.Errorf("failed to seed pricing for %s/%s: %w", p.Country, p.ConversationType, err)
		}
	}
	log.Println("Seeded/Updated conversation_pricing with Meta pricing data")

	// 3. Seed policies
	var policyCount int
	err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM policies").Scan(&policyCount)
	if err != nil {
		return fmt.Errorf("failed to check policies: %w", err)
	}

	if policyCount == 0 && policiesPath != "" {
		if _, err := os.Stat(policiesPath); err == nil {
			data, err := os.ReadFile(policiesPath)
			if err != nil {
				return fmt.Errorf("failed to read policies file: %w", err)
			}

			var seed PolicySeedWrapper
			if err := yaml.Unmarshal(data, &seed); err != nil {
				return fmt.Errorf("failed to parse policies YAML: %w", err)
			}

			for _, p := range seed.Policies {
				rulesJSON, err := json.Marshal(p.Rules)
				if err != nil {
					return fmt.Errorf("failed to marshal policy rules: %w", err)
				}

				_, err = db.Pool.Exec(ctx,
					`INSERT INTO policies (id, name, type, channel, message_type, is_enabled, rules) 
					 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
					p.ID, p.Name, p.Type, p.Channel, p.MessageType, p.IsEnabled, rulesJSON)
				if err != nil {
					return fmt.Errorf("failed to seed policy %s: %w", p.ID, err)
				}
			}
			log.Printf("Seeded %d policies from %s", len(seed.Policies), policiesPath)
		}
	}

	return nil
}
