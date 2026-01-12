package models

import (
	"database/sql"
	"time"

	"github.com/rohanthewiz/logger"
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
	AuthoredAt   sql.NullTime   `json:"authored_at"`   // Last human authoring timestamp (disk only, not in cache)
	SyncedAt     sql.NullTime   `json:"synced_at"`     // Last sync timestamp for distributed scenarios
	DeletedAt    sql.NullTime   `json:"deleted_at"`    // Soft delete timestamp, null if not deleted
}

// CreateNotesTableSQL returns the DDL statement for creating the notes table (disk DB).
// Design notes:
// - id is a BIGINT with auto-increment via SEQUENCE for primary key
// - guid has a UNIQUE constraint for external reference integrity
// - is_private defaults to false (public visibility)
// - timestamps use CURRENT_TIMESTAMP defaults where appropriate
// - authored_at tracks when a person last created/updated the note (for peer-to-peer sync)
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
    authored_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    synced_at     TIMESTAMP,
    deleted_at    TIMESTAMP
);
`

// CreateNotesCacheTableSQL returns the DDL for the in-memory cache notes table.
// This schema intentionally excludes authored_at since the cache is for fast reads
// and authored_at is only relevant for the disk database (peer-to-peer sync scenarios).
const CreateNotesCacheTableSQL = `
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
	AuthoredAt   *string `json:"authored_at,omitempty"` // Last human authoring timestamp (disk only)
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
	if n.AuthoredAt.Valid {
		s := n.AuthoredAt.Time.Format(time.RFC3339)
		out.AuthoredAt = &s
	}
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
//
// Encryption behavior for private notes:
// - If IsPrivate is true and encryption is enabled, the body is encrypted before
//   being written to disk. The IV is stored in encryption_iv.
// - The cache stores the UNENCRYPTED body for fast reads.
// - This means disk contains encrypted data (secure at rest) while memory has
//   plaintext for performance.
// CreateNote creates a new note in both disk and cache databases.
// The userGUID parameter is required to set note ownership (created_by).
func CreateNote(input NoteInput, userGUID string) (*Note, error) {
	// Prepare body and IV for disk storage
	// For private notes, we encrypt the body; for public notes, we store plainly
	diskBody := toNullString(input.Body)
	diskEncryptionIV := toNullString(input.EncryptionIV)

	if input.IsPrivate && IsEncryptionEnabled() && input.Body != nil && *input.Body != "" {
		encryptedBody, iv, err := EncryptNoteBody(input.Body)
		if err != nil {
			return nil, serr.Wrap(err, "failed to encrypt private note body")
		}
		diskBody = toNullString(&encryptedBody)
		diskEncryptionIV = toNullString(&iv)
	}

	// Set ownership from the authenticated user
	createdBy := sql.NullString{String: userGUID, Valid: userGUID != ""}
	updatedBy := sql.NullString{String: userGUID, Valid: userGUID != ""}

	// authored_at uses DEFAULT CURRENT_TIMESTAMP, so no need to include in INSERT VALUES
	query := `
		INSERT INTO notes (guid, title, description, body, tags, is_private, encryption_iv, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, guid, title, description, body, tags, is_private, encryption_iv,
		          created_by, updated_by, created_at, updated_at, authored_at, synced_at, deleted_at
	`

	note := &Note{}
	// Insert into disk DB first (source of truth) - body is encrypted for private notes
	err := db.QueryRow(query,
		input.GUID,
		input.Title,
		toNullString(input.Description),
		diskBody,
		toNullString(input.Tags),
		input.IsPrivate,
		diskEncryptionIV,
		createdBy,
		updatedBy,
	).Scan(
		&note.ID, &note.GUID, &note.Title, &note.Description, &note.Body,
		&note.Tags, &note.IsPrivate, &note.EncryptionIV, &note.CreatedBy,
		&note.UpdatedBy, &note.CreatedAt, &note.UpdatedAt, &note.AuthoredAt, &note.SyncedAt, &note.DeletedAt,
	)

	if err != nil {
		return nil, err
	}

	// Record change for sync (non-blocking)
	// Track all fields as changed for create operations
	fragment := createFragmentFromInput(input, FragmentTitle|FragmentDescription|FragmentBody|FragmentTags|FragmentIsPrivate)
	if fragmentID, err := insertNoteFragment(fragment); err != nil {
		logger.LogErr(err, "failed to record note fragment", "note_guid", input.GUID)
	} else {
		if err := insertNoteChange(GenerateChangeGUID(), input.GUID, OperationCreate, sql.NullInt64{Int64: fragmentID, Valid: true}, userGUID); err != nil {
			logger.LogErr(err, "failed to record note change", "note_guid", input.GUID)
		}
	}

	// For private notes, the note.Body from disk is encrypted.
	// We need to store the UNENCRYPTED body in cache for fast reads.
	cacheBody := note.Body
	if input.IsPrivate && IsEncryptionEnabled() && input.Body != nil {
		// Use the original unencrypted body for cache
		cacheBody = toNullString(input.Body)
	}

	// Insert into cache with the same ID from disk to maintain consistency
	// Note: Cache stores unencrypted body for performance; encryption_iv is still stored
	// for reference but the body is plaintext in cache
	cacheInsertQuery := `
		INSERT INTO notes (id, guid, title, description, body, tags, is_private, encryption_iv,
		                   created_by, updated_by, created_at, updated_at, synced_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = cacheDB.Exec(cacheInsertQuery,
		note.ID, note.GUID, note.Title, note.Description, cacheBody,
		note.Tags, note.IsPrivate, note.EncryptionIV, note.CreatedBy,
		note.UpdatedBy, note.CreatedAt, note.UpdatedAt, note.SyncedAt, note.DeletedAt,
	)
	if err != nil {
		// Cache insert failed - log error but return the note since disk write succeeded
		// This maintains disk DB as source of truth
		return note, serr.Wrap(err, "note created in disk DB but failed to update cache")
	}

	// Return note with unencrypted body for the caller
	note.Body = cacheBody
	return note, nil
}

// GetNoteByID retrieves a single note by its primary key from the cache.
// The userGUID parameter filters to notes owned by that user.
// Returns nil, nil if the note doesn't exist or isn't owned by the user.
func GetNoteByID(id int64, userGUID string) (*Note, error) {
	query := `
		SELECT id, guid, title, description, body, tags, is_private, encryption_iv,
		       created_by, updated_by, created_at, updated_at, synced_at, deleted_at
		FROM notes
		WHERE id = ? AND created_by = ? AND deleted_at IS NULL
	`

	note := &Note{}
	// Read from cache for better performance
	err := cacheDB.QueryRow(query, id, userGUID).Scan(
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

// ListNotes retrieves all non-deleted notes owned by a user with pagination.
// The userGUID parameter filters to notes owned by that user.
// Ordered by created_at descending (newest first).
// limit=0 returns all notes, offset skips the first N results.
func ListNotes(userGUID string, limit, offset int) ([]Note, error) {
	query := `
		SELECT id, guid, title, description, body, tags, is_private, encryption_iv,
		       created_by, updated_by, created_at, updated_at, synced_at, deleted_at
		FROM notes
		WHERE created_by = ? AND deleted_at IS NULL
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
		rows, err = cacheDB.Query(query, userGUID, limit, offset)
	} else {
		rows, err = cacheDB.Query(query, userGUID)
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
//
// Encryption behavior for private notes:
// - If IsPrivate is true and encryption is enabled, the body is encrypted
//   before being written to disk. A new IV is generated for each update.
// - The cache stores the UNENCRYPTED body for fast reads.
// - If a note changes from private to public, the body is stored unencrypted.
// The userGUID parameter is used to verify ownership and set updated_by.
func UpdateNote(id int64, input NoteInput, userGUID string) (*Note, error) {
	// First verify the note exists, isn't deleted, and is owned by this user
	existing, err := GetNoteByID(id, userGUID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil // Not found or not owned by user
	}

	// Set updated_by from the authenticated user
	updatedBy := sql.NullString{String: userGUID, Valid: userGUID != ""}

	// Prepare body and IV for disk storage
	// For private notes, we encrypt the body; for public notes, we store plainly
	diskBody := toNullString(input.Body)
	diskEncryptionIV := toNullString(input.EncryptionIV)

	if input.IsPrivate && IsEncryptionEnabled() && input.Body != nil && *input.Body != "" {
		encryptedBody, iv, err := EncryptNoteBody(input.Body)
		if err != nil {
			return nil, serr.Wrap(err, "failed to encrypt private note body")
		}
		diskBody = toNullString(&encryptedBody)
		diskEncryptionIV = toNullString(&iv)
	}

	// Perform the update on disk DB first (source of truth)
	// authored_at is updated to track last human modification (for peer-to-peer sync)
	// Also filter by created_by to enforce ownership
	diskUpdateQuery := `
		UPDATE notes
		SET title = ?, description = ?, body = ?, tags = ?, is_private = ?,
		    encryption_iv = ?, updated_by = ?, updated_at = CURRENT_TIMESTAMP,
		    authored_at = CURRENT_TIMESTAMP
		WHERE id = ? AND created_by = ? AND deleted_at IS NULL
	`

	result, err := db.Exec(diskUpdateQuery,
		input.Title,
		toNullString(input.Description),
		diskBody,
		toNullString(input.Tags),
		input.IsPrivate,
		diskEncryptionIV,
		updatedBy,
		id,
		userGUID,
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

	// Record change for sync (non-blocking)
	// Only track fields that actually changed
	bitmask := computeChangeBitmask(existing, input)
	if bitmask != 0 {
		fragment := createDeltaFragment(input, bitmask)
		if fragmentID, err := insertNoteFragment(fragment); err != nil {
			logger.LogErr(err, "failed to record update fragment", "note_id", id)
		} else {
			if err := insertNoteChange(GenerateChangeGUID(), existing.GUID, OperationUpdate, sql.NullInt64{Int64: fragmentID, Valid: true}, userGUID); err != nil {
				logger.LogErr(err, "failed to record update change", "note_id", id)
			}
		}
	}

	// Update cache with UNENCRYPTED body for fast reads
	cacheUpdateQuery := `
		UPDATE notes
		SET title = ?, description = ?, body = ?, tags = ?, is_private = ?,
		    encryption_iv = ?, updated_by = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND deleted_at IS NULL
	`

	_, err = cacheDB.Exec(cacheUpdateQuery,
		input.Title,
		toNullString(input.Description),
		toNullString(input.Body), // Unencrypted for cache
		toNullString(input.Tags),
		input.IsPrivate,
		diskEncryptionIV, // Store the IV in cache too for reference
		toNullString(input.UpdatedBy),
		id,
	)
	if err != nil {
		// Cache update failed - the disk is still updated (source of truth)
		// Return error to indicate cache is out of sync
		return nil, serr.Wrap(err, "note updated in disk DB but failed to update cache")
	}

	// Fetch the updated note from cache (will have unencrypted body)
	return GetNoteByID(id, userGUID)
}

// DeleteNote performs a soft delete by setting deleted_at timestamp in both databases.
// The note remains in the database but is excluded from normal queries.
// The userGUID parameter verifies ownership before deletion.
// Returns true if a note was deleted, false if not found or not owned by user.
func DeleteNote(id int64, userGUID string) (bool, error) {
	// First get the note GUID for change tracking, also verify ownership
	var noteGUID string
	err := db.QueryRow(`SELECT guid FROM notes WHERE id = ? AND created_by = ? AND deleted_at IS NULL`, id, userGUID).Scan(&noteGUID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, serr.Wrap(err, "failed to get note GUID for delete tracking")
	}

	query := `
		UPDATE notes
		SET deleted_at = CURRENT_TIMESTAMP
		WHERE id = ? AND created_by = ? AND deleted_at IS NULL
	`

	// Delete from disk DB first (source of truth)
	result, err := db.Exec(query, id, userGUID)
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

	// Record change for sync (non-blocking)
	// Delete operations don't have a fragment (null fragment ID)
	if err := insertNoteChange(GenerateChangeGUID(), noteGUID, OperationDelete, sql.NullInt64{}, userGUID); err != nil {
		logger.LogErr(err, "failed to record delete change", "note_id", id)
	}

	// Also delete from cache
	_, err = cacheDB.Exec(`UPDATE notes SET deleted_at = CURRENT_TIMESTAMP WHERE id = ? AND deleted_at IS NULL`, id)
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
