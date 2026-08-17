package models

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/rohanthewiz/serr"
)

// migrate.go exposes low-level, value-preserving insert helpers used by the
// standalone DuckDB→bytdb migration tool (scripts/migrate). Unlike the
// normal Create* functions, these preserve the source GUIDs, timestamps,
// and ownership verbatim and record NO sync change-tracking — a migration
// is a bulk data move, not a fresh authoring event.
//
// Fresh integer ids are drawn from each engine's own sequence (via
// nextval) rather than reusing the source ids: the old single-database id
// space would otherwise collide with the two independent, offset id spaces
// of the new public/private databases. The helpers return the new id so the
// caller can remap foreign keys (note_categories) accordingly.

// MigrateInsertUser inserts a user verbatim into the public database,
// returning its new id.
func MigrateInsertUser(guid, username string, email, displayName sql.NullString,
	passwordHash string, isActive, isAdmin bool,
	createdAt, updatedAt time.Time, lastLoginAt sql.NullTime) (int64, error) {

	query := `INSERT INTO users
		(id, guid, username, email, password_hash, display_name, is_active, is_admin, created_at, updated_at, last_login_at)
		VALUES (nextval('users_id_seq'), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id`
	var id int64
	err := pubDB.QueryRow(query, guid, username, email, passwordHash, displayName,
		isActive, isAdmin, createdAt, updatedAt, lastLoginAt).Scan(&id)
	if err != nil {
		return 0, serr.Wrap(err, "failed to migrate user", "username", username)
	}
	return id, nil
}

// MigrateInsertCategory inserts a category verbatim into the public catalog,
// returning its new id.
func MigrateInsertCategory(guid, name string, description, subcategories, createdBy sql.NullString,
	createdAt, updatedAt time.Time) (int64, error) {

	query := `INSERT INTO categories
		(id, guid, name, description, subcategories, created_by, created_at, updated_at)
		VALUES (nextval('categories_id_seq'), ?, ?, ?, ?, ?, ?, ?)
		RETURNING id`
	var id int64
	err := pubDB.QueryRow(query, guid, name, description, subcategories, createdBy, createdAt, updatedAt).Scan(&id)
	if err != nil {
		return 0, serr.Wrap(err, "failed to migrate category", "name", name)
	}
	return id, nil
}

// MigrateInsertNote inserts a note verbatim into the database matching its
// privacy (private notes to the encrypted database), returning its new id.
// The body must already be plaintext — the caller decrypts any legacy
// per-note ciphertext before calling, since bytdb encrypts the private
// database as a whole.
func MigrateInsertNote(guid, title string, description, body, tags sql.NullString,
	isPrivate, isFlagged bool, createdBy, updatedBy sql.NullString,
	createdAt, updatedAt time.Time, authoredAt, syncedAt, deletedAt sql.NullTime) (int64, error) {

	en := noteEngine(isPrivate)
	// version starts at 1: a migrated note has no edit history in the new
	// database, so the counter starts where a freshly created note's does.
	query := `INSERT INTO notes
		(id, guid, title, description, body, tags, is_private, is_flagged,
		 created_by, updated_by, created_at, updated_at, authored_at, synced_at, deleted_at, version)
		VALUES (nextval('notes_id_seq'), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
		RETURNING id`
	var id int64
	err := en.QueryRow(query, guid, title, description, body, tags, isPrivate, isFlagged,
		createdBy, updatedBy, createdAt, updatedAt, authoredAt, syncedAt, deletedAt).Scan(&id)
	if err != nil {
		return 0, serr.Wrap(err, "failed to migrate note", "guid", guid)
	}
	return id, nil
}

// MigrateInsertNoteCategory inserts a note⇄category link into the note's
// database (selected by isPrivate). noteID and categoryID must be the NEW
// ids returned by MigrateInsertNote / MigrateInsertCategory.
func MigrateInsertNoteCategory(noteID, categoryID int64, subcategories sql.NullString,
	createdAt time.Time, isPrivate bool) error {

	en := noteEngine(isPrivate)
	_, err := en.Exec(`INSERT INTO note_categories (note_id, category_id, subcategories, created_at)
		VALUES (?, ?, ?, ?)`, noteID, categoryID, subcategories, createdAt)
	if err != nil {
		return serr.Wrap(err, "failed to migrate note_category link",
			"note_id", strconv.FormatInt(noteID, 10), "category_id", strconv.FormatInt(categoryID, 10))
	}
	return nil
}
