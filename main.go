package main

import (
	"context"
	"gonotes/models"
	"gonotes/web"
	"os"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rutil/fileops"
	"github.com/rohanthewiz/serr"
)

func main() {
	// Initialize logger
	logger.SetLogLevel("info")

	// Pickup local configs
	if issues, err := fileops.EnvFromFile("config/cfg_files/.env"); err != nil {
		for _, issue := range issues {
			logger.Warn("Cfg file issue", serr.StringFromErr(issue))
		}
	}

	// Initialize DuckDB database and create tables
	if err := models.InitDB(); err != nil {
		logger.LogErr(err, "Failed to initialize database")
		os.Exit(1)
	}
	defer models.CloseDB()

	// Initialize JWT token signing
	// In production, set GONOTES_JWT_SECRET environment variable
	if err := models.InitJWT(); err != nil {
		logger.LogErr(err, "Failed to initialize JWT")
		os.Exit(1)
	}

	// Initialize sync client if configured via environment variables.
	// The sync client runs a background goroutine that authenticates with
	// the hub, pulls/pushes changes, and resolves conflicts automatically.
	initSyncClient()

	// Start server
	srv := web.NewServer()
	logger.Info("Starting GoNotes Web on port 8080")

	logger.LogErr(web.Run(srv))
}

// initSyncClient loads sync configuration from environment variables and
// starts the background sync goroutine if enabled. Errors during setup
// are logged but don't prevent the server from starting — sync is an
// optional enhancement, not a hard dependency.
func initSyncClient() {
	syncConfig, err := models.LoadSyncConfig()
	if err != nil {
		logger.LogErr(err, "Failed to load sync config")
		return
	}

	if !syncConfig.Enabled {
		logger.Info("Sync is disabled (set GONOTES_SYNC_ENABLED=true to enable)")
		return
	}

	client, err := models.NewSyncClient(syncConfig)
	if err != nil {
		logger.LogErr(err, "Failed to create sync client")
		return
	}

	// Use a background context — the sync client manages its own lifecycle
	// via Stop(). In a future iteration this could use a signal-aware
	// context for graceful OS signal handling.
	ctx := context.Background()
	client.Start(ctx)

	logger.Info("Sync client initialized and running",
		"hub_url", syncConfig.HubURL,
		"interval", syncConfig.Interval.String(),
	)
}
