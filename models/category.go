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
const CreateNoteCategoriesTableSQL = `
CREATE TABLE IF NOT EXISTS note_categories (
    note_id     BIGINT NOT NULL,
    category_id BIGINT NOT NULL,
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (note_id, category_id),
    FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE,
    FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE
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

// CreateCategory creates a new category in both disk and cache databases
func CreateCategory(input CategoryInput) (*Category, error) {
	if input.Name == "" {
		return nil, serr.NewErr("category name is required")
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
		VALUES (?, ?, ?, ?, ?)`
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
		return nil, serr.NewErr("category not found")
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
		return nil, serr.NewErr("category name is required")
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

	// Update disk database first
	query := `UPDATE categories
		SET name = ?, description = ?, subcategories = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		RETURNING id, name, description, subcategories, created_at, updated_at`

	var category Category
	err = db.QueryRow(query, input.Name, description, subcatsJSON, id).Scan(
		&category.ID,
		&category.Name,
		&category.Description,
		&category.Subcategories,
		&category.CreatedAt,
		&category.UpdatedAt,
	)
	if err != nil {
		return nil, serr.Wrap(err, "failed to update category in disk database")
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

// NoteCategory represents the many-to-many relationship between notes and categories
type NoteCategory struct {
	NoteID     int64     `json:"note_id"`
	CategoryID int64     `json:"category_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// AddCategoryToNote adds a category to a note
func AddCategoryToNote(noteID, categoryID int64) error {
	// Verify note exists
	_, err := GetNote(noteID)
	if err != nil {
		return err
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
		return serr.NewErr("category already added to this note")
	}

	// Insert into disk database first
	query := `INSERT INTO note_categories (note_id, category_id, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)`
	_, err = db.Exec(query, noteID, categoryID)
	if err != nil {
		return serr.Wrap(err, "failed to add category to note in disk database")
	}

	// Insert into cache database
	_, cacheErr := cacheDB.Exec(query, noteID, categoryID)
	if cacheErr != nil {
		return serr.Wrap(cacheErr, "relationship created on disk but cache update failed")
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
		return serr.NewErr("relationship not found")
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
