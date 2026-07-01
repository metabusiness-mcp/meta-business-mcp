package config

import (
	"os"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	// Ensure env is clean
	os.Clearenv()

	cfg, err := LoadConfig("non_existent_file.yaml")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Server.HTTPPort != 8080 {
		t.Errorf("Expected default HTTPPort 8080, got %d", cfg.Server.HTTPPort)
	}
	if cfg.Database.Host != "localhost" {
		t.Errorf("Expected default Host 'localhost', got '%s'", cfg.Database.Host)
	}
	if cfg.Redis.Addr != "localhost:6379" {
		t.Errorf("Expected default Redis Addr 'localhost:6379', got '%s'", cfg.Redis.Addr)
	}
	if cfg.NATS.URL != "nats://localhost:4222" {
		t.Errorf("Expected default NATS URL, got '%s'", cfg.NATS.URL)
	}
	if cfg.Meta.APIURL != "https://graph.facebook.com" {
		t.Errorf("Expected default Meta API URL, got '%s'", cfg.Meta.APIURL)
	}
	if cfg.PoliciesPath != "policies.yaml" {
		t.Errorf("Expected default PoliciesPath 'policies.yaml', got '%s'", cfg.PoliciesPath)
	}
}

func TestLoadConfigEnvOverrides(t *testing.T) {
	os.Clearenv()
	os.Setenv("SERVER_HTTP_PORT", "9090")
	os.Setenv("SERVER_MCP_NAME", "custom-mcp")
	os.Setenv("SERVER_MCP_VERSION", "2.0.0")
	os.Setenv("DB_HOST", "db-host")
	os.Setenv("DB_PORT", "9999")
	os.Setenv("DB_USER", "db-user")
	os.Setenv("DB_PASSWORD", "db-pass")
	os.Setenv("DB_NAME", "db-name")
	os.Setenv("DB_SSLMODE", "require")
	os.Setenv("REDIS_ADDR", "redis-host:6380")
	os.Setenv("REDIS_PASSWORD", "redis-pass")
	os.Setenv("REDIS_DB", "2")
	os.Setenv("NATS_URL", "nats://nats-host:4222")
	os.Setenv("META_API_URL", "http://meta-mock")
	os.Setenv("META_ACCESS_TOKEN", "token")
	os.Setenv("META_PHONE_NUMBER_ID", "phone-id")
	os.Setenv("META_WABA_ID", "waba-id")
	os.Setenv("META_WEBHOOK_VERIFY_TOKEN", "verify-token")
	os.Setenv("POLICIES_PATH", "custom_policies.yaml")

	cfg, err := LoadConfig("non_existent_file.yaml")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Server.HTTPPort != 9090 {
		t.Errorf("Expected HTTPPort 9090, got %d", cfg.Server.HTTPPort)
	}
	if cfg.Server.MCPName != "custom-mcp" {
		t.Errorf("Expected MCPName 'custom-mcp', got '%s'", cfg.Server.MCPName)
	}
	if cfg.Server.MCPVersion != "2.0.0" {
		t.Errorf("Expected MCPVersion '2.0.0', got '%s'", cfg.Server.MCPVersion)
	}
	if cfg.Database.Host != "db-host" {
		t.Errorf("Expected Host 'db-host', got '%s'", cfg.Database.Host)
	}
	if cfg.Database.Port != 9999 {
		t.Errorf("Expected Port 9999, got %d", cfg.Database.Port)
	}
	if cfg.Database.User != "db-user" {
		t.Errorf("Expected User 'db-user', got '%s'", cfg.Database.User)
	}
	if cfg.Database.Password != "db-pass" {
		t.Errorf("Expected Password 'db-pass', got '%s'", cfg.Database.Password)
	}
	if cfg.Database.DBName != "db-name" {
		t.Errorf("Expected DBName 'db-name', got '%s'", cfg.Database.DBName)
	}
	if cfg.Database.SSLMode != "require" {
		t.Errorf("Expected SSLMode 'require', got '%s'", cfg.Database.SSLMode)
	}
	if cfg.Redis.Addr != "redis-host:6380" {
		t.Errorf("Expected Redis Addr 'redis-host:6380', got '%s'", cfg.Redis.Addr)
	}
	if cfg.Redis.Password != "redis-pass" {
		t.Errorf("Expected Redis Password 'redis-pass', got '%s'", cfg.Redis.Password)
	}
	if cfg.Redis.DB != 2 {
		t.Errorf("Expected Redis DB 2, got %d", cfg.Redis.DB)
	}
	if cfg.NATS.URL != "nats://nats-host:4222" {
		t.Errorf("Expected NATS URL, got '%s'", cfg.NATS.URL)
	}
	if cfg.Meta.APIURL != "http://meta-mock" {
		t.Errorf("Expected Meta API URL, got '%s'", cfg.Meta.APIURL)
	}
	if cfg.Meta.AccessToken != "token" {
		t.Errorf("Expected Meta Access Token 'token', got '%s'", cfg.Meta.AccessToken)
	}
	if cfg.Meta.PhoneNumberID != "phone-id" {
		t.Errorf("Expected Meta Phone ID 'phone-id', got '%s'", cfg.Meta.PhoneNumberID)
	}
	if cfg.Meta.WABAID != "waba-id" {
		t.Errorf("Expected WABA ID 'waba-id', got '%s'", cfg.Meta.WABAID)
	}
	if cfg.Meta.WebhookVerifyToken != "verify-token" {
		t.Errorf("Expected WebhookVerifyToken 'verify-token', got '%s'", cfg.Meta.WebhookVerifyToken)
	}
	if cfg.PoliciesPath != "custom_policies.yaml" {
		t.Errorf("Expected PoliciesPath 'custom_policies.yaml', got '%s'", cfg.PoliciesPath)
	}
}

func TestLoadConfigFileAndEnv(t *testing.T) {
	os.Clearenv()
	tempFile, err := os.CreateTemp(".", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp config: %v", err)
	}
	defer os.Remove(tempFile.Name())

	yamlContent := `
server:
  http_port: 8085
database:
  host: "yaml-host"
`
	if _, err := tempFile.Write([]byte(yamlContent)); err != nil {
		t.Fatalf("Failed to write yaml: %v", err)
	}
	tempFile.Close()

	// 1. Load from file only
	cfg, err := LoadConfig(tempFile.Name())
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Server.HTTPPort != 8085 {
		t.Errorf("Expected HTTPPort from file 8085, got %d", cfg.Server.HTTPPort)
	}
	if cfg.Database.Host != "yaml-host" {
		t.Errorf("Expected Database.Host from file 'yaml-host', got '%s'", cfg.Database.Host)
	}
	// Database port should be default
	if cfg.Database.Port != 5432 {
		t.Errorf("Expected default Port 5432, got %d", cfg.Database.Port)
	}

	// 2. Override with env
	os.Setenv("DB_HOST", "env-override-host")
	cfg, err = LoadConfig(tempFile.Name())
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Database.Host != "env-override-host" {
		t.Errorf("Expected Database.Host to be overridden to 'env-override-host', got '%s'", cfg.Database.Host)
	}
	if cfg.Server.HTTPPort != 8085 {
		t.Errorf("Expected HTTPPort to remain 8085, got %d", cfg.Server.HTTPPort)
	}
}
