# CLI Flags for Directory and Port

**Date:** 2026-03-25
**Session ID:** (not available in context)

## Summary

Added CLI flags `--dir` and `--port` to GoNotes using `urfave/cli/v2`, enabling multiple instances on the same machine for sync testing.

## Motivation

To test peer-to-peer sync between two GoNotes instances on one machine, each instance needs its own database directory and web port. Previously both were hardcoded (`~/.gonotes/` and `8444`).

## Changes Made

### 1. Added `urfave/cli/v2` dependency
- `go get github.com/urfave/cli/v2`

### 2. `main.go` — Restructured with urfave/cli
- Created a `cli.App` with name "gonotes" and two global flags:
  - `--dir` / `-d` (default: `~/.gonotes`) — working directory for data and config
  - `--port` / `-p` (default: `8444`) — web server port
- Extracted startup logic into `serve(dir, port string) error` function
- Replaced hardcoded `filepath.Join(home, ".gonotes")` with the `--dir` flag value
- Passes `--port` flag value to `web.NewServer(port)`
- Fixed stale log message that said "port 8080"

### 3. `web/server.go` — Parameterized port
- Changed `NewServer()` to `NewServer(port string)`
- Exported constant `WebPort = "8444"` (was `webPort`)
- Removed redundant log from `Run()` (caller logs the port)
- Removed unused `logger` import

### 4. `web/api/notes_test.go` — Updated caller
- Changed `web.NewServer()` to `web.NewServer(web.WebPort)`

## Usage

```bash
# Default (same as before)
gonotes

# Two instances for sync testing
gonotes --port 8444                              # Terminal 1
gonotes --dir ~/.gonotes-test --port 8555        # Terminal 2
```

## Help output

```
NAME:
   gonotes - A self-hosted note-taking application

USAGE:
   gonotes [global options] command [command options]

GLOBAL OPTIONS:
   --dir value, -d value   working directory for data and config (default: "~/.gonotes")
   --port value, -p value  web server port (default: "8444")
   --help, -h              show help
```

## Files Modified
- `main.go`
- `web/server.go`
- `web/api/notes_test.go`
- `go.mod` / `go.sum` (new dependency)
