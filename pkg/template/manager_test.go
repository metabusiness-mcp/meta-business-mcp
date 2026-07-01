package template

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/db"
)

func TestTemplateManagerSyncAndGet(t *testing.T) {
	ctx := context.Background()

	// Connect to postgres/redis
	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "password",
			DBName:   "meta_mcp",
			SSLMode:  "disable",
		},
		Redis: config.RedisConfig{
			Addr: "localhost:6379",
		},
		Meta: config.MetaConfig{
			APIURL:      "http://localhost:8081",
			WABAID:      "mock-waba-id",
			AccessToken: "mock-access-token",
		},
		PoliciesPath: "policies.yaml",
	}

	database, err := db.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Run migration
	_ = db.Migrate(ctx, database)

	mgr := NewManager(database, cfg)

	// Clean up templates table
	_, _ = database.Pool.Exec(ctx, "TRUNCATE templates CASCADE")

	t.Run("SyncTemplates retrieves from mock meta and saves to DB", func(t *testing.T) {
		err := mgr.SyncTemplates(ctx)
		if err != nil {
			t.Fatalf("SyncTemplates failed: %v", err)
		}

		// Retrieve one of the seeded mock templates
		tmpl, err := mgr.GetTemplate(ctx, "sample_flight_confirmation", "en")
		if err != nil {
			t.Fatalf("Failed to get template: %v", err)
		}

		if tmpl.Name != "sample_flight_confirmation" {
			t.Errorf("Expected template name 'sample_flight_confirmation', got '%s'", tmpl.Name)
		}
		if tmpl.Category != "utility" {
			t.Errorf("Expected template category 'utility', got '%s'", tmpl.Category)
		}
		if tmpl.Status != "APPROVED" {
			t.Errorf("Expected status APPROVED, got '%s'", tmpl.Status)
		}
	})

	t.Run("GetTemplate returns error when not found", func(t *testing.T) {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		nonExistentName := fmt.Sprintf("non_existent_%d", rng.Intn(100000))
		_, err := mgr.GetTemplate(ctx, nonExistentName, "en")
		if err == nil {
			t.Errorf("Expected error for non-existent template, got nil")
		}
	})

	t.Run("ListTemplates returns all templates with no filters", func(t *testing.T) {
		templates, total, err := mgr.ListTemplates(ctx, "", "", "", 50, 0)
		if err != nil {
			t.Fatalf("ListTemplates failed: %v", err)
		}
		if total < 2 {
			t.Errorf("Expected at least 2 templates from sync, got %d", total)
		}
		if len(templates) < 2 {
			t.Errorf("Expected at least 2 templates in result, got %d", len(templates))
		}
	})

	t.Run("ListTemplates filters by status", func(t *testing.T) {
		templates, total, err := mgr.ListTemplates(ctx, "APPROVED", "", "", 50, 0)
		if err != nil {
			t.Fatalf("ListTemplates failed: %v", err)
		}
		for _, tmpl := range templates {
			if tmpl.Status != "APPROVED" {
				t.Errorf("Expected all templates to have status 'APPROVED', got '%s'", tmpl.Status)
			}
		}
		if total != len(templates) {
			t.Errorf("Expected total %d to match returned count %d", total, len(templates))
		}
	})

	t.Run("ListTemplates filters by category", func(t *testing.T) {
		templates, _, err := mgr.ListTemplates(ctx, "", "utility", "", 50, 0)
		if err != nil {
			t.Fatalf("ListTemplates failed: %v", err)
		}
		for _, tmpl := range templates {
			if tmpl.Category != "utility" {
				t.Errorf("Expected category 'utility', got '%s'", tmpl.Category)
			}
		}
	})

	t.Run("ListTemplates pagination works", func(t *testing.T) {
		_, total, err := mgr.ListTemplates(ctx, "", "", "", 50, 0)
		if err != nil {
			t.Fatalf("ListTemplates failed: %v", err)
		}

		// Get first page with limit 1
		page1, _, err := mgr.ListTemplates(ctx, "", "", "", 1, 0)
		if err != nil {
			t.Fatalf("ListTemplates page1 failed: %v", err)
		}
		if len(page1) != 1 {
			t.Errorf("Expected 1 template on page 1, got %d", len(page1))
		}

		// Get second page
		page2, total2, err := mgr.ListTemplates(ctx, "", "", "", 1, 1)
		if err != nil {
			t.Fatalf("ListTemplates page2 failed: %v", err)
		}
		if total != total2 {
			t.Errorf("Expected total to be consistent across pages")
		}
		if len(page2) > 0 && page1[0].Name == page2[0].Name && page1[0].Locale == page2[0].Locale {
			t.Errorf("Expected different templates on page 1 and page 2")
		}
	})

	t.Run("ListTemplates empty result for non-existent status", func(t *testing.T) {
		templates, total, err := mgr.ListTemplates(ctx, "NONEXISTENT_STATUS", "", "", 50, 0)
		if err != nil {
			t.Fatalf("ListTemplates failed: %v", err)
		}
		if total != 0 {
			t.Errorf("Expected 0 templates for non-existent status, got %d", total)
		}
		if len(templates) != 0 {
			t.Errorf("Expected empty result, got %d templates", len(templates))
		}
	})
}
