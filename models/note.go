package models

import (
	"database/sql"
	"time"

	"github.com/rohanthewiz/serr"
)

// Note represents a note in the system. The model is designed to support:
// - Encryption via EncryptionIV for sensitive notes
// - Soft deletes via DeletedAt for data recovery
// - Sync tracking via SyncedAt for distributed scenarios
// - Audit trail via CreatedBy/UpdatedBy fields
type Note struct {
	ID           int64          `json:"id"`            // Primary key, auto-incremented
	GUID         string         `json:"guid"`          // Unique identifier for external references/sync
	Title        string         `json:"title"`         // Note title, required
	Description  sql.NullString `json:"description"`   // Optional short description
	Body         sql.NullString `json:"body"`          // Main content of the note
	Tags         sql.NullString `json:"tags"`          // Comma-separated tags for categorization
	IsPrivate    bool           `json:"is_private"`    // Visibility flag, defaults to false
	EncryptionIV sql.NullString `json:"encryption_iv"` // Initialization vector if note is encrypted
	CreatedBy    sql.NullString `json:"created_by"`    // User who created the note
	UpdatedBy    sql.NullString `json:"updated_by"`    // User who last updated the note
	CreatedAt    time.Time      `json:"created_at"`    // Timestamp of creation
	UpdatedAt    time.Time      `json:"updated_at"`    // Timestamp of last update
	SyncedAt     sql.NullTime   `json:"synced_at"`     // Last sync timestamp for distributed scenarios
	DeletedAt    sql.NullTime   `json:"deleted_at"`    // Soft delete timestamp, null if not deleted
}

// CreateNotesTableSQL returns the DDL statement for creating the notes table.
// Design notes:
// - id is a BIGINT with auto-increment via SEQUENCE for primary key
// - guid has a UNIQUE constraint for external reference integrity
// - is_private defaults to false (public visibility)
// - timestamps use CURRENT_TIMESTAMP defaults where appropriate
const CreateNotesTableSQL = `
CREATE SEQUENCE IF NOT EXISTS notes_id_seq START 1;

CREATE TABLE IF NOT EXISTS notes (
    id            BIGINT PRIMARY KEY DEFAULT nextval('notes_id_seq'),
    guid          VARCHAR NOT NULL UNIQUE,
    title         VARCHAR NOT NULL,
    description   VARCHAR,
    body          VARCHAR,
    tags          VARCHAR,
    is_private    BOOLEAN DEFAULT false,
    encryption_iv VARCHAR,
    created_by    VARCHAR,
    updated_by    VARCHAR,
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    synced_at     TIMESTAMP,
    deleted_at    TIMESTAMP
);
`

// DropNotesTableSQL is provided for testing and migration rollback scenarios.
// Use with caution in production environments.
const DropNotesTableSQL = `
DROP TABLE IF EXISTS notes;
DROP SEQUENCE IF EXISTS notes_id_seq;
`

// NoteInput represents the data required to create or update a note.
// Using a separate struct from Note allows us to control which fields
// are settable via API vs auto-generated (like ID, timestamps).
type NoteInput struct {
	GUID         string  `json:"guid"`
	Title        string  `json:"title"`
	Description  *string `json:"description,omitempty"`
	Body         *string `json:"body,omitempty"`
	Tags         *string `json:"tags,omitempty"`
	IsPrivate    bool    `json:"is_private"`
	EncryptionIV *string `json:"encryption_iv,omitempty"`
	CreatedBy    *string `json:"created_by,omitempty"`
	UpdatedBy    *string `json:"updated_by,omitempty"`
}

// NoteOutput provides a JSON-friendly representation of a Note.
// sql.Null* types don't serialize well to JSON, so we convert
// them to pointer types which marshal as null or the value.
type NoteOutput struct {
	ID           int64   `json:"id"`
	GUID         string  `json:"guid"`
	Title        string  `json:"title"`
	Description  *string `json:"description,omitempty"`
	Body         *string `json:"body,omitempty"`
	Tags         *string `json:"tags,omitempty"`
	IsPrivate    bool    `json:"is_private"`
	EncryptionIV *string `json:"encryption_iv,omitempty"`
	CreatedBy    *string `json:"created_by,omitempty"`
	UpdatedBy    *string `json:"updated_by,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	SyncedAt     *string `json:"synced_at,omitempty"`
	DeletedAt    *string `json:"deleted_at,omitempty"`
}

// ToOutput converts a Note to NoteOutput for JSON serialization.
// Handles the sql.Null* to pointer conversion for clean JSON output.
func (n *Note) ToOutput() NoteOutput {
	out := NoteOutput{
		ID:        n.ID,
		GUID:      n.GUID,
		Title:     n.Title,
		IsPrivate: n.IsPrivate,
		CreatedAt: n.CreatedAt.Format(time.RFC3339),
		UpdatedAt: n.UpdatedAt.Format(time.RFC3339),
	}

	// Convert sql.NullString fields to *string
	if n.Description.Valid {
		out.Description = &n.Description.String
	}
	if n.Body.Valid {
		out.Body = &n.Body.String
	}
	if n.Tags.Valid {
		out.Tags = &n.Tags.String
	}
	if n.EncryptionIV.Valid {
		out.EncryptionIV = &n.EncryptionIV.String
	}
	if n.CreatedBy.Valid {
		out.CreatedBy = &n.CreatedBy.String
	}
	if n.UpdatedBy.Valid {
		out.UpdatedBy = &n.UpdatedBy.String
	}

	// Convert sql.NullTime fields to *string
	if n.SyncedAt.Valid {
		s := n.SyncedAt.Time.Format(time.RFC3339)
		out.SyncedAt = &s
	}
	if n.DeletedAt.Valid {
		s := n.DeletedAt.Time.Format(time.RFC3339)
		out.DeletedAt = &s
	}

	return out
}

// CreateNote inserts a new note into both the disk database (source of truth)
// and the in-memory cache. The ID and timestamps are auto-generated by the disk database.
// Returns the created note with all fields populated.
func CreateNote(input NoteInput) (*Note, error) {
	query := `
		INSERT INTO notes (guid, title, description, body, tags, is_private, encryption_iv, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, guid, title, description, body, tags, is_private, encryption_iv,
		          created_by, updated_by, created_at, updated_at, synced_at, deleted_at
	`

	note := &Note{}
	// Insert into disk DB first (source of truth)
	err := db.QueryRow(query,
		input.GUID,
		input.Title,
		toNullString(input.Description),
		toNullString(input.Body),
		toNullString(input.Tags),
		input.IsPrivate,
		toNullString(input.EncryptionIV),
		toNullString(input.CreatedBy),
		toNullString(input.UpdatedBy),
	).Scan(
		&note.ID, &note.GUID, &note.Title, &note.Description, &note.Body,
		&note.Tags, &note.IsPrivate, &note.EncryptionIV, &note.CreatedBy,
		&note.UpdatedBy, &note.CreatedAt, &note.UpdatedAt, &note.SyncedAt, &note.DeletedAt,
	)

	if err != nil {
		return nil, err
	}

	// Insert into cache with the same ID from disk to maintain consistency
	cacheInsertQuery := `
		INSERT INTO notes (id, guid, title, description, body, tags, is_private, encryption_iv,
		                   created_by, updated_by, created_at, updated_at, synced_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = cacheDB.Exec(cacheInsertQuery,
		note.ID, note.GUID, note.Title, note.Description, note.Body,
		note.Tags, note.IsPrivate, note.EncryptionIV, note.CreatedBy,
		note.UpdatedBy, note.CreatedAt, note.UpdatedAt, note.SyncedAt, note.DeletedAt,
	)
	if err != nil {
		// Cache insert failed - log error but return the note since disk write succeeded
		// This maintains disk DB as source of truth
		return note, serr.Wrap(err, "note created in disk DB but failed to update cache")
	}

	return note, nil
}

// GetNoteByID retrieves a single note by its primary key from the cache.
// Returns nil, nil if the note doesn't exist (soft-deleted notes are excluded).
func GetNoteByID(id int64) (*Note, error) {
	query := `
		SELECT id, guid, title, description, body, tags, is_private, encryption_iv,
		       created_by, updated_by, created_at, updated_at, synced_at, deleted_at
		FROM notes
		WHERE id = ? AND deleted_at IS NULL
	`

	note := &Note{}
	// Read from cache for better performance
	err := cacheDB.QueryRow(query, id).Scan(
		&note.ID, &note.GUID, &note.Title, &note.Description, &note.Body,
		&note.Tags, &note.IsPrivate, &note.EncryptionIV, &note.CreatedBy,
		&note.UpdatedBy, &note.CreatedAt, &note.UpdatedAt, &note.SyncedAt, &note.DeletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return note, nil
}

// GetNoteByGUID retrieves a single note by its GUID from the cache.
// Useful for external references and sync operations.
func GetNoteByGUID(guid string) (*Note, error) {
	query := `
		SELECT id, guid, title, description, body, tags, is_private, encryption_iv,
		       created_by, updated_by, created_at, updated_at, synced_at, deleted_at
		FROM notes
		WHERE guid = ? AND deleted_at IS NULL
	`

	note := &Note{}
	// Read from cache for better performance
	err := cacheDB.QueryRow(query, guid).Scan(
		&note.ID, &note.GUID, &note.Title, &note.Description, &note.Body,
		&note.Tags, &note.IsPrivate, &note.EncryptionIV, &note.CreatedBy,
		&note.UpdatedBy, &note.CreatedAt, &note.UpdatedAt, &note.SyncedAt, &note.DeletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return note, nil
}

// ListNotes retrieves all non-deleted notes with pagination from the cache.
// Ordered by created_at descending (newest first).
// limit=0 returns all notes, offset skips the first N results.
func ListNotes(limit, offset int) ([]Note, error) {
	query := `
		SELECT id, guid, title, description, body, tags, is_private, encryption_iv,
		       created_by, updated_by, created_at, updated_at, synced_at, deleted_at
		FROM notes
		WHERE deleted_at IS NULL
		ORDER BY created_at DESC
	`

	// Add pagination if limit is specified
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
	}

	var rows *sql.Rows
	var err error

	// Read from cache for better performance
	if limit > 0 {
		rows, err = cacheDB.Query(query, limit, offset)
	} else {
		rows, err = cacheDB.Query(query)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []Note
	for rows.Next() {
		var note Note
		err := rows.Scan(
			&note.ID, &note.GUID, &note.Title, &note.Description, &note.Body,
			&note.Tags, &note.IsPrivate, &note.EncryptionIV, &note.CreatedBy,
			&note.UpdatedBy, &note.CreatedAt, &note.UpdatedAt, &note.SyncedAt, &note.DeletedAt,
		)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}

	return notes, rows.Err()
}

// UpdateNote modifies an existing note identified by ID in both databases.
// Only non-nil fields in the input are updated; updated_at is auto-set.
// Returns the updated note or nil if not found.
// Note: DuckDB's RETURNING clause on UPDATE has limitations, so we
// perform the update and then fetch the updated record separately.
func UpdateNote(id int64, input NoteInput) (*Note, error) {
	// First verify the note exists and isn't deleted (reads from cache)
	existing, err := GetNoteByID(id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}

	// Perform the update on disk DB first (source of truth)
	updateQuery := `
		UPDATE notes
		SET title = ?, description = ?, body = ?, tags = ?, is_private = ?,
		    encryption_iv = ?, updated_by = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND deleted_at IS NULL
	`

	result, err := db.Exec(updateQuery,
		input.Title,
		toNullString(input.Description),
		toNullString(input.Body),
		toNullString(input.Tags),
		input.IsPrivate,
		toNullString(input.EncryptionIV),
		toNullString(input.UpdatedBy),
		id,
	)
	if err != nil {
		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rowsAffected == 0 {
		return nil, nil
	}

	// Also update in cache
	_, err = cacheDB.Exec(updateQuery,
		input.Title,
		toNullString(input.Description),
		toNullString(input.Body),
		toNullString(input.Tags),
		input.IsPrivate,
		toNullString(input.EncryptionIV),
		toNullString(input.UpdatedBy),
		id,
	)
	if err != nil {
		// Cache update failed - the disk is still updated (source of truth)
		// Return error to indicate cache is out of sync
		return nil, serr.Wrap(err, "note updated in disk DB but failed to update cache")
	}

	// Fetch the updated note from cache
	return GetNoteByID(id)
}

// DeleteNote performs a soft delete by setting deleted_at timestamp in both databases.
// The note remains in the database but is excluded from normal queries.
// Returns true if a note was deleted, false if not found.
func DeleteNote(id int64) (bool, error) {
	query := `
		UPDATE notes
		SET deleted_at = CURRENT_TIMESTAMP
		WHERE id = ? AND deleted_at IS NULL
	`

	// Delete from disk DB first (source of truth)
	result, err := db.Exec(query, id)
	if err != nil {
		return false, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	if rowsAffected == 0 {
		return false, nil
	}

	// Also delete from cache
	_, err = cacheDB.Exec(query, id)
	if err != nil {
		// Cache delete failed - disk is updated but cache is out of sync
		return true, serr.Wrap(err, "note deleted in disk DB but failed to update cache")
	}

	return true, nil
}

// HardDeleteNote permanently removes a note from both databases.
// Use with caution - this cannot be undone. Primarily for testing
// and administrative cleanup of soft-deleted records.
func HardDeleteNote(id int64) (bool, error) {
	query := `DELETE FROM notes WHERE id = ?`

	// Delete from disk DB first (source of truth)
	result, err := db.Exec(query, id)
	if err != nil {
		return false, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	if rowsAffected == 0 {
		return false, nil
	}

	// Also delete from cache
	_, err = cacheDB.Exec(query, id)
	if err != nil {
		// Cache delete failed - disk is updated but cache is out of sync
		return true, serr.Wrap(err, "note hard deleted in disk DB but failed to update cache")
	}

	return true, nil
}

// toNullString converts a *string to sql.NullString for database operations.
// Returns a valid NullString if the pointer is non-nil, invalid otherwise.
func toNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: *s, Valid: true}
}
