package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	mcpServer "github.com/mark3labs/mcp-go/server"
	"meta-business-mcp/pkg/campaign"
	"meta-business-mcp/pkg/compliance"
	"meta-business-mcp/pkg/config"
	"meta-business-mcp/pkg/dashboard"
	"meta-business-mcp/pkg/db"
	"meta-business-mcp/pkg/delivery"
	"meta-business-mcp/pkg/errorintel"
	"meta-business-mcp/pkg/mcp"
	"meta-business-mcp/pkg/observability"
	"meta-business-mcp/pkg/policy"
	"meta-business-mcp/pkg/ratelimit"
	"meta-business-mcp/pkg/state"
	"meta-business-mcp/pkg/template"
	"meta-business-mcp/pkg/userintel"
	"meta-business-mcp/pkg/webhook"
)

func main() {
	// Standard log outputs MUST go to stderr, leaving stdout for JSON-RPC communication only.
	log.SetOutput(os.Stderr)
	log.Println("[Server] Initializing Meta Business MCP...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Load config
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("[Server] Failed to load config: %v", err)
	}

	// Acquire instance lock based on HTTP port to prevent concurrent instances
	lockPort := cfg.Server.HTTPPort + 10000
	lockLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", lockPort))
	if err != nil {
		log.Fatalf("[Server] Another instance of Meta Business MCP is already running on port %d (failed to acquire instance lock on port %d: %v)", cfg.Server.HTTPPort, lockPort, err)
	}
	defer lockLn.Close()

	// 2. Connect DB & Redis
	database, err := db.Connect(ctx, cfg)
	if err != nil {
		log.Fatalf("[Server] Database connection failed: %v", err)
	}
	defer database.Close()

	// 3. Apply Migrations & Seeds
	if err := db.Migrate(ctx, database); err != nil {
		log.Fatalf("[Server] Migrations failed: %v", err)
	}
	if err := db.Seed(ctx, database, cfg.PoliciesPath); err != nil {
		log.Fatalf("[Server] Database seeding failed: %v", err)
	}

	// 4. Initialize Engines
	stateEngine := state.NewEngine(database)
	userManager := userintel.NewManager(database)
	complianceEngine := compliance.NewEngine(database, stateEngine, userManager)
	policyEngine := policy.NewEngine(database)
	errorIntel := errorintel.NewEngine(database)
	templateManager := template.NewManager(database, cfg)
	campaignManager := campaign.NewManager(database)

	// Sync templates on startup to populate local cache
	go func() {
		log.Println("[Server] Syncing templates from Meta Cloud API...")
		if err := templateManager.SyncTemplates(ctx); err != nil {
			log.Printf("[Server] Template sync warning on startup: %v", err)
		}
	}()

	// 5. Initialize NATS Orchestrator & Workers
	orchestrator, err := delivery.NewOrchestrator(cfg)
	if err != nil {
		log.Fatalf("[Server] Failed to start delivery orchestrator: %v", err)
	}
	defer orchestrator.Close()

	limiter := ratelimit.NewLimiter(database.Redis)
	worker := delivery.NewWorker(
		database, cfg, orchestrator, limiter, complianceEngine, stateEngine, errorIntel,
	)
	if err := worker.Start(ctx); err != nil {
		log.Fatalf("[Server] Failed to start delivery workers: %v", err)
	}

	// 5b. Start Scheduler for scheduled messages and campaigns
	scheduler, err := delivery.NewScheduler(
		database, cfg, orchestrator, complianceEngine, policyEngine, userManager, delivery.RealClock{},
	)
	if err != nil {
		log.Fatalf("[Server] Failed to create scheduler: %v", err)
	}
	go func() {
		if err := scheduler.Start(ctx); err != nil {
			log.Printf("[Server] Scheduler stopped: %v", err)
		}
	}()

	// 6. Start HTTP server (webhook receiver, health check, metrics)
	receiver := webhook.NewReceiver(database, cfg, stateEngine)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	r.Handle("/metrics", observability.MetricsHandler())

	r.Get("/webhook", receiver.Verify)
	r.Post("/webhook", receiver.HandleEvent)

	// Register dashboard API routes and static file serving
	dashboard.RegisterRoutes(r, cfg, database)
	if dashboardFS := getDashboardFS(); dashboardFS != nil {
		staticHandler := dashboard.StaticHandler(dashboardFS)
		r.Get("/*", staticHandler.ServeHTTP)
		log.Println("[Server] Embedded dashboard available at /")
	} else {
		log.Println("[Server] Dashboard static files not embedded (development mode)")
	}

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler: r,
	}

	go func() {
		log.Printf("[Server] HTTP Web Server running on port %d...", cfg.Server.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Server] HTTP server failed: %v", err)
		}
	}()

	// Graceful shutdown listener
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		log.Println("[Server] Shutting down HTTP server...")
		_ = httpServer.Shutdown(context.Background())
		cancel()
	}()

	// 7. Start MCP Server (stdio blocks)
	mcpSrv := mcp.NewServer(
		database, cfg, complianceEngine, policyEngine, errorIntel, templateManager, userManager, campaignManager, orchestrator,
	)

	log.Println("[Server] Starting MCP stdio server channel...")
	if err := mcpServer.ServeStdio(mcpSrv.GetMCPServer()); err != nil {
		log.Fatalf("[Server] MCP Server failed: %v", err)
	}
}
