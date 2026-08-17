package main

import (
	"context"
	"fmt"
	"gonotes/models"
	"gonotes/tui"
	"gonotes/web"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// tuiProbeTimeout bounds the health check that decides local vs HTTP mode.
// It sits on the critical path of every `gonotes tui` launch, so it has to be
// short — but not so short that a busy loopback server (or a laptop waking
// from sleep) is misread as absent, which would send the TUI into
// models.InitDB and straight into a lock conflict. Two seconds is the
// compromise; a live local server answers in single-digit milliseconds.
const tuiProbeTimeout = 2 * time.Second

// runTui prepares the working directory, decides how the TUI will reach its
// data, then hands the terminal to the Bubble Tea interface.
//
// The decision is the part worth reading. bytdb is single-process: if a GoNotes
// server (or the MacApp, which embeds one) is already running against this data
// directory, opening the databases here would fail on the lock. So the launch
// probes for a live server first — but "a server answered" is NOT the question,
// and treating it as the answer is how this went wrong.
//
// WHAT THE PROBE HAS TO ESTABLISH. Deferring to a server is only correct when
// that server holds the very files this launch would otherwise open. A server on
// port 8444 serving a different data directory is not a reason to abandon the
// one the user named with -d; it is an unrelated process that happens to be
// listening. Before /api/v1/health reported its data_dir there was no way to
// tell the two apart, so -d was silently overridden by whatever was running —
// which points the TUI at a different set of notes, with writes enabled and no
// sign on screen that anything unusual happened.
//
// SO THE RULE DEPENDS ON WHO CHOSE THE URL:
//
//	GONOTES_URL set        the user named a server, possibly on another machine
//	                       where a data directory path means nothing. Honored as
//	                       given; no identity check. Not answering is worth a
//	                       word, because they expected it to be there.
//	GONOTES_URL unset      the default URL is a GUESS that a local server holds
//	                       these files. Guesses get checked: HTTP mode only when
//	                       the server reports the same resolved data directory,
//	                       and local mode otherwise.
//	server too old to say  cannot be checked, so it keeps the old behavior (HTTP)
//	                       and says so, rather than silently changing which
//	                       notes an existing setup shows.
//
// Every outcome is labelled — see tui.Mode — so that whichever way it goes, the
// screen says which notes these are.
//
// The web server, JWT signing, and the sync client are skipped either way; in
// HTTP mode the remote server owns all of that.
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

	// Pickup local configs (may contain the encryption key, and may set
	// GONOTES_URL — so this has to happen before the probe reads it).
	if issues, err := fileops.EnvFromFile("config/cfg_files/.env"); err != nil {
		for _, issue := range issues {
			logger.Warn("Cfg file issue", serr.StringFromErr(issue))
		}
	}

	// The data directory is resolved AFTER the Chdir above, which is what makes
	// it comparable with what a server reports: both sides answer "where does
	// ./data land from where I am standing".
	dataDir := models.ResolvedDataDir()
	serverURL := tui.ServerURL()
	info, up := tui.ProbeServer(serverURL, tuiProbeTimeout)

	// HTTP mode. Note the early return: no InitDB, no CloseDB, no encryption
	// setup. The server holds the key and bytdb encrypts whole databases at
	// rest, so it decrypts private bodies on read — which is why HTTP mode
	// shows the same note text local mode does rather than ciphertext.
	useHTTP, mode := decideStore(up, info, serverURL, dir, dataDir)
	if useHTTP {
		return tui.Run(tui.NewHTTPStore(serverURL), mode)
	}

	if err := models.InitDB(); err != nil {
		// Insurance for the case the identity check is supposed to make
		// impossible. Choosing local mode over a live server is a bet that the
		// server locked a DIFFERENT directory, so these files are free; if the
		// bet is wrong the files will not open, and refusing to start would be a
		// worse answer than the server we just declined. Reachable only when
		// something answered the probe, so it never turns a genuinely broken
		// database into a silent redirect on a machine with no server at all.
		if up {
			return tui.Run(tui.NewHTTPStore(serverURL), tui.Mode{
				Badge:  badgeForURL(serverURL),
				Notice: "Local notes would not open — using the server at " + serverURL,
			})
		}
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

	// The mode carries whatever the decision had to say — including, when the
	// probe found a server serving someone else's directory, the fact that it
	// was deliberately passed over.
	return tui.Run(tui.NewLocalStore(), mode)
}

// decideStore applies the rule in runTui's comment: which store to use, and how
// to label it on screen.
//
// Split out from runTui so the rule can be tested without a terminal, a data
// directory or a real server — the mistake this guards against is a decision
// mistake, not a plumbing one, and a decision is testable when it is a function
// of its inputs.
//
// BADGE AND NOTICE ARE DECIDED TOGETHER because they are not interchangeable.
// The notice is one status line at startup and it does not survive contact with
// a cats pane: the capture hint claims that line within a second of launch. So
// anything the user must still know a minute later belongs in the badge, which
// sits beside the list title for the life of the process. Every unusual outcome
// below therefore sets BOTH — the notice to explain it once, the badge to keep
// saying which notes these are.
func decideStore(up bool, info tui.ServerInfo, serverURL, dir, dataDir string) (useHTTP bool, mode tui.Mode) {
	explicit := tui.ServerURLIsExplicit()
	server := badgeForURL(serverURL)

	if !up {
		if explicit {
			// They pointed GONOTES_URL somewhere and it is not answering. Local
			// mode is still the right fallback — the notes are right here — but
			// silence would let someone edit local notes for an hour believing
			// they were writing to the server.
			return false, tui.Mode{
				Badge:  localBadge(dir, true),
				Notice: "No server at " + serverURL + " — using local notes",
			}
		}
		// The ordinary local launch: nothing happened worth reporting.
		return false, tui.Mode{Badge: localBadge(dir, false)}
	}

	if explicit {
		return true, tui.Mode{Badge: server} // named on purpose, honored as named
	}

	switch info.DataDir {
	case "":
		// Too old to identify itself. Keep the long-standing behavior so an
		// existing setup does not change under its user, and say why it could
		// not be checked.
		return true, tui.Mode{
			Badge:  server,
			Notice: "Connected to " + serverURL + " (it did not report its data directory)",
		}

	case dataDir:
		return true, tui.Mode{Badge: server}

	default:
		return false, tui.Mode{
			Badge: localBadge(dir, true),
			Notice: "Ignored the server at " + serverURL +
				" — it serves " + info.DataDir + ", not " + dataDir,
		}
	}
}

// badgeForURL is the persistent label for HTTP mode: the server, minus the
// scheme, which is noise once it is the only thing on the line.
func badgeForURL(serverURL string) string {
	return strings.TrimPrefix(strings.TrimPrefix(serverURL, "https://"), "http://")
}

// localBadge labels a local launch.
//
// A directory other than the default always earns a badge — that is the "which
// notes am I looking at" question outright. The default directory earns one only
// when the launch was unusual (a server was expected and missing, or found and
// passed over), because there the useful fact is the negative one: you are on
// local notes, NOT on the server you might reasonably assume.
//
// An ordinary launch gets nothing. A permanent label that is always there
// teaches the eye to stop seeing it, which is the wrong training for a sign
// whose whole job is to catch attention on the rare launch that is not ordinary.
func localBadge(dir string, unusual bool) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return dir
	}
	if !sameDir(dir, filepath.Join(home, ".gonotes")) {
		return "local · " + dir
	}
	if unusual {
		return "local"
	}
	return ""
}

// sameDir compares two directory paths after resolving them the way
// models.ResolvedDataDir does, so "~/.gonotes", "/Users/me/.gonotes" and a
// symlinked spelling of either are one directory rather than three.
func sameDir(a, b string) bool {
	resolve := func(p string) string {
		abs, err := filepath.Abs(p)
		if err != nil {
			return p
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			return real
		}
		return abs
	}
	return resolve(a) == resolve(b)
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