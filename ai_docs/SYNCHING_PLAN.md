# GoNotes Comprehensive Synching Plan

## Table of Contents
1. [Executive Summary](#executive-summary)
2. [Current State Analysis](#current-state-analysis)
3. [Design Principles](#design-principles)
4. [Architecture Overview](#architecture-overview)
5. [Category & Subcategory Sync Design](#category--subcategory-sync-design)
6. [Change Tracking Enhancements](#change-tracking-enhancements)
7. [Sync Protocol](#sync-protocol)
8. [Conflict Resolution](#conflict-resolution)
9. [Active vs Passive Synching](#active-vs-passive-synching)
10. [API Endpoints](#api-endpoints)
11. [Data Models & Schema Changes](#data-models--schema-changes)
12. [Implementation Phases](#implementation-phases)
13. [Security Considerations](#security-considerations)
14. [Edge Cases & Failure Modes](#edge-cases--failure-modes)

---

## Executive Summary

GoNotes needs to synchronize **notes, categories, subcategories, and note-category
relationships** across multiple machines belonging to the same user. The existing
infrastructure provides a solid foundation: change tracking with delta fragments for
notes, per-peer sync tracking via `note_change_sync_peers`, and a timestamp-based pull
API (`GET /api/v1/sync/changes`). However, categories, subcategories, and
note-category mappings are not yet tracked in the change log. This plan extends the
existing event-sourced change tracking to cover the full data model, introduces a
**hub-and-spoke sync topology** with one designated server, and defines both active
(push/pull polling) and passive (startup reconciliation) sync modes.

---

## Current State Analysis

### What Exists Today

| Component | Status | Notes |
|-----------|--------|-------|
| Note change tracking | **Implemented** | `note_changes` + `note_fragments` tables with bitmask delta storage |
| Per-peer sync tracking | **Implemented** | `note_change_sync_peers` table, `GetUnsentChangesForPeer()` |
| Timestamp-based pull | **Implemented** | `GET /api/v1/sync/changes?since=<RFC3339>` |
| Peer sync integration test | **Implemented** | 3-peer simulation in `peer_sync_integration_test.go` (on `claude/implement-note-syncing-FevWs` branch) |
| Category CRUD | **Implemented** | Full CRUD with disk+cache dual-write |
| Note-category relationships | **Implemented** | Junction table with per-note subcategory selection |
| Category change tracking | **Not implemented** | Categories have no entry in `note_changes` |
| Note-category change tracking | **Not implemented** | Relationship changes are untracked |
| Applying received changes | **Not implemented** | `applyChangeOnPeer()` is a stub in integration test |
| Conflict resolution | **Not implemented** | `authored_at` field exists but no resolution logic |
| Network transport for sync | **Not implemented** | No peer-to-peer HTTP calls |

### Key Observations

1. **Categories lack GUIDs.** Notes use GUIDs for cross-machine identity; categories
   use only auto-increment IDs. Two machines creating the same category independently
   will have different IDs with no way to reconcile.

2. **The `FragmentCategories` bitmask (0x04) is reserved but unused.** The
   `note_fragments.categories` column exists but is never written to. The comment in
   `computeChangeBitmask()` explicitly says "Category changes are tracked separately"
   -- but they aren't tracked at all yet.

3. **The sync endpoint only returns note-level changes.** There is no way for a peer
   to discover that a category was created, renamed, or deleted on another machine.

4. **The `authored_at` field is designed for last-write-wins conflict resolution** but
   the resolution logic is not implemented.

---

## Design Principles

1. **Same user, multiple machines.** This is not multi-user collaboration. A single
   user runs GoNotes on several machines (e.g. work laptop, home desktop, VPS) and
   wants their notes and categories to stay synchronized.

2. **Hub-and-spoke topology.** One GoNotes instance is designated the **hub** (always
   reachable, e.g., a VPS or cloud server). Spoke instances sync exclusively with the
   hub. This avoids the combinatorial complexity of full-mesh peer-to-peer sync while
   still achieving eventual consistency across all machines.

3. **Eventual consistency.** All machines converge to the same state given sufficient
   connectivity. Temporary network partitions are tolerated; changes queue locally and
   sync when connectivity resumes.

4. **GUIDs everywhere.** Every entity that crosses machine boundaries must have a
   globally unique identifier. Auto-increment IDs are local and cannot be relied upon
   for cross-machine identity.

5. **Non-destructive change tracking.** Change recording must never block or fail CRUD
   operations. If change tracking fails, the operation succeeds and the change is
   logged for manual recovery.

6. **Delta efficiency.** Only transmit what changed. The existing bitmask+fragment
   pattern for notes extends naturally to categories.

7. **Idempotent application.** Applying the same change twice must produce the same
   result. This simplifies retry logic and eliminates concerns about duplicate delivery.

---

## Architecture Overview

```
┌──────────────┐         ┌──────────────┐         ┌──────────────┐
│  Spoke A     │         │    Hub       │         │  Spoke B     │
│  (laptop)    │◄───────►│  (VPS)       │◄───────►│  (desktop)   │
│              │  HTTPS   │              │  HTTPS   │              │
└──────────────┘         └──────────────┘         └──────────────┘
                                ▲
                                │ HTTPS
                         ┌──────┴──────┐
                         │  Spoke C    │
                         │  (work PC)  │
                         └─────────────┘
```

### Sync Flow

A spoke syncs with the hub in two phases per cycle:

1. **Push**: Spoke sends its local unsynced changes to the hub.
   Hub validates and applies them, then ACKs.
2. **Pull**: Spoke requests changes from the hub since its last sync point.
   Hub returns changes (including those received from other spokes).
   Spoke applies them locally.

This two-phase push-then-pull ensures that a spoke's own changes are on the hub
before it pulls, preventing the spoke from receiving its own changes back as conflicts.

### Entity Sync Scope

All synced entities and their relationships:

```
User (same user across all machines, authenticated via JWT)
 ├── Notes (synced via note_changes + note_fragments)
 ├── Categories (NEW: synced via category_changes + category_fragments)
 └── Note-Category Mappings (NEW: synced via mapping_changes)
```

---

## Category & Subcategory Sync Design

### Problem: Categories Need GUIDs

Currently, categories are identified only by auto-increment `id`. When Machine A
creates a "Kubernetes" category (id=1) and Machine B independently creates a
"Kubernetes" category (id=5), there is no way to recognize these as the same logical
entity.

### Solution: Add GUID to Categories

Add a `guid` column to the `categories` table, generated at creation time via
`uuid.New()`. This GUID becomes the cross-machine identifier, just as `notes.guid`
is for notes.

**Migration:**
```sql
ALTER TABLE categories ADD COLUMN IF NOT EXISTS guid VARCHAR;
UPDATE categories SET guid = uuid() WHERE guid IS NULL;
-- After backfill, enforce uniqueness:
CREATE UNIQUE INDEX IF NOT EXISTS idx_categories_guid ON categories(guid);
```

### Category Change Tracking

Introduce a parallel change-tracking system for categories, following the same
pattern as notes but simpler (fewer fields).

**New table: `category_changes`**
```sql
CREATE TABLE IF NOT EXISTS category_changes (
    id                   BIGINT PRIMARY KEY DEFAULT nextval('category_changes_id_seq'),
    guid                 VARCHAR NOT NULL UNIQUE,
    category_guid        VARCHAR NOT NULL,
    operation            INTEGER NOT NULL,  -- 1:Create, 2:Update, 3:Delete, 9:Sync
    category_fragment_id BIGINT,
    user                 VARCHAR,
    created_at           TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (category_fragment_id) REFERENCES category_fragments(id)
);
```

**New table: `category_fragments`**
```sql
CREATE TABLE IF NOT EXISTS category_fragments (
    id            BIGINT PRIMARY KEY DEFAULT nextval('category_fragments_id_seq'),
    bitmask       SMALLINT NOT NULL,
    name          VARCHAR,
    description   VARCHAR,
    subcategories VARCHAR  -- JSON array
);
```

**Category bitmask constants:**
```go
const (
    CatFragmentName          = 0x80  // 128 - bit 7
    CatFragmentDescription   = 0x40  // 64  - bit 6
    CatFragmentSubcategories = 0x20  // 32  - bit 5
)
```

### Note-Category Mapping Change Tracking

Note-category relationships are a different beast: they are link records, not
standalone entities. The simplest robust approach is to record mapping changes as
part of the **note's** change stream using the existing `FragmentCategories` bitmask
bit (0x04) and the `note_fragments.categories` column.

**`categories` field in `note_fragments`:**

When a note's category associations change (add, remove, or subcategory update), we
record a note change with `FragmentCategories` set. The `categories` field contains
a JSON snapshot of the note's **complete** current category state:

```json
[
  {
    "category_guid": "cat-guid-1",
    "category_name": "Kubernetes",
    "selected_subcategories": ["pod", "deployment"]
  },
  {
    "category_guid": "cat-guid-2",
    "category_name": "Go",
    "selected_subcategories": []
  }
]
```

**Why full snapshot instead of incremental add/remove?**

- **Idempotent**: Applying the snapshot produces the correct state regardless of
  prior state. No ordering dependency between multiple add/remove operations.
- **Simple conflict resolution**: Last-write-wins on the whole mapping set for a note
  is easier to reason about than merging individual add/remove deltas.
- **Small payload**: Most notes have 1-3 categories; the snapshot is tiny.

When applying this on the receiving end, the receiver:
1. Looks up each `category_guid` to find the local `category_id`
   (categories must be synced before note-category mappings).
2. Replaces the note's entire `note_categories` set with the snapshot.

---

## Change Tracking Enhancements

### Unified Change Stream

Rather than querying `note_changes` and `category_changes` separately, the sync
protocol uses a **unified change envelope** that wraps either type:

```go
type SyncChange struct {
    ID           int64     `json:"id"`
    GUID         string    `json:"guid"`
    EntityType   string    `json:"entity_type"`   // "note" or "category"
    EntityGUID   string    `json:"entity_guid"`
    Operation    int32     `json:"operation"`
    Fragment     any       `json:"fragment,omitempty"`
    User         string    `json:"user,omitempty"`
    CreatedAt    time.Time `json:"created_at"`
}
```

The hub's sync endpoint returns a combined, chronologically ordered stream of both
note and category changes. This ensures categories are created before any note
mappings reference them.

### Change Recording for Category CRUD

Mirroring the note pattern, each category CRUD function records a change:

| Operation | Fragment | Bitmask |
|-----------|----------|---------|
| CreateCategory | Full snapshot (name, description, subcategories) | `CatFragmentName \| CatFragmentDescription \| CatFragmentSubcategories` |
| UpdateCategory | Delta of changed fields | Only changed bits |
| DeleteCategory | None (null fragment) | 0 |

### Change Recording for Note-Category Mapping Changes

When `AddCategoryToNote`, `RemoveCategoryFromNote`, or
`UpdateNoteCategorySubcategories` is called:

1. Query the note's **current full category state** after the operation.
2. Serialize it as JSON.
3. Insert a `note_change` with `FragmentCategories` set and the JSON in
   `note_fragments.categories`.

This reuses the existing note change infrastructure with no new tables.

---

## Sync Protocol

### Authentication

Spokes authenticate to the hub using the same JWT mechanism used by the web UI. The
spoke stores the hub URL and credentials in its local configuration. On each sync
cycle, the spoke obtains (or refreshes) a JWT token from the hub.

**Configuration (spoke side):**
```go
type SyncConfig struct {
    HubURL       string        // e.g. "https://notes.example.com"
    Username     string
    Password     string        // stored encrypted or via env var
    SyncInterval time.Duration // for active sync polling
    Enabled      bool
}
```

### Push Phase: Spoke -> Hub

**Endpoint:** `POST /api/v1/sync/push`

**Request body:**
```json
{
  "peer_id": "spoke-guid",
  "changes": [
    {
      "guid": "change-guid-1",
      "entity_type": "category",
      "entity_guid": "cat-guid-1",
      "operation": 1,
      "fragment": {
        "bitmask": 224,
        "name": "Kubernetes",
        "description": "K8s notes",
        "subcategories": "[\"pod\",\"deployment\"]"
      },
      "created_at": "2026-02-08T10:00:00Z"
    },
    {
      "guid": "change-guid-2",
      "entity_type": "note",
      "entity_guid": "note-guid-1",
      "operation": 1,
      "fragment": {
        "bitmask": 228,
        "title": "K8s Pods",
        "body": "How pods work...",
        "categories": "[{\"category_guid\":\"cat-guid-1\",\"selected_subcategories\":[\"pod\"]}]"
      },
      "created_at": "2026-02-08T10:01:00Z"
    }
  ]
}
```

**Hub processing:**
1. Validate JWT and verify user identity.
2. For each change (processed in order):
   a. Check if `change.guid` already exists locally (idempotency). If so, skip.
   b. Resolve `entity_guid` to local entity. For creates, create with the provided GUID.
   c. Apply the change to the local database.
   d. Record as `operation=9` (Sync) in local `note_changes`/`category_changes`.
   e. Mark the change as synced from the spoke's `peer_id`.
3. Return ACK with list of accepted change GUIDs.

**Response:**
```json
{
  "success": true,
  "data": {
    "accepted": ["change-guid-1", "change-guid-2"],
    "rejected": [],
    "conflicts": []
  }
}
```

### Pull Phase: Spoke <- Hub

**Endpoint:** `GET /api/v1/sync/pull?since=<RFC3339>&peer_id=<spoke-guid>&limit=100`

The hub returns all changes **not originating from** this spoke, created after the
`since` timestamp. This uses the existing `GetUnsentChangesForPeer` pattern but
extended to include category changes.

**Response:**
```json
{
  "success": true,
  "data": {
    "changes": [
      {
        "guid": "change-guid-5",
        "entity_type": "note",
        "entity_guid": "note-guid-3",
        "operation": 2,
        "fragment": {
          "bitmask": 32,
          "body": "Updated content from another machine"
        },
        "created_at": "2026-02-08T11:00:00Z"
      }
    ],
    "has_more": false
  }
}
```

**Spoke processing:**
1. For each change (processed in order):
   a. Check if `change.guid` already exists locally (idempotency). If so, skip.
   b. Apply the change to the local database.
   c. Record as `operation=9` (Sync) in local change tables.
2. Update local sync point to the `created_at` of the last processed change.
3. If `has_more` is true, immediately request the next page.

### Sync Point Tracking

Each spoke stores its last successful sync point locally:

```sql
CREATE TABLE IF NOT EXISTS sync_state (
    hub_url       VARCHAR NOT NULL,
    peer_id       VARCHAR NOT NULL,
    last_push_at  TIMESTAMP,      -- Last change we pushed
    last_pull_at  TIMESTAMP,      -- Last change we pulled
    last_sync_at  TIMESTAMP,      -- Last successful full sync cycle
    PRIMARY KEY (hub_url, peer_id)
);
```

---

## Conflict Resolution

### Strategy: Last-Write-Wins (LWW) with `authored_at`

For a single-user system, conflicts are rare (they occur only when the user edits the
same entity on two machines without syncing in between). LWW is appropriate because:

- The user is the single author; there is no "whose change wins?" ambiguity.
- The `authored_at` timestamp on notes already tracks the last human edit time.
- Simplicity: no vector clocks, no CRDTs, no manual merge UI needed.

### Conflict Detection

A conflict exists when the hub receives a push for an entity that has been modified
since the spoke last pulled. Detection:

```
spoke_change.entity_guid == hub_entity.guid
AND hub_entity.authored_at > spoke_last_pull_at
AND spoke_change.created_at > spoke_last_pull_at
```

### Resolution Rules

| Scenario | Resolution |
|----------|------------|
| Note updated on spoke, same note updated on hub | **Later `authored_at` wins.** Losing change is preserved in change history for audit but not applied. |
| Category updated on both | **Later `updated_at` wins.** Same approach. |
| Note deleted on one side, updated on other | **Delete wins.** Rationale: the user explicitly chose to delete; recovering a deleted note is easier than un-deleting one that shouldn't exist. |
| Same category name created on two machines | **Merge by name.** Detect duplicate names, merge subcategories (union), keep the older GUID. |
| Note-category mappings differ | **Later timestamp wins.** The full-snapshot approach means the later snapshot replaces the earlier one. |

### Conflict Logging

All conflicts are recorded for transparency:

```sql
CREATE TABLE IF NOT EXISTS sync_conflicts (
    id              BIGINT PRIMARY KEY DEFAULT nextval('sync_conflicts_id_seq'),
    entity_type     VARCHAR NOT NULL,
    entity_guid     VARCHAR NOT NULL,
    local_change    VARCHAR,       -- JSON of local change
    remote_change   VARCHAR,       -- JSON of incoming change
    resolution      VARCHAR,       -- "local_wins", "remote_wins", "merged"
    resolved_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

---

## Active vs Passive Synching

### Active Sync (Polling)

Active sync runs on a configurable interval (default: 5 minutes) when the
application is running and network is available.

**Implementation:**

```go
func StartActiveSync(config SyncConfig) {
    ticker := time.NewTicker(config.SyncInterval)
    go func() {
        for range ticker.C {
            if err := runSyncCycle(config); err != nil {
                logger.LogErr(err, "active sync cycle failed")
                // Exponential backoff on repeated failures
            }
        }
    }()
}

func runSyncCycle(config SyncConfig) error {
    // 1. Authenticate with hub
    token, err := authenticateWithHub(config)
    if err != nil { return err }

    // 2. Push local changes
    if err := pushChanges(config, token); err != nil { return err }

    // 3. Pull remote changes
    if err := pullChanges(config, token); err != nil { return err }

    // 4. Update sync state
    return updateSyncState(config)
}
```

**Backoff strategy for failures:**
- 1st failure: retry in 30 seconds
- 2nd failure: retry in 1 minute
- 3rd+ failure: retry in 5 minutes
- Reset backoff on successful sync

**Network detection:**
Before each sync cycle, perform a lightweight health check
(`GET /api/v1/health`) against the hub. If unreachable, skip the cycle and
queue changes locally. Changes accumulate in the change tables and will be
pushed on the next successful cycle.

### Passive Sync (Startup Reconciliation)

Passive sync runs once at application startup and provides a full reconciliation.

**Implementation:**

```go
func RunPassiveSync(config SyncConfig) error {
    // 1. Authenticate with hub
    token, err := authenticateWithHub(config)
    if err != nil { return err }

    // 2. Push ALL unsynced local changes (may be many if offline for days)
    if err := pushAllChanges(config, token); err != nil { return err }

    // 3. Pull ALL changes since last sync (paginated)
    if err := pullAllChanges(config, token); err != nil { return err }

    // 4. Verify consistency (optional integrity check)
    if err := verifyConsistency(config, token); err != nil {
        logger.Warn("consistency check found discrepancies", "err", err)
    }

    return nil
}
```

**Consistency verification (lightweight):**
After push+pull, the spoke requests entity counts and checksums from the hub:

```
GET /api/v1/sync/status
Response: { "note_count": 42, "category_count": 8, "checksum": "abc123" }
```

The checksum is computed as a hash of all entity GUIDs sorted alphabetically. If
the spoke's checksum differs, it triggers a full reconciliation by pulling all
entities (not just changes since last sync).

### Manual Sync (User-Triggered)

The UI provides a "Sync Now" button that triggers an immediate sync cycle, bypassing
the polling interval. This gives the user control when they know they need fresh data.

### Sync State Machine

```
                    ┌─────────┐
                    │  IDLE   │
                    └────┬────┘
                         │ Timer tick / Manual trigger / Startup
                         ▼
                    ┌─────────┐
          ┌────────│  PUSH   │────────┐
          │        └─────────┘        │
          │ Failure          Success  │
          ▼                           ▼
    ┌───────────┐              ┌─────────┐
    │  BACKOFF  │              │  PULL   │
    └─────┬─────┘              └────┬────┘
          │ Timer                    │
          ▼                          │ Success / Failure
    ┌─────────┐                      ▼
    │  IDLE   │               ┌─────────────┐
    └─────────┘               │  RECONCILE  │ (on checksum mismatch)
                              └──────┬──────┘
                                     │
                                     ▼
                              ┌─────────┐
                              │  IDLE   │
                              └─────────┘
```

---

## API Endpoints

### New Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/v1/sync/push` | Spoke pushes local changes to hub |
| `GET` | `/api/v1/sync/pull` | Spoke pulls changes from hub |
| `POST` | `/api/v1/sync/ack` | Spoke acknowledges received changes |
| `GET` | `/api/v1/sync/status` | Get sync status (counts, checksums) |
| `GET` | `/api/v1/health` | Lightweight health check for network detection |

### Enhanced Existing Endpoint

`GET /api/v1/sync/changes` -- extend to include category changes in the unified
change stream, with `entity_type` field to distinguish note vs category changes.

---

## Data Models & Schema Changes

### New/Modified Tables

**1. `categories` table -- add GUID column:**
```sql
ALTER TABLE categories ADD COLUMN IF NOT EXISTS guid VARCHAR;
-- Backfill existing rows
UPDATE categories SET guid = uuid() WHERE guid IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_categories_guid ON categories(guid);
```

**2. `category_fragments` table (new):**
```sql
CREATE SEQUENCE IF NOT EXISTS category_fragments_id_seq START 1;
CREATE TABLE IF NOT EXISTS category_fragments (
    id            BIGINT PRIMARY KEY DEFAULT nextval('category_fragments_id_seq'),
    bitmask       SMALLINT NOT NULL,
    name          VARCHAR,
    description   VARCHAR,
    subcategories VARCHAR
);
```

**3. `category_changes` table (new):**
```sql
CREATE SEQUENCE IF NOT EXISTS category_changes_id_seq START 1;
CREATE TABLE IF NOT EXISTS category_changes (
    id                   BIGINT PRIMARY KEY DEFAULT nextval('category_changes_id_seq'),
    guid                 VARCHAR NOT NULL UNIQUE,
    category_guid        VARCHAR NOT NULL,
    operation            INTEGER NOT NULL,
    category_fragment_id BIGINT,
    user                 VARCHAR,
    created_at           TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (category_fragment_id) REFERENCES category_fragments(id)
);
CREATE INDEX IF NOT EXISTS idx_category_changes_category_guid ON category_changes(category_guid);
CREATE INDEX IF NOT EXISTS idx_category_changes_created_at ON category_changes(created_at);
```

**4. `category_change_sync_peers` table (new):**
```sql
CREATE TABLE IF NOT EXISTS category_change_sync_peers (
    category_change_id BIGINT NOT NULL,
    peer_id            VARCHAR NOT NULL,
    synced_at          TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (category_change_id, peer_id),
    FOREIGN KEY (category_change_id) REFERENCES category_changes(id)
);
CREATE INDEX IF NOT EXISTS idx_cat_change_sync_peers_peer_id ON category_change_sync_peers(peer_id);
```

**5. `sync_state` table (new):**
```sql
CREATE TABLE IF NOT EXISTS sync_state (
    hub_url       VARCHAR NOT NULL,
    peer_id       VARCHAR NOT NULL,
    last_push_at  TIMESTAMP,
    last_pull_at  TIMESTAMP,
    last_sync_at  TIMESTAMP,
    PRIMARY KEY (hub_url, peer_id)
);
```

**6. `sync_conflicts` table (new):**
```sql
CREATE SEQUENCE IF NOT EXISTS sync_conflicts_id_seq START 1;
CREATE TABLE IF NOT EXISTS sync_conflicts (
    id              BIGINT PRIMARY KEY DEFAULT nextval('sync_conflicts_id_seq'),
    entity_type     VARCHAR NOT NULL,
    entity_guid     VARCHAR NOT NULL,
    local_change    VARCHAR,
    remote_change   VARCHAR,
    resolution      VARCHAR,
    resolved_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### New Go Structs

```go
// CategoryChange tracks category modifications for sync
type CategoryChange struct {
    ID                 int64
    GUID               string
    CategoryGUID       string
    Operation          int32
    CategoryFragmentID sql.NullInt64
    User               sql.NullString
    CreatedAt          time.Time
}

// CategoryFragment stores delta info for category changes
type CategoryFragment struct {
    ID            int64
    Bitmask       int16
    Name          sql.NullString
    Description   sql.NullString
    Subcategories sql.NullString   // JSON array
}

// SyncChange is the unified envelope for the sync protocol
type SyncChange struct {
    ID         int64     `json:"id"`
    GUID       string    `json:"guid"`
    EntityType string    `json:"entity_type"`   // "note" or "category"
    EntityGUID string    `json:"entity_guid"`
    Operation  int32     `json:"operation"`
    Fragment   any       `json:"fragment,omitempty"`
    User       string    `json:"user,omitempty"`
    CreatedAt  time.Time `json:"created_at"`
}

// SyncPushRequest is the request body for POST /api/v1/sync/push
type SyncPushRequest struct {
    PeerID  string       `json:"peer_id"`
    Changes []SyncChange `json:"changes"`
}

// SyncPushResponse is the response for POST /api/v1/sync/push
type SyncPushResponse struct {
    Accepted  []string       `json:"accepted"`
    Rejected  []string       `json:"rejected,omitempty"`
    Conflicts []SyncConflict `json:"conflicts,omitempty"`
}

// SyncPullResponse is the response for GET /api/v1/sync/pull
type SyncPullResponse struct {
    Changes []SyncChange `json:"changes"`
    HasMore bool         `json:"has_more"`
}

// SyncConflict describes a detected conflict
type SyncConflict struct {
    EntityType  string `json:"entity_type"`
    EntityGUID  string `json:"entity_guid"`
    Resolution  string `json:"resolution"`
    Detail      string `json:"detail,omitempty"`
}

// SyncStatusResponse is the response for GET /api/v1/sync/status
type SyncStatusResponse struct {
    NoteCount     int    `json:"note_count"`
    CategoryCount int    `json:"category_count"`
    Checksum      string `json:"checksum"`
}

// SyncState tracks sync progress per hub
type SyncState struct {
    HubURL     string
    PeerID     string
    LastPushAt sql.NullTime
    LastPullAt sql.NullTime
    LastSyncAt sql.NullTime
}

// SyncConfig holds the configuration for sync
type SyncConfig struct {
    HubURL       string
    Username     string
    Password     string
    SyncInterval time.Duration
    Enabled      bool
    PeerID       string   // This instance's GUID (generated once, stored locally)
}
```

### Modifications to Existing Code

**`models/category.go`:**
- Add `GUID string` field to `Category` struct.
- Add `GUID string` field to `CategoryInput`.
- Generate GUID in `CreateCategory()` (like notes).
- Add change tracking calls to `CreateCategory`, `UpdateCategory`, `DeleteCategory`.
- Add change tracking calls to `AddCategoryToNoteWithSubcategories`,
  `UpdateNoteCategorySubcategories`, `RemoveCategoryFromNote`.

**`models/note_change.go`:**
- Implement category change functions: `insertCategoryChange`,
  `insertCategoryFragment`, `computeCategoryChangeBitmask`, etc.
- Add `GetUnsentCategoryChangesForPeer()`.
- Add `GetUnifiedChangesForPeer()` that merges note+category changes chronologically.
- Activate the `FragmentCategories` bitmask in note-category mapping operations.

**`models/db.go`:**
- Add `category_fragments`, `category_changes`, `category_change_sync_peers`,
  `sync_state`, `sync_conflicts` table creation in `createTables()`.
- Add GUID migration for existing categories.
- Add GUID column to cache schema for categories.
- Update `syncCategoriesFromDisk()` to include GUID.

**`web/api/sync.go`:**
- Implement `PushChanges` handler.
- Implement `PullChanges` handler.
- Implement `AckChanges` handler.
- Implement `SyncStatus` handler.
- Implement `HealthCheck` handler.

**`web/routes.go`:**
- Register new sync endpoints.

---

## Implementation Phases

### Phase 1: Category GUIDs & Change Tracking (Foundation)

**Goal:** Categories get GUIDs and all CRUD operations record changes.

1. Add `guid` column to `categories` table (migration + backfill).
2. Update `Category` struct, `CategoryInput`, `CategoryOutput`.
3. Generate GUID in `CreateCategory()`.
4. Create `category_fragments` and `category_changes` tables.
5. Implement `insertCategoryFragment`, `insertCategoryChange`.
6. Implement `computeCategoryChangeBitmask`.
7. Add change tracking to `CreateCategory`, `UpdateCategory`, `DeleteCategory`.
8. Add note-category mapping change tracking using `FragmentCategories` bitmask.
9. Create `category_change_sync_peers` table.
10. Implement `GetUnsentCategoryChangesForPeer`, `MarkCategoryChangeSyncedToPeer`.
11. Write tests for all new functions.

### Phase 2: Unified Sync Protocol & Endpoints

**Goal:** Hub can receive and serve unified change streams.

1. Create `SyncChange` unified envelope type.
2. Implement `GetUnifiedChangesForPeer()` (merges note + category changes).
3. Implement `POST /api/v1/sync/push` endpoint.
4. Implement `GET /api/v1/sync/pull` endpoint.
5. Implement `POST /api/v1/sync/ack` endpoint.
6. Implement `GET /api/v1/sync/status` endpoint.
7. Implement `GET /api/v1/health` endpoint.
8. Implement change application logic (the currently-stubbed `applyChangeOnPeer`):
   - `applyNoteCreate`, `applyNoteUpdate`, `applyNoteDelete`
   - `applyCategoryCreate`, `applyCategoryUpdate`, `applyCategoryDelete`
   - `applyNoteCategoryMapping`
9. Write integration tests with realistic sync scenarios.

### Phase 3: Conflict Resolution

**Goal:** Detect and resolve conflicts using LWW.

1. Add `authored_at` / `updated_at` comparison logic.
2. Implement conflict detection in push handler.
3. Implement LWW resolution for notes.
4. Implement LWW resolution for categories.
5. Implement name-based category deduplication.
6. Create `sync_conflicts` table and logging.
7. Write conflict scenario tests.

### Phase 4: Active & Passive Sync Client

**Goal:** Spokes can autonomously sync with the hub.

1. Create `SyncConfig` and config file/env var loading.
2. Create `sync_state` table for tracking sync progress.
3. Implement `runSyncCycle()` (push + pull + reconcile).
4. Implement active sync with configurable polling interval.
5. Implement passive sync at startup.
6. Implement exponential backoff on failures.
7. Implement network detection (health check).
8. Implement consistency verification (checksum comparison).
9. Add "Sync Now" manual trigger support.
10. Write end-to-end sync tests.

### Phase 5: Production Hardening

**Goal:** Reliable, observable, secure sync in production.

1. Add MsgPack encoding support for sync payloads (bandwidth optimization).
2. Add gzip compression for sync requests/responses.
3. Add sync metrics/logging (latency, throughput, errors, conflict rate).
4. Add change log pruning (archive old changes after all peers have synced).
5. Add rate limiting on sync endpoints.
6. Add configurable sync batch size.
7. Document deployment patterns (hub setup, spoke configuration).
8. Chaos testing (network partitions, partial failures, clock skew).

---

## Security Considerations

1. **Transport encryption:** All sync communication over HTTPS (TLS 1.3).

2. **Authentication:** JWT tokens authenticate spokes to the hub. Tokens are
   short-lived (1 hour) with refresh capability.

3. **User scoping:** The hub enforces that a spoke can only push/pull changes for the
   authenticated user. No cross-user data leakage.

4. **Private notes:** Notes marked `is_private` are encrypted at rest on disk. During
   sync, the **plaintext** body is transmitted over the encrypted HTTPS channel. The
   receiving machine encrypts it with its own local key before storing to disk. This
   means encryption keys can differ between machines (they are local secrets, not
   shared).

5. **Change GUID uniqueness:** Change GUIDs are UUIDs generated independently on each
   machine. Collision probability is negligible (2^122 combinations).

6. **Credential storage:** Hub credentials on spokes should be stored securely
   (environment variables or encrypted config, never in plaintext config files
   committed to VCS).

7. **Replay protection:** Change GUIDs provide idempotency. Replaying a push with
   the same change GUIDs is a no-op on the hub.

---

## Edge Cases & Failure Modes

### 1. Offline for Extended Period

A spoke that has been offline for weeks may have hundreds of unsynced changes.

**Handling:** Push/pull are paginated. The spoke pushes in batches (e.g., 100 changes
per request) and pulls in batches. Progress is checkpointed after each batch so a
network interruption mid-sync can resume from the last batch.

### 2. Category Created on Two Machines

User creates "Docker" on Machine A and "Docker" on Machine B, each with different
GUIDs.

**Handling:** During sync, the hub detects two categories with the same name but
different GUIDs. It merges them: keeps the older GUID, unions the subcategories,
and creates a redirect record so the newer GUID resolves to the older one. All
note-category mappings referencing the newer GUID are updated to the older one.

### 3. Note Deleted on One Machine, Updated on Another

**Handling:** Delete wins. The update change is recorded in `sync_conflicts` for
audit but not applied. Rationale: explicit deletion is a deliberate user action.

### 4. Hub Unreachable

**Handling:** All changes accumulate locally in change tables. The active sync
enters backoff mode. When connectivity resumes, the next sync cycle pushes all
accumulated changes. No data loss.

### 5. Spoke Database Corruption

**Handling:** The spoke can perform a full resync by:
1. Setting `last_pull_at` to epoch (1970-01-01).
2. Pulling all changes from hub.
This effectively rebuilds the spoke's database from the hub's complete history.

### 6. Clock Skew Between Machines

**Handling:** `authored_at` timestamps are generated locally and may differ between
machines. For LWW conflict resolution, a few seconds of clock skew is acceptable
for a single-user system. If needed, the hub can normalize timestamps by recording
when it received each change (server-side `received_at`), providing a consistent
ordering even with client clock skew.

### 7. Partial Push Failure

Spoke pushes 50 changes, network drops after 30 are received.

**Handling:** The hub ACKs with the list of accepted change GUIDs. The spoke
retries only the un-ACKed changes. Idempotent change application means the 30
already-received changes are safely skipped if re-sent.

### 8. Subcategory Rename/Reorganization

User renames a subcategory on one machine (e.g., "k8s-pod" -> "pods").

**Handling:** Subcategories are stored as JSON arrays in the `categories` table.
A rename on Machine A produces a category update change with the new subcategories
array. When applied on Machine B, the entire subcategories array is replaced.
Note-category mappings that reference the old subcategory name will need updating.
The category change fragment includes the full new subcategories list, and the
applying logic also updates any `note_categories.subcategories` values that
reference the old name.

---

## Summary

This plan extends GoNotes' existing note-level change tracking to encompass the full
data model: categories, subcategories, and note-category relationships. The key
additions are:

- **GUIDs for categories** to enable cross-machine identity
- **Category change tracking** parallel to the existing note change system
- **Note-category mapping snapshots** using the existing `FragmentCategories` bitmask
- **Hub-and-spoke topology** for practical, reliable multi-machine sync
- **Active polling + passive startup reconciliation** for comprehensive coverage
- **Last-write-wins conflict resolution** appropriate for single-user scenarios
- **Phased implementation** starting with the data foundation and building up to
  production-ready sync
