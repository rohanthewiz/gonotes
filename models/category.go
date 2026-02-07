package models

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rohanthewiz/serr"
)

// CreateCategoriesTableSQL returns the DDL statement for creating the categories table.
const CreateCategoriesTableSQL = `
CREATE SEQUENCE IF NOT EXISTS categories_id_seq START 1;

CREATE TABLE IF NOT EXISTS categories (
    id            BIGINT PRIMARY KEY DEFAULT nextval('categories_id_seq'),
    name          VARCHAR NOT NULL,
    description   VARCHAR,
    subcategories VARCHAR,
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
`

// CreateNoteCategoriesTableSQL returns the DDL statement for creating the note_categories join table.
// The subcategories column stores a JSON array of subcategory names that apply to this note-category
// relationship, enabling queries like {"category": "k8s", "subcategories": ["pod", "replicaset"]}.
const CreateNoteCategoriesTableSQL = `
CREATE TABLE IF NOT EXISTS note_categories (
    note_id       BIGINT NOT NULL,
    category_id   BIGINT NOT NULL,
    subcategories VARCHAR,
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (note_id, category_id),
    FOREIGN KEY (note_id) REFERENCES notes(id),
    FOREIGN KEY (category_id) REFERENCES categories(id)
);
`

// DropCategoriesTableSQL is provided for testing and migration rollback scenarios.
const DropCategoriesTableSQL = `
DROP TABLE IF EXISTS note_categories;
DROP TABLE IF EXISTS categories;
DROP SEQUENCE IF EXISTS categories_id_seq;
`

// Category represents a category that can be assigned to notes
type Category struct {
	ID            int64          `json:"id"`
	Name          string         `json:"name"`
	Description   sql.NullString `json:"description,omitempty"`
	Subcategories sql.NullString `json:"subcategories,omitempty"` // JSON array stored as string
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// CategoryInput is used for creating/updating categories via API
type CategoryInput struct {
	Name          string   `json:"name"`
	Description   *string  `json:"description,omitempty"`
	Subcategories []string `json:"subcategories,omitempty"`
}

// CategoryOutput is used for API responses with proper null handling
type CategoryOutput struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Description   *string   `json:"description,omitempty"`
	Subcategories []string  `json:"subcategories,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ToOutput converts a Category to CategoryOutput for API responses
func (c *Category) ToOutput() CategoryOutput {
	output := CategoryOutput{
		ID:        c.ID,
		Name:      c.Name,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}

	if c.Description.Valid {
		output.Description = &c.Description.String
	}

	if c.Subcategories.Valid && c.Subcategories.String != "" {
		var subcats []string
		if err := json.Unmarshal([]byte(c.Subcategories.String), &subcats); err == nil {
			output.Subcategories = subcats
		}
	}

	return output
}

// NoteCategoryDetailOutput enriches CategoryOutput with note-specific subcategory selections.
// When retrieving categories for a specific note, the full list of available subcategories
// comes from the category itself, while selected_subcategories reflects which ones are
// actually assigned in the note_categories junction table for that note.
type NoteCategoryDetailOutput struct {
	ID                    int64     `json:"id"`
	Name                  string    `json:"name"`
	Description           *string   `json:"description,omitempty"`
	Subcategories         []string  `json:"subcategories,omitempty"`
	SelectedSubcategories []string  `json:"selected_subcategories,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// CreateCategory creates a new category in both disk and cache databases
func CreateCategory(input CategoryInput) (*Category, error) {
	if input.Name == "" {
		return nil, serr.New("category name is required")
	}

	// Convert subcategories to JSON string
	var subcatsJSON sql.NullString
	if len(input.Subcategories) > 0 {
		jsonBytes, err := json.Marshal(input.Subcategories)
		if err != nil {
			return nil, serr.Wrap(err, "failed to marshal subcategories")
		}
		subcatsJSON = sql.NullString{String: string(jsonBytes), Valid: true}
	}

	// Convert description to sql.NullString
	var description sql.NullString
	if input.Description != nil {
		description = sql.NullString{String: *input.Description, Valid: true}
	}

	// Insert into disk database first (source of truth)
	query := `INSERT INTO categories (name, description, subcategories, created_at, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id, name, description, subcategories, created_at, updated_at`

	var category Category
	err := db.QueryRow(query, input.Name, description, subcatsJSON).Scan(
		&category.ID,
		&category.Name,
		&category.Description,
		&category.Subcategories,
		&category.CreatedAt,
		&category.UpdatedAt,
	)
	if err != nil {
		return nil, serr.Wrap(err, "failed to create category in disk database")
	}

	// Insert into cache database
	cacheQuery := `INSERT INTO categories (id, name, description, subcategories, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`
	_, cacheErr := cacheDB.Exec(cacheQuery,
		category.ID,
		category.Name,
		category.Description,
		category.Subcategories,
		category.CreatedAt,
		category.UpdatedAt,
	)

	if cacheErr != nil {
		// Disk write succeeded, cache failed - return data with error
		return &category, serr.Wrap(cacheErr, "category created on disk but cache update failed")
	}

	return &category, nil
}

// GetCategory retrieves a category by ID from cache
func GetCategory(id int64) (*Category, error) {
	query := `SELECT id, name, description, subcategories, created_at, updated_at
		FROM categories WHERE id = ?`

	var category Category
	err := cacheDB.QueryRow(query, id).Scan(
		&category.ID,
		&category.Name,
		&category.Description,
		&category.Subcategories,
		&category.CreatedAt,
		&category.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, serr.New("category not found")
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category")
	}

	return &category, nil
}

// ListCategories retrieves all categories from cache
func ListCategories(limit, offset int) ([]Category, error) {
	query := `SELECT id, name, description, subcategories, created_at, updated_at
		FROM categories ORDER BY created_at DESC`

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	if offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", offset)
	}

	rows, err := cacheDB.Query(query)
	if err != nil {
		return nil, serr.Wrap(err, "failed to list categories")
	}
	defer rows.Close()

	var categories []Category
	for rows.Next() {
		var category Category
		err := rows.Scan(
			&category.ID,
			&category.Name,
			&category.Description,
			&category.Subcategories,
			&category.CreatedAt,
			&category.UpdatedAt,
		)
		if err != nil {
			return nil, serr.Wrap(err, "failed to scan category")
		}
		categories = append(categories, category)
	}

	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating categories")
	}

	return categories, nil
}

// UpdateCategory updates a category in both disk and cache databases
func UpdateCategory(id int64, input CategoryInput) (*Category, error) {
	if input.Name == "" {
		return nil, serr.New("category name is required")
	}

	// Verify category exists
	_, err := GetCategory(id)
	if err != nil {
		return nil, err
	}

	// Convert subcategories to JSON string
	var subcatsJSON sql.NullString
	if len(input.Subcategories) > 0 {
		jsonBytes, err := json.Marshal(input.Subcategories)
		if err != nil {
			return nil, serr.Wrap(err, "failed to marshal subcategories")
		}
		subcatsJSON = sql.NullString{String: string(jsonBytes), Valid: true}
	}

	// Convert description to sql.NullString
	var description sql.NullString
	if input.Description != nil {
		description = sql.NullString{String: *input.Description, Valid: true}
	}

	// Update disk database first (using separate UPDATE and SELECT since DuckDB can have
	// issues with UPDATE...RETURNING when sequences are involved)
	updateQuery := `UPDATE categories
		SET name = ?, description = ?, subcategories = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`

	_, err = db.Exec(updateQuery, input.Name, description, subcatsJSON, id)
	if err != nil {
		return nil, serr.Wrap(err, "failed to update category in disk database")
	}

	// Fetch the updated record
	selectQuery := `SELECT id, name, description, subcategories, created_at, updated_at
		FROM categories WHERE id = ?`

	var category Category
	err = db.QueryRow(selectQuery, id).Scan(
		&category.ID,
		&category.Name,
		&category.Description,
		&category.Subcategories,
		&category.CreatedAt,
		&category.UpdatedAt,
	)
	if err != nil {
		return nil, serr.Wrap(err, "failed to retrieve updated category from disk database")
	}

	// Update cache database
	cacheQuery := `UPDATE categories
		SET name = ?, description = ?, subcategories = ?, updated_at = ?
		WHERE id = ?`
	_, cacheErr := cacheDB.Exec(cacheQuery,
		category.Name,
		category.Description,
		category.Subcategories,
		category.UpdatedAt,
		category.ID,
	)

	if cacheErr != nil {
		return &category, serr.Wrap(cacheErr, "category updated on disk but cache update failed")
	}

	return &category, nil
}

// DeleteCategory deletes a category from both disk and cache databases
func DeleteCategory(id int64) error {
	// Verify category exists
	_, err := GetCategory(id)
	if err != nil {
		return err
	}

	// Delete from disk database first
	query := `DELETE FROM categories WHERE id = ?`
	_, err = db.Exec(query, id)
	if err != nil {
		return serr.Wrap(err, "failed to delete category from disk database")
	}

	// Delete from cache database
	_, cacheErr := cacheDB.Exec(query, id)
	if cacheErr != nil {
		return serr.Wrap(cacheErr, "category deleted from disk but cache delete failed")
	}

	return nil
}

// NoteCategory represents the many-to-many relationship between notes and categories.
// Subcategories is a JSON array stored as a string, allowing a note to be associated
// with specific subcategories within a category.
type NoteCategory struct {
	NoteID        int64          `json:"note_id"`
	CategoryID    int64          `json:"category_id"`
	Subcategories sql.NullString `json:"subcategories,omitempty"` // JSON array of subcategory names
	CreatedAt     time.Time      `json:"created_at"`
}

// NoteCategoryOutput is used for API responses with proper null handling
type NoteCategoryOutput struct {
	NoteID        int64     `json:"note_id"`
	CategoryID    int64     `json:"category_id"`
	Subcategories []string  `json:"subcategories,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// ToOutput converts a NoteCategory to NoteCategoryOutput for API responses
func (nc *NoteCategory) ToOutput() NoteCategoryOutput {
	output := NoteCategoryOutput{
		NoteID:     nc.NoteID,
		CategoryID: nc.CategoryID,
		CreatedAt:  nc.CreatedAt,
	}

	if nc.Subcategories.Valid && nc.Subcategories.String != "" {
		var subcats []string
		if err := json.Unmarshal([]byte(nc.Subcategories.String), &subcats); err == nil {
			output.Subcategories = subcats
		}
	}

	return output
}

// AddCategoryToNote adds a category to a note without subcategories.
// For adding with subcategories, use AddCategoryToNoteWithSubcategories.
func AddCategoryToNote(noteID, categoryID int64) error {
	return AddCategoryToNoteWithSubcategories(noteID, categoryID, nil)
}

// AddCategoryToNoteWithSubcategories adds a category to a note with optional subcategories.
// The subcategories slice can be nil or empty for no subcategories.
// Note: The caller (API layer) should verify note ownership before calling this function.
func AddCategoryToNoteWithSubcategories(noteID, categoryID int64, subcategories []string) error {
	// Verify note exists using a simple existence check (ownership verified by API layer)
	var exists int
	err := cacheDB.QueryRow(`SELECT 1 FROM notes WHERE id = ? AND deleted_at IS NULL`, noteID).Scan(&exists)
	if err != nil {
		return serr.New("note not found")
	}

	// Verify category exists
	_, err = GetCategory(categoryID)
	if err != nil {
		return err
	}

	// Check if relationship already exists
	var count int
	checkQuery := `SELECT COUNT(*) FROM note_categories WHERE note_id = ? AND category_id = ?`
	err = cacheDB.QueryRow(checkQuery, noteID, categoryID).Scan(&count)
	if err != nil {
		return serr.Wrap(err, "failed to check existing relationship")
	}
	if count > 0 {
		return serr.New("category already added to this note")
	}

	// Convert subcategories to JSON string
	var subcatsJSON sql.NullString
	if len(subcategories) > 0 {
		jsonBytes, err := json.Marshal(subcategories)
		if err != nil {
			return serr.Wrap(err, "failed to marshal subcategories")
		}
		subcatsJSON = sql.NullString{String: string(jsonBytes), Valid: true}
	}

	// Insert into disk database first
	query := `INSERT INTO note_categories (note_id, category_id, subcategories, created_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)`
	_, err = db.Exec(query, noteID, categoryID, subcatsJSON)
	if err != nil {
		return serr.Wrap(err, "failed to add category to note in disk database")
	}

	// Insert into cache database
	_, cacheErr := cacheDB.Exec(query, noteID, categoryID, subcatsJSON)
	if cacheErr != nil {
		return serr.Wrap(cacheErr, "relationship created on disk but cache update failed")
	}

	return nil
}

// UpdateNoteCategorySubcategories updates the subcategories for an existing note-category relationship.
func UpdateNoteCategorySubcategories(noteID, categoryID int64, subcategories []string) error {
	// Check if relationship exists
	var count int
	checkQuery := `SELECT COUNT(*) FROM note_categories WHERE note_id = ? AND category_id = ?`
	err := cacheDB.QueryRow(checkQuery, noteID, categoryID).Scan(&count)
	if err != nil {
		return serr.Wrap(err, "failed to check existing relationship")
	}
	if count == 0 {
		return serr.New("relationship not found")
	}

	// Convert subcategories to JSON string
	var subcatsJSON sql.NullString
	if len(subcategories) > 0 {
		jsonBytes, err := json.Marshal(subcategories)
		if err != nil {
			return serr.Wrap(err, "failed to marshal subcategories")
		}
		subcatsJSON = sql.NullString{String: string(jsonBytes), Valid: true}
	}

	// Update disk database first
	query := `UPDATE note_categories SET subcategories = ? WHERE note_id = ? AND category_id = ?`
	_, err = db.Exec(query, subcatsJSON, noteID, categoryID)
	if err != nil {
		return serr.Wrap(err, "failed to update subcategories in disk database")
	}

	// Update cache database
	_, cacheErr := cacheDB.Exec(query, subcatsJSON, noteID, categoryID)
	if cacheErr != nil {
		return serr.Wrap(cacheErr, "subcategories updated on disk but cache update failed")
	}

	return nil
}

// RemoveCategoryFromNote removes a category from a note
func RemoveCategoryFromNote(noteID, categoryID int64) error {
	// Delete from disk database first
	query := `DELETE FROM note_categories WHERE note_id = ? AND category_id = ?`
	result, err := db.Exec(query, noteID, categoryID)
	if err != nil {
		return serr.Wrap(err, "failed to remove category from note in disk database")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return serr.Wrap(err, "failed to get rows affected")
	}
	if rowsAffected == 0 {
		return serr.New("relationship not found")
	}

	// Delete from cache database
	_, cacheErr := cacheDB.Exec(query, noteID, categoryID)
	if cacheErr != nil {
		return serr.Wrap(cacheErr, "relationship deleted from disk but cache delete failed")
	}

	return nil
}

// GetNoteCategories retrieves all categories for a note
func GetNoteCategories(noteID int64) ([]Category, error) {
	query := `SELECT c.id, c.name, c.description, c.subcategories, c.created_at, c.updated_at
		FROM categories c
		INNER JOIN note_categories nc ON c.id = nc.category_id
		WHERE nc.note_id = ?
		ORDER BY c.name ASC`

	rows, err := cacheDB.Query(query, noteID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get note categories")
	}
	defer rows.Close()

	var categories []Category
	for rows.Next() {
		var category Category
		err := rows.Scan(
			&category.ID,
			&category.Name,
			&category.Description,
			&category.Subcategories,
			&category.CreatedAt,
			&category.UpdatedAt,
		)
		if err != nil {
			return nil, serr.Wrap(err, "failed to scan category")
		}
		categories = append(categories, category)
	}

	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating note categories")
	}

	return categories, nil
}

// GetNoteCategoryDetails retrieves categories for a note along with which subcategories
// are specifically selected for this note. Unlike GetNoteCategories which only returns
// category data, this also pulls nc.subcategories from the junction table so the caller
// knows which subcategories the user chose when linking the category to the note.
func GetNoteCategoryDetails(noteID int64) ([]NoteCategoryDetailOutput, error) {
	query := `SELECT c.id, c.name, c.description, c.subcategories, c.created_at, c.updated_at,
		nc.subcategories
		FROM categories c
		INNER JOIN note_categories nc ON c.id = nc.category_id
		WHERE nc.note_id = ?
		ORDER BY c.name ASC`

	rows, err := cacheDB.Query(query, noteID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get note category details")
	}
	defer rows.Close()

	var results []NoteCategoryDetailOutput
	for rows.Next() {
		var (
			cat              Category
			selectedSubcJSON sql.NullString
		)
		err := rows.Scan(
			&cat.ID, &cat.Name, &cat.Description, &cat.Subcategories,
			&cat.CreatedAt, &cat.UpdatedAt,
			&selectedSubcJSON,
		)
		if err != nil {
			return nil, serr.Wrap(err, "failed to scan note category detail")
		}

		// Build the output by converting the category fields and adding selected subcategories
		detail := NoteCategoryDetailOutput{
			ID:        cat.ID,
			Name:      cat.Name,
			CreatedAt: cat.CreatedAt,
			UpdatedAt: cat.UpdatedAt,
		}
		if cat.Description.Valid {
			detail.Description = &cat.Description.String
		}
		if cat.Subcategories.Valid && cat.Subcategories.String != "" {
			var subcats []string
			if err := json.Unmarshal([]byte(cat.Subcategories.String), &subcats); err == nil {
				detail.Subcategories = subcats
			}
		}
		if selectedSubcJSON.Valid && selectedSubcJSON.String != "" {
			var selectedSubcats []string
			if err := json.Unmarshal([]byte(selectedSubcJSON.String), &selectedSubcats); err == nil {
				detail.SelectedSubcategories = selectedSubcats
			}
		}

		results = append(results, detail)
	}

	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating note category details")
	}

	return results, nil
}

// GetCategoryNotes retrieves all notes for a category
func GetCategoryNotes(categoryID int64) ([]Note, error) {
	query := `SELECT n.id, n.guid, n.title, n.description, n.body, n.tags,
		n.is_private, n.encryption_iv, n.created_by, n.updated_by,
		n.created_at, n.updated_at, n.synced_at, n.deleted_at
		FROM notes n
		INNER JOIN note_categories nc ON n.id = nc.note_id
		WHERE nc.category_id = ? AND n.deleted_at IS NULL
		ORDER BY n.created_at DESC`

	rows, err := cacheDB.Query(query, categoryID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category notes")
	}
	defer rows.Close()

	var notes []Note
	for rows.Next() {
		var note Note
		err := rows.Scan(
			&note.ID,
			&note.GUID,
			&note.Title,
			&note.Description,
			&note.Body,
			&note.Tags,
			&note.IsPrivate,
			&note.EncryptionIV,
			&note.CreatedBy,
			&note.UpdatedBy,
			&note.CreatedAt,
			&note.UpdatedAt,
			&note.SyncedAt,
			&note.DeletedAt,
		)
		if err != nil {
			return nil, serr.Wrap(err, "failed to scan note")
		}
		notes = append(notes, note)
	}

	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating category notes")
	}

	return notes, nil
}

// GetCategoryByName retrieves a category by its name from cache.
// Returns nil, nil if the category doesn't exist.
func GetCategoryByName(name string) (*Category, error) {
	query := `SELECT id, name, description, subcategories, created_at, updated_at
		FROM categories WHERE name = ?`

	var category Category
	err := cacheDB.QueryRow(query, name).Scan(
		&category.ID,
		&category.Name,
		&category.Description,
		&category.Subcategories,
		&category.CreatedAt,
		&category.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category by name")
	}

	return &category, nil
}

// GetNotesByCategoryName retrieves all notes that belong to the specified category name.
// The userGUID parameter filters to notes owned by that user.
// Returns empty slice if the category doesn't exist or has no notes.
func GetNotesByCategoryName(categoryName string, userGUID string) ([]Note, error) {
	query := `SELECT n.id, n.guid, n.title, n.description, n.body, n.tags,
		n.is_private, n.encryption_iv, n.created_by, n.updated_by,
		n.created_at, n.updated_at, n.synced_at, n.deleted_at
		FROM notes n
		INNER JOIN note_categories nc ON n.id = nc.note_id
		INNER JOIN categories c ON nc.category_id = c.id
		WHERE c.name = ? AND n.created_by = ? AND n.deleted_at IS NULL
		ORDER BY n.created_at DESC`

	rows, err := cacheDB.Query(query, categoryName, userGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get notes by category name")
	}
	defer rows.Close()

	var notes []Note
	for rows.Next() {
		var note Note
		err := rows.Scan(
			&note.ID,
			&note.GUID,
			&note.Title,
			&note.Description,
			&note.Body,
			&note.Tags,
			&note.IsPrivate,
			&note.EncryptionIV,
			&note.CreatedBy,
			&note.UpdatedBy,
			&note.CreatedAt,
			&note.UpdatedAt,
			&note.SyncedAt,
			&note.DeletedAt,
		)
		if err != nil {
			return nil, serr.Wrap(err, "failed to scan note")
		}
		notes = append(notes, note)
	}

	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating notes")
	}

	return notes, nil
}

// NoteCategoryMapping is a lightweight struct for the bulk note-category mapping endpoint.
// Unlike NoteCategoryDetailOutput (which is per-note), this returns ALL mappings across
// all notes so the client can build a lookup table for client-side category filtering.
type NoteCategoryMapping struct {
	NoteID                int64    `json:"note_id"`
	CategoryID            int64    `json:"category_id"`
	CategoryName          string   `json:"category_name"`
	SelectedSubcategories []string `json:"selected_subcategories,omitempty"`
}

// GetAllNoteCategoryMappings retrieves every note-category relationship in one query.
// The userGUID scopes results to only that user's notes (via notes.created_by).
// This powers the search-bar category filter — the client caches the result in a
// lookup map keyed by note ID so filtering is instant without per-note API calls.
func GetAllNoteCategoryMappings(userGUID string) ([]NoteCategoryMapping, error) {
	query := `SELECT nc.note_id, nc.category_id, c.name, nc.subcategories
		FROM note_categories nc
		INNER JOIN categories c ON nc.category_id = c.id
		INNER JOIN notes n ON nc.note_id = n.id
		WHERE n.created_by = ? AND n.deleted_at IS NULL
		ORDER BY nc.note_id, c.name`

	rows, err := cacheDB.Query(query, userGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to query note-category mappings")
	}
	defer rows.Close()

	var mappings []NoteCategoryMapping
	for rows.Next() {
		var (
			m            NoteCategoryMapping
			subcatsJSON  sql.NullString
		)
		if err := rows.Scan(&m.NoteID, &m.CategoryID, &m.CategoryName, &subcatsJSON); err != nil {
			return nil, serr.Wrap(err, "failed to scan note-category mapping")
		}
		// Parse the JSON subcategories array stored in the junction table
		if subcatsJSON.Valid && subcatsJSON.String != "" {
			var subcats []string
			if err := json.Unmarshal([]byte(subcatsJSON.String), &subcats); err == nil {
				m.SelectedSubcategories = subcats
			}
		}
		mappings = append(mappings, m)
	}

	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating note-category mappings")
	}

	return mappings, nil
}

// GetNotesByCategoryAndSubcategories retrieves notes that belong to the specified category
// and have ALL the specified subcategories. This uses DuckDB's JSON functions to query
// the subcategories array stored in the note_categories table.
// The userGUID parameter filters to notes owned by that user.
// Returns empty slice if no matching notes are found.
func GetNotesByCategoryAndSubcategories(categoryName string, subcategories []string, userGUID string) ([]Note, error) {
	if len(subcategories) == 0 {
		return GetNotesByCategoryName(categoryName, userGUID)
	}

	// Build the query with JSON array contains checks for each subcategory.
	// DuckDB supports list_contains for checking if an array contains a value.
	// Since subcategories is stored as JSON string, we need to parse it first.
	query := `SELECT n.id, n.guid, n.title, n.description, n.body, n.tags,
		n.is_private, n.encryption_iv, n.created_by, n.updated_by,
		n.created_at, n.updated_at, n.synced_at, n.deleted_at
		FROM notes n
		INNER JOIN note_categories nc ON n.id = nc.note_id
		INNER JOIN categories c ON nc.category_id = c.id
		WHERE c.name = ? AND n.created_by = ? AND n.deleted_at IS NULL AND nc.subcategories IS NOT NULL`

	// Add a condition for each subcategory to ensure ALL are present.
	// Using DuckDB's json_extract_string with list_contains.
	for range subcategories {
		query += ` AND list_contains(json_extract_string(nc.subcategories, '$[*]')::VARCHAR[], ?)`
	}

	query += ` ORDER BY n.created_at DESC`

	// Build args: category name first, userGUID second, then each subcategory
	args := make([]interface{}, 0, len(subcategories)+2)
	args = append(args, categoryName)
	args = append(args, userGUID)
	for _, subcat := range subcategories {
		args = append(args, subcat)
	}

	rows, err := cacheDB.Query(query, args...)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get notes by category and subcategories")
	}
	defer rows.Close()

	var notes []Note
	for rows.Next() {
		var note Note
		err := rows.Scan(
			&note.ID,
			&note.GUID,
			&note.Title,
			&note.Description,
			&note.Body,
			&note.Tags,
			&note.IsPrivate,
			&note.EncryptionIV,
			&note.CreatedBy,
			&note.UpdatedBy,
			&note.CreatedAt,
			&note.UpdatedAt,
			&note.SyncedAt,
			&note.DeletedAt,
		)
		if err != nil {
			return nil, serr.Wrap(err, "failed to scan note")
		}
		notes = append(notes, note)
	}

	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating notes")
	}

	return notes, nil
}
