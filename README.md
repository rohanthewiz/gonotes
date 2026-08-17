# GoNotes Web

A modern, web-based note-taking platform built with Go, featuring server-side rendering and embedded assets.

## Project Status
This is **alpha** not ready for use!

---

## Install as a Native macOS App

`mac-install.sh` builds GoNotes and installs it as a native macOS app
(`~/Applications/GoNotes.app`) — a small Swift/WebKit window that runs the
bundled server and shows the UI in its own window instead of a browser.

### Quick install

```bash
curl -fsSL https://raw.githubusercontent.com/rohanthewiz/gonotes/master/mac-install.sh | bash
```

Or from a checkout of this repo:

```bash
./mac-install.sh
```

Re-run any time to update to the latest `master`.

### What it does

- Ensures Go ≥ 1.26.1 is available (uses system Go, or auto-installs a private copy under `~/.local/go`). The main binary is now pure Go (bytdb-backed) and needs no C compiler; only the optional DuckDB→bytdb migration tool under `scripts/migrate` is cgo.
- Clones/updates the source into `~/.gonotes-src` and builds the `gonotes` binary.
- Generates an app icon and assembles `GoNotes.app`, wiring up Cmd+Q and the standard edit shortcuts.

On first launch, register an account in the app window — the first user you register becomes **admin**. The app stores its data in `~/.gonotes/data` (the same location the `gonotes` CLI uses by default), so the app and command line share one database. Logs go to `~/Library/Logs/GoNotes/gonotes.log`.

### Requirements

- macOS 11+ (Apple Silicon or Intel)
- Xcode Command Line Tools (`xcode-select --install`) — provides `git`, `swiftc`, and the C compiler

### Configuration

Override defaults with environment variables, e.g. `GN_PORT=9000 ./mac-install.sh`:

| Variable | Default | Description |
|----------|---------|-------------|
| `GN_BRANCH` | `master` | Branch to install |
| `GN_DIR` | `~/.gonotes-src` | Source checkout directory |
| `GN_GO_VERSION` | `1.24.4` | Go version to fetch if no suitable one is found |
| `GN_APP_DIR` | `~/Applications` | Where `GoNotes.app` is installed |
| `GN_APP_NAME` | `GoNotes` | App (and window) name |
| `GN_PORT` | `8444` | Port the app's server listens on |

> **Note:** the databases are single-writer. Don't run the app and a terminal `gonotes` at the same time — they share `~/.gonotes/data/notes_public.bytdb` and `notes_private.bytdb`, and the second to start won't be able to open them.

## Storage

GoNotes stores data in two [bytdb](https://github.com/rohanthewiz/bytdb) databases under `~/.gonotes/data`, split by privacy:

- `notes_public.bytdb` — non-private notes and their category links, plus the shared/system tables (the categories catalog, users, invite tokens, sync state, sync conflicts). Plaintext on disk.
- `notes_private.bytdb` — private notes and their category links and sync change-tracking. When `GONOTES_ENCRYPTION_KEY` (32 bytes) is set, this file is AES-256-GCM encrypted at rest; rows stay plaintext in RAM so queries pay no crypto cost. Reopening it later requires the same key.

bytdb keeps all rows in memory (the on-disk write-ahead log is only for durability), so there is no separate read cache. Reads that span a user's whole note set — listing, search, sync — fan out to both databases on separate goroutines and merge (divide and conquer). Note ids are globally unique across the two databases (the private one offsets its id sequence), so a note stays addressable by id no matter which database holds it, and toggling a note's privacy moves it between the two files while preserving its id.

Migrating from a pre-existing DuckDB database? See [scripts/migrate](scripts/migrate) — a standalone tool that reads the legacy `data/notes.ddb`, decrypts any legacy per-note ciphertext, and writes the two bytdb files.

---

## Architecture: Hub-Spoke Sync

GoNotes supports syncing notes and categories between machines using a **hub-spoke model**. The hub is multi-user (each user's data is fully isolated), while spokes are single-user instances that sync with the hub in the background.

### How It Works

- The spoke runs a background goroutine that periodically authenticates with the hub, pulls new changes, resolves conflicts, pushes local changes, and verifies consistency via checksums.
- Conflict resolution is automatic: **delete-wins** (deletes take priority), then **last-writer-wins** on `authored_at` timestamp. All conflicts are logged to a `sync_conflicts` table for auditing.
- Changes are tracked at the field level using bitmask-driven delta fragments, with body diffs for efficient storage of large note edits.
- All sync data is **user-scoped** on the hub — each spoke only sees its own user's notes and categories.

### Hub Setup

1. Set the JWT secret (minimum 32 characters) and start the server:
   ```bash
   export GONOTES_JWT_SECRET="your-secret-at-least-32-chars-long"
   ./gonotes
   ```

2. **Register the first user** — this user automatically becomes **admin**:
   ```bash
   curl -X POST http://localhost:8080/api/v1/auth/register \
     -H "Content-Type: application/json" \
     -d '{"username": "admin", "password": "MySecurePass123!"}'
   ```

3. Verify the hub is reachable:
   ```bash
   curl http://<hub-ip>:8080/api/v1/health
   # {"success":true,"data":{"status":"ok"}}
   ```

### Adding Spoke Users

New users can only register with an **invite token** created by the admin. There are two ways to set up a spoke:

#### Option A: Config Export/Import (Recommended)

1. On the hub, log in as admin and open **Settings** from the user menu.
2. Enter your password and click **Export Spoke Config** — this downloads a JSON file containing the hub URL, credentials (base64-encoded), JWT secret, and a fresh invite token.
3. On the new spoke machine, start GoNotes and visit `/setup` in the browser.
4. Upload the exported JSON file, review the preview, and click **Apply Configuration**.
5. Restart the spoke — sync will activate automatically.

#### Option B: Manual Configuration

1. On the hub, create an invite token (as admin):
   ```bash
   curl -X POST http://<hub-ip>:8080/api/v1/admin/invites \
     -H "Authorization: Bearer <admin-jwt>" \
     -H "Content-Type: application/json"
   ```

2. On the spoke, set environment variables (or create `config/cfg_files/.env`):
   ```bash
   export GONOTES_JWT_SECRET="your-spoke-jwt-secret-32-chars"
   export GONOTES_SYNC_ENABLED=true
   export GONOTES_SYNC_HUB_URL=http://<hub-ip>:8080
   export GONOTES_SYNC_USERNAME=myuser
   export GONOTES_SYNC_PASSWORD_B64=$(echo -n 'MySecurePass123!' | base64)
   export GONOTES_SYNC_INVITE_TOKEN=<token-from-admin>
   export GONOTES_SYNC_INTERVAL=5m
   ```

3. Start the spoke:
   ```bash
   ./gonotes
   ```
   On first run, it will auto-register on the hub using the invite token, then begin syncing. The invite token is consumed on first use and can be removed afterward.

4. Confirm sync is running — look for these log lines:
   ```
   Sync client initialized and running
   Sync cycle completed successfully
   ```

### Sync Control API

The spoke exposes three endpoints for UI integration (all require authentication):

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET`  | `/api/v1/sync/control/status`   | Returns sync state (enabled, connected, last sync time, errors) |
| `POST` | `/api/v1/sync/control/toggle`   | Enable/disable sync at runtime. Body: `{"enabled": true}` |
| `POST` | `/api/v1/sync/control/sync-now`  | Trigger an immediate sync cycle. Returns 409 if already in progress |

### Environment Variables Reference

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `GONOTES_JWT_SECRET` | Yes | — | JWT signing secret (min 32 chars) |
| `GONOTES_SYNC_ENABLED` | No | `false` | Enable the sync client on this instance |
| `GONOTES_SYNC_HUB_URL` | When sync enabled | — | Base URL of the hub instance |
| `GONOTES_SYNC_USERNAME` | When sync enabled | — | Username for hub authentication |
| `GONOTES_SYNC_PASSWORD_B64` | When sync enabled | — | Base64-encoded password (`echo -n 'pass' \| base64`) |
| `GONOTES_SYNC_PASSWORD` | — | — | Legacy plaintext password (fallback if `_B64` not set) |
| `GONOTES_SYNC_INTERVAL` | No | `5m` | Polling interval between sync cycles (minimum 10s) |
| `GONOTES_SYNC_INVITE_TOKEN` | No | — | One-time invite token for auto-registration on the hub |

---

## Terminal UI

Browse and edit notes right in the terminal — no web server needed:

```bash
./gonotes tui                 # uses ~/.gonotes (same data as the web app)
./gonotes tui -d /path/to/dir # or point at another working directory
```

On first run (empty database) the TUI walks you through creating an account; afterwards it signs you in with just your password (the username is prefilled when there's a single user).

### Categories and subcategories

A note can sit in several categories, and each category can carry subcategories — `Work` with `backend`, `api`, `ops`. The TUI writes both from one field and reads both back in the same notation, so there is no separate setup step and nothing that only the web UI can do.

**Entering them.** On the note form (`n` for a new note, `e` to edit one), the Categories field is comma-separated and a `/` adds subcategories:

```
Title       Rate limiter design
Description
Tags        infra,api
Categories  Work/backend, Personal
Private     [ ]
```

That files the note under Work's `backend` subcategory and under Personal plainly. `Work/backend/api` selects two subcategories of Work at once; `Work/backend, Work/api` means exactly the same thing (the second form is what a Markdown export writes). `ctrl+s` saves — and anything that does not exist yet is created right there, categories and subcategories alike. It is the same notation as the Markdown frontmatter and `gn-clip.sh -c "Work/backend"`.

**Seeing them.** The note view (`enter` on a row) names the filing in its header:

```
On-call runbook
updated 2026-08-17 10:07  •  #infra,api  •  in Reading, Work/ops
```

The form prefills the field with precisely what is stored, so opening a note and saving it unchanged leaves its filing alone.

**Browsing by them.** `c` from the notes list opens the categories screen, where every row shows what that category offers:

```
│ Work
│ ▸ backend, api, ops

  Reading
  created 2026-08-17
```

- `enter` filters the note list to the whole category.
- `s` drills into that category's subcategories. `space` toggles rows into the filter and `enter` applies it; the heading shows what is about to be applied (`filter: Work/backend/api`). Toggling more than one narrows to the notes carrying **all** of them — the same rule as the web UI's chips. With nothing toggled, `enter` filters by the highlighted row alone.

```
notes list ──"c"──► categories ──"s"──► subcategories
     ▲                   │                     │
     └───── enter ◄───────┴────── enter ◄───────┘
        (filter applied; esc peels it back off)
```

The active filter appears in the list title — `GoNotes — Work/backend` — and `esc` backs out one layer at a time: the search first, then the subcategory, then the category. Once there is nothing left to back out of, `esc` quits, the same as `q`. A note form with unsaved edits is never one of those layers — `esc` there stops and offers to save first.

**Editing the list a category offers.** On the subcategories screen, `n` adds a name and `d` removes one. That list is a palette rather than an assignment: removing a name does not refile anything, so notes already filed under it keep it until they are next edited.

### Keys

| Screen | Key | Action |
|---|---|---|
| Notes list | `/` | Fuzzy search across title, tags, description, and body |
| | `enter` | View note (markdown rendered) |
| | `n` / `e` | New / edit note |
| | `f` | Toggle the follow-up flag |
| | `d` | Delete (with confirmation) |
| | `c` | Category picker — filter the list by category |
| | `esc` | Clear search, then subcategory filter, then category filter — then quit |
| | `q` | Quit |
| Note view | `↑/↓` | Scroll |
| | `e` / `f` / `d` | Edit / flag / delete |
| Note form | `tab` | Next field |
| | `ctrl+e` | Edit the body in `$VISUAL`/`$EDITOR` |
| | `ctrl+s` | Save |
| | `esc` | Cancel — with unsaved changes, asks first: `s` save & exit, `d` discard, `esc` keep editing |
| Categories | `enter` | Filter notes by the selected category |
| | `s` | Open that category's subcategories |
| | `n` / `d` | New / delete category |
| Subcategories | `enter` | Filter notes by the selected subcategories |
| | `space` | Toggle a subcategory into the filter (several = notes carrying all of them) |
| | `n` / `d` | Add / remove a subcategory of this category |

Notes:
- Category and subcategory entry is covered above, under [Categories and subcategories](#categories-and-subcategories).
- The TUI shares the databases with the web server. Avoid running both against the same directory at the same time (bytdb allows a single writer process per database).
- With `GONOTES_ENCRYPTION_KEY` set (env or `config/cfg_files/.env`), private notes are encrypted at rest, same as the web app.

---

## Markdown Export / Import (Obsidian-compatible)

GoNotes can export your notes to a folder of Markdown files with YAML frontmatter — openable directly as an Obsidian vault — and import Markdown files (edited exports or plain `.md` files) back in.

### Export

```bash
./gonotes export-md --out ~/notes-vault --user <username>
```

| Flag | Aliases | Required | Description |
|------|---------|----------|-------------|
| `--out` | `-o` | Yes | Directory to write Markdown files into |
| `--user` | `-u` | Yes | Username whose notes to export |
| `--skip-private` | | No | Exclude private notes from the export |

- One file per note: `<Category>/<Title>.md` (uncategorized notes go in the vault root). All categories/subcategories, tags, and timestamps are carried in the frontmatter.
- **Private notes are exported decrypted by default** — exporting is the deliberate act that takes them out of encrypted storage. They keep `private: true` in frontmatter so a re-import restores privacy (and encryption). Use `--skip-private` to leave them out entirely.

Exported file format:

```markdown
---
guid: 0197f3c8-…            # stable identity — do not edit
title: 'Groceries: weekly'
tags:
    - errand
    - home
categories:
    - Household/weekly       # Name or Name/Subcategory
created: "2026-07-08T13:11:33Z"
updated: "2026-07-08T13:11:33Z"
---

Note body, with [[Wiki Links]] intact.
```

### Import

```bash
./gonotes import-md --in ~/notes-vault --user <username>
```

| Flag | Aliases | Required | Description |
|------|---------|----------|-------------|
| `--in` | `-i` | Yes | Directory to import from (`.md` files, searched recursively; hidden dirs like `.obsidian` are skipped) |
| `--user` | `-u` | Yes | Username to import notes under (must already exist) |

The `guid` frontmatter field anchors identity, so the whole flow is **idempotent** — export, edit in Obsidian, import, repeat:

- **guid matches an existing note** → updated, but only if the content actually differs (unchanged files are skipped). Updates go through the normal write path, so private notes are re-encrypted and hub-spoke sync change tracking keeps working.
- **guid present but unknown** → created, preserving the frontmatter `created`/`updated` timestamps.
- **no frontmatter at all** (a plain Markdown file) → created; the title comes from the filename and the containing folder becomes its category. The generated guid is **written back into the file** so a second import won't duplicate it.

Notes:

- Frontmatter categories are authoritative; missing categories are created on the fly. Categories are only ever added — removing one from a file does not unlink it in GoNotes.
- Filenames/folders are just presentation; renaming or moving a file changes nothing as long as the `guid` line is intact.

### Important: stop the server first

As with `import-gob`, the databases are single-writer, so stop the running server before invoking `export-md` or `import-md`.

---

## Importing Notes from Legacy `go_notes`

GoNotes can bulk-import notes from a `.gob` file produced by the legacy [`go_notes`](https://github.com/) project (single `[]note.Note` value encoded with `encoding/gob`).

### Usage

```bash
./gonotes import-gob --file /path/to/notes.gob --user <username>
```

Flags:

| Flag     | Aliases | Required | Description                                                                 |
|----------|---------|----------|-----------------------------------------------------------------------------|
| `--file` | `-f`    | Yes      | Path to the `.gob` file to import                                           |
| `--user` | `-u`    | Yes      | Username to import notes under (must already exist)                         |
| `--dir`  | `-d`    | No       | Working directory (inherited from the top-level flag, default `~/.gonotes`) |

The command prints a one-line summary on completion:

```
import-gob: 42 imported, 0 skipped (duplicate GUID), 0 errored (of 42 total)
```

### Behavior

- **Timestamps are preserved.** Each imported note keeps its original `CreatedAt` and `UpdatedAt`; `AuthoredAt` is set to the source `UpdatedAt`.
- **Idempotent.** Notes are matched by source `Guid`; running the same import twice will skip everything on the second run.
- **Ownership.** All imported notes get `created_by` / `updated_by` set to the GUID of the user passed via `--user`.
- **Privacy.** All imported notes are stored as `IsPrivate=false` (no encryption). The legacy `Public` flag has different semantics (cross-user sharing, not encryption) and is intentionally discarded. Toggle individual notes to private after import if encryption is desired.
- **Tags pass through.** The legacy singular `Tag` field maps to the destination `Tags` field as-is (comma-separated string).

### Important: stop the server first

The bytdb databases are single-writer, so the running server must be stopped before invoking `import-gob`. After the import completes, restart the server normally.

### Field mapping

| Source (`go_notes/note.Note`)                 | Destination                   | Notes                                                                     |
|-----------------------------------------------|-------------------------------|---------------------------------------------------------------------------|
| `Guid`                                        | `GUID`                        | Preserved verbatim — drives duplicate detection                           |
| `Title`                                       | `Title`                       | Direct                                                                    |
| `Description`, `Body`, `Tag`                  | `Description`, `Body`, `Tags` | Empty source strings → `NULL`                                             |
| `CreatedAt`                                   | `CreatedAt`                   | Preserved                                                                 |
| `UpdatedAt`                                   | `UpdatedAt`, `AuthoredAt`     | Both populated from source `UpdatedAt`                                    |
| `Id`, `User`, `Creator`, `SharedBy`, `Public` | —                             | Discarded; destination assigns its own ID; ownership comes from `--user`  |

---

Built with Go, RWeb, Element, and Claude Opus
