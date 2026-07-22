package models

import (
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Sync Conflict Detection & Resolution (Phase 3)
//
// When pulling changes from the hub, a spoke may find that it has pending
// local changes on the same entity. This file detects those conflicts and
// applies automatic resolution rules (delete-wins, then LWW on authored_at)
// and logs every conflict to sync_conflicts (public database) for audit.
//
// Two-database note: note change-tracking is split across the two note
// databases, so pending-note-change lookups fan out and merge; the
// categories catalog, its change-tracking, and sync_conflicts are all in
// the public database.
// ============================================================================

// SyncConflict records a detected conflict and its resolution for auditing.
type SyncConflict struct {
	ID           int64
	EntityType   string
	EntityGUID   string
	LocalChange  string
	RemoteChange string
	Resolution   string
	ResolvedAt   time.Time
	CreatedAt    time.Time
}

// DetectNoteConflict returns the most recent pending local (non-sync)
// change for the note the remote change targets, or nil if none.
func DetectNoteConflict(remoteChange SyncChange) (*NoteChange, error) {
	pending, err := GetPendingNoteChanges(remoteChange.EntityGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to check pending note changes")
	}
	if len(pending) == 0 {
		return nil, nil
	}
	return &pending[len(pending)-1], nil
}

// DetectCategoryConflict returns the most recent pending local (non-sync)
// change for the category the remote change targets, or nil if none.
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

// ResolveConflict applies the resolution rules and returns the winning
// change: delete-wins first, then last-writer-wins on authored_at (equal
// timestamps default to remote, giving hub authority).
func ResolveConflict(local, remote SyncChange) (winner SyncChange, resolution string, err error) {
	if remote.Operation == OperationDelete {
		return remote, "delete_wins_remote", nil
	}
	if local.Operation == OperationDelete {
		return local, "delete_wins_local", nil
	}
	if local.AuthoredAt.After(remote.AuthoredAt) {
		return local, "lww_local", nil
	}
	return remote, "lww_remote", nil
}

// DeduplicateCategoryByName handles hub and spoke both independently
// creating a category with the same name. The remote (hub) GUID is kept as
// canonical; local note_categories rows — which may live in either note
// database — are remapped, then the local category is removed.
func DeduplicateCategoryByName(localGUID, remoteGUID string) error {
	var localID int64
	err := pubDB.QueryRow(`SELECT id FROM categories WHERE guid = ?`, localGUID).Scan(&localID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil // Local category already gone.
		}
		return serr.Wrap(err, "failed to look up local category for dedup")
	}

	var remoteID int64
	if err = pubDB.QueryRow(`SELECT id FROM categories WHERE guid = ?`, remoteGUID).Scan(&remoteID); err != nil {
		return serr.Wrap(err, "failed to look up remote category for dedup")
	}

	// Remap link rows in BOTH note databases (a category can be linked by
	// private and public notes alike).
	for _, en := range []*dbEngine{pubDB, privDB} {
		if err := remapNoteCategoriesInEngine(en, localID, remoteID); err != nil {
			return err
		}
	}

	// Remove the local category from the catalog.
	if _, err = pubDB.Exec(`DELETE FROM categories WHERE id = ?`, localID); err != nil {
		return serr.Wrap(err, "failed to delete old category during dedup")
	}

	logger.Info("Deduplicated category by name",
		"local_guid", localGUID, "remote_guid", remoteGUID, "local_id", localID, "remote_id", remoteID)
	return nil
}

// remapNoteCategoriesInEngine moves note_categories rows from localID to
// remoteID within one engine, skipping rows that already exist, then
// deletes the old rows.
func remapNoteCategoriesInEngine(en *dbEngine, localID, remoteID int64) error {
	rows, err := en.Query(`SELECT note_id, subcategories FROM note_categories WHERE category_id = ?`, localID)
	if err != nil {
		return serr.Wrap(err, "failed to query note_categories for dedup")
	}
	type link struct {
		noteID int64
		subs   sql.NullString
	}
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.noteID, &l.subs); err != nil {
			rows.Close()
			return serr.Wrap(err, "failed to scan note_category for dedup")
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return serr.Wrap(err, "error iterating note_categories for dedup")
	}
	rows.Close()

	for _, l := range links {
		var exists int
		if err := en.QueryRow(`SELECT COUNT(*) FROM note_categories WHERE note_id = ? AND category_id = ?`, l.noteID, remoteID).Scan(&exists); err != nil {
			return serr.Wrap(err, "failed to check existing mapping for dedup")
		}
		if exists == 0 {
			if _, err := en.Exec(
				`INSERT INTO note_categories (note_id, category_id, subcategories, created_at)
				 VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, l.noteID, remoteID, l.subs); err != nil {
				return serr.Wrap(err, "failed to insert remapped note_category")
			}
		}
	}
	if _, err := en.Exec(`DELETE FROM note_categories WHERE category_id = ?`, localID); err != nil {
		return serr.Wrap(err, "failed to delete old note_categories during dedup")
	}
	return nil
}

// InsertSyncConflict logs a conflict to sync_conflicts (public database).
// Errors are logged, never propagated — logging must not block sync.
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

	_, err = pubDB.Exec(
		`INSERT INTO sync_conflicts (id, entity_type, entity_guid, local_change, remote_change, resolution, resolved_at)
		 VALUES (nextval('sync_conflicts_id_seq'), ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		entityType, entityGUID, string(localJSON), string(remoteJSON), resolution,
	)
	if err != nil {
		logger.LogErr(err, "failed to insert sync conflict record",
			"entity_type", entityType, "entity_guid", entityGUID, "resolution", resolution)
	}
}

// GetPendingNoteChanges returns local (non-sync) changes for a note that
// have not been pushed, across both note databases, oldest first.
func GetPendingNoteChanges(entityGUID string) ([]NoteChange, error) {
	query := `
		SELECT id, guid, note_guid, operation, note_fragment_id, change_user, created_at
		FROM note_changes
		WHERE note_guid = ? AND operation != ?
		ORDER BY created_at ASC
	`
	changes, err := queryBothNotes(func(en *dbEngine) ([]NoteChange, error) {
		rows, err := en.Query(query, entityGUID, OperationSync)
		if err != nil {
			return nil, serr.Wrap(err, "failed to query pending note changes")
		}
		defer rows.Close()
		var out []NoteChange
		for rows.Next() {
			var c NoteChange
			if err := scanNoteChange(rows, &c); err != nil {
				return nil, serr.Wrap(err, "failed to scan pending note change")
			}
			out = append(out, c)
		}
		return out, rows.Err()
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].CreatedAt.Before(changes[j].CreatedAt) })
	return changes, nil
}

// GetPendingCategoryChanges returns local (non-sync) changes for a category
// (public database), oldest first.
func GetPendingCategoryChanges(entityGUID string) ([]CategoryChange, error) {
	query := `
		SELECT id, guid, category_guid, operation, category_fragment_id, change_user, created_at
		FROM category_changes
		WHERE category_guid = ? AND operation != ?
		ORDER BY created_at ASC
	`
	rows, err := pubDB.Query(query, entityGUID, OperationSync)
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
