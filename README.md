# GoNotes Web

A modern, web-based note-taking platform built with Go, featuring server-side rendering and embedded assets.

## Project Status
This is **alpha** not ready for use!

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

DuckDB holds an exclusive lock on `./data/notes.ddb`, so the running server must be stopped before invoking `import-gob`. After the import completes, restart the server normally.

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
