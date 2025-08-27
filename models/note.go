package models

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"
	"github.com/rohanthewiz/serr"
	"github.com/rohanthewiz/logger"
)

// Note represents a note in the system
type Note struct {
	ID          int64          `db:"id" json:"id"`
	GUID        string         `db:"guid" json:"guid"`
	Title       string         `db:"title" json:"title"`
	Description sql.NullString `db:"description" json:"description,omitempty"`
	Body        sql.NullString `db:"body" json:"body,omitempty"`
	Tags        string         `db:"tags" json:"tags"` // JSON array
	IsPrivate   bool           `db:"is_private" json:"is_private"`
	CreatedBy   sql.NullString `db:"created_by" json:"created_by,omitempty"`
	UpdatedBy   sql.NullString `db:"updated_by" json:"updated_by,omitempty"`
	CreatedAt   time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at" json:"updated_at"`
	SyncedAt    sql.NullTime   `db:"synced_at" json:"synced_at,omitempty"`
	DeletedAt   sql.NullTime   `db:"deleted_at" json:"deleted_at,omitempty"`
}

// User represents a user in the system
type User struct {
	ID          int64          `db:"id" json:"id"`
	GUID        string         `db:"guid" json:"guid"`
	Email       sql.NullString `db:"email" json:"email,omitempty"`
	Name        sql.NullString `db:"name" json:"name,omitempty"`
	CreatedAt   time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at" json:"updated_at"`
	LastLoginAt sql.NullTime   `db:"last_login_at" json:"last_login_at,omitempty"`
	IsActive    bool           `db:"is_active" json:"is_active"`
}

// NoteUser represents the relationship between notes and users
type NoteUser struct {
	NoteGUID   string         `db:"note_guid" json:"note_guid"`
	UserGUID   string         `db:"user_guid" json:"user_guid"`
	Permission string         `db:"permission" json:"permission"` // read, write, owner
	SharedBy   sql.NullString `db:"shared_by" json:"shared_by,omitempty"`
	SharedAt   time.Time      `db:"shared_at" json:"shared_at"`
}

// generateGUID creates a unique identifier
func generateGUID() string {
	h := sha1.New()
	h.Write([]byte(time.Now().String()))
	return hex.EncodeToString(h.Sum(nil))[:16] // Use first 16 chars for shorter GUIDs
}

// Save creates a new note using dual-database write-through
func (n *Note) Save(userGUID string) error {
	n.GUID = generateGUID()
	n.CreatedBy = sql.NullString{String: userGUID, Valid: true}
	n.UpdatedBy = sql.NullString{String: userGUID, Valid: true}
	n.CreatedAt = time.Now()
	n.UpdatedAt = time.Now()
	
	// Use transaction for atomicity
	tx, err := BeginDualTx()
	if err != nil {
		return serr.Wrap(err, "failed to begin transaction")
	}
	defer tx.Rollback()
	
	// Insert note
	query := `
		INSERT INTO notes (guid, title, description, body, tags, is_private,
		                  created_by, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	
	err = tx.Exec(query, n.GUID, n.Title, n.Description, n.Body, n.Tags, n.IsPrivate,
	              n.CreatedBy, n.UpdatedBy, n.CreatedAt, n.UpdatedAt)
	if err != nil {
		return serr.Wrap(err, "failed to save note")
	}
	
	// Create ownership record
	err = tx.Exec(`
		INSERT INTO note_users (note_guid, user_guid, permission, shared_by, shared_at)
		VALUES (?, ?, 'owner', ?, ?)
	`, n.GUID, userGUID, userGUID, time.Now())
	if err != nil {
		return serr.Wrap(err, "failed to create ownership record")
	}
	
	return tx.Commit()
}

// Update modifies an existing note using dual-database write-through
func (n *Note) Update(userGUID string) error {
	n.UpdatedBy = sql.NullString{String: userGUID, Valid: true}
	n.UpdatedAt = time.Now()
	
	query := `
		UPDATE notes 
		SET title = ?, description = ?, body = ?, tags = ?, is_private = ?,
		    updated_by = ?, updated_at = ?
		WHERE guid = ?
	`
	
	// Use WriteThrough for dual-database consistency
	err := WriteThrough(query, n.Title, n.Description, n.Body, n.Tags, n.IsPrivate,
	                   n.UpdatedBy, n.UpdatedAt, n.GUID)
	return serr.Wrap(err, "failed to update note")
}

// Delete soft-deletes a note
func (n *Note) Delete(userGUID string) error {
	query := `
		UPDATE notes 
		SET deleted_at = ?, updated_by = ?, updated_at = ?
		WHERE guid = ?
	`
	
	now := time.Now()
	err := WriteThrough(query, now, userGUID, now, n.GUID)
	return serr.Wrap(err, "failed to delete note")
}

// GetNoteByGUID retrieves a single note by GUID
func GetNoteByGUID(guid string) (*Note, error) {
	query := `
		SELECT id, guid, title, description, body, tags, is_private,
		       created_by, updated_by, created_at, updated_at, synced_at, deleted_at
		FROM notes
		WHERE guid = ? AND deleted_at IS NULL
	`
	
	note := &Note{}
	row := QueryRowFromCache(query, guid)
	err := row.Scan(&note.ID, &note.GUID, &note.Title, &note.Description,
	               &note.Body, &note.Tags, &note.IsPrivate, &note.CreatedBy, 
	               &note.UpdatedBy, &note.CreatedAt, &note.UpdatedAt, 
	               &note.SyncedAt, &note.DeletedAt)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get note by GUID")
	}
	
	return note, nil
}

// GetNotesForUser retrieves notes from the in-memory cache
func GetNotesForUser(userGUID string, limit int, offset int) ([]Note, error) {
	query := `
		SELECT n.id, n.guid, n.title, n.description, n.body, n.tags, n.is_private,
		       n.created_by, n.updated_by, n.created_at, n.updated_at,
		       n.synced_at, n.deleted_at
		FROM notes n
		JOIN note_users nu ON n.guid = nu.note_guid
		WHERE nu.user_guid = ? AND n.deleted_at IS NULL
		ORDER BY n.updated_at DESC
		LIMIT ? OFFSET ?
	`
	
	rows, err := ReadFromCache(query, userGUID, limit, offset)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get notes")
	}
	defer rows.Close()
	
	return scanNotes(rows)
}

// GetRecentNotes gets the most recently updated notes
func GetRecentNotes(userGUID string, limit int) ([]Note, error) {
	return GetNotesForUser(userGUID, limit, 0)
}

// SearchByTitle performs fast title search using in-memory cache
func SearchByTitle(userGUID string, searchQuery string) ([]Note, error) {
	query := `
		SELECT n.id, n.guid, n.title, n.description, n.body, n.tags, n.is_private,
		       n.created_by, n.updated_by, n.created_at, n.updated_at,
		       n.synced_at, n.deleted_at
		FROM notes n
		JOIN note_users nu ON n.guid = nu.note_guid
		WHERE nu.user_guid = ? 
		  AND n.title LIKE ? 
		  AND n.deleted_at IS NULL
		ORDER BY n.updated_at DESC
	`
	
	rows, err := ReadFromCache(query, userGUID, "%"+searchQuery+"%")
	if err != nil {
		return nil, serr.Wrap(err, "failed to search notes by title")
	}
	defer rows.Close()
	
	return scanNotes(rows)
}

// SearchByTag searches for notes with a specific tag
func SearchByTag(userGUID string, tag string) ([]Note, error) {
	query := `
		SELECT n.id, n.guid, n.title, n.description, n.body, n.tags, n.is_private,
		       n.created_by, n.updated_by, n.created_at, n.updated_at,
		       n.synced_at, n.deleted_at
		FROM notes n
		JOIN note_users nu ON n.guid = nu.note_guid
		WHERE nu.user_guid = ? 
		  AND n.tags LIKE ? 
		  AND n.deleted_at IS NULL
		ORDER BY n.updated_at DESC
	`
	
	rows, err := ReadFromCache(query, userGUID, "%\""+tag+"\"%")
	if err != nil {
		return nil, serr.Wrap(err, "failed to search notes by tag")
	}
	defer rows.Close()
	
	return scanNotes(rows)
}

// SearchByBody performs full-text search in note bodies
func SearchByBody(userGUID string, searchQuery string) ([]Note, error) {
	query := `
		SELECT n.id, n.guid, n.title, n.description, n.body, n.tags, n.is_private,
		       n.created_by, n.updated_by, n.created_at, n.updated_at,
		       n.synced_at, n.deleted_at
		FROM notes n
		JOIN note_users nu ON n.guid = nu.note_guid
		WHERE nu.user_guid = ? 
		  AND n.body LIKE ? 
		  AND n.deleted_at IS NULL
		ORDER BY n.updated_at DESC
	`
	
	rows, err := ReadFromCache(query, userGUID, "%"+searchQuery+"%")
	if err != nil {
		return nil, serr.Wrap(err, "failed to search notes by body")
	}
	defer rows.Close()
	
	return scanNotes(rows)
}

// SearchAll performs a comprehensive search across title, tags, and body
func SearchAll(userGUID string, searchQuery string) ([]Note, error) {
	query := `
		SELECT DISTINCT n.id, n.guid, n.title, n.description, n.body, n.tags, n.is_private,
		       n.created_by, n.updated_by, n.created_at, n.updated_at,
		       n.synced_at, n.deleted_at
		FROM notes n
		JOIN note_users nu ON n.guid = nu.note_guid
		WHERE nu.user_guid = ? 
		  AND (n.title LIKE ? OR n.tags LIKE ? OR n.body LIKE ?)
		  AND n.deleted_at IS NULL
		ORDER BY n.updated_at DESC
	`
	
	searchPattern := "%" + searchQuery + "%"
	rows, err := ReadFromCache(query, userGUID, searchPattern, searchPattern, searchPattern)
	if err != nil {
		return nil, serr.Wrap(err, "failed to search all notes")
	}
	defer rows.Close()
	
	return scanNotes(rows)
}

// UserCanEditNote checks if a user has write permission for a note
func UserCanEditNote(userGUID, noteGUID string) (bool, error) {
	query := `
		SELECT permission
		FROM note_users
		WHERE user_guid = ? AND note_guid = ?
	`
	
	var permission string
	err := QueryRowFromCache(query, userGUID, noteGUID).Scan(&permission)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, serr.Wrap(err, "failed to check edit permission")
	}
	
	return permission == "write" || permission == "owner", nil
}

// UserCanReadNote checks if a user has read permission for a note
func UserCanReadNote(userGUID, noteGUID string) (bool, error) {
	query := `
		SELECT COUNT(*)
		FROM note_users
		WHERE user_guid = ? AND note_guid = ?
	`
	
	var count int
	err := QueryRowFromCache(query, userGUID, noteGUID).Scan(&count)
	if err != nil {
		return false, serr.Wrap(err, "failed to check read permission")
	}
	
	return count > 0, nil
}

// Helper function to scan notes from rows
func scanNotes(rows *sql.Rows) ([]Note, error) {
	var notes []Note
	for rows.Next() {
		var note Note
		err := rows.Scan(&note.ID, &note.GUID, &note.Title, &note.Description,
		                &note.Body, &note.Tags, &note.IsPrivate, &note.CreatedBy, 
		                &note.UpdatedBy, &note.CreatedAt, &note.UpdatedAt,
		                &note.SyncedAt, &note.DeletedAt)
		if err != nil {
			logger.LogErr(err, "failed to scan note")
			continue
		}
		notes = append(notes, note)
	}
	return notes, nil
}

// GetTagsFromJSON extracts tags from JSON string
func (n *Note) GetTagsFromJSON() []string {
	var tags []string
	if n.Tags != "" {
		_ = json.Unmarshal([]byte(n.Tags), &tags)
	}
	return tags
}

// SetTagsAsJSON sets tags as JSON string
func (n *Note) SetTagsAsJSON(tags []string) {
	if len(tags) == 0 {
		n.Tags = "[]"
		return
	}
	jsonBytes, _ := json.Marshal(tags)
	n.Tags = string(jsonBytes)
}

// GetAllUniqueTags gets all unique tags for a user
func GetAllUniqueTags(userGUID string) ([]string, error) {
	query := `
		SELECT DISTINCT n.tags
		FROM notes n
		JOIN note_users nu ON n.guid = nu.note_guid
		WHERE nu.user_guid = ? AND n.deleted_at IS NULL
	`
	
	rows, err := ReadFromCache(query, userGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get unique tags")
	}
	defer rows.Close()
	
	tagMap := make(map[string]bool)
	for rows.Next() {
		var tagsJSON string
		if err := rows.Scan(&tagsJSON); err != nil {
			continue
		}
		
		var tags []string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			continue
		}
		
		for _, tag := range tags {
			tagMap[tag] = true
		}
	}
	
	// Convert map to slice
	uniqueTags := make([]string, 0, len(tagMap))
	for tag := range tagMap {
		uniqueTags = append(uniqueTags, tag)
	}
	
	return uniqueTags, nil
}