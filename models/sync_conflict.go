package models

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Sync Conflict Detection & Resolution (Phase 3)
//
// When pulling changes from the hub, a spoke may find that it has pending
// local changes on the same entity. This file detects those conflicts and
// applies automatic resolution rules:
//
//   1. Delete-wins: if either side deleted the entity, the delete takes priority.
//      Rationale: deletes are intentional and rare; resurrecting deleted content
//      surprises users more than losing an edit.
//
//   2. Last-Writer-Wins (LWW) on authored_at: the change with the more recent
//      authored_at timestamp wins. This works well for single-user sync across
//      multiple devices because the user's most recent edit is the one they
//      expect to see.
//
// All conflicts are logged to the sync_conflicts table for audit/debugging.
// ============================================================================

// SyncConflict records a detected conflict and its resolution for auditing.
// Even though resolution is automatic, persisting the history helps diagnose
// unexpected data states after the fact.
type SyncConflict struct {
	ID           int64
	EntityType   string    // "note" or "category"
	EntityGUID   string    // GUID of the conflicting entity
	LocalChange  string    // JSON-serialized local SyncChange
	RemoteChange string    // JSON-serialized remote SyncChange
	Resolution   string    // "local_wins", "remote_wins", "delete_wins", "dedup_category"
	ResolvedAt   time.Time // When the conflict was resolved
	CreatedAt    time.Time // When the conflict was detected
}

// DDL for the sync_conflicts table and its auto-increment sequence.

const DDLCreateSyncConflictsSequence = `
CREATE SEQUENCE IF NOT EXISTS sync_conflicts_id_seq START 1;
`

const DDLCreateSyncConflictsTable = `
CREATE TABLE IF NOT EXISTS sync_conflicts (
    id             BIGINT PRIMARY KEY DEFAULT nextval('sync_conflicts_id_seq'),
    entity_type    VARCHAR NOT NULL,
    entity_guid    VARCHAR NOT NULL,
    local_change   VARCHAR,
    remote_change  VARCHAR,
    resolution     VARCHAR NOT NULL,
    resolved_at    TIMESTAMP,
    created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
`

const DDLCreateSyncConflictsIndexEntityGUID = `
CREATE INDEX IF NOT EXISTS idx_sync_conflicts_entity_guid ON sync_conflicts(entity_guid);
`

// DetectNoteConflict checks whether there are pending local (non-sync)
// changes for the same note that the remote change targets.
// Returns the most recent local change if a conflict exists, nil otherwise.
func DetectNoteConflict(remoteChange SyncChange) (*NoteChange, error) {
	pending, err := GetPendingNoteChanges(remoteChange.EntityGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to check pending note changes")
	}
	if len(pending) == 0 {
		return nil, nil // No conflict — safe to apply remote change directly
	}

	// Return the most recent local change for resolution comparison.
	// Pending changes are ordered by created_at ASC, so the last entry
	// is the most recent human edit.
	return &pending[len(pending)-1], nil
}

// DetectCategoryConflict checks whether there are pending local (non-sync)
// changes for the same category that the remote change targets.
func DetectCategoryConflict(remoteChange SyncChange) (*CategoryChange, error) {
	pending, err := GetPendingCategoryChanges(remoteChange.EntityGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to check pending category changes")
	}
	if len(pending) == 0 {
		return nil, nil
	}

	return &pending[len(pending)-1], nil
}

// ResolveConflict applies the resolution rules and returns the winning change.
//
// Resolution order:
//  1. Delete-wins: if either side is a delete, the delete wins.
//  2. LWW on authored_at: whichever change has the later authored_at wins.
//     If timestamps are equal, remote wins to preserve hub authority.
//
// The resolution string describes which rule was applied, e.g.
// "delete_wins_remote", "delete_wins_local", "lww_remote", "lww_local".
func ResolveConflict(local, remote SyncChange) (winner SyncChange, resolution string, err error) {
	// Rule 1: Delete-wins — intentional destructive actions take priority
	if remote.Operation == OperationDelete {
		return remote, "delete_wins_remote", nil
	}
	if local.Operation == OperationDelete {
		return local, "delete_wins_local", nil
	}

	// Rule 2: LWW on authored_at — most recent human edit wins.
	// Using After (not Before) so that equal timestamps default to remote,
	// giving hub authority when clocks are in sync.
	if local.AuthoredAt.After(remote.AuthoredAt) {
		return local, "lww_local", nil
	}

	return remote, "lww_remote", nil
}

// DeduplicateCategoryByName handles the case where both hub and spoke
// created a category with the same name independently. We keep the remote
// (hub) GUID as canonical and remap any local note_categories rows.
func DeduplicateCategoryByName(localGUID, remoteGUID string) error {
	// Look up the local category's ID so we can remap note_categories
	var localID int64
	err := db.QueryRow(`SELECT id FROM categories WHERE guid = ?`, localGUID).Scan(&localID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil // Local category already gone, nothing to deduplicate
		}
		return serr.Wrap(err, "failed to look up local category for dedup")
	}

	// Look up the remote category's ID (should exist after sync apply)
	var remoteID int64
	err = db.QueryRow(`SELECT id FROM categories WHERE guid = ?`, remoteGUID).Scan(&remoteID)
	if err != nil {
		return serr.Wrap(err, "failed to look up remote category for dedup")
	}

	// Remap note_categories from localID → remoteID.
	// Use INSERT OR IGNORE semantics: if the mapping already exists for
	// the remote category, skip. Then delete the old mapping.
	rows, err := db.Query(
		`SELECT note_id, subcategories FROM note_categories WHERE category_id = ?`, localID,
	)
	if err != nil {
		return serr.Wrap(err, "failed to query note_categories for dedup")
	}
	defer rows.Close()

	for rows.Next() {
		var noteID int64
		var subcategories sql.NullString
		if err := rows.Scan(&noteID, &subcategories); err != nil {
			return serr.Wrap(err, "failed to scan note_category for dedup")
		}

		// Check if the mapping already exists for the remote category
		var exists int
		err = db.QueryRow(
			`SELECT COUNT(*) FROM note_categories WHERE note_id = ? AND category_id = ?`,
			noteID, remoteID,
		).Scan(&exists)
		if err != nil {
			return serr.Wrap(err, "failed to check existing mapping for dedup")
		}

		if exists == 0 {
			_, err = db.Exec(
				`INSERT INTO note_categories (note_id, category_id, subcategories, created_at)
				 VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
				noteID, remoteID, subcategories,
			)
			if err != nil {
				return serr.Wrap(err, "failed to insert remapped note_category")
			}
		}
	}
	if err := rows.Err(); err != nil {
		return serr.Wrap(err, "error iterating note_categories for dedup")
	}

	// Delete the local category's mappings and the category itself
	_, err = db.Exec(`DELETE FROM note_categories WHERE category_id = ?`, localID)
	if err != nil {
		return serr.Wrap(err, "failed to delete old note_categories during dedup")
	}
	_, err = db.Exec(`DELETE FROM categories WHERE id = ?`, localID)
	if err != nil {
		return serr.Wrap(err, "failed to delete old category during dedup")
	}

	// Mirror changes to cache
	if cacheDB != nil {
		_, _ = cacheDB.Exec(`DELETE FROM note_categories WHERE category_id = ?`, localID)
		_, _ = cacheDB.Exec(`DELETE FROM categories WHERE id = ?`, localID)
	}

	logger.Info("Deduplicated category by name",
		"local_guid", localGUID,
		"remote_guid", remoteGUID,
		"local_id", localID,
		"remote_id", remoteID,
	)

	return nil
}

// InsertSyncConflict logs a conflict to the sync_conflicts table.
// Errors are logged but not propagated — conflict logging should never
// block the sync cycle.
func InsertSyncConflict(entityType, entityGUID string, local, remote SyncChange, resolution string) {
	localJSON, err := json.Marshal(local)
	if err != nil {
		logger.LogErr(err, "failed to marshal local change for conflict log")
		localJSON = []byte("{}")
	}
	remoteJSON, err := json.Marshal(remote)
	if err != nil {
		logger.LogErr(err, "failed to marshal remote change for conflict log")
		remoteJSON = []byte("{}")
	}

	_, err = db.Exec(
		`INSERT INTO sync_conflicts (entity_type, entity_guid, local_change, remote_change, resolution, resolved_at)
		 VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		entityType, entityGUID, string(localJSON), string(remoteJSON), resolution,
	)
	if err != nil {
		logger.LogErr(err, "failed to insert sync conflict record",
			"entity_type", entityType,
			"entity_guid", entityGUID,
			"resolution", resolution,
		)
	}
}

// GetPendingNoteChanges returns local (non-sync) changes for a note entity
// that have not been pushed to any peer. These represent human edits made
// on this device since the last sync.
//
// Filters: operation != OperationSync (9) — excludes changes received from peers.
// Ordered by created_at ASC for chronological replay.
func GetPendingNoteChanges(entityGUID string) ([]NoteChange, error) {
	query := `
		SELECT id, guid, note_guid, operation, note_fragment_id, "user", created_at
		FROM note_changes
		WHERE note_guid = ? AND operation != ?
		ORDER BY created_at ASC
	`

	rows, err := db.Query(query, entityGUID, OperationSync)
	if err != nil {
		return nil, serr.Wrap(err, "failed to query pending note changes")
	}
	defer rows.Close()

	var changes []NoteChange
	for rows.Next() {
		var c NoteChange
		if err := rows.Scan(&c.ID, &c.GUID, &c.NoteGUID, &c.Operation, &c.NoteFragmentID, &c.User, &c.CreatedAt); err != nil {
			return nil, serr.Wrap(err, "failed to scan pending note change")
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating pending note changes")
	}

	return changes, nil
}

// GetPendingCategoryChanges returns local (non-sync) changes for a category
// entity, mirroring GetPendingNoteChanges.
func GetPendingCategoryChanges(entityGUID string) ([]CategoryChange, error) {
	query := `
		SELECT id, guid, category_guid, operation, category_fragment_id, "user", created_at
		FROM category_changes
		WHERE category_guid = ? AND operation != ?
		ORDER BY created_at ASC
	`

	rows, err := db.Query(query, entityGUID, OperationSync)
	if err != nil {
		return nil, serr.Wrap(err, "failed to query pending category changes")
	}
	defer rows.Close()

	var changes []CategoryChange
	for rows.Next() {
		var c CategoryChange
		if err := rows.Scan(&c.ID, &c.GUID, &c.CategoryGUID, &c.Operation, &c.CategoryFragmentID, &c.User, &c.CreatedAt); err != nil {
			return nil, serr.Wrap(err, "failed to scan pending category change")
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating pending category changes")
	}

	return changes, nil
}
