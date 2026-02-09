---
name: GoNotes Architecture
description: System architecture documentation for the GoNotes note-taking application
---

# GoNotes Architecture

## System Overview

GoNotes is a self-hosted note-taking application written in Go with a web frontend. It supports user authentication, hierarchical categories, AES-256 encryption for private notes, and peer-to-peer synchronization between devices.

```
┌──────────────────────────────────────────────────────┐
│                    GoNotes Server                    │
│                                                      │
│  main.go                                             │
│    ├── models.InitDB()    (DuckDB disk + cache)      │
│    ├── models.InitJWT()   (JWT signing key)           │
│    └── web.NewServer()    (RWeb HTTP on :8000)        │
│                                                      │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────┐ │
│  │  Middleware  │→ │   Handlers   │→ │   Models    │ │
│  │  (CORS,JWT, │  │  (api/*.go)  │  │  (models/)  │ │
│  │  Security,  │  │              │  │             │ │
│  │  Logging)   │  │  Pages:      │  │  DuckDB     │ │
│  │             │  │  (pages/)    │  │  Disk+Cache │ │
│  └─────────────┘  └──────────────┘  └─────────────┘ │
└──────────────────────────────────────────────────────┘
```

## Module Layout

```
gonotes/
├── main.go                     # Entry point: InitDB, InitJWT, start server
├── go.mod                      # Module: gonotes, Go 1.24
├── models/                     # Data layer (all DB interaction)
│   ├── db.go                   # DuckDB connection pool, schema DDL, cache init
│   ├── note.go                 # Note CRUD (disk + cache dual-write)
│   ├── category.go             # Category CRUD, note-category relationships
│   ├── user.go                 # User accounts, password hashing
│   ├── token.go                # JWT creation, validation, InitJWT
│   ├── encryption.go           # AES-256-GCM encrypt/decrypt for private notes
│   ├── note_change.go          # Note change tracking (fragments + sync peers)
│   ├── category_change.go      # Category change tracking (parallel to notes)
│   ├── sync_apply.go           # ApplySync* functions for incoming sync data
│   ├── sync_protocol.go        # Unified SyncChange envelope, pull/push logic
│   └── msgpack.go              # MessagePack serialization helpers
├── web/
│   ├── server.go               # NewServer, NewTestServer, middleware setup
│   ├── routes.go               # All route registrations
│   ├── middleware.go            # CORS, SecurityHeaders, Logging middleware
│   ├── static.go               # Embedded static file serving
│   ├── static/                 # JS, CSS, images (embedded at compile time)
│   ├── api/                    # JSON API handlers
│   │   ├── auth.go             # Register, Login, GetCurrentUser, RefreshToken
│   │   ├── notes.go            # CRUD + pagination for notes
│   │   ├── categories.go       # CRUD + note-category relationships
│   │   └── sync.go             # Sync endpoints (pull, push, snapshot, status, health)
│   └── pages/                  # Server-rendered HTML pages
│       ├── landing/            # Main landing page
│       ├── auth/               # Login/register pages
│       ├── comps/              # Shared UI components
│       └── shared/             # Shared page helpers
├── ai_docs/                    # AI-consumable documentation
│   ├── SKILL.md                # API reference for UI/client development
│   ├── SYNCHING_PLAN.md        # Detailed sync architecture plan
│   └── ARCHITECTURE.md         # This file
└── data/                       # DuckDB database files (created at runtime)
    └── notes.ddb               # Production database
```

## Database Architecture

### Dual-Database Pattern

GoNotes uses two DuckDB databases running simultaneously:

```
┌───────────────────┐         ┌───────────────────┐
│    Disk Database   │────────→│  In-Memory Cache   │
│   (Source of Truth)│  sync   │  (Fast Reads)      │
│                    │  on     │                    │
│  - Full schema     │  start  │  - Read-only cache │
│  - authored_at col │         │  - No authored_at  │
│  - Encrypted body  │         │  - Plaintext body  │
│  - Change tracking │         │  - No change tables│
└───────────────────┘         └───────────────────┘
```

**Disk DB** (`./data/notes.ddb`): Source of truth for all data. Contains the full schema including `authored_at` timestamps, encrypted note bodies, and all change tracking tables. All writes go here first.

**In-Memory Cache** (`:memory:`): Read-optimized copy. Populated at startup via `syncCacheFromDisk()`. Contains notes, categories, and note_categories — but **not** `authored_at` (only needed for sync) and **not** change tracking tables. Private note bodies are decrypted before caching for fast reads.

**Write path**: All mutations write to disk first, then update the cache. This ensures durability while keeping reads fast.

**Read path**: All queries read from the cache unless they specifically need disk-only columns (like `authored_at` for sync).

### Schema Overview

```sql
-- Core data tables (both disk and cache)
users              (id, guid, username, password_hash, email, display_name, ...)
notes              (id, guid, title, description, body, tags, is_private,
                    encryption_iv, created_by, updated_by, created_at, updated_at,
                    authored_at*, synced_at, deleted_at)    -- *disk only
categories         (id, guid, name, description, subcategories, created_at, updated_at)
note_categories    (note_id, category_id, subcategories, created_at)

-- Change tracking (disk only, not in cache)
note_fragments             (id, bitmask, title, description, body, body_is_diff,
                            tags, is_private, categories)
note_changes               (id, guid, note_guid, note_fragment_id, operation,
                            user, created_at)
note_change_sync_peers     (id, note_change_id, peer_id, synced_at)

category_fragments         (id, bitmask, name, description, subcategories)
category_changes           (id, guid, category_guid, category_fragment_id,
                            operation, user, created_at)
category_change_sync_peers (id, category_change_id, peer_id, synced_at)
```

### DuckDB-Specific Notes

- Uses `go-duckdb` driver with `?` parameter placeholders (not `$1`)
- `FOREIGN KEY` constraints do not support `CASCADE` in DuckDB
- Sequences are used for auto-increment IDs on fragments/changes
- `uuid()` function is used for GUID generation in migrations
- In-memory databases are created with empty DSN string `""`

## Authentication

### JWT Token Flow

```
Client                          Server
  │                               │
  │  POST /api/v1/auth/login      │
  │  {username, password}         │
  │──────────────────────────────→│
  │                               │  bcrypt.CompareHashAndPassword()
  │  200 {user, token}            │  jwt.NewWithClaims(HS256, 7-day expiry)
  │←──────────────────────────────│
  │                               │
  │  GET /api/v1/notes            │
  │  Authorization: Bearer <jwt>  │
  │──────────────────────────────→│
  │                               │  JWTAuthMiddleware validates token
  │                               │  Sets user_guid in context
  │  200 [notes...]               │  Handler reads via GetCurrentUserGUID()
  │←──────────────────────────────│
```

- JWT tokens are signed with HS256 using `GONOTES_JWT_SECRET` env var
- Token expiration: 7 days
- Middleware sets user context on every request; handlers call `GetCurrentUserGUID()` to enforce auth
- Auth endpoints (register/login) do not require tokens
- Health endpoint is unauthenticated

### Encryption

Private notes (`is_private: true`) use AES-256-GCM encryption:
- Requires `GONOTES_ENCRYPTION_KEY` env var (exactly 32 characters)
- Each note gets a unique initialization vector (IV) stored in `encryption_iv`
- Encrypted on disk, decrypted in cache for fast plaintext reads
- Encryption is optional — disabled if the env var is not set

## Middleware Stack

Applied in order on every request:

1. **RequestInfo** (rweb built-in) — logs basic request metrics
2. **CorsMiddleware** — handles CORS headers for browser clients
3. **JWTAuthMiddleware** — validates Bearer tokens, sets user context
4. **SecurityHeadersMiddleware** — adds security headers (CSP, X-Frame-Options, etc.)
5. **LoggingMiddleware** — structured request logging

## Sync Architecture

### Hub-and-Spoke Topology

```
          ┌──────────┐
          │   Hub    │
          │  (VPS)   │
          └────┬─────┘
               │
      ┌────────┼────────┐
      │        │        │
 ┌────▼──┐ ┌──▼───┐ ┌──▼───┐
 │Spoke A│ │Spoke B│ │Spoke C│
 │(laptop)│ │(phone)│ │(tablet)│
 └────────┘ └──────┘ └───────┘
```

One hub (typically a VPS) serves as the central sync point. Spokes (clients/devices) sync with the hub. Spokes do not sync directly with each other.

### Pull-First Sync (Rebase Model)

Spokes always **pull first**, then **push**. This is analogous to a git rebase workflow:

1. Spoke calls `GET /api/v1/sync/pull?peer_id=<id>&limit=100`
2. Hub returns unsent `SyncChange` objects (unified note+category stream)
3. Spoke applies received changes locally
4. Spoke calls `POST /api/v1/sync/push` with its local changes
5. Hub applies incoming changes and returns accepted/rejected results

### Change Tracking

Every note or category mutation automatically creates a change record:

```
User Action → CRUD Function → Change Record + Fragment
                                    │
                                    ▼
                              note_changes / category_changes
                              note_fragments / category_fragments
```

**Fragments** store only the fields that changed (delta storage). A bitmask indicates which fields are present:

| Bit   | Note Fragment         | Category Fragment    |
|-------|-----------------------|----------------------|
| 0x80  | Title                 | Name                 |
| 0x40  | Description           | Description          |
| 0x20  | Body                  | Subcategories        |
| 0x10  | Tags                  | —                    |
| 0x08  | IsPrivate             | —                    |
| 0x04  | Categories            | —                    |

**Body diffs**: For note updates, the body field may contain a unified diff patch rather than the full body text. The `body_is_diff` flag indicates whether to apply the fragment as a patch (`true`) or a full replacement (`false`).

### Unified SyncChange Envelope

The `SyncChange` struct wraps both note and category changes into a single stream:

```json
{
  "id": 42,
  "guid": "change-uuid",
  "entity_type": "note",
  "entity_guid": "note-uuid",
  "operation": 1,
  "fragment": { "bitmask": 224, "title": "...", "body": "..." },
  "authored_at": "2025-01-15T10:00:00Z",
  "user": "user-guid",
  "created_at": "2025-01-15T10:00:01Z"
}
```

**Operation constants**: Create=1, Update=2, Delete=3, Sync=9

**Ordering guarantee**: When merging note and category changes at the same timestamp, categories sort first to ensure category definitions exist before note-category mappings reference them.

### Per-Peer Tracking

Each peer has a stable identity (`peer_id`). The `*_sync_peers` tables track which changes have been sent to which peers:

```
note_change_sync_peers:     (note_change_id, peer_id, synced_at)
category_change_sync_peers: (category_change_id, peer_id, synced_at)
```

`GetUnifiedChangesForPeer()` returns only changes not yet synced to the requesting peer. After delivery, `MarkSyncChangesForPeer()` records the send.

### Idempotency

The sync protocol is designed for safe replay:

1. **Change GUID check**: Before applying an incoming change, check if its GUID already exists in `note_changes` or `category_changes`
2. **Entity GUID check**: For create operations, check if the entity (note/category) already exists by GUID — handles cases where the change was applied under a different internal GUID
3. Both checks return success (nil error) on duplicate, ensuring "at-least-once" delivery is safe

### Sync Status & Checksums

`GET /api/v1/sync/status` returns note/category counts and a SHA-256 checksum of sorted entity GUIDs. Peers compare checksums to quickly detect whether their data sets have diverged without exchanging every record.

## Key Libraries

| Library | Purpose |
|---------|---------|
| `github.com/rohanthewiz/rweb` | HTTP server framework (Echo-like) |
| `github.com/rohanthewiz/element` | HTML generation (builder pattern) |
| `github.com/rohanthewiz/serr` | Structured error wrapping |
| `github.com/rohanthewiz/logger` | Structured logging |
| `github.com/marcboeker/go-duckdb` | DuckDB driver for `database/sql` |
| `github.com/golang-jwt/jwt/v5` | JWT token creation and validation |
| `github.com/google/uuid` | UUID generation for GUIDs |
| `github.com/sergi/go-diff` | Unified diff for body delta patches |
| `github.com/vmihailenco/msgpack/v5` | MessagePack serialization |
| `golang.org/x/crypto` | bcrypt password hashing |

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `GONOTES_JWT_SECRET` | Production | JWT signing secret (min 32 chars). Random fallback in dev. |
| `GONOTES_ENCRYPTION_KEY` | No | AES-256 key (exactly 32 chars). Encryption disabled if unset. |

## Data Lifecycle

### Notes
- **Create**: Disk insert (encrypted if private) + cache insert (plaintext) + change record
- **Update**: Disk update + cache update + change record with delta fragment (body diff if applicable)
- **Delete**: Soft delete (`deleted_at` timestamp) on both disk and cache + change record
- Notes are never physically deleted; `deleted_at IS NULL` filters active notes

### Categories
- **Create**: Disk insert + cache insert + change record (GUID auto-generated)
- **Update**: Disk update + cache update + change record with delta fragment
- **Delete**: Hard delete (`DELETE FROM`) on both disk and cache + change record
- Categories use hard delete, not soft delete

### Note-Category Relationships
- Stored in `note_categories` junction table with per-note `subcategories` selection
- Changes tracked as note change records with `FragmentCategories` (0x04) bitmask
