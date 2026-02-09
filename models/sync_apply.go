package models

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// ApplySyncNoteCreate inserts a note from sync data with an explicit authored_at.
// Unlike CreateNote (which auto-generates authored_at via DEFAULT CURRENT_TIMESTAMP),
// this preserves the original authoring timestamp from the source machine so that
// the synced note reflects when it was truly authored, not when it was received.
// Records a change with OperationSync so downstream peers don't re-propagate it.
func ApplySyncNoteCreate(noteGUID, title string, fragment NoteFragment, authoredAt time.Time, userGUID string) (*Note, error) {
	// Extract field values from fragment, falling back to defaults for unset fields
	description := fragment.Description
	body := fragment.Body
	tags := fragment.Tags
	isPrivate := false
	if fragment.IsPrivate.Valid {
		isPrivate = fragment.IsPrivate.Bool
	}

	// If the fragment body is a diff, this is an error for creates — creates need full body.
	// A create should never have a diff (no base to apply it against).
	if fragment.BodyIsDiff {
		return nil, serr.New("cannot apply body diff for note creation — need full body snapshot")
	}

	createdBy := sql.NullString{String: userGUID, Valid: userGUID != ""}

	// Insert into disk DB with explicit authored_at (NOT DEFAULT CURRENT_TIMESTAMP)
	query := `
		INSERT INTO notes (guid, title, description, body, tags, is_private, created_by, updated_by,
		                   authored_at, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		RETURNING id, guid, title, description, body, tags, is_private, encryption_iv,
		          created_by, updated_by, created_at, updated_at, authored_at, synced_at, deleted_at
	`

	note := &Note{}
	err := db.QueryRow(query,
		noteGUID, title, description, body, tags, isPrivate, createdBy, createdBy, authoredAt,
	).Scan(
		&note.ID, &note.GUID, &note.Title, &note.Description, &note.Body,
		&note.Tags, &note.IsPrivate, &note.EncryptionIV, &note.CreatedBy,
		&note.UpdatedBy, &note.CreatedAt, &note.UpdatedAt, &note.AuthoredAt, &note.SyncedAt, &note.DeletedAt,
	)
	if err != nil {
		return nil, serr.Wrap(err, "failed to insert synced note into disk")
	}

	// Record change with OperationSync so it won't be pushed back to the originator
	syncFragment := createFragmentFromInput(NoteInput{
		Title:       title,
		Description: nullStringToPtr(description),
		Body:        nullStringToPtr(body),
		Tags:        nullStringToPtr(tags),
		IsPrivate:   isPrivate,
	}, FragmentTitle|FragmentDescription|FragmentBody|FragmentTags|FragmentIsPrivate)
	if fragmentID, err := insertNoteFragment(syncFragment); err != nil {
		logger.LogErr(err, "failed to record sync note create fragment", "note_guid", noteGUID)
	} else {
		if err := insertNoteChange(GenerateChangeGUID(), noteGUID, OperationSync,
			sql.NullInt64{Int64: fragmentID, Valid: true}, userGUID); err != nil {
			logger.LogErr(err, "failed to record sync note create change", "note_guid", noteGUID)
		}
	}

	// Insert into cache (no authored_at in cache schema)
	cacheQuery := `
		INSERT INTO notes (id, guid, title, description, body, tags, is_private, encryption_iv,
		                   created_by, updated_by, created_at, updated_at, synced_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = cacheDB.Exec(cacheQuery,
		note.ID, note.GUID, note.Title, note.Description, note.Body,
		note.Tags, note.IsPrivate, note.EncryptionIV, note.CreatedBy,
		note.UpdatedBy, note.CreatedAt, note.UpdatedAt, note.SyncedAt, note.DeletedAt,
	)
	if err != nil {
		return note, serr.Wrap(err, "synced note created on disk but cache insert failed")
	}

	return note, nil
}

// ApplySyncNoteUpdate updates a note from sync data, preserving the source authored_at.
// Builds a dynamic SET clause from the fragment bitmask so only changed fields are
// updated. If the fragment body is a diff, it applies the diff against the current body.
func ApplySyncNoteUpdate(noteGUID string, fragment NoteFragment, authoredAt time.Time) error {
	// Get the current note to apply diffs against
	existing, err := GetNoteByGUID(noteGUID)
	if err != nil {
		return serr.Wrap(err, "failed to get existing note for sync update")
	}
	if existing == nil {
		return serr.New("note not found for sync update: " + noteGUID)
	}

	// Build the fields to update dynamically based on the bitmask
	setClauses := []string{}
	args := []interface{}{}

	if fragment.Bitmask&FragmentTitle != 0 && fragment.Title.Valid {
		setClauses = append(setClauses, "title = ?")
		args = append(args, fragment.Title.String)
	}
	if fragment.Bitmask&FragmentDescription != 0 {
		setClauses = append(setClauses, "description = ?")
		args = append(args, fragment.Description)
	}
	if fragment.Bitmask&FragmentBody != 0 {
		// Resolve body: apply diff if needed, otherwise use full snapshot
		var resolvedBody sql.NullString
		if fragment.BodyIsDiff && fragment.Body.Valid {
			currentBody := ""
			if existing.Body.Valid {
				currentBody = existing.Body.String
			}
			newBody, err := applyBodyDiff(currentBody, fragment.Body.String)
			if err != nil {
				return serr.Wrap(err, "failed to apply body diff during sync update")
			}
			resolvedBody = sql.NullString{String: newBody, Valid: true}
		} else {
			resolvedBody = fragment.Body
		}
		setClauses = append(setClauses, "body = ?")
		args = append(args, resolvedBody)
	}
	if fragment.Bitmask&FragmentTags != 0 {
		setClauses = append(setClauses, "tags = ?")
		args = append(args, fragment.Tags)
	}
	if fragment.Bitmask&FragmentIsPrivate != 0 && fragment.IsPrivate.Valid {
		setClauses = append(setClauses, "is_private = ?")
		args = append(args, fragment.IsPrivate.Bool)
	}

	if len(setClauses) == 0 {
		return nil // Nothing to update
	}

	// Always update authored_at (from source) and synced_at (current time)
	setClauses = append(setClauses, "authored_at = ?", "synced_at = CURRENT_TIMESTAMP", "updated_at = CURRENT_TIMESTAMP")
	args = append(args, authoredAt)

	// Build and execute the disk update query
	query := "UPDATE notes SET " + joinStrings(setClauses, ", ") + " WHERE guid = ? AND deleted_at IS NULL"
	args = append(args, noteGUID)

	result, err := db.Exec(query, args...)
	if err != nil {
		return serr.Wrap(err, "failed to update note from sync")
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return serr.New("no rows updated for sync note update: " + noteGUID)
	}

	// Record change with OperationSync
	if fragmentID, err := insertNoteFragment(fragment); err != nil {
		logger.LogErr(err, "failed to record sync update fragment", "note_guid", noteGUID)
	} else {
		if err := insertNoteChange(GenerateChangeGUID(), noteGUID, OperationSync,
			sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
			logger.LogErr(err, "failed to record sync update change", "note_guid", noteGUID)
		}
	}

	// Update cache (mirror the same SET clause, minus authored_at/synced_at)
	// Re-read the note from disk to get the fully resolved state
	diskNote, err := getNoteByGUIDFromDisk(noteGUID)
	if err != nil || diskNote == nil {
		return serr.Wrap(err, "failed to read updated note from disk for cache sync")
	}

	cacheQuery := `
		UPDATE notes SET title = ?, description = ?, body = ?, tags = ?, is_private = ?,
		    updated_at = ?, synced_at = ?
		WHERE guid = ? AND deleted_at IS NULL
	`
	_, err = cacheDB.Exec(cacheQuery,
		diskNote.Title, diskNote.Description, diskNote.Body, diskNote.Tags,
		diskNote.IsPrivate, diskNote.UpdatedAt, diskNote.SyncedAt, noteGUID,
	)
	if err != nil {
		return serr.Wrap(err, "sync note updated on disk but cache update failed")
	}

	return nil
}

// ApplySyncNoteDelete soft-deletes a note received via sync.
// Sets deleted_at on both disk and cache databases.
func ApplySyncNoteDelete(noteGUID string) error {
	// Delete from disk
	result, err := db.Exec(
		`UPDATE notes SET deleted_at = CURRENT_TIMESTAMP, synced_at = CURRENT_TIMESTAMP WHERE guid = ? AND deleted_at IS NULL`,
		noteGUID,
	)
	if err != nil {
		return serr.Wrap(err, "failed to soft-delete synced note from disk")
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		// Note already deleted or doesn't exist — idempotent, not an error
		return nil
	}

	// Record change with OperationSync
	if err := insertNoteChange(GenerateChangeGUID(), noteGUID, OperationDelete,
		sql.NullInt64{}, ""); err != nil {
		logger.LogErr(err, "failed to record sync delete change", "note_guid", noteGUID)
	}

	// Delete from cache
	_, err = cacheDB.Exec(
		`UPDATE notes SET deleted_at = CURRENT_TIMESTAMP WHERE guid = ? AND deleted_at IS NULL`,
		noteGUID,
	)
	if err != nil {
		return serr.Wrap(err, "synced note deleted from disk but cache delete failed")
	}

	return nil
}

// ApplySyncCategoryCreate creates a category from sync data.
func ApplySyncCategoryCreate(categoryGUID, name string, fragment CategoryFragment) (*Category, error) {
	// Extract field values from fragment
	description := fragment.Description
	subcategories := fragment.Subcategories

	// Insert into disk database
	query := `INSERT INTO categories (guid, name, description, subcategories, created_at, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id, guid, name, description, subcategories, created_at, updated_at`

	var category Category
	err := db.QueryRow(query, categoryGUID, name, description, subcategories).Scan(
		&category.ID, &category.GUID, &category.Name, &category.Description,
		&category.Subcategories, &category.CreatedAt, &category.UpdatedAt,
	)
	if err != nil {
		return nil, serr.Wrap(err, "failed to insert synced category into disk")
	}

	// Record change with OperationSync
	if fragmentID, err := insertCategoryFragment(fragment); err != nil {
		logger.LogErr(err, "failed to record sync category create fragment", "category_guid", categoryGUID)
	} else {
		if err := insertCategoryChange(GenerateChangeGUID(), categoryGUID, OperationSync,
			sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
			logger.LogErr(err, "failed to record sync category create change", "category_guid", categoryGUID)
		}
	}

	// Insert into cache
	cacheQuery := `INSERT INTO categories (id, guid, name, description, subcategories, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err = cacheDB.Exec(cacheQuery,
		category.ID, category.GUID, category.Name, category.Description,
		category.Subcategories, category.CreatedAt, category.UpdatedAt,
	)
	if err != nil {
		return &category, serr.Wrap(err, "synced category created on disk but cache insert failed")
	}

	return &category, nil
}

// ApplySyncCategoryUpdate updates a category from sync data.
func ApplySyncCategoryUpdate(categoryGUID string, fragment CategoryFragment) error {
	// Build dynamic SET clause from bitmask
	setClauses := []string{}
	args := []interface{}{}

	if fragment.Bitmask&CatFragmentName != 0 && fragment.Name.Valid {
		setClauses = append(setClauses, "name = ?")
		args = append(args, fragment.Name.String)
	}
	if fragment.Bitmask&CatFragmentDescription != 0 {
		setClauses = append(setClauses, "description = ?")
		args = append(args, fragment.Description)
	}
	if fragment.Bitmask&CatFragmentSubcategories != 0 {
		setClauses = append(setClauses, "subcategories = ?")
		args = append(args, fragment.Subcategories)
	}

	if len(setClauses) == 0 {
		return nil
	}

	setClauses = append(setClauses, "updated_at = CURRENT_TIMESTAMP")

	query := "UPDATE categories SET " + joinStrings(setClauses, ", ") + " WHERE guid = ?"
	args = append(args, categoryGUID)

	_, err := db.Exec(query, args...)
	if err != nil {
		return serr.Wrap(err, "failed to update category from sync")
	}

	// Record change with OperationSync
	if fragmentID, err := insertCategoryFragment(fragment); err != nil {
		logger.LogErr(err, "failed to record sync category update fragment", "category_guid", categoryGUID)
	} else {
		if err := insertCategoryChange(GenerateChangeGUID(), categoryGUID, OperationSync,
			sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
			logger.LogErr(err, "failed to record sync category update change", "category_guid", categoryGUID)
		}
	}

	// Update cache — re-read from disk for the resolved state
	selectQuery := `SELECT id, guid, name, description, subcategories, created_at, updated_at
		FROM categories WHERE guid = ?`
	var cat Category
	err = db.QueryRow(selectQuery, categoryGUID).Scan(
		&cat.ID, &cat.GUID, &cat.Name, &cat.Description,
		&cat.Subcategories, &cat.CreatedAt, &cat.UpdatedAt,
	)
	if err != nil {
		return serr.Wrap(err, "failed to read updated category from disk for cache sync")
	}

	cacheQuery := `UPDATE categories SET name = ?, description = ?, subcategories = ?, updated_at = ?
		WHERE guid = ?`
	_, err = cacheDB.Exec(cacheQuery, cat.Name, cat.Description, cat.Subcategories, cat.UpdatedAt, categoryGUID)
	if err != nil {
		return serr.Wrap(err, "synced category updated on disk but cache update failed")
	}

	return nil
}

// ApplySyncCategoryDelete deletes a category from sync.
func ApplySyncCategoryDelete(categoryGUID string) error {
	// Delete from disk
	_, err := db.Exec(`DELETE FROM categories WHERE guid = ?`, categoryGUID)
	if err != nil {
		return serr.Wrap(err, "failed to delete synced category from disk")
	}

	// Record change with OperationSync
	if err := insertCategoryChange(GenerateChangeGUID(), categoryGUID, OperationDelete,
		sql.NullInt64{}, ""); err != nil {
		logger.LogErr(err, "failed to record sync category delete change", "category_guid", categoryGUID)
	}

	// Delete from cache
	_, err = cacheDB.Exec(`DELETE FROM categories WHERE guid = ?`, categoryGUID)
	if err != nil {
		return serr.Wrap(err, "synced category deleted from disk but cache delete failed")
	}

	return nil
}

// ApplySyncNoteCategoryMapping replaces a note's entire category set from a sync snapshot.
// The snapshot is a JSON array of NoteCategoryMappingSnapshot objects that use category GUIDs.
// This atomically replaces all mappings, resolving GUIDs to local category IDs.
func ApplySyncNoteCategoryMapping(noteGUID string, mappingsJSON string) error {
	// Resolve note GUID to local ID
	note, err := GetNoteByGUID(noteGUID)
	if err != nil {
		return serr.Wrap(err, "failed to resolve note GUID for category mapping sync")
	}
	if note == nil {
		return serr.New("note not found for category mapping sync: " + noteGUID)
	}

	// Parse the mapping snapshot
	var mappings []NoteCategoryMappingSnapshot
	if err := json.Unmarshal([]byte(mappingsJSON), &mappings); err != nil {
		return serr.Wrap(err, "failed to parse category mapping snapshot")
	}

	// Delete all existing mappings for this note (both databases)
	_, err = db.Exec(`DELETE FROM note_categories WHERE note_id = ?`, note.ID)
	if err != nil {
		return serr.Wrap(err, "failed to clear existing note-category mappings on disk")
	}
	_, err = cacheDB.Exec(`DELETE FROM note_categories WHERE note_id = ?`, note.ID)
	if err != nil {
		return serr.Wrap(err, "failed to clear existing note-category mappings in cache")
	}

	// Insert new mappings, resolving category GUIDs to local IDs
	insertQuery := `INSERT INTO note_categories (note_id, category_id, subcategories, created_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)`

	for _, mapping := range mappings {
		cat, err := GetCategoryByGUID(mapping.CategoryGUID)
		if err != nil || cat == nil {
			// Category doesn't exist locally yet — skip (will be resolved in a later sync pass)
			logger.LogErr(serr.New("category not found locally during mapping sync"),
				"skipping mapping", "category_guid", mapping.CategoryGUID)
			continue
		}

		// Convert subcategories to JSON
		var subcatsJSON sql.NullString
		if len(mapping.SelectedSubcategories) > 0 {
			jsonBytes, err := json.Marshal(mapping.SelectedSubcategories)
			if err == nil {
				subcatsJSON = sql.NullString{String: string(jsonBytes), Valid: true}
			}
		}

		// Insert into both databases
		if _, err := db.Exec(insertQuery, note.ID, cat.ID, subcatsJSON); err != nil {
			logger.LogErr(err, "failed to insert synced note-category mapping on disk",
				"note_id", note.ID, "category_guid", mapping.CategoryGUID)
			continue
		}
		if _, err := cacheDB.Exec(insertQuery, note.ID, cat.ID, subcatsJSON); err != nil {
			logger.LogErr(err, "failed to insert synced note-category mapping in cache",
				"note_id", note.ID, "category_guid", mapping.CategoryGUID)
		}
	}

	return nil
}

// Helper functions

// nullStringToPtr converts a sql.NullString to a *string pointer
func nullStringToPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

// joinStrings joins a slice of strings with a separator.
// Avoids importing strings package for a simple utility.
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}

// getNoteByGUIDFromDisk retrieves a note by GUID directly from the disk database.
// Used by sync operations that need the canonical state after a disk write.
func getNoteByGUIDFromDisk(guid string) (*Note, error) {
	query := `
		SELECT id, guid, title, description, body, tags, is_private, encryption_iv,
		       created_by, updated_by, created_at, updated_at, authored_at, synced_at, deleted_at
		FROM notes
		WHERE guid = ? AND deleted_at IS NULL
	`

	note := &Note{}
	err := db.QueryRow(query, guid).Scan(
		&note.ID, &note.GUID, &note.Title, &note.Description, &note.Body,
		&note.Tags, &note.IsPrivate, &note.EncryptionIV, &note.CreatedBy,
		&note.UpdatedBy, &note.CreatedAt, &note.UpdatedAt, &note.AuthoredAt, &note.SyncedAt, &note.DeletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get note by GUID from disk")
	}

	return note, nil
}
