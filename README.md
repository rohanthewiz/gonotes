# GoNotes Web

A modern, web-based note-taking platform built with Go, featuring server-side rendering and embedded assets.

## Project Status
This is **pre-alpha** not ready for use!

---

## Peer-to-Peer Sync

GoNotes supports syncing notes and categories between machines using a hub-spoke model. One instance acts as the **hub** (source of truth) and one or more **spoke** instances sync with it in the background.

### How It Works

- The spoke runs a background goroutine that periodically authenticates with the hub, pulls new changes, resolves conflicts, pushes local changes, and verifies consistency via checksums.
- Conflict resolution is automatic: **delete-wins** (deletes take priority), then **last-writer-wins** on `authored_at` timestamp. All conflicts are logged to a `sync_conflicts` table for auditing.
- Changes are tracked at the field level using bitmask-driven delta fragments, with body diffs for efficient storage of large note edits.

### Hub Setup

1. Deploy and start the GoNotes binary:
   ```bash
   ./gonotes
   ```

2. (Recommended) Restrict registration with a shared secret:
   ```bash
   export GONOTES_REGISTRATION_SECRET=your-secret-here
   ./gonotes
   ```

3. Register a user account:
   ```bash
   curl -X POST http://localhost:8080/api/v1/auth/register \
     -H "Content-Type: application/json" \
     -d '{
       "username": "myuser",
       "password": "MySecurePass123!",
       "registration_secret": "your-secret-here"
     }'
   ```
   Omit `registration_secret` if `GONOTES_REGISTRATION_SECRET` is not set.

4. Verify the hub is reachable:
   ```bash
   curl http://<hub-ip>:8080/api/v1/health
   # {"success":true,"data":{"status":"ok"}}
   ```

### Spoke Setup

1. Set environment variables:
   ```bash
   export GONOTES_SYNC_ENABLED=true
   export GONOTES_SYNC_HUB_URL=http://<hub-ip>:8080
   export GONOTES_SYNC_USERNAME=myuser
   export GONOTES_SYNC_PASSWORD=MySecurePass123!
   export GONOTES_SYNC_INTERVAL=5m  # optional, defaults to 5m (minimum 10s)
   ```

2. Start the server:
   ```bash
   ./gonotes
   ```

3. Confirm sync is running — look for these log lines:
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
| `GONOTES_SYNC_ENABLED` | No | `false` | Enable the sync client on this instance |
| `GONOTES_SYNC_HUB_URL` | When sync enabled | — | Base URL of the hub instance |
| `GONOTES_SYNC_USERNAME` | When sync enabled | — | Username for hub authentication |
| `GONOTES_SYNC_PASSWORD` | When sync enabled | — | Password for hub authentication |
| `GONOTES_SYNC_INTERVAL` | No | `5m` | Polling interval between sync cycles |
| `GONOTES_REGISTRATION_SECRET` | No | — | Shared secret required to register new accounts (hub-side) |

---

Built with Go, RWeb, and Element