package main

import (
	"context"
	"fmt"
	"gonotes/models"
	"gonotes/tui"
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
				EnvVars: []string{"GONOTES_PORT", "PORT"},
				Usage:   "web server port",
			},
		},
		Action: func(c *cli.Context) error {
			return serve(c.String("dir"), c.String("port"))
		},
		Commands: []*cli.Command{
			{
				Name:  "tui",
				Usage: "Browse and edit notes in a terminal UI (no web server needed)",
				// The command declares its own --dir so the natural invocation
				// `gonotes tui -d <dir>` works; urfave/cli only accepts global
				// flags BEFORE the subcommand (`gonotes -d <dir> tui`), which
				// trips people up. Command-level flags win on lookup.
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "dir",
						Aliases: []string{"d"},
						Value:   defaultDir,
						Usage:   "working directory for data and config",
					},
				},
				Action: func(c *cli.Context) error {
					return runTui(c.String("dir"))
				},
			},
			{
				Name:  "import-gob",
				Usage: "Import notes from a .gob file produced by the legacy go_notes project",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "file",
						Aliases:  []string{"f"},
						Usage:    "path to the .gob file to import",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "user",
						Aliases:  []string{"u"},
						Usage:    "username to import notes under (must already exist)",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					return runImportGob(c.String("dir"), c.String("file"), c.String("user"))
				},
			},
			{
				Name:  "export-md",
				Usage: "Export notes as Markdown files with YAML frontmatter (Obsidian-compatible)",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "out",
						Aliases:  []string{"o"},
						Usage:    "directory to write Markdown files into",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "user",
						Aliases:  []string{"u"},
						Usage:    "username whose notes to export",
						Required: true,
					},
					&cli.BoolFlag{
						Name:  "skip-private",
						Usage: "exclude private notes from the export (by default they are exported decrypted)",
					},
				},
				Action: func(c *cli.Context) error {
					return runExportMd(c.String("dir"), c.String("out"), c.String("user"), c.Bool("skip-private"))
				},
			},
			{
				Name:  "import-md",
				Usage: "Import Markdown files (with optional YAML frontmatter) as notes",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "in",
						Aliases:  []string{"i"},
						Usage:    "directory to import .md files from (searched recursively)",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "user",
						Aliases:  []string{"u"},
						Usage:    "username to import notes under (must already exist)",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					return runImportMd(c.String("dir"), c.String("in"), c.String("user"))
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		logger.LogErr(err)
		os.Exit(1)
	}
}

// runTui prepares the working directory and database, then hands the
// terminal to the Bubble Tea interface. The web server, JWT signing, and the
// sync client are all skipped — the TUI talks to the models layer directly,
// so none of the HTTP machinery is needed.
func runTui(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return serr.Wrap(err, "failed to create directory", "dir", dir)
	}
	if err := os.Chdir(dir); err != nil {
		return serr.Wrap(err, "failed to change to directory", "dir", dir)
	}

	// Quiet the logger before anything else: the TUI owns the terminal, and
	// stray Info lines from the models layer would be drawn over the UI.
	// Real errors still come through (and are also surfaced in the status bar).
	logger.SetLogLevel("error")

	// Pickup local configs (may contain the encryption key)
	if issues, err := fileops.EnvFromFile("config/cfg_files/.env"); err != nil {
		for _, issue := range issues {
			logger.Warn("Cfg file issue", serr.StringFromErr(issue))
		}
	}

	if err := models.InitDB(); err != nil {
		return serr.Wrap(err, "failed to initialize database")
	}
	defer models.CloseDB()

	// Encryption is optional: without a key, private notes simply aren't
	// encrypted at rest (matching the web server's behavior).
	if os.Getenv(models.EncryptionKeyEnvVar) != "" {
		if err := models.InitEncryption(); err != nil {
			return serr.Wrap(err, "failed to initialize encryption")
		}
	}

	return tui.Run()
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