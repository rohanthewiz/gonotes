# GoNotes Comprehensive Synching Plan

## Table of Contents
1. [Executive Summary](#executive-summary)
2. [Current State Analysis](#current-state-analysis)
3. [Design Principles](#design-principles)
4. [Architecture Overview](#architecture-overview)
5. [Category & Subcategory Sync Design](#category--subcategory-sync-design)
6. [`authored_at` Semantics](#authored_at-semantics)
7. [Body Diff Storage](#body-diff-storage)
8. [Change Tracking Enhancements](#change-tracking-enhancements)
9. [Sync Protocol](#sync-protocol)
10. [Conflict Resolution](#conflict-resolution)
11. [Active vs Passive Synching](#active-vs-passive-synching)
12. [API Endpoints](#api-endpoints)
13. [Data Models & Schema Changes](#data-models--schema-changes)
14. [Implementation Phases](#implementation-phases)
15. [Security Considerations](#security-considerations)
16. [Edge Cases & Failure Modes](#edge-cases--failure-modes)

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
(pull/push polling) and passive (startup reconciliation) sync modes.

**Key design decisions:**
- **Pull-first sync** (rebase model): Spokes pull remote changes and resolve conflicts
  locally before pushing, analogous to `git pull --rebase` then `git push`.
- **`authored_at` preservation**: `authored_at` tracks the original human authoring
  time and is *never* updated during sync operations -- only on local create/update.
- **Body diffs**: Note body changes are stored as unified diffs (not full snapshots)
  to massively reduce change log storage for large notes with small edits.

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
   pattern for notes extends naturally to categories. Note body changes use true
   diffs (unified diff format) rather than full snapshots.

7. **Idempotent application.** Applying the same change twice must produce the same
   result. This simplifies retry logic and eliminates concerns about duplicate delivery.

8. **`authored_at` integrity.** The `authored_at` timestamp represents when the user
   actually authored or edited a note on the originating machine. It is set on create,
   updated on local edit, and **preserved as-is** during sync. Sync operations must
   never overwrite `authored_at` with the receiving machine's current time.

9. **Pull-first sync (rebase model).** Spokes always pull remote changes first, apply
   them locally, resolve any conflicts on the spoke side, and then push local changes.
   This keeps the hub simple (it only accepts non-conflicting pushes) and mirrors the
   `git pull --rebase && git push` workflow that developers are familiar with.

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

### Sync Flow (Pull-First / Rebase Model)

A spoke syncs with the hub in two phases per cycle, **pulling before pushing**
(analogous to `git pull --rebase` before `git push`):

1. **Pull**: Spoke requests changes from the hub since its last sync point.
   Hub returns changes from other spokes. Spoke applies them locally.
   If any pulled changes conflict with pending local changes, the spoke resolves
   conflicts locally (using LWW on `authored_at`) before proceeding.
2. **Push**: Spoke sends its local unsynced changes to the hub.
   Since conflicts were already resolved during pull, the hub can apply these
   changes without conflict resolution logic. Hub validates and ACKs.

**Why pull-first?**
- **Conflicts are resolved locally.** The spoke has full context of both its own
  pending changes and the incoming remote changes, making resolution straightforward.
- **Hub stays simple.** The hub is a dumb relay -- it applies pre-resolved changes
  without needing its own conflict resolution logic.
- **Familiar mental model.** Works like `git pull --rebase && git push`: get up to
  date, layer your changes on top, then push cleanly.
- **No self-conflict.** After pulling and resolving, the spoke's push contains
  changes that are guaranteed not to conflict with the hub's current state.

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

## `authored_at` Semantics

The `authored_at` timestamp is the cornerstone of conflict resolution. It must
accurately reflect when the user actually wrote or edited a note on the originating
machine.

### Rules

| Operation | `authored_at` Behavior |
|-----------|----------------------|
| **CreateNote** (local) | Set to `CURRENT_TIMESTAMP` (same as `created_at`). The user is authoring this note now. |
| **UpdateNote** (local) | Set to `CURRENT_TIMESTAMP`. The user is editing this note now. |
| **DeleteNote** (local) | Not updated. `deleted_at` is set instead. |
| **Sync Create** (received from hub) | Set to the **source machine's `authored_at`** value from the change envelope. Never use the local `CURRENT_TIMESTAMP`. |
| **Sync Update** (received from hub) | Set to the **source machine's `authored_at`** value from the change envelope. Never use the local `CURRENT_TIMESTAMP`. |
| **Sync Delete** (received from hub) | Not relevant (soft delete via `deleted_at`). |

### Implementation

The existing `CreateNote` and `UpdateNote` functions already set
`authored_at = CURRENT_TIMESTAMP` on local operations. For sync operations, a
separate code path (`applySyncNoteCreate`, `applySyncNoteUpdate`) must accept
`authored_at` as an explicit parameter and write it directly:

```go
// applySyncNoteUpdate applies a note update received from the hub.
// Critically, authored_at is preserved from the source machine --
// it is NOT set to CURRENT_TIMESTAMP.
func applySyncNoteUpdate(noteGUID string, fragment NoteFragment, authoredAt time.Time) error {
    // ... build SET clause from fragment bitmask ...
    // SET authored_at = ? (the source's authored_at, NOT CURRENT_TIMESTAMP)
    // SET synced_at = CURRENT_TIMESTAMP (marks when we received this sync)
}
```

### Why This Matters

Without this rule, conflict resolution breaks. Consider:

1. User edits note on Laptop at 10:00 AM (`authored_at = 10:00`).
2. User edits same note on Desktop at 10:05 AM (`authored_at = 10:05`).
3. Laptop syncs: pulls Desktop's change. If the sync overwrites `authored_at` with
   `CURRENT_TIMESTAMP` (say 10:10), the Laptop's record now says the note was
   authored at 10:10 -- which is wrong. The Desktop's edit was at 10:05.
4. If Desktop then syncs, LWW would compare 10:10 vs 10:05 and incorrectly
   think the Laptop's version is newer.

By preserving `authored_at` from the source, LWW correctly resolves that the
Desktop's 10:05 edit wins over the Laptop's 10:00 edit.

### For Categories

Categories should follow the same pattern. Add an `updated_at` column to categories
(or use the existing one) and apply the same rules: set on local CRUD, preserve
from source on sync.

---

## Body Diff Storage

### Problem: Full Body Snapshots Waste Space

Currently, when a note body is edited, the entire new body is stored in the
`note_fragments.body` column. For a 10,000-character note where the user fixes a
typo, we store all 10,000 characters. Over time with many edits, the `note_fragments`
table becomes enormous.

### Solution: Unified Diffs for Body Changes

Store note body changes as **unified diffs** (similar to `git diff` output) instead
of full snapshots. This dramatically reduces storage for incremental edits.

**Format:** Use the standard unified diff format produced by the Myers diff algorithm.
Go library: `github.com/sergi/go-diff/diffmatchpatch` (already transitively available
via `go-difflib` in the test dependencies).

### When to Use Diffs vs Full Snapshots

| Operation | Body Storage | Rationale |
|-----------|-------------|-----------|
| **Create** (operation=1) | **Full snapshot** | No prior state to diff against |
| **Update** (operation=2) | **Unified diff** | Diff against previous body state |
| **Sync Create** (operation=9, new entity) | **Full snapshot** | Receiver has no prior state |
| **Sync Update** (operation=9, existing entity) | **Unified diff** | Diff computed on source machine against source's prior state |
| **Delete** (operation=3) | No body | Fragment is null |

### Schema Change

Add a `body_is_diff` boolean to `note_fragments` to distinguish diffs from full
snapshots:

```sql
ALTER TABLE note_fragments ADD COLUMN IF NOT EXISTS body_is_diff BOOLEAN DEFAULT false;
```

### Computing Diffs

```go
import "github.com/sergi/go-diff/diffmatchpatch"

// computeBodyDiff computes a unified diff between old and new body content.
// Returns the diff as a string that can be stored in note_fragments.body.
func computeBodyDiff(oldBody, newBody string) string {
    dmp := diffmatchpatch.New()
    diffs := dmp.DiffMain(oldBody, newBody, true)
    patches := dmp.PatchMake(oldBody, diffs)
    return dmp.PatchToText(patches)
}

// applyBodyDiff applies a unified diff to an existing body.
// Returns the patched body or an error if the patch cannot be applied cleanly.
func applyBodyDiff(currentBody, diffText string) (string, error) {
    dmp := diffmatchpatch.New()
    patches, err := dmp.PatchFromText(diffText)
    if err != nil {
        return "", serr.Wrap(err, "failed to parse body diff")
    }
    result, applied := dmp.PatchApply(patches, currentBody)
    // Verify all patches applied cleanly
    for i, ok := range applied {
        if !ok {
            return "", serr.New("patch hunk " + strconv.Itoa(i) + " failed to apply")
        }
    }
    return result, nil
}
```

### Integration with Change Tracking

In `createDeltaFragment` (update operations):

1. If `FragmentBody` is set in the bitmask, compute a diff between the existing
   note body and the new body using `computeBodyDiff()`.
2. Store the diff string in `fragment.Body`.
3. Set `fragment.BodyIsDiff = true`.
4. If the diff is larger than the new body itself (rare, but possible for
   near-total rewrites), fall back to a full snapshot with `BodyIsDiff = false`.

In `createFragmentFromInput` (create operations):

1. Store the full body as-is (no diff).
2. Set `fragment.BodyIsDiff = false`.

### Applying Body Diffs on Sync

When a spoke receives a change with `body_is_diff = true`:

1. Load the note's current body from the local database.
2. Call `applyBodyDiff(currentBody, fragment.Body)` to produce the new body.
3. If the patch fails (e.g., the local base state diverged due to a conflict),
   request a full snapshot from the hub as a fallback.

### Fallback: Full Snapshot Recovery

If a diff cannot be applied (patch failure), the receiver can request a full
snapshot of the note:

```
GET /api/v1/sync/snapshot?entity_type=note&entity_guid=<guid>
```

This returns the complete current state of the entity from the hub, bypassing
the change log entirely. This is a safety net, not the normal path.

### NoteFragment Struct Update

```go
type NoteFragment struct {
    ID          int64
    Bitmask     int16
    Title       sql.NullString
    Description sql.NullString
    Body        sql.NullString // Full body or unified diff (see BodyIsDiff)
    BodyIsDiff  bool           // true = Body contains a diff, false = full snapshot
    Tags        sql.NullString
    IsPrivate   sql.NullBool
    Categories  sql.NullString
}
```

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
    AuthoredAt   time.Time `json:"authored_at"`   // Source machine's authored_at (preserved, never overwritten)
    User         string    `json:"user,omitempty"`
    CreatedAt    time.Time `json:"created_at"`
}
```

The `AuthoredAt` field carries the originating machine's authoring timestamp through
the sync pipeline. Receivers must use this value (not `CURRENT_TIMESTAMP`) when
applying sync changes locally. This is critical for correct LWW conflict resolution.

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

### Phase 1: Pull (Spoke <- Hub)

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
        "authored_at": "2026-02-08T10:05:00Z",
        "fragment": {
          "bitmask": 32,
          "body": "@@ -10,7 +10,7 @@\n content\n-old line\n+new line\n content",
          "body_is_diff": true
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
   b. **Check for local conflicts:** If the spoke has pending (un-pushed) changes
      for the same `entity_guid`, resolve using LWW on `authored_at`. (See
      [Conflict Resolution](#conflict-resolution).)
   c. Apply the change to the local database. **Crucially, use the source's
      `authored_at` -- do NOT set `authored_at = CURRENT_TIMESTAMP`.**
   d. If the fragment body is a diff (`body_is_diff = true`), apply it to the
      current local body using `applyBodyDiff()`. If the patch fails, request
      a full snapshot as fallback.
   e. Record as `operation=9` (Sync) in local change tables.
2. Update local sync point to the `created_at` of the last processed change.
3. If `has_more` is true, immediately request the next page.

### Phase 2: Push (Spoke -> Hub)

**Endpoint:** `POST /api/v1/sync/push`

After pulling and resolving conflicts locally, the spoke pushes its remaining
local changes. These are guaranteed to not conflict with the hub's current state
(because the spoke just pulled the hub's latest state and resolved any overlaps).

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
      "authored_at": "2026-02-08T10:00:00Z",
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
      "authored_at": "2026-02-08T10:01:00Z",
      "fragment": {
        "bitmask": 228,
        "title": "K8s Pods",
        "body": "How pods work...",
        "body_is_diff": false,
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
   c. Apply the change to the local database. **Use the source's `authored_at` --
      do NOT set `authored_at = CURRENT_TIMESTAMP`.** Set `synced_at = CURRENT_TIMESTAMP`.
   d. If body is a diff, apply it to the hub's current body state.
   e. Record as `operation=9` (Sync) in local `note_changes`/`category_changes`.
   f. Mark the change as synced from the spoke's `peer_id`.
3. Return ACK with list of accepted change GUIDs.

**Response:**
```json
{
  "success": true,
  "data": {
    "accepted": ["change-guid-1", "change-guid-2"],
    "rejected": []
  }
}
```

**Note:** The response no longer includes a `conflicts` field. In the pull-first
model, conflicts are resolved on the spoke before pushing. The hub should reject
a push only for validation failures (e.g., malformed data), not conflicts.

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

### Where Conflicts Are Resolved: The Spoke

In the pull-first model, conflict resolution happens **on the spoke during the pull
phase**, before any push occurs. This is analogous to `git pull --rebase`:

1. Spoke pulls remote changes from hub.
2. For each pulled change, spoke checks if it has a **pending local change** for the
   same entity (i.e., a local change that hasn't been pushed yet).
3. If yes, that's a conflict. Spoke resolves it using LWW on `authored_at`.
4. After resolving all conflicts, spoke pushes only the winning local changes.

The hub never needs to resolve conflicts -- it only receives pre-resolved changes.

### Conflict Detection (Spoke-Side)

During the pull phase, for each incoming remote change:

```
incoming_change.entity_guid EXISTS in local pending_changes
AND local_pending_change.operation != OperationSync
```

A pending local change is one that exists in `note_changes` or `category_changes`
but has NOT been pushed to the hub (no entry in the sync_peers table for the hub).

### Resolution Rules

| Scenario | Resolution |
|----------|------------|
| Note updated locally, same note updated on hub | **Later `authored_at` wins.** Losing change is preserved in `sync_conflicts` for audit. If remote wins, apply remote and discard local pending change. If local wins, apply remote then overwrite with local (local gets pushed later). |
| Category updated on both | **Later `updated_at` wins.** Same approach. |
| Note deleted on one side, updated on other | **Delete wins.** Rationale: the user explicitly chose to delete; recovering a deleted note is easier than un-deleting one that shouldn't exist. |
| Same category name created on two machines | **Merge by name.** Detect duplicate names, merge subcategories (union), keep the older GUID. |
| Note-category mappings differ | **Later timestamp wins.** The full-snapshot approach means the later snapshot replaces the earlier one. |
| Body diff conflict (both sides edited body) | If remote wins: apply remote diff, discard local diff. If local wins: skip remote diff, local diff stays pending for push. |

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

    // 2. Pull remote changes first (rebase model)
    //    This also resolves any conflicts with pending local changes
    if err := pullAndResolveConflicts(config, token); err != nil { return err }

    // 3. Push local changes (post-conflict-resolution, guaranteed clean)
    if err := pushChanges(config, token); err != nil { return err }

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

    // 2. Pull ALL changes since last sync (paginated) and resolve conflicts
    //    This may be many changes if the spoke has been offline for days
    if err := pullAllAndResolveConflicts(config, token); err != nil { return err }

    // 3. Push ALL unsynced local changes (post-conflict-resolution)
    if err := pushAllChanges(config, token); err != nil { return err }

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
          ┌────────│  PULL   │────────┐
          │        └─────────┘        │
          │ Failure          Success  │
          ▼                           ▼
    ┌───────────┐          ┌──────────────┐
    │  BACKOFF  │          │  RESOLVE     │ (conflict resolution on spoke)
    └─────┬─────┘          └──────┬───────┘
          │ Timer                 │
          ▼                       ▼
    ┌─────────┐            ┌─────────┐
    │  IDLE   │            │  PUSH   │────────┐
    └─────────┘            └─────────┘        │
                                    │         │ Failure → BACKOFF
                                    │ Success
                                    ▼
                            ┌─────────────┐
                            │  RECONCILE  │ (on checksum mismatch)
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
| `GET` | `/api/v1/sync/pull` | Spoke pulls changes from hub (Phase 1 of sync cycle) |
| `POST` | `/api/v1/sync/push` | Spoke pushes local changes to hub (Phase 2, after pull) |
| `POST` | `/api/v1/sync/ack` | Spoke acknowledges received changes |
| `GET` | `/api/v1/sync/snapshot` | Full snapshot of an entity (fallback when diff fails) |
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

**2. `note_fragments` table -- add `body_is_diff` column:**
```sql
ALTER TABLE note_fragments ADD COLUMN IF NOT EXISTS body_is_diff BOOLEAN DEFAULT false;
```
When `body_is_diff = true`, the `body` column contains a unified diff (patch text)
rather than the full body content. This reduces storage for incremental edits.

**3. `category_fragments` table (new):**
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

**4. `category_changes` table (new):**
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

**5. `category_change_sync_peers` table (new):**
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

**6. `sync_state` table (new):**
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

**7. `sync_conflicts` table (new):**
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

// SyncChange is the unified envelope for the sync protocol.
// AuthoredAt carries the source machine's authoring timestamp -- receivers must
// preserve this value and never overwrite it with CURRENT_TIMESTAMP.
type SyncChange struct {
    ID         int64     `json:"id"`
    GUID       string    `json:"guid"`
    EntityType string    `json:"entity_type"`   // "note" or "category"
    EntityGUID string    `json:"entity_guid"`
    Operation  int32     `json:"operation"`
    Fragment   any       `json:"fragment,omitempty"`
    AuthoredAt time.Time `json:"authored_at"`   // Source machine's authored_at (preserved through sync)
    User       string    `json:"user,omitempty"`
    CreatedAt  time.Time `json:"created_at"`
}

// SyncPushRequest is the request body for POST /api/v1/sync/push
type SyncPushRequest struct {
    PeerID  string       `json:"peer_id"`
    Changes []SyncChange `json:"changes"`
}

// SyncPushResponse is the response for POST /api/v1/sync/push.
// No Conflicts field -- in the pull-first model, conflicts are resolved on the
// spoke before pushing. The hub only rejects for validation failures.
type SyncPushResponse struct {
    Accepted []string `json:"accepted"`
    Rejected []string `json:"rejected,omitempty"`
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
- Add `BodyIsDiff bool` field to `NoteFragment` struct.
- Add `body_is_diff` column to `note_fragments` table DDL and migration.
- Implement `computeBodyDiff()` and `applyBodyDiff()` using `go-diff`.
- Update `createDeltaFragment()` to compute body diffs for updates.
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

**`models/note.go`:**
- Create `applySyncNoteCreate()` -- accepts explicit `authored_at` from source.
- Create `applySyncNoteUpdate()` -- accepts explicit `authored_at` from source.
  Must NOT set `authored_at = CURRENT_TIMESTAMP`. Must apply body diffs when
  `body_is_diff = true`.

**`web/api/sync.go`:**
- Implement `PullChanges` handler (spoke calls this first).
- Implement `PushChanges` handler (spoke calls this after pull).
- Implement `AckChanges` handler.
- Implement `SnapshotEntity` handler (fallback for diff failures).
- Implement `SyncStatus` handler.
- Implement `HealthCheck` handler.

**`web/routes.go`:**
- Register new sync endpoints.

---

## Implementation Phases

### Phase 1: Foundation (GUIDs, Body Diffs, `authored_at` Rules, Category Change Tracking)

**Goal:** Categories get GUIDs, note body changes use diffs, `authored_at` semantics
are enforced, and all CRUD operations record changes.

**1a. Body diff storage:**
1. Add `body_is_diff` column to `note_fragments` table (migration).
2. Add `github.com/sergi/go-diff` dependency.
3. Implement `computeBodyDiff()` and `applyBodyDiff()` functions.
4. Update `createDeltaFragment()` to compute body diffs for update operations.
5. Add size comparison fallback: if diff > full body, store full snapshot instead.
6. Update `NoteFragment` struct with `BodyIsDiff` field.
7. Write tests for diff computation and application (including edge cases).

**1b. `authored_at` enforcement:**
1. Verify `CreateNote` sets `authored_at = CURRENT_TIMESTAMP` (already done).
2. Verify `UpdateNote` sets `authored_at = CURRENT_TIMESTAMP` (already done).
3. Create `applySyncNoteCreate()` that accepts explicit `authored_at` from source.
4. Create `applySyncNoteUpdate()` that accepts explicit `authored_at` from source.
5. Ensure sync code paths NEVER set `authored_at = CURRENT_TIMESTAMP`.
6. Add `AuthoredAt` field to `SyncChange` envelope.
7. Write tests verifying `authored_at` preservation through sync.

**1c. Category GUIDs & change tracking:**
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

**Goal:** Hub can receive and serve unified change streams with body diffs.

1. Create `SyncChange` unified envelope type (with `AuthoredAt` field).
2. Implement `GetUnifiedChangesForPeer()` (merges note + category changes).
3. Implement `GET /api/v1/sync/pull` endpoint (spoke pulls first).
4. Implement `POST /api/v1/sync/push` endpoint (spoke pushes after pull).
5. Implement `POST /api/v1/sync/ack` endpoint.
6. Implement `GET /api/v1/sync/snapshot` endpoint (fallback for diff failures).
7. Implement `GET /api/v1/sync/status` endpoint.
8. Implement `GET /api/v1/health` endpoint.
9. Implement change application logic (preserving `authored_at` from source):
   - `applySyncNoteCreate`, `applySyncNoteUpdate`, `applySyncNoteDelete`
   - `applySyncCategoryCreate`, `applySyncCategoryUpdate`, `applySyncCategoryDelete`
   - `applySyncNoteCategoryMapping`
   - Body diff application with `applyBodyDiff()` and snapshot fallback
10. Write integration tests with realistic sync scenarios.

### Phase 3: Conflict Resolution (Spoke-Side)

**Goal:** Detect and resolve conflicts on the spoke during pull, using LWW.

1. Add `authored_at` / `updated_at` comparison logic.
2. Implement spoke-side conflict detection during pull phase:
   - Identify pending local changes that overlap with incoming remote changes.
3. Implement LWW resolution for notes (compare `authored_at` timestamps).
4. Implement LWW resolution for categories (compare `updated_at` timestamps).
5. Implement name-based category deduplication (merge by name).
6. Handle body diff conflicts: discard losing side's diff, apply winner's.
7. Create `sync_conflicts` table and logging.
8. Write conflict scenario tests (including body diff conflict cases).

### Phase 4: Active & Passive Sync Client

**Goal:** Spokes can autonomously sync with the hub.

1. Create `SyncConfig` and config file/env var loading.
2. Create `sync_state` table for tracking sync progress.
3. Implement `runSyncCycle()` (pull + resolve conflicts + push + reconcile).
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

### 8. Body Diff Patch Failure

A spoke receives a body diff that cannot be applied cleanly to its local body state
(e.g., the local state diverged due to a conflict that was resolved differently).

**Handling:** The spoke requests a full snapshot from the hub via
`GET /api/v1/sync/snapshot?entity_type=note&entity_guid=<guid>`. The snapshot
contains the complete current body from the hub, bypassing the diff mechanism
entirely. The spoke replaces its local body with the snapshot. This is a rare
fallback path -- under normal operation, diffs apply cleanly because pull-first
ensures the spoke has the correct base state before applying diffs.

### 9. Subcategory Rename/Reorganization

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
- **Pull-first sync (rebase model)** -- spokes pull, resolve conflicts locally, then
  push cleanly. Keeps the hub simple and mirrors the familiar git rebase workflow.
- **`authored_at` preservation** -- authoring timestamps are never overwritten during
  sync, ensuring correct LWW conflict resolution across machines
- **Body diffs** -- note body changes stored as unified diffs instead of full
  snapshots, massively reducing change log storage for incremental edits
- **Active polling + passive startup reconciliation** for comprehensive coverage
- **Spoke-side LWW conflict resolution** appropriate for single-user scenarios
- **Phased implementation** starting with the data foundation and building up to
  production-ready sync
