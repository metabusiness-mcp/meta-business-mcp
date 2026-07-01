package config

import (
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server            ServerConfig    `yaml:"server"`
	Database          DatabaseConfig  `yaml:"database"`
	Redis             RedisConfig     `yaml:"redis"`
	NATS              NATSConfig      `yaml:"nats"`
	Meta              MetaConfig      `yaml:"meta"`
	Dashboard         DashboardConfig `yaml:"dashboard"`
	PoliciesPath      string          `yaml:"policies_path"`
	Tier              string          `yaml:"tier"`
	SchedPollInterval string          `yaml:"sched_poll_interval"`
}

type DashboardConfig struct {
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password_hash"`
	SessionKey   string `yaml:"session_key"`
}

type ServerConfig struct {
	HTTPPort   int    `yaml:"http_port"`
	MCPName    string `yaml:"mcp_name"`
	MCPVersion string `yaml:"mcp_version"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type NATSConfig struct {
	URL string `yaml:"url"`
}

type MetaConfig struct {
	APIURL             string `yaml:"api_url"`
	AccessToken        string `yaml:"access_token"`
	PhoneNumberID      string `yaml:"phone_number_id"`
	WABAID             string `yaml:"waba_id"`
	WebhookVerifyToken string `yaml:"webhook_verify_token"`
}

func LoadConfig(path string) (*Config, error) {
	// Set defaults
	cfg := &Config{
		Server: ServerConfig{
			HTTPPort:   8080,
			MCPName:    "meta-business-mcp",
			MCPVersion: "1.0.0",
		},
		Database: DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "password",
			DBName:   "meta_mcp",
			SSLMode:  "disable",
		},
		Redis: RedisConfig{
			Addr: "localhost:6379",
		},
		NATS: NATSConfig{
			URL: "nats://localhost:4222",
		},
		Meta: MetaConfig{
			APIURL: "https://graph.facebook.com",
		},
		PoliciesPath:      "policies.yaml",
		Tier:              "oss",
		SchedPollInterval: "30s",
	}

	// Load from file if exists
	if _, err := os.Stat(path); err == nil {
		data, err := os.ReadFile(path)
		if err == nil {
			_ = yaml.Unmarshal(data, cfg)
		}
	}

	// Override from Env
	overrideFromEnv(cfg)

	return cfg, nil
}

func overrideFromEnv(cfg *Config) {
	if val := os.Getenv("SERVER_HTTP_PORT"); val != "" {
		if p, err := strconv.Atoi(val); err == nil {
			cfg.Server.HTTPPort = p
		}
	}
	if val := os.Getenv("SERVER_MCP_NAME"); val != "" {
		cfg.Server.MCPName = val
	}
	if val := os.Getenv("SERVER_MCP_VERSION"); val != "" {
		cfg.Server.MCPVersion = val
	}
	if val := os.Getenv("DB_HOST"); val != "" {
		cfg.Database.Host = val
	}
	if val := os.Getenv("DB_PORT"); val != "" {
		if p, err := strconv.Atoi(val); err == nil {
			cfg.Database.Port = p
		}
	}
	if val := os.Getenv("DB_USER"); val != "" {
		cfg.Database.User = val
	}
	if val := os.Getenv("DB_PASSWORD"); val != "" {
		cfg.Database.Password = val
	}
	if val := os.Getenv("DB_NAME"); val != "" {
		cfg.Database.DBName = val
	}
	if val := os.Getenv("DB_SSLMODE"); val != "" {
		cfg.Database.SSLMode = val
	}
	if val := os.Getenv("REDIS_ADDR"); val != "" {
		cfg.Redis.Addr = val
	}
	if val := os.Getenv("REDIS_PASSWORD"); val != "" {
		cfg.Redis.Password = val
	}
	if val := os.Getenv("REDIS_DB"); val != "" {
		if d, err := strconv.Atoi(val); err == nil {
			cfg.Redis.DB = d
		}
	}
	if val := os.Getenv("NATS_URL"); val != "" {
		cfg.NATS.URL = val
	}
	if val := os.Getenv("META_API_URL"); val != "" {
		cfg.Meta.APIURL = val
	}
	if val := os.Getenv("META_ACCESS_TOKEN"); val != "" {
		cfg.Meta.AccessToken = val
	}
	if val := os.Getenv("META_PHONE_NUMBER_ID"); val != "" {
		cfg.Meta.PhoneNumberID = val
	}
	if val := os.Getenv("META_WABA_ID"); val != "" {
		cfg.Meta.WABAID = val
	}
	if val := os.Getenv("META_WEBHOOK_VERIFY_TOKEN"); val != "" {
		cfg.Meta.WebhookVerifyToken = val
	}
	if val := os.Getenv("POLICIES_PATH"); val != "" {
		cfg.PoliciesPath = val
	}
	if val := os.Getenv("TIER"); val != "" {
		cfg.Tier = val
	}
	if val := os.Getenv("SCHEDULER_POLL_INTERVAL"); val != "" {
		cfg.SchedPollInterval = val
	}
	if val := os.Getenv("DASHBOARD_USERNAME"); val != "" {
		cfg.Dashboard.Username = val
	}
	if val := os.Getenv("DASHBOARD_PASSWORD_HASH"); val != "" {
		cfg.Dashboard.PasswordHash = val
	}
	if val := os.Getenv("DASHBOARD_SESSION_KEY"); val != "" {
		cfg.Dashboard.SessionKey = val
	}
}
