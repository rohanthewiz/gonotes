# GoNotes Peer-to-Peer Sync Mechanism

## Overview

GoNotes implements a **peer-to-peer note synchronization system** based on change tracking and delta synchronization. The system ensures **eventual consistency** across all peers, where each peer maintains awareness of other peers and tracks sync progress using sync points.

## Core Principles

### 1. Peer Tracking by GUID
- Each peer is uniquely identified by a **GUID**
- Peers track which other peers they're aware of
- Each peer relationship maintains an independent sync state

### 2. Sync Point Tracking
- For each known peer, the system tracks a **sync point**
- The sync point marks the latest changes received from that peer
- Implemented via the `note_change_sync_peers` table
- Prevents duplicate transmission of changes

### 3. GUID-Based Note Syncing
- Notes are uniquely identified by **GUID** (not database ID)
- GUIDs enable consistent identification across different peer databases
- Allows notes created on different peers to be properly synchronized

### 4. Eventual Consistency
- System reaches **steady state** where all peers have all notes from all known peers
- Multiple sync rounds propagate changes through the network
- Convergence is guaranteed in a connected peer graph

## Database Schema

### Change Tracking Tables

#### `note_changes`
Tracks every modification to notes for sync purposes.

```sql
CREATE TABLE note_changes (
    id               BIGINT PRIMARY KEY,
    guid             VARCHAR NOT NULL UNIQUE,    -- Unique change identifier
    note_guid        VARCHAR NOT NULL,           -- Which note was modified
    operation        INTEGER NOT NULL,           -- 1:Create, 2:Update, 3:Delete, 9:Sync
    note_fragment_id BIGINT,                     -- FK to delta (null for deletes)
    user             VARCHAR,                    -- User who made the change
    created_at       TIMESTAMP                   -- Immutable timestamp
);
```

**Operations:**
- `1` = **Create** - Note was created
- `2` = **Update** - Note was modified
- `3` = **Delete** - Note was deleted (soft delete)
- `9` = **Sync** - Change received from peer

#### `note_fragments`
Stores **delta information** - only the fields that changed.

```sql
CREATE TABLE note_fragments (
    id          BIGINT PRIMARY KEY,
    bitmask     SMALLINT NOT NULL,   -- Indicates which fields are active
    title       VARCHAR,             -- New title (if changed)
    description VARCHAR,             -- New description (if changed)
    body        VARCHAR,             -- New body (if changed)
    tags        VARCHAR,             -- New tags (if changed)
    is_private  BOOLEAN,             -- New privacy value (if changed)
    categories  VARCHAR              -- JSON array of category changes
);
```

**Bitmask flags:**
```go
FragmentTitle       = 0x80  // bit 7 - Title changed
FragmentDescription = 0x40  // bit 6 - Description changed
FragmentBody        = 0x20  // bit 5 - Body changed
FragmentTags        = 0x10  // bit 4 - Tags changed
FragmentIsPrivate   = 0x08  // bit 3 - Privacy changed
FragmentCategories  = 0x04  // bit 2 - Categories changed
```

**Benefits:**
- Bandwidth efficiency - only transmit what changed
- Storage efficiency - compact representation
- Conflict resolution - know exactly what was modified

#### `note_change_sync_peers`
Tracks which peers have received which changes.

```sql
CREATE TABLE note_change_sync_peers (
    note_change_id BIGINT NOT NULL,  -- FK to note_changes
    peer_id        VARCHAR NOT NULL, -- Unique peer identifier (GUID)
    synced_at      TIMESTAMP,        -- When synced to peer
    PRIMARY KEY (note_change_id, peer_id)
);
```

**Purpose:**
- Prevents duplicate sends to the same peer
- Enables efficient query: "What changes hasn't Peer X received?"
- Tracks sync progress per peer relationship

## Sync Algorithm

### Phase 1: Change Detection
```go
// Get all changes not yet sent to target peer
changes := GetUnsentChangesForPeer(targetPeerGUID, limit)
```

This query finds changes where:
- No entry exists in `note_change_sync_peers` for this peer
- Ordered by `created_at` (oldest first) for proper replay

### Phase 2: Change Transmission
For each unsent change:
1. **Package the change** with its fragment (if exists)
2. **Transmit to target peer** (HTTP, WebSocket, etc.)
3. **Target validates** the change
4. **Target applies** the change to local database
5. **Target confirms** receipt

### Phase 3: Sync Acknowledgment
```go
// Mark change as successfully synced to peer
MarkChangeSyncedToPeer(changeID, peerGUID)
```

This creates a record in `note_change_sync_peers`, preventing re-transmission.

### Phase 4: Applying Received Changes
When a peer receives a change:

1. **Validate** change integrity (signature, schema, etc.)
2. **Check for conflicts** (multiple changes to same note)
3. **Apply change** based on operation:
   - **CREATE**: Insert new note with provided GUID
   - **UPDATE**: Apply delta fragment to existing note
   - **DELETE**: Soft delete note (set `deleted_at`)
4. **Record sync operation** in local `note_changes` (operation = 9)
5. **Send acknowledgment** back to source peer

## Integration Test Explanation

The integration test (`peer_sync_integration_test.go`) simulates a complete 3-peer sync scenario.

### Test Structure

#### Setup: 3 Peer Simulators
```go
peer1 := PeerSimulator{GUID: "peer-guid-001", UserGUID: "user-peer1", Name: "Peer1"}
peer2 := PeerSimulator{GUID: "peer-guid-002", UserGUID: "user-peer2", Name: "Peer2"}
peer3 := PeerSimulator{GUID: "peer-guid-003", UserGUID: "user-peer3", Name: "Peer3"}
```

Each peer represents an independent node with:
- Unique **peer GUID** (identifies the peer/device)
- Unique **user GUID** (identifies the user on that peer)
- Human-readable **name** for logging

### Test Phases

#### Phase 1: Initial Note Creation
Each peer creates 2 notes locally:
- **Peer 1**: `peer1-note1`, `peer1-note2`
- **Peer 2**: `peer2-note1`, `peer2-note2`
- **Peer 3**: `peer3-note1`, `peer3-note2`

**State after Phase 1:**
- 6 total notes in system
- Each peer has 2 notes
- Each peer has 2 change records (local creates)

#### Phase 2: First Sync Round
Bidirectional sync between all peer pairs:
```
Peer1 ↔ Peer2
Peer2 ↔ Peer3
Peer1 ↔ Peer3
```

**Sync operations:**
1. `syncPeerToPeer(peer1, peer2)` - Peer2 receives Peer1's notes
2. `syncPeerToPeer(peer2, peer1)` - Peer1 receives Peer2's notes
3. `syncPeerToPeer(peer2, peer3)` - Peer3 receives Peer2's notes
4. `syncPeerToPeer(peer3, peer2)` - Peer2 receives Peer3's notes
5. `syncPeerToPeer(peer1, peer3)` - Peer3 receives Peer1's notes
6. `syncPeerToPeer(peer3, peer1)` - Peer1 receives Peer3's notes

**State after Phase 2:**
- All peers have knowledge of all 6 notes
- Each peer tracks sync state with other peers

#### Phase 3: Verification
Verify each peer has received all changes:
```go
verifyPeerHasAllNoteChanges(t, peer1, 6, "after first sync round")
verifyPeerHasAllNoteChanges(t, peer2, 6, "after first sync round")
verifyPeerHasAllNoteChanges(t, peer3, 6, "after first sync round")
```

#### Phase 4: Update Propagation
Peer 1 updates one of its notes:
```go
updateTestNote(t, peer1, note1P1.ID, "Updated Title", "Updated content")
```

This creates a new change record (operation = UPDATE) with delta fragment.

#### Phase 5: Second Sync Round
Propagate the update to other peers:
```go
syncPeerToPeer(t, peer1, peer2)  // Peer2 receives update
syncPeerToPeer(t, peer1, peer3)  // Peer3 receives update
```

#### Phase 6: Delete Propagation
Peer 2 deletes a note:
```go
deleteTestNote(t, peer2, note2P2.ID)
```

Creates a DELETE change record (no fragment needed).

#### Phase 7: Third Sync Round
Propagate the delete to other peers:
```go
syncPeerToPeer(t, peer2, peer1)  // Peer1 receives delete
syncPeerToPeer(t, peer2, peer3)  // Peer3 receives delete
```

#### Phase 8: Steady State Verification
Verify no unsent changes remain between any peer pair:
```go
verifyNoUnsentChanges(t, peer1, peer2)
verifyNoUnsentChanges(t, peer1, peer3)
verifyNoUnsentChanges(t, peer2, peer1)
verifyNoUnsentChanges(t, peer2, peer3)
verifyNoUnsentChanges(t, peer3, peer1)
verifyNoUnsentChanges(t, peer3, peer2)
```

**Steady State Criteria:**
- ✓ No peer has unsent changes to any other peer
- ✓ All peers have identical note state (same notes, same content)
- ✓ System has converged to consistent state

## Additional Test Scenarios

### 1. Timestamp-Based Sync Points
`TestPeerSyncWithTimestamps` demonstrates:
- Recording sync checkpoint timestamps
- Querying changes since last sync
- Incremental sync windows

### 2. Conflict Detection
`TestPeerSyncConflictDetection` demonstrates:
- Detecting when peers modify the same note
- Identifying concurrent modifications
- Foundation for conflict resolution strategies

**Conflict Resolution Strategies:**
- **Last-write-wins** - Most recent timestamp wins
- **Vector clocks** - Track causality relationships
- **Manual resolution** - User chooses which version to keep
- **Operational transformation** - Merge concurrent edits
- **CRDTs** - Conflict-free replicated data types

### 3. Multi-Round Convergence
`TestMultiRoundSyncConvergence` demonstrates:
- Iterative syncing until no changes remain
- Guaranteed convergence in connected graphs
- Automatic detection of steady state

## Running the Tests

```bash
# Run all peer sync integration tests
go test -v -run TestPeerSync ./models/

# Run specific test
go test -v -run TestThreePeerSyncIntegration ./models/

# Run with race detection
go test -race -v -run TestPeerSync ./models/

# Run convergence test
go test -v -run TestMultiRoundSyncConvergence ./models/
```

## Expected Output

```
=== Starting 3-Peer Sync Integration Test ===

--- Phase 1: Creating initial notes on each peer ---
Peer1 created notes: peer1-note1, peer1-note2
Peer2 created notes: peer2-note1, peer2-note2
Peer3 created notes: peer3-note1, peer3-note2

--- Phase 2: First sync round - Peer1 ↔ Peer2 ↔ Peer3 ---
  Peer1 → Peer2: Syncing 2 changes
    ✓ Successfully synced 2 changes
  Peer2 → Peer1: Syncing 2 changes
    ✓ Successfully synced 2 changes
  [... additional sync operations ...]

--- Phase 8: Final verification of steady state ---
  ✓ Peer1 → Peer2: No unsent changes (in sync)
  ✓ Peer1 → Peer3: No unsent changes (in sync)
  ✓ Peer2 → Peer1: No unsent changes (in sync)
  ✓ Peer2 → Peer3: No unsent changes (in sync)
  ✓ Peer3 → Peer1: No unsent changes (in sync)
  ✓ Peer3 → Peer3: No unsent changes (in sync)

=== SUCCESS: All peers reached steady state ===
Total changes tracked: 8 (6 creates + 1 update + 1 delete)
```

## Key Functions Reference

### Core Sync Functions (models/note_change.go)

#### `GetUnsentChangesForPeer(peerID string, limit int) ([]NoteChange, error)`
Returns changes not yet synced to the specified peer.

**Query:**
```sql
SELECT * FROM note_changes nc
WHERE nc.id NOT IN (
    SELECT note_change_id
    FROM note_change_sync_peers
    WHERE peer_id = ?
)
ORDER BY nc.created_at ASC
LIMIT ?
```

#### `MarkChangeSyncedToPeer(noteChangeID int64, peerID string) error`
Records that a change was successfully synced to a peer.

**Insert:**
```sql
INSERT INTO note_change_sync_peers (note_change_id, peer_id)
VALUES (?, ?)
```

#### `GetNoteChangeWithFragment(changeID int64) (*NoteChangeOutput, error)`
Retrieves a complete change with its delta fragment for transmission.

**Returns:**
```go
type NoteChangeOutput struct {
    ID             int64
    GUID           string
    NoteGUID       string
    Operation      int32
    NoteFragmentID sql.NullInt64
    Fragment       *NoteFragment    // Includes delta details
    User           sql.NullString
    CreatedAt      time.Time
}
```

#### `GetUserChangesSince(userGUID string, since time.Time, limit int) ([]NoteChangeOutput, error)`
Retrieves all changes for a user since a specific timestamp.

**Use case:** Client requesting changes since last sync.

## Production Implementation Checklist

To implement full peer-to-peer sync in production:

- [ ] **Peer Discovery** - How peers find each other (DNS, DHT, central registry)
- [ ] **Network Transport** - HTTP/REST, WebSockets, gRPC, or custom protocol
- [ ] **Authentication** - Verify peer identity and permissions
- [ ] **Encryption** - TLS for transport, optionally E2E encryption
- [ ] **Conflict Resolution** - Choose strategy (LWW, vector clocks, CRDTs)
- [ ] **Offline Support** - Queue changes when peer is unreachable
- [ ] **Retry Logic** - Exponential backoff for failed syncs
- [ ] **Batch Operations** - Sync multiple changes in single request
- [ ] **Compression** - Reduce bandwidth (gzip, brotli)
- [ ] **Progress Tracking** - Show sync status to user
- [ ] **Error Handling** - Graceful degradation, rollback on failure
- [ ] **Testing** - Integration tests, chaos testing, network partitions
- [ ] **Monitoring** - Metrics for sync latency, throughput, errors
- [ ] **Schema Versioning** - Handle peers with different database versions

## Sync Protocol Example

### Request: Get Changes for Peer
```
GET /api/v1/sync/changes?since=2024-01-18T10:00:00Z&limit=100
Authorization: Bearer <peer-jwt>
```

### Response: Change List
```json
[
  {
    "id": 42,
    "guid": "change-uuid-1",
    "note_guid": "note-uuid-1",
    "operation": 1,
    "fragment": {
      "bitmask": 240,
      "title": "New Note Title",
      "body": "Note content here"
    },
    "user": "user-uuid-1",
    "created_at": "2024-01-18T10:15:00Z"
  }
]
```

### Request: Acknowledge Sync
```
POST /api/v1/sync/ack
Content-Type: application/json

{
  "peer_id": "peer-guid-002",
  "change_ids": [42, 43, 44]
}
```

## Performance Considerations

### Bandwidth Optimization
- **Delta sync** - Only transmit changed fields (via fragments)
- **Batching** - Send multiple changes in one request
- **Compression** - gzip/brotli compression
- **Selective sync** - Sync only subscribed notes/categories

### Storage Optimization
- **Change cleanup** - Archive/delete old changes after all peers synced
- **Fragment deduplication** - Reuse fragments for identical changes
- **Soft delete retention** - Configurable retention period

### Query Optimization
- **Indexes** - `created_at`, `note_guid`, `peer_id`
- **Pagination** - Limit query results
- **Caching** - Cache recent change lists

## Security Considerations

### Authentication
- Verify peer identity via JWT or mutual TLS
- Rotate keys regularly
- Revoke compromised peers

### Authorization
- Peers can only sync notes they have permission to access
- Respect privacy flags (`is_private`)
- Filter changes by user permissions

### Data Integrity
- Validate change signatures
- Verify fragment checksums
- Reject malformed data

### Privacy
- Encrypt private notes end-to-end
- Don't sync private notes to untrusted peers
- Audit log all sync operations

## Future Enhancements

1. **Partial Sync** - Sync only specific categories/tags
2. **Mesh Networking** - Peers relay changes for offline peers
3. **WebRTC** - Peer-to-peer without central server
4. **Operational Transform** - Real-time collaborative editing
5. **Attachment Sync** - Binary file synchronization
6. **Schema Migration** - Handle database version differences
7. **Peer Reputation** - Trust scores for peers
8. **Rate Limiting** - Prevent abuse/DoS attacks

## References

- **Change Tracking**: `models/note_change.go`
- **Note Model**: `models/note.go`
- **Database Schema**: `models/db.go`
- **Sync API**: `web/api/sync.go`
- **Integration Tests**: `models/peer_sync_integration_test.go`
- **Unit Tests**: `models/note_change_test.go`

---

**Summary:** GoNotes implements a robust peer-to-peer sync system based on change tracking, delta synchronization, and eventual consistency. The integration test validates that 3 peers can successfully synchronize all notes and reach a steady state where all peers have identical data.
