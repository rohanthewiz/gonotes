package main

import (
	"context"
	"fmt"
	"gonotes/models"
	"gonotes/web"
	"os"
	"path/filepath"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rutil/fileops"
	"github.com/rohanthewiz/serr"
	"github.com/urfave/cli/v2"
)

func main() {
	// Initialize logger
	logger.SetLogLevel("info")

	// Resolve default directory: ~/.gonotes
	home, err := os.UserHomeDir()
	if err != nil {
		logger.LogErr(err, "Failed to get user home directory")
		os.Exit(1)
	}
	defaultDir := filepath.Join(home, ".gonotes")

	app := &cli.App{
		Name:  "gonotes",
		Usage: "A self-hosted note-taking application",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "dir",
				Aliases: []string{"d"},
				Value:   defaultDir,
				Usage:   "working directory for data and config",
			},
			&cli.StringFlag{
				Name:    "port",
				Aliases: []string{"p"},
				Value:   web.WebPort,
				Usage:   "web server port",
			},
		},
		Action: func(c *cli.Context) error {
			return serve(c.String("dir"), c.String("port"))
		},
	}

	if err := app.Run(os.Args); err != nil {
		logger.LogErr(err)
		os.Exit(1)
	}
}

func serve(dir, port string) error {
	// Ensure working directory exists and switch to it.
	// All relative paths (DB, config) resolve under this directory.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("failed to change to directory %s: %w", dir, err)
	}
	logger.Info("Working directory set to", "path", dir)

	// Pickup local configs
	if issues, err := fileops.EnvFromFile("config/cfg_files/.env"); err != nil {
		for _, issue := range issues {
			logger.Warn("Cfg file issue", serr.StringFromErr(issue))
		}
	}

	// Initialize DuckDB database and create tables
	if err := models.InitDB(); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer models.CloseDB()

	// Initialize JWT token signing
	// In production, set GONOTES_JWT_SECRET environment variable
	if err := models.InitJWT(); err != nil {
		return fmt.Errorf("failed to initialize JWT: %w", err)
	}

	// Initialize sync client if configured via environment variables.
	initSyncClient()

	// Start server
	srv := web.NewServer(port)
	logger.Info("Starting GoNotes Web", "port", port)

	return web.Run(srv)
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