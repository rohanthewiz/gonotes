---
name: gonotes
description: Work with the GoNotes app — capture notes (especially from the clipboard), query/export/import them, and navigate the codebase. Use whenever the task mentions gonotes, "save this as a note", "add a note from my clipboard", note capture, the notes DB, or the GoNotes web/TUI/sync layers.
---

# GoNotes

GoNotes is a self-hosted note app: a Go binary that serves a web UI (`gonotes`),
a terminal UI (`gonotes tui`), and Markdown/gob import-export commands. Data
lives in DuckDB at `<dir>/data/notes.ddb`, where `<dir>` defaults to `~/.gonotes`.

## The one rule that breaks everything

**DuckDB is single-writer.** The server (or `GoNotes.app`) holds an exclusive
lock on `data/notes.ddb`. Any CLI command that touches the DB — `tui`,
`import-md`, `export-md`, `import-gob` — will fail to open the database while
the server runs, and vice-versa.

So before doing anything, find out which mode is live:

```bash
curl -s -m 2 http://localhost:8444/api/v1/health   # {"success":true,...} => server is up
pgrep -fl gonotes                                   # what's actually running
```

- **Server up** → use the HTTP API. Do not run the CLI DB commands.
- **Server down** → use the CLI (`tui`, `import-md`, …). Never start a second
  writer alongside a running one.

Default port is `8444` (`web.WebPort`, override with `--port` / `$GONOTES_PORT`).

## Adding a note from the clipboard

This is the most common ask. Use the bundled script — it handles clipboard
reading, title extraction, JSON escaping, auth, and categories:

```bash
.claude/skills/gonotes/scripts/gn-clip.sh                       # title = first line of clipboard
.claude/skills/gonotes/scripts/gn-clip.sh -t "Deploy checklist" # explicit title
.claude/skills/gonotes/scripts/gn-clip.sh -t "Auth notes" -c "Work/backend" -g "auth,jwt" -p
```

| Flag | Meaning |
|---|---|
| `-t <title>` | Title. Omitted → first non-blank clipboard line (leading `#`s stripped) becomes the title and is removed from the body. |
| `-k` | Keep that first line in the body too (no removal). |
| `-c <cat[/sub]>` | Category, created if it doesn't exist. `Work/backend` = category `Work`, subcategory `backend`. Repeatable. |
| `-g <tags>` | Comma-separated tags. |
| `-p` | Mark private (encrypted at rest when `GONOTES_ENCRYPTION_KEY` is set). |
| `-f` | Set the follow-up flag. |
| `-u <user>` | Username for login (else `$GONOTES_USER`, else prompt). |
| `-U <url>` | Base URL (default `http://localhost:8444`, or `$GONOTES_URL`). |
| `-n` | Dry run — print the JSON payload, post nothing. |

Auth: the script caches a JWT at `~/.gonotes/.api_token` (mode 600) and reuses
it until it 401s, then logs in again. Supply the password non-interactively via
`$GONOTES_PASSWORD`, or `$GONOTES_SYNC_PASSWORD_B64` (base64) if the machine is
already configured as a sync spoke; otherwise it prompts.

Run `-n` first when the clipboard content is unclear — it shows exactly what
would be stored without writing.

### Doing it by hand (server up)

The whole flow is four calls; `jq -n --arg` is what keeps arbitrary clipboard
text from breaking the JSON:

```bash
TOKEN=$(curl -s -X POST http://localhost:8444/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"me","password":"…"}' | jq -r '.data.token')

pbpaste > /tmp/clip.txt
jq -n --arg guid "$(uuidgen)" --arg title "Pasted note" --rawfile body /tmp/clip.txt \
   '{guid:$guid, title:$title, body:$body, is_private:false, is_flagged:false}' \
| curl -s -X POST http://localhost:8444/api/v1/notes \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d @-
```

`guid` and `title` are the only required fields; a duplicate `guid` returns 409.

### Doing it with the server stopped

`import-md` treats a plain `.md` file as a new note: filename → title,
containing folder → category. It writes the generated `guid` back into the
file, so a re-import updates rather than duplicates.

```bash
mkdir -p /tmp/gnclip/Inbox                      # "Inbox" becomes the category
pbpaste > "/tmp/gnclip/Inbox/Pasted $(date +%F).md"
gonotes import-md -i /tmp/gnclip -u <username>
```

## Other common tasks

**Read notes back** (server up):

```bash
curl -s -H "Authorization: Bearer $TOKEN" 'http://localhost:8444/api/v1/notes?limit=20' | jq '.data[].title'
curl -s -H "Authorization: Bearer $TOKEN" 'http://localhost:8444/api/v1/notes/search?q=duckdb' | jq
```

**Markdown round-trip** (server stopped) — export is Obsidian-compatible and
idempotent in both directions, anchored on the frontmatter `guid`:

```bash
gonotes export-md -o ~/notes-vault -u <user> [--skip-private]
gonotes import-md -i ~/notes-vault -u <user>
```

Private notes export **decrypted** by default; `--skip-private` omits them.

**Terminal UI**: `gonotes tui` (or `-d <dir>`). Keys: `/` search, `n`/`e`
new/edit, `c` category filter, `f` flag, `d` delete, `ctrl+e` edit body in
`$EDITOR`, `ctrl+s` save.

**Build & test**: `go build -o gonotes .` (cgo — DuckDB needs a C compiler);
`go test ./...`. Tests that touch models open their own DB, so stop the server
before running the full suite.

## Codebase map

| Path | What lives there |
|---|---|
| `main.go` | urfave/cli wiring: default action serves; subcommands `tui`, `import-gob`, `export-md`, `import-md`. |
| `models/` | Data layer + business logic: `note.go`, `category.go`, `user.go`, `db.go` (DuckDB + cache), `encryption.go`, `sync_*.go`. |
| `web/routes.go` | Every route in one file — read it first when looking for an endpoint. |
| `web/api/` | JSON handlers. All responses use the `APIResponse` envelope: `{success, data?, error?}`. |
| `web/pages/` | Server-rendered HTML built with `rohanthewiz/element`. |
| `tui/` | Bubble Tea screens: `browse`, `detail`, `form`, `categories`, `login`. |
| `md_*.go`, `import_gob.go` | Markdown frontmatter format, import/export, legacy gob import. |
| `ai_docs/claude_sessions/` | Session logs — check for prior context via `/sess-list`, `/sess-load`. |

Conventions in this repo: errors are wrapped with `serr` and logged once at the
top with `logger.LogErr`; handlers authenticate via
`api.GetCurrentUserGUID(ctx)` and return 401 themselves rather than relying on
route-level middleware; every query is user-scoped by GUID.

## Note model

`models.NoteInput` (the API create/update payload):

`guid` (required), `title` (required), `description`, `body`, `tags`
(comma-separated string, not an array), `is_private`, `is_flagged`.

Categories are attached separately, after the note exists:

```bash
POST /api/v1/categories                              {"name":"Work"}
POST /api/v1/notes/<note_id>/categories/<cat_id>     {"subcategories":["backend"]}
```

Markdown frontmatter uses a different shape — `guid`, `title`, `description`,
`tags` (list), `categories` (list of `Name` or `Name/Subcategory`), `private`,
`flagged`, `created`, `updated`. See `md_format.go`.

## Sync (hub-spoke)

Spokes sync to a hub on an interval, configured entirely by env vars
(`GONOTES_SYNC_ENABLED`, `_HUB_URL`, `_USERNAME`, `_PASSWORD_B64`,
`_INVITE_TOKEN`, `_INTERVAL`) read from the environment or
`<dir>/config/cfg_files/.env`. Conflict resolution is delete-wins, then
last-writer-wins on `authored_at`; conflicts land in the `sync_conflicts`
table. Runtime control: `GET /api/v1/sync/control/status`,
`POST /api/v1/sync/control/toggle`, `POST /api/v1/sync/control/sync-now`.
Full setup walkthrough is in `README.md`.
