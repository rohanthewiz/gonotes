# Plan: NoteChange Model for Peer-to-Peer Sync

## Purpose
Track note changes (create, update, delete) with delta/diff storage for efficient peer-to-peer syncing. Each change records only what changed (using bitmask pattern from older project), and tracks which peers have received each change.

## Key Design Decisions
- **Delta storage**: NoteFragment stores only changed fields with bitmask indicator
- **Automatic tracking**: CRUD functions record changes automatically
- **Per-peer tracking**: Separate table tracks which peers received each change
- **Disk-only**: No cache needed (sync data is write-heavy, read-infrequent)
- **Non-blocking**: Change tracking failures don't block CRUD operations

---

## Data Models

### NoteChange Struct
```go
type NoteChange struct {
    ID             int64          // Primary key
    GUID           string         // Unique identifier for this change
    NoteGUID       string         // GUID of the affected note
    Operation      int32          // 1: Create, 2: Update, 3: Delete, 9: Sync
    NoteFragmentID sql.NullInt64  // FK to note_fragments (null for deletes)
    User           sql.NullString // User who made the change
    CreatedAt      time.Time      // Immutable timestamp
}

const (
    OperationCreate = 1
    OperationUpdate = 2
    OperationDelete = 3
    OperationSync   = 9  // Change received from peer
)
```

### NoteFragment Struct (Delta Storage)
```go
type NoteFragment struct {
    ID          int64          // Primary key
    Bitmask     int16          // Indicates which fields are active
    Title       sql.NullString // New title (if changed)
    Description sql.NullString // New description (if changed)
    Body        sql.NullString // New body (if changed)
    Tags        sql.NullString // New tags (if changed)
    IsPrivate   sql.NullBool   // New privacy value (if changed)
    Categories  sql.NullString // JSON array of category changes
}

// Bitmask constants
const (
    FragmentTitle       = 0x80 // 128
    FragmentDescription = 0x40 // 64
    FragmentBody        = 0x20 // 32
    FragmentTags        = 0x10 // 16
    FragmentIsPrivate   = 0x08 // 8
    FragmentCategories  = 0x04 // 4
)
```

### NoteChangeSyncPeer (Per-Peer Tracking)
```go
type NoteChangeSyncPeer struct {
    NoteChangeID int64     // FK to note_changes
    PeerID       string    // Unique peer identifier
    SyncedAt     time.Time // When synced to peer
}
```

---

## SQL Schema

### `models/note_change.go` - Add DDL constants:

```sql
-- note_fragments table
CREATE SEQUENCE IF NOT EXISTS note_fragments_id_seq START 1;

CREATE TABLE IF NOT EXISTS note_fragments (
    id          BIGINT PRIMARY KEY DEFAULT nextval('note_fragments_id_seq'),
    bitmask     SMALLINT NOT NULL,
    title       VARCHAR,
    description VARCHAR,
    body        VARCHAR,
    tags        VARCHAR,
    is_private  BOOLEAN,
    categories  VARCHAR
);

-- note_changes table
CREATE SEQUENCE IF NOT EXISTS note_changes_id_seq START 1;

CREATE TABLE IF NOT EXISTS note_changes (
    id               BIGINT PRIMARY KEY DEFAULT nextval('note_changes_id_seq'),
    guid             VARCHAR NOT NULL UNIQUE,
    note_guid        VARCHAR NOT NULL,
    operation        INTEGER NOT NULL,
    note_fragment_id BIGINT,
    user             VARCHAR,
    created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (note_fragment_id) REFERENCES note_fragments(id)
);

CREATE INDEX IF NOT EXISTS idx_note_changes_note_guid ON note_changes(note_guid);
CREATE INDEX IF NOT EXISTS idx_note_changes_created_at ON note_changes(created_at);

-- note_change_sync_peers table
CREATE TABLE IF NOT EXISTS note_change_sync_peers (
    note_change_id BIGINT NOT NULL,
    peer_id        VARCHAR NOT NULL,
    synced_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (note_change_id, peer_id),
    FOREIGN KEY (note_change_id) REFERENCES note_changes(id)
);

CREATE INDEX IF NOT EXISTS idx_note_change_sync_peers_peer_id ON note_change_sync_peers(peer_id);
```

---

## Implementation Steps

### 1. Create `models/note_change.go`
New file with:
- Struct definitions (NoteChange, NoteFragment, NoteChangeSyncPeer)
- Constants (Operation codes, Bitmask values)
- SQL DDL constants
- Helper functions:
  - `computeChangeBitmask(existing *Note, input NoteInput) int16`
  - `createFragmentFromInput(input NoteInput, bitmask int16) NoteFragment`
  - `createDeltaFragment(input NoteInput, bitmask int16) NoteFragment`
  - `insertNoteFragment(fragment NoteFragment) (int64, error)`
  - `insertNoteChange(changeGUID, noteGUID string, operation int32, fragmentID sql.NullInt64, user string) error`
  - `GenerateChangeGUID() string`
- Sync functions:
  - `MarkChangeSyncedToPeer(noteChangeID int64, peerID string) error`
  - `GetUnsentChangesForPeer(peerID string, limit int) ([]NoteChange, error)`
  - `GetNoteFragment(id int64) (*NoteFragment, error)`
- Output types for API

### 2. Modify `models/db.go`
In `createTables()`, add table creation (order matters for FK):
1. `note_fragments` table
2. `note_changes` table
3. `note_change_sync_peers` table

### 3. Modify `models/note.go` - CreateNote
After successful disk insert (~line 227), add:
```go
// Record change for sync (non-blocking)
fragment := createFragmentFromInput(input, FragmentTitle|FragmentDescription|FragmentBody|FragmentTags|FragmentIsPrivate)
if fragmentID, err := insertNoteFragment(fragment); err != nil {
    logger.LogErr(err, "failed to record note fragment", "note_guid", input.GUID)
} else {
    if err := insertNoteChange(GenerateChangeGUID(), input.GUID, OperationCreate, sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
        logger.LogErr(err, "failed to record note change", "note_guid", input.GUID)
    }
}
```

### 4. Modify `models/note.go` - UpdateNote
Before disk update, fetch existing note. After successful disk update (~line 432):
```go
bitmask := computeChangeBitmask(existing, input)
if bitmask != 0 {
    fragment := createDeltaFragment(input, bitmask)
    if fragmentID, err := insertNoteFragment(fragment); err != nil {
        logger.LogErr(err, "failed to record update fragment", "note_id", id)
    } else {
        if err := insertNoteChange(GenerateChangeGUID(), existing.GUID, OperationUpdate, sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
            logger.LogErr(err, "failed to record update change", "note_id", id)
        }
    }
}
```

### 5. Modify `models/note.go` - DeleteNote
Before soft delete, get note GUID. After successful disk update (~line 486):
```go
if err := insertNoteChange(GenerateChangeGUID(), noteGUID, OperationDelete, sql.NullInt64{}, ""); err != nil {
    logger.LogErr(err, "failed to record delete change", "note_id", id)
}
```

### 6. Add UUID dependency
```bash
go get github.com/google/uuid
```

### 7. Create `models/note_change_test.go`
Test cases:
- `TestNoteChangeOnCreate` - verify change recorded on create
- `TestNoteChangeOnUpdate` - verify delta/bitmask correct
- `TestNoteChangeOnDelete` - verify delete change (no fragment)
- `TestUnsentChangesForPeer` - verify peer filtering
- `TestMarkChangeSyncedToPeer` - verify sync tracking
- `TestChangeBitmaskComputation` - unit test bitmask logic

---

## Files to Create/Modify
| File | Action |
|------|--------|
| `models/note_change.go` | CREATE - structs, DDL, functions |
| `models/db.go` | MODIFY - add table creation |
| `models/note.go` | MODIFY - add change tracking to CRUD |
| `models/note_change_test.go` | CREATE - tests |
| `go.mod` | MODIFY - add uuid dependency |

---

## Verification Steps
1. Run `go get github.com/google/uuid`
2. Run existing tests: `go test ./...` (should pass unchanged)
3. Implement changes incrementally
4. After implementation: `go test ./...`
5. Manual verification:
   - Create a note → verify `note_changes` and `note_fragments` records
   - Update a note → verify delta recorded (only changed fields in fragment)
   - Delete a note → verify change recorded with no fragment
   - Query `GetUnsentChangesForPeer("peer1", 10)` → should return all changes
   - Call `MarkChangeSyncedToPeer(id, "peer1")` → change excluded from next query
