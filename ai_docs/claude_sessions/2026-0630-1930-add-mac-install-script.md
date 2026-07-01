# Add macOS install script to GoNotes

Date: 2026-0630-1930 · Session: 756e807b-b655-44df-8c7f-bbe1d22bd22d

## Goal

Port KRo's `mac-install.sh` to GoNotes: a self-contained installer that builds
the `gonotes` binary and wraps it in a native macOS app (`GoNotes.app`) using a
Swift/WebKit shell, so the server runs in its own window instead of a browser.
Along the way, make the server read its port from an env var, then run and
verify the installer end to end.

## What was done

### 1. New `mac-install.sh` (adapted from KRo)

Self-contained bash installer that:

- Detects platform/arch; requires `git`, `swiftc`, and a C compiler (GoNotes
  links DuckDB via cgo, so `CGO_ENABLED=1` and clang from the Xcode Command
  Line Tools are needed).
- Resolves Go (system Go if `>=` the target, else a cached/auto-installed
  private copy under `~/.local/go`). Target version `1.24.4` to match `go.mod`.
- Syncs the source repo and `git reset --hard` to `origin/$GN_BRANCH`.
- Builds `gonotes` with `-trimpath -ldflags "-s -w"` (no `BuildNumber` var in
  GoNotes, unlike KRo).
- Generates an app icon via a tiny embedded AppKit Swift program: a white note
  page (folded corner + text lines) on a blue→violet squircle gradient.
- Builds `GoNotes.app` with an embedded Swift/WebKit `AppDelegate` that starts
  the bundled server, polls `GET /api/v1/health` until ready, and loads the UI.
  Carries over KRo's recent fixes: self-clearing `RETURN` traps, a main menu so
  Cmd+Q and edit shortcuts bind, and a runtime app-icon set for Cmd+Tab/Dock.

Env overrides: `GN_REPO_URL`, `GN_BRANCH`, `GN_DIR`, `GN_GO_VERSION`,
`GN_GO_DIR`, `GN_APP_DIR`, `GN_APP_NAME`, `GN_PORT`.

Key differences from KRo discovered while adapting:

- Health endpoint is `/api/v1/health` (not `/health`).
- Web assets are embedded (`//go:embed all:static`), so no runtime web dir.
- Default branch is **`master`**, not `main` — initial version hardcoded `main`
  and would have failed the clone; fixed with a `GN_BRANCH` var (default
  `master`).

### 2. Server reads a port from the environment

The user asked to make the server read a `PORT` env var. Between sessions,
upstream `master` had independently reworked `main.go` into a `urfave/cli` app
with a `--port` flag (default `web.WebPort = "8444"`) and a `--dir` flag that
`os.Chdir`s into a working directory. Reconciled during a rebase:

- Took upstream's `web/server.go` (`NewServer(port string)`) wholesale — my
  earlier `listenAddress()` helper became redundant (zero delta).
- Fulfilled the request the idiomatic cli way: added
  `EnvVars: []string{"GONOTES_PORT", "PORT"}` to the `--port` flag in `main.go`.
  Precedence verified at runtime: `--port` flag › env var › default.

### 3. Ran and verified the installer

- First run failed: the installer's checkout dir (`~/.gonotes`) collided with
  GoNotes' own default data directory — a prior CLI run had left
  `~/.gonotes/data` in place, and the installer refused to overwrite the
  non-git dir. **Fix:** moved the source checkout to `~/.gonotes-src`.
- Re-ran: built with system Go 1.26.1, installed and launched the app.
- Clean-start verification: with a free port, the app spawned its own bundled
  server (`--dir ... --port 8444`), health returned 200, and a fresh data dir
  was created — confirming the `--dir` path works.

### 4. Login/database fix

The app was pointed at a **separate, empty** data dir
(`~/Library/Application Support/GoNotes`), so it came up with no user accounts
and the user's login failed ("is my password no longer valid?"). The real
account/notes live in `~/.gonotes/data/notes.ddb` (~21 MB), the CLI default.

**Fix:** changed the app wrapper's `--dir` to `~/.gonotes` so the app and CLI
share one database. Re-ran the installer; verified the child server runs with
`--dir ~/.gonotes` and serves the real DB (health 200, UI assets loading).

## Files changed

- `mac-install.sh` — new installer (embeds the Swift app + icon generator).
- `main.go` — added `EnvVars` to the `--port` flag (upstream cli rework kept).
- `web/server.go` — no net change (upstream version kept).

## Commits pushed to `origin/master`

- `9313f48` Add macOS installer; read server port from PORT env
- `328ca9e` Move installer checkout to ~/.gonotes-src to avoid data-dir collision
- `95aa3bf` Point the app at ~/.gonotes so it shares the CLI's database

(The redundant doc-archive-rename commit made early on was auto-dropped during
rebase — the same change was already upstream as `916cb02`.)

## Gotchas / notes for next time

- **Single-writer DB:** the app and a terminal `gonotes` now share
  `~/.gonotes/data/notes.ddb`. DuckDB is single-writer — don't run both at once;
  the second to start can't open the DB. (Had to stop a stale terminal instance
  during this session for the same reason.)
- The app wrapper reuses an already-running server if the health check passes
  *before* it spawns its own — so a stray instance on the port will be adopted
  silently (no app-managed data dir/log created). Watch for this when verifying.
- App icon generation needs a window-server connection (offscreen AppKit draw);
  it can fail on a headless/SSH install and is treated as non-fatal.
- If macOS-standard data location is ever preferred, switch the wrapper `--dir`
  back to `~/Library/Application Support/GoNotes` and migrate the DB.
