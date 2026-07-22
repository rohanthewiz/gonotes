package models

import (
	"database/sql"
	"sort"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// Note represents a note in the system. The model supports soft deletes
// (DeletedAt), sync tracking (SyncedAt / AuthoredAt), and an audit trail
// (CreatedBy / UpdatedBy).
//
// Storage split: a note lives in exactly one of the two databases,
// selected by IsPrivate — private notes in the encrypted database,
// non-private notes in the public one (see store.go / db.go). Note ids
// are globally unique across the two databases (the private database
// offsets its id sequence), so a note is addressable by id regardless of
// which database holds it.
//
// EncryptionIV is retained on the struct for API/transport compatibility
// but is no longer persisted: bytdb encrypts the whole private database at
// rest, replacing the previous per-note AES field. It is always empty.
type Note struct {
	ID           int64          `json:"id"`            // Primary key
	GUID         string         `json:"guid"`          // Unique identifier for external references/sync
	Title        string         `json:"title"`         // Note title, required
	Description  sql.NullString `json:"description"`   // Optional short description
	Body         sql.NullString `json:"body"`          // Main content of the note
	Tags         sql.NullString `json:"tags"`          // Comma-separated tags for categorization
	IsPrivate    bool           `json:"is_private"`    // Visibility flag; also selects which database holds the note
	IsFlagged    bool           `json:"is_flagged"`    // Flag for follow-up, defaults to false
	EncryptionIV sql.NullString `json:"encryption_iv"` // Deprecated: no longer persisted (whole-DB encryption)
	CreatedBy    sql.NullString `json:"created_by"`    // User who created the note
	UpdatedBy    sql.NullString `json:"updated_by"`    // User who last updated the note
	CreatedAt    time.Time      `json:"created_at"`    // Timestamp of creation
	UpdatedAt    time.Time      `json:"updated_at"`    // Timestamp of last update
	AuthoredAt   sql.NullTime   `json:"authored_at"`   // Last human authoring timestamp (for peer-to-peer sync)
	SyncedAt     sql.NullTime   `json:"synced_at"`     // Last sync timestamp for distributed scenarios
	DeletedAt    sql.NullTime   `json:"deleted_at"`    // Soft delete timestamp, null if not deleted
}

// noteCols is the canonical notes column list (encryption_iv dropped),
// shared by every notes SELECT so the projection and scanNoteRow stay in
// lockstep.
const noteCols = `id, guid, title, description, body, tags, is_private, is_flagged,
	created_by, updated_by, created_at, updated_at, authored_at, synced_at, deleted_at`

// scanner is satisfied by both *shimRow and *shimRows.
type scanner interface {
	Scan(dest ...any) error
}

// scanNoteRow reads one notes row (projected as noteCols) into n.
func scanNoteRow(s scanner, n *Note) error {
	return s.Scan(
		&n.ID, &n.GUID, &n.Title, &n.Description, &n.Body, &n.Tags,
		&n.IsPrivate, &n.IsFlagged, &n.CreatedBy, &n.UpdatedBy,
		&n.CreatedAt, &n.UpdatedAt, &n.AuthoredAt, &n.SyncedAt, &n.DeletedAt,
	)
}

// NoteInput represents the data required to create or update a note.
type NoteInput struct {
	GUID         string  `json:"guid"`
	Title        string  `json:"title"`
	Description  *string `json:"description,omitempty"`
	Body         *string `json:"body,omitempty"`
	Tags         *string `json:"tags,omitempty"`
	IsPrivate    bool    `json:"is_private"`
	IsFlagged    bool    `json:"is_flagged"`
	EncryptionIV *string `json:"encryption_iv,omitempty"` // Deprecated: ignored (whole-DB encryption)
	CreatedBy    *string `json:"created_by,omitempty"`
	UpdatedBy    *string `json:"updated_by,omitempty"`
}

// NoteOutput provides a JSON-friendly representation of a Note.
type NoteOutput struct {
	ID           int64   `json:"id"`
	GUID         string  `json:"guid"`
	Title        string  `json:"title"`
	Description  *string `json:"description,omitempty"`
	Body         *string `json:"body,omitempty"`
	Tags         *string `json:"tags,omitempty"`
	IsPrivate    bool    `json:"is_private"`
	IsFlagged    bool    `json:"is_flagged"`
	EncryptionIV *string `json:"encryption_iv,omitempty"`
	CreatedBy    *string `json:"created_by,omitempty"`
	UpdatedBy    *string `json:"updated_by,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	AuthoredAt   *string `json:"authored_at,omitempty"`
	SyncedAt     *string `json:"synced_at,omitempty"`
	DeletedAt    *string `json:"deleted_at,omitempty"`
}

// ToOutput converts a Note to NoteOutput for JSON serialization.
func (n *Note) ToOutput() NoteOutput {
	out := NoteOutput{
		ID:        n.ID,
		GUID:      n.GUID,
		Title:     n.Title,
		IsPrivate: n.IsPrivate,
		IsFlagged: n.IsFlagged,
		CreatedAt: n.CreatedAt.Format(time.RFC3339),
		UpdatedAt: n.UpdatedAt.Format(time.RFC3339),
	}

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

// CreateNote inserts a new note into the database selected by its
// privacy: private notes go to the encrypted database, others to the
// public one. The id is drawn from that database's notes_id_seq (offset
// in the private database so ids are globally unique). No app-level
// encryption is applied — the private database is encrypted at rest as a
// whole.
func CreateNote(input NoteInput, userGUID string) (*Note, error) {
	en := noteEngine(input.IsPrivate)

	createdBy := sql.NullString{String: userGUID, Valid: userGUID != ""}
	updatedBy := sql.NullString{String: userGUID, Valid: userGUID != ""}

	// id is drawn via nextval() in the INSERT — bytdb rejects DEFAULT
	// nextval(...). authored_at/created_at/updated_at take their column
	// defaults (CURRENT_TIMESTAMP).
	query := `
		INSERT INTO notes (id, guid, title, description, body, tags, is_private, is_flagged, created_by, updated_by)
		VALUES (nextval('notes_id_seq'), ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING ` + noteCols

	note := &Note{}
	err := scanNoteRow(en.QueryRow(query,
		input.GUID,
		input.Title,
		toNullString(input.Description),
		toNullString(input.Body),
		toNullString(input.Tags),
		input.IsPrivate,
		input.IsFlagged,
		createdBy,
		updatedBy,
	), note)
	if err != nil {
		return nil, err
	}

	// Record change for sync (non-blocking). The change-tracking rows live
	// in the same database as the note, so private-note history stays in
	// the encrypted database.
	fragment := createFragmentFromInput(input, FragmentTitle|FragmentDescription|FragmentBody|FragmentTags|FragmentIsPrivate)
	if fragmentID, err := insertNoteFragment(en, fragment); err != nil {
		logger.LogErr(err, "failed to record note fragment", "note_guid", input.GUID)
	} else {
		if err := insertNoteChange(en, GenerateChangeGUID(), input.GUID, OperationCreate, sql.NullInt64{Int64: fragmentID, Valid: true}, userGUID); err != nil {
			logger.LogErr(err, "failed to record note change", "note_guid", input.GUID)
		}
	}

	return note, nil
}

// CreateNoteWithTimestamps inserts a note preserving caller-supplied
// timestamps (bulk-import path). Skips sync change recording (an import is
// a snapshot replay, not a fresh authoring event).
func CreateNoteWithTimestamps(input NoteInput, userGUID string,
	createdAt, updatedAt, authoredAt time.Time) (*Note, error) {
	en := noteEngine(input.IsPrivate)

	createdBy := sql.NullString{String: userGUID, Valid: userGUID != ""}
	updatedBy := sql.NullString{String: userGUID, Valid: userGUID != ""}

	query := `
		INSERT INTO notes (id, guid, title, description, body, tags, is_private, is_flagged,
		                   created_by, updated_by, created_at, updated_at, authored_at)
		VALUES (nextval('notes_id_seq'), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING ` + noteCols

	note := &Note{}
	err := scanNoteRow(en.QueryRow(query,
		input.GUID,
		input.Title,
		toNullString(input.Description),
		toNullString(input.Body),
		toNullString(input.Tags),
		input.IsPrivate,
		input.IsFlagged,
		createdBy,
		updatedBy,
		createdAt,
		updatedAt,
		authoredAt,
	), note)
	if err != nil {
		return nil, err
	}
	return note, nil
}

// GetNoteByID retrieves a single note by primary key, owned by userGUID.
// The read fans out to both databases concurrently (divide and conquer);
// since ids are globally unique, at most one database holds the row.
// Returns nil, nil if the note doesn't exist or isn't owned by the user.
func GetNoteByID(id int64, userGUID string) (*Note, error) {
	query := `SELECT ` + noteCols + `
		FROM notes
		WHERE id = ? AND created_by = ? AND deleted_at IS NULL`

	note, err := firstFromBothNotes(func(en *dbEngine) (*Note, error) {
		n := &Note{}
		if e := scanNoteRow(en.QueryRow(query, id, userGUID), n); e != nil {
			return nil, e // sql.ErrNoRows when this engine lacks the row
		}
		return n, nil
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return note, err
}

// GetNoteByGUID retrieves a single note by GUID from whichever database
// holds it (fan-out). Used by external references and sync.
func GetNoteByGUID(guid string) (*Note, error) {
	query := `SELECT ` + noteCols + `
		FROM notes
		WHERE guid = ? AND deleted_at IS NULL`

	note, err := firstFromBothNotes(func(en *dbEngine) (*Note, error) {
		n := &Note{}
		if e := scanNoteRow(en.QueryRow(query, guid), n); e != nil {
			return nil, e
		}
		return n, nil
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return note, err
}

// ListNotes retrieves all non-deleted notes owned by a user, newest
// first, with pagination. Because a user's notes are spread across both
// databases, the two are queried concurrently, merged, globally sorted by
// created_at descending, and only then sliced to the requested page —
// pushing LIMIT to each engine independently could not produce a correct
// global page. limit=0 returns everything.
func ListNotes(userGUID string, limit, offset int) ([]Note, error) {
	query := `SELECT ` + noteCols + `
		FROM notes
		WHERE created_by = ? AND deleted_at IS NULL`

	notes, err := queryBothNotes(func(en *dbEngine) ([]Note, error) {
		return queryNotes(en, query, userGUID)
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(notes, func(i, j int) bool {
		return notes[i].CreatedAt.After(notes[j].CreatedAt)
	})

	return paginate(notes, limit, offset), nil
}

// queryNotes runs a notes SELECT on one engine and collects the rows.
func queryNotes(en *dbEngine, query string, args ...any) ([]Note, error) {
	rows, err := en.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []Note
	for rows.Next() {
		var note Note
		if err := scanNoteRow(rows, &note); err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	return notes, rows.Err()
}

// paginate applies an in-memory limit/offset to an already-sorted slice.
func paginate(notes []Note, limit, offset int) []Note {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(notes) {
		return nil
	}
	notes = notes[offset:]
	if limit > 0 && limit < len(notes) {
		notes = notes[:limit]
	}
	return notes
}

// UpdateNote modifies an existing note owned by userGUID. When the note's
// privacy flips it must move between the two databases (the note keeps its
// id); otherwise it is updated in place. Returns the updated note, or nil
// if not found.
func UpdateNote(id int64, input NoteInput, userGUID string) (*Note, error) {
	existing, err := GetNoteByID(id, userGUID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil // Not found or not owned by user
	}

	srcEngine := noteEngine(existing.IsPrivate)
	dstEngine := noteEngine(input.IsPrivate)
	updatedBy := sql.NullString{String: userGUID, Valid: userGUID != ""}

	// Compute the sync change first (uses the pre-update state), then apply
	// the write. The change-tracking rows land in the destination engine so
	// history follows the note across a privacy move.
	bitmask := computeChangeBitmask(existing, input)

	if srcEngine == dstEngine {
		updateQuery := `
			UPDATE notes
			SET title = ?, description = ?, body = ?, tags = ?, is_private = ?, is_flagged = ?,
			    updated_by = ?, updated_at = CURRENT_TIMESTAMP, authored_at = CURRENT_TIMESTAMP
			WHERE id = ? AND created_by = ? AND deleted_at IS NULL`

		result, err := dstEngine.Exec(updateQuery,
			input.Title,
			toNullString(input.Description),
			toNullString(input.Body),
			toNullString(input.Tags),
			input.IsPrivate,
			input.IsFlagged,
			updatedBy,
			id,
			userGUID,
		)
		if err != nil {
			return nil, err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return nil, nil
		}
	} else {
		// Privacy flip: move the note (and its category links) to the other
		// database, preserving id/guid/created_at, then delete the original.
		if err := moveNoteBetweenEngines(srcEngine, dstEngine, existing, input, updatedBy); err != nil {
			return nil, serr.Wrap(err, "failed to move note between databases on privacy change")
		}
	}

	// Record the change (non-blocking) in the destination engine.
	if bitmask != 0 {
		fragment := createDeltaFragment(existing, input, bitmask)
		if fragmentID, err := insertNoteFragment(dstEngine, fragment); err != nil {
			logger.LogErr(err, "failed to record update fragment", "note_id", id)
		} else {
			if err := insertNoteChange(dstEngine, GenerateChangeGUID(), existing.GUID, OperationUpdate, sql.NullInt64{Int64: fragmentID, Valid: true}, userGUID); err != nil {
				logger.LogErr(err, "failed to record update change", "note_id", id)
			}
		}
	}

	return GetNoteByID(id, userGUID)
}

// moveNoteBetweenEngines relocates a note from src to dst when its privacy
// changes. The id, guid, created_by, and created_at are preserved so
// external references and ordering survive the move; updated_at/authored_at
// advance to now, and is_private takes the new value. note_categories rows
// follow the note.
func moveNoteBetweenEngines(src, dst *dbEngine, existing *Note, input NoteInput, updatedBy sql.NullString) error {
	now := time.Now().UTC()

	insertQuery := `
		INSERT INTO notes (id, guid, title, description, body, tags, is_private, is_flagged,
		                   created_by, updated_by, created_at, updated_at, authored_at, synced_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := dst.Exec(insertQuery,
		existing.ID,
		existing.GUID,
		input.Title,
		toNullString(input.Description),
		toNullString(input.Body),
		toNullString(input.Tags),
		input.IsPrivate,
		input.IsFlagged,
		existing.CreatedBy,
		updatedBy,
		existing.CreatedAt,
		now,
		now,
		existing.SyncedAt,
		existing.DeletedAt,
	)
	if err != nil {
		return serr.Wrap(err, "failed to insert note into destination database")
	}

	// Move category links: copy then delete. Best-effort logging on the
	// copy so a link hiccup doesn't strand the note.
	if err := moveNoteCategories(src, dst, existing.ID); err != nil {
		logger.LogErr(err, "failed to move note category links", "note_id", existing.ID)
	}

	if _, err := src.Exec(`DELETE FROM notes WHERE id = ?`, existing.ID); err != nil {
		return serr.Wrap(err, "failed to delete note from source database after move")
	}
	return nil
}

// moveNoteCategories copies a note's note_categories rows from src to dst
// and removes them from src. Used by the privacy-flip move.
func moveNoteCategories(src, dst *dbEngine, noteID int64) error {
	rows, err := src.Query(`SELECT note_id, category_id, subcategories, created_at FROM note_categories WHERE note_id = ?`, noteID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type link struct {
		noteID        int64
		categoryID    int64
		subcategories sql.NullString
		createdAt     time.Time
	}
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.noteID, &l.categoryID, &l.subcategories, &l.createdAt); err != nil {
			return err
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, l := range links {
		if _, err := dst.Exec(
			`INSERT INTO note_categories (note_id, category_id, subcategories, created_at) VALUES (?, ?, ?, ?)`,
			l.noteID, l.categoryID, l.subcategories, l.createdAt,
		); err != nil {
			return err
		}
	}
	if _, err := src.Exec(`DELETE FROM note_categories WHERE note_id = ?`, noteID); err != nil {
		return err
	}
	return nil
}

// DeleteNote soft-deletes a note (sets deleted_at) in whichever database
// holds it, verifying ownership. Returns true if a note was deleted.
func DeleteNote(id int64, userGUID string) (bool, error) {
	// Locate the note (and its engine) by ownership.
	existing, err := GetNoteByID(id, userGUID)
	if err != nil {
		return false, err
	}
	if existing == nil {
		return false, nil
	}
	en := noteEngine(existing.IsPrivate)

	result, err := en.Exec(`
		UPDATE notes SET deleted_at = CURRENT_TIMESTAMP
		WHERE id = ? AND created_by = ? AND deleted_at IS NULL`, id, userGUID)
	if err != nil {
		return false, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return false, nil
	}

	// Record change for sync (non-blocking). Delete has no fragment.
	if err := insertNoteChange(en, GenerateChangeGUID(), existing.GUID, OperationDelete, sql.NullInt64{}, userGUID); err != nil {
		logger.LogErr(err, "failed to record delete change", "note_id", id)
	}

	return true, nil
}

// HardDeleteNote permanently removes a note. Since a note lives in only
// one database, the DELETE is issued to both — the database without the
// row simply affects zero rows. Primarily for tests and admin cleanup.
func HardDeleteNote(id int64) (bool, error) {
	var affected int64
	for _, en := range []*dbEngine{pubDB, privDB} {
		result, err := en.Exec(`DELETE FROM notes WHERE id = ?`, id)
		if err != nil {
			return affected > 0, err
		}
		if n, _ := result.RowsAffected(); n > 0 {
			affected += n
		}
	}
	return affected > 0, nil
}

// SearchNotesByTitle finds non-deleted notes owned by a user whose title
// contains query (case-insensitive), across both databases, most recently
// updated first, capped at limit. Used by the note-linking autocomplete.
func SearchNotesByTitle(query string, userGUID string, limit int) ([]Note, error) {
	if limit <= 0 {
		limit = 20
	}

	sqlQuery := `SELECT ` + noteCols + `
		FROM notes
		WHERE created_by = ? AND deleted_at IS NULL
		  AND LOWER(title) LIKE '%' || LOWER(?) || '%'
		ORDER BY updated_at DESC`

	notes, err := queryBothNotes(func(en *dbEngine) ([]Note, error) {
		return queryNotes(en, sqlQuery, userGUID, query)
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(notes, func(i, j int) bool {
		return notes[i].UpdatedAt.After(notes[j].UpdatedAt)
	})
	return paginate(notes, limit, 0), nil
}

// ToggleNoteFlag flips is_flagged on a note in whichever database holds
// it. Returns the updated note or nil if not found.
func ToggleNoteFlag(id int64, userGUID string) (*Note, error) {
	existing, err := GetNoteByID(id, userGUID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}
	en := noteEngine(existing.IsPrivate)

	result, err := en.Exec(`
		UPDATE notes SET is_flagged = NOT is_flagged
		WHERE id = ? AND created_by = ? AND deleted_at IS NULL`, id, userGUID)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, nil
	}

	return GetNoteByID(id, userGUID)
}

// toNullString converts a *string to sql.NullString for database
// operations. A nil pointer becomes an invalid (NULL) value.
func toNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: *s, Valid: true}
}
