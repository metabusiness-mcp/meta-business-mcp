package dashboard

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
)

// RegisterRoutes registers all dashboard API routes on the given chi router.
// Auth endpoints are public; all other /api/* routes require a valid session cookie.
// The static file handler for the embedded dashboard is also registered as a catch-all.
func RegisterRoutes(r chi.Router, cfg *config.Config, database *db.DB) {
	// Auto-generate credentials if not configured
	ensureCredentials(cfg)

	store := NewSessionStore(24 * time.Hour)
	auth := AuthMiddleware(store)

	// Auth endpoints (no authentication required)
	r.Post("/api/auth/login", handleLogin(cfg, store))
	r.Post("/api/auth/logout", handleLogout(store))
	r.Get("/api/auth/check", handleAuthCheck(store))

	// Protected API endpoints (require valid session cookie)
	r.Group(func(r chi.Router) {
		r.Use(auth)

		r.Get("/api/messages", handleGetMessages(database))
		r.Get("/api/conversations", handleGetConversations(database))
		r.Get("/api/compliance/events", handleGetComplianceEvents(database))
		r.Get("/api/templates", handleGetTemplates(database))
		r.Get("/api/metrics/summary", handleGetMetricsSummary(database))
		r.Get("/api/config/webhook", handleGetWebhookConfig(cfg))
		r.Get("/api/config/meta", handleGetMetaConfig(cfg))
	})
}

// ensureCredentials auto-generates dashboard password and session key if not configured.
// Prints the generated password to stderr so the admin can log in.
func ensureCredentials(cfg *config.Config) {
	needsPassword := cfg.Dashboard.PasswordHash == ""
	needsSessionKey := cfg.Dashboard.SessionKey == ""

	if !needsPassword && !needsSessionKey {
		return
	}

	// Check env vars first
	if needsPassword {
		if envHash := os.Getenv("DASHBOARD_PASSWORD_HASH"); envHash != "" {
			cfg.Dashboard.PasswordHash = envHash
			needsPassword = false
		}
	}
	if needsSessionKey {
		if envKey := os.Getenv("DASHBOARD_SESSION_KEY"); envKey != "" {
			cfg.Dashboard.SessionKey = envKey
			needsSessionKey = false
		}
	}

	// Generate random password if still needed
	if needsPassword {
		randomPass := generateRandomString(16)
		hash, err := bcrypt.GenerateFromPassword([]byte(randomPass), 10)
		if err != nil {
			log.Fatalf("[Dashboard] Failed to generate password hash: %v", err)
		}
		cfg.Dashboard.PasswordHash = string(hash)

		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "╔══════════════════════════════════════════════════════════╗")
		fmt.Fprintln(os.Stderr, "║              DASHBOARD CREDENTIALS (auto-generated)      ║")
		fmt.Fprintln(os.Stderr, "╠══════════════════════════════════════════════════════════╣")
		fmt.Fprintf(os.Stderr, "║  Username: %-44s ║\n", cfg.Dashboard.Username)
		fmt.Fprintf(os.Stderr, "║  Password: %-44s ║\n", randomPass)
		fmt.Fprintln(os.Stderr, "║                                                          ║")
		fmt.Fprintln(os.Stderr, "║  Save this password! It changes on every restart.        ║")
		fmt.Fprintln(os.Stderr, "║  To set a fixed password, use DASHBOARD_PASSWORD_HASH.   ║")
		fmt.Fprintln(os.Stderr, "╚══════════════════════════════════════════════════════════╝")
		fmt.Fprintln(os.Stderr, "")
	}

	// Generate random session key if still needed
	if needsSessionKey {
		cfg.Dashboard.SessionKey = generateRandomString(32)
	}
}

func generateRandomString(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:length]
}
