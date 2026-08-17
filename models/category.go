package models

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/rohanthewiz/serr"
)

// category.go manages the categories catalog and the note⇄category links.
//
// Two-database layout:
//   - The categories catalog lives ONLY in the public database (a shared
//     table). Its integer id is therefore globally meaningful.
//   - note_categories link rows live in the SAME database as their note
//     (a private note's links in the encrypted database). They reference
//     the catalog by category_id, but there is no SQL foreign key — the
//     link and the category may sit in different databases.
//
// Consequently any query that joins note_categories to categories is
// resolved in Go: link rows are read from the note's database, then the
// category rows are fetched from the public catalog and merged. Queries
// that join note_categories to notes (both co-located) stay in SQL but
// fan out across the two databases and merge.

// categoryCols is the canonical categories projection.
const categoryCols = `id, guid, name, description, subcategories, created_by, created_at, updated_at`

func scanCategory(s scanner, c *Category) error {
	return s.Scan(&c.ID, &c.GUID, &c.Name, &c.Description, &c.Subcategories, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
}

// noteColsN is the notes projection with an `n.` alias, for JOIN queries.
//
// It must stay column-for-column identical to noteCols in note.go — both feed
// the same scanNoteRow, so a column added to one and not the other produces a
// scan whose destinations outnumber its columns, at runtime, on whichever
// screen happens to use the JOIN.
const noteColsN = `n.id, n.guid, n.title, n.description, n.body, n.tags, n.is_private, n.is_flagged,
	n.created_by, n.updated_by, n.created_at, n.updated_at, n.authored_at, n.synced_at, n.deleted_at, n.version`

// Category represents a category that can be assigned to notes. GUID
// provides cross-machine identity for peer-to-peer sync.
type Category struct {
	ID            int64          `json:"id"`
	GUID          string         `json:"guid"`
	Name          string         `json:"name"`
	Description   sql.NullString `json:"description,omitempty"`
	Subcategories sql.NullString `json:"subcategories,omitempty"` // JSON array stored as string
	CreatedBy     sql.NullString `json:"created_by,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// CategoryInput is used for creating/updating categories via API.
type CategoryInput struct {
	Name          string   `json:"name"`
	Description   *string  `json:"description,omitempty"`
	Subcategories []string `json:"subcategories,omitempty"`
}

// CategoryOutput is used for API responses with proper null handling.
type CategoryOutput struct {
	ID            int64     `json:"id"`
	GUID          string    `json:"guid"`
	Name          string    `json:"name"`
	Description   *string   `json:"description,omitempty"`
	Subcategories []string  `json:"subcategories,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ToOutput converts a Category to CategoryOutput for API responses.
func (c *Category) ToOutput() CategoryOutput {
	output := CategoryOutput{
		ID:        c.ID,
		GUID:      c.GUID,
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

// NoteCategoryDetailOutput enriches CategoryOutput with the note-specific
// subcategory selections stored in the junction table.
type NoteCategoryDetailOutput struct {
	ID                    int64     `json:"id"`
	Name                  string    `json:"name"`
	Description           *string   `json:"description,omitempty"`
	Subcategories         []string  `json:"subcategories,omitempty"`
	SelectedSubcategories []string  `json:"selected_subcategories,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// CreateCategory creates a new category in the public catalog.
func CreateCategory(input CategoryInput, userGUID string) (*Category, error) {
	if input.Name == "" {
		return nil, serr.New("category name is required")
	}

	var subcatsJSON sql.NullString
	if len(input.Subcategories) > 0 {
		jsonBytes, err := json.Marshal(input.Subcategories)
		if err != nil {
			return nil, serr.Wrap(err, "failed to marshal subcategories")
		}
		subcatsJSON = sql.NullString{String: string(jsonBytes), Valid: true}
	}

	var description sql.NullString
	if input.Description != nil {
		description = sql.NullString{String: *input.Description, Valid: true}
	}

	categoryGUID := uuid.New().String()

	createdBy := sql.NullString{}
	if userGUID != "" {
		createdBy = sql.NullString{String: userGUID, Valid: true}
	}

	// created_at/updated_at take their column defaults (CURRENT_TIMESTAMP).
	query := `INSERT INTO categories (id, guid, name, description, subcategories, created_by)
		VALUES (nextval('categories_id_seq'), ?, ?, ?, ?, ?)
		RETURNING ` + categoryCols

	var category Category
	err := scanCategory(pubDB.QueryRow(query, categoryGUID, input.Name, description, subcatsJSON, createdBy), &category)
	if err != nil {
		return nil, serr.Wrap(err, "failed to create category")
	}

	// Record change for sync (non-blocking).
	recordCategoryCreateChange(category, input)

	return &category, nil
}

// getCategoryByID fetches a category from the public catalog by id,
// returning nil, nil when absent. Internal helper for cross-DB joins.
func getCategoryByID(id int64) (*Category, error) {
	var category Category
	err := scanCategory(pubDB.QueryRow(`SELECT `+categoryCols+` FROM categories WHERE id = ?`, id), &category)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &category, nil
}

// GetCategory retrieves a category by id. With a non-empty userGUID it
// enforces ownership.
func GetCategory(id int64, userGUID string) (*Category, error) {
	query := `SELECT ` + categoryCols + ` FROM categories WHERE id = ?`
	args := []any{id}
	if userGUID != "" {
		query += ` AND created_by = ?`
		args = append(args, userGUID)
	}

	var category Category
	err := scanCategory(pubDB.QueryRow(query, args...), &category)
	if err == sql.ErrNoRows {
		return nil, serr.New("category not found")
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category")
	}
	return &category, nil
}

// ListCategories retrieves categories from the public catalog.
func ListCategories(limit, offset int, userGUID string) ([]Category, error) {
	query := `SELECT ` + categoryCols + ` FROM categories`
	var args []any
	if userGUID != "" {
		query += ` WHERE created_by = ?`
		args = append(args, userGUID)
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	if offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", offset)
	}

	rows, err := pubDB.Query(query, args...)
	if err != nil {
		return nil, serr.Wrap(err, "failed to list categories")
	}
	defer rows.Close()

	var categories []Category
	for rows.Next() {
		var category Category
		if err := scanCategory(rows, &category); err != nil {
			return nil, serr.Wrap(err, "failed to scan category")
		}
		categories = append(categories, category)
	}
	return categories, rows.Err()
}

// UpdateCategory updates a category in the public catalog, recording a sync
// change. A non-empty userGUID enforces ownership.
func UpdateCategory(id int64, input CategoryInput, userGUID string) (*Category, error) {
	if input.Name == "" {
		return nil, serr.New("category name is required")
	}

	existing, err := GetCategory(id, userGUID)
	if err != nil {
		return nil, err
	}

	var subcatsJSON sql.NullString
	if len(input.Subcategories) > 0 {
		jsonBytes, err := json.Marshal(input.Subcategories)
		if err != nil {
			return nil, serr.Wrap(err, "failed to marshal subcategories")
		}
		subcatsJSON = sql.NullString{String: string(jsonBytes), Valid: true}
	}

	var description sql.NullString
	if input.Description != nil {
		description = sql.NullString{String: *input.Description, Valid: true}
	}

	// Separate UPDATE + SELECT (kept from the DuckDB era for parity).
	updateQuery := `UPDATE categories
		SET name = ?, description = ?, subcategories = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`
	if _, err = pubDB.Exec(updateQuery, input.Name, description, subcatsJSON, id); err != nil {
		return nil, serr.Wrap(err, "failed to update category")
	}

	var category Category
	err = scanCategory(pubDB.QueryRow(`SELECT `+categoryCols+` FROM categories WHERE id = ?`, id), &category)
	if err != nil {
		return nil, serr.Wrap(err, "failed to retrieve updated category")
	}

	// Record change for sync (non-blocking).
	recordCategoryUpdateChange(*existing, category, input)

	return &category, nil
}

// DeleteCategory removes a category from the public catalog, recording a
// sync change. A non-empty userGUID enforces ownership.
func DeleteCategory(id int64, userGUID string) error {
	existing, err := GetCategory(id, userGUID)
	if err != nil {
		return err
	}

	if _, err = pubDB.Exec(`DELETE FROM categories WHERE id = ?`, id); err != nil {
		return serr.Wrap(err, "failed to delete category")
	}

	// Record change for sync (non-blocking).
	recordCategoryDeleteChange(existing.GUID)

	return nil
}

// NoteCategory represents the many-to-many link between notes and
// categories. Subcategories is a JSON array stored as a string.
type NoteCategory struct {
	NoteID        int64          `json:"note_id"`
	CategoryID    int64          `json:"category_id"`
	Subcategories sql.NullString `json:"subcategories,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

// NoteCategoryOutput is used for API responses with proper null handling.
type NoteCategoryOutput struct {
	NoteID        int64     `json:"note_id"`
	CategoryID    int64     `json:"category_id"`
	Subcategories []string  `json:"subcategories,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// ToOutput converts a NoteCategory to NoteCategoryOutput.
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
func AddCategoryToNote(noteID, categoryID int64, userGUID string) error {
	return AddCategoryToNoteWithSubcategories(noteID, categoryID, nil, userGUID)
}

// AddCategoryToNoteWithSubcategories links a category to a note with
// optional subcategories. The link row is written to the note's own
// database; the category is validated against the public catalog.
func AddCategoryToNoteWithSubcategories(noteID, categoryID int64, subcategories []string, userGUID string) error {
	en := engineForOwnedNote(noteID, userGUID)
	if en == nil {
		return serr.New("note not found")
	}

	// Verify the category exists (and, if scoped, is owned by the user).
	if _, err := GetCategory(categoryID, userGUID); err != nil {
		return err
	}

	// Reject a duplicate link in the note's database.
	var count int
	if err := en.QueryRow(`SELECT COUNT(*) FROM note_categories WHERE note_id = ? AND category_id = ?`, noteID, categoryID).Scan(&count); err != nil {
		return serr.Wrap(err, "failed to check existing relationship")
	}
	if count > 0 {
		return serr.New("category already added to this note")
	}

	var subcatsJSON sql.NullString
	if len(subcategories) > 0 {
		jsonBytes, err := json.Marshal(subcategories)
		if err != nil {
			return serr.Wrap(err, "failed to marshal subcategories")
		}
		subcatsJSON = sql.NullString{String: string(jsonBytes), Valid: true}
	}

	query := `INSERT INTO note_categories (note_id, category_id, subcategories, created_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)`
	if _, err := en.Exec(query, noteID, categoryID, subcatsJSON); err != nil {
		return serr.Wrap(err, "failed to add category to note")
	}

	// Record note-category mapping change for sync (non-blocking).
	recordNoteCategoryMappingChange(noteID)

	return nil
}

// UpdateNoteCategorySubcategories updates the subcategories on an existing
// link, in the note's database.
func UpdateNoteCategorySubcategories(noteID, categoryID int64, subcategories []string) error {
	en := engineForNoteID(noteID)
	if en == nil {
		return serr.New("relationship not found")
	}

	var count int
	if err := en.QueryRow(`SELECT COUNT(*) FROM note_categories WHERE note_id = ? AND category_id = ?`, noteID, categoryID).Scan(&count); err != nil {
		return serr.Wrap(err, "failed to check existing relationship")
	}
	if count == 0 {
		return serr.New("relationship not found")
	}

	var subcatsJSON sql.NullString
	if len(subcategories) > 0 {
		jsonBytes, err := json.Marshal(subcategories)
		if err != nil {
			return serr.Wrap(err, "failed to marshal subcategories")
		}
		subcatsJSON = sql.NullString{String: string(jsonBytes), Valid: true}
	}

	if _, err := en.Exec(`UPDATE note_categories SET subcategories = ? WHERE note_id = ? AND category_id = ?`, subcatsJSON, noteID, categoryID); err != nil {
		return serr.Wrap(err, "failed to update subcategories")
	}

	recordNoteCategoryMappingChange(noteID)
	return nil
}

// RemoveCategoryFromNote unlinks a category from a note. The DELETE is
// issued to whichever database holds the note.
func RemoveCategoryFromNote(noteID, categoryID int64) error {
	en := engineForNoteID(noteID)
	if en == nil {
		return serr.New("relationship not found")
	}

	result, err := en.Exec(`DELETE FROM note_categories WHERE note_id = ? AND category_id = ?`, noteID, categoryID)
	if err != nil {
		return serr.Wrap(err, "failed to remove category from note")
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return serr.New("relationship not found")
	}

	recordNoteCategoryMappingChange(noteID)
	return nil
}

// noteCategoryLink is a raw link row from note_categories.
type noteCategoryLink struct {
	CategoryID    int64
	Subcategories sql.NullString
}

// noteLinks reads a note's category links from its database.
func noteLinks(noteID int64) ([]noteCategoryLink, error) {
	en := engineForNoteID(noteID)
	if en == nil {
		return nil, nil
	}
	rows, err := en.Query(`SELECT category_id, subcategories FROM note_categories WHERE note_id = ?`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var links []noteCategoryLink
	for rows.Next() {
		var l noteCategoryLink
		if err := rows.Scan(&l.CategoryID, &l.Subcategories); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// GetNoteCategories retrieves all categories for a note, resolving link
// rows (in the note's database) against the public catalog. A non-empty
// userGUID keeps only categories owned by that user.
func GetNoteCategories(noteID int64, userGUID string) ([]Category, error) {
	links, err := noteLinks(noteID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get note categories")
	}

	var categories []Category
	for _, l := range links {
		cat, err := getCategoryByID(l.CategoryID)
		if err != nil {
			return nil, serr.Wrap(err, "failed to resolve category")
		}
		if cat == nil {
			continue
		}
		if userGUID != "" && !(cat.CreatedBy.Valid && cat.CreatedBy.String == userGUID) {
			continue
		}
		categories = append(categories, *cat)
	}

	sort.SliceStable(categories, func(i, j int) bool { return categories[i].Name < categories[j].Name })
	return categories, nil
}

// GetNoteCategoryDetails retrieves a note's categories along with the
// subcategories selected for this note (from the junction table).
func GetNoteCategoryDetails(noteID int64, userGUID string) ([]NoteCategoryDetailOutput, error) {
	links, err := noteLinks(noteID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get note category details")
	}

	var results []NoteCategoryDetailOutput
	for _, l := range links {
		cat, err := getCategoryByID(l.CategoryID)
		if err != nil {
			return nil, serr.Wrap(err, "failed to resolve category")
		}
		if cat == nil {
			continue
		}
		if userGUID != "" && !(cat.CreatedBy.Valid && cat.CreatedBy.String == userGUID) {
			continue
		}

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
		if l.Subcategories.Valid && l.Subcategories.String != "" {
			var selected []string
			if err := json.Unmarshal([]byte(l.Subcategories.String), &selected); err == nil {
				detail.SelectedSubcategories = selected
			}
		}
		results = append(results, detail)
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return results, nil
}

// notesForCategoryID returns all non-deleted notes linked to a category,
// across both databases (notes and their links are co-located, so the join
// stays in SQL per database). A non-empty userGUID enforces ownership.
func notesForCategoryID(categoryID int64, userGUID string) ([]Note, error) {
	build := func() (string, []any) {
		q := `SELECT ` + noteColsN + `
			FROM notes n
			INNER JOIN note_categories nc ON n.id = nc.note_id
			WHERE nc.category_id = ? AND n.deleted_at IS NULL`
		args := []any{categoryID}
		if userGUID != "" {
			q += ` AND n.created_by = ?`
			args = append(args, userGUID)
		}
		return q, args
	}

	notes, err := queryBothNotes(func(en *dbEngine) ([]Note, error) {
		q, args := build()
		return queryNotes(en, q, args...)
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].CreatedAt.After(notes[j].CreatedAt) })
	return notes, nil
}

// GetCategoryNotes retrieves all notes for a category (both databases).
func GetCategoryNotes(categoryID int64, userGUID string) ([]Note, error) {
	notes, err := notesForCategoryID(categoryID, userGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category notes")
	}
	return notes, nil
}

// GetCategoryByName retrieves a category by name from the public catalog.
// Returns nil, nil if not found.
func GetCategoryByName(name string, userGUID string) (*Category, error) {
	query := `SELECT ` + categoryCols + ` FROM categories WHERE name = ?`
	args := []any{name}
	if userGUID != "" {
		query += ` AND created_by = ?`
		args = append(args, userGUID)
	}

	var category Category
	err := scanCategory(pubDB.QueryRow(query, args...), &category)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to get category by name")
	}
	return &category, nil
}

// GetNotesByCategoryName retrieves all of a user's notes in a category,
// resolving the name against the public catalog then joining per database.
func GetNotesByCategoryName(categoryName string, userGUID string) ([]Note, error) {
	cat, err := GetCategoryByName(categoryName, userGUID)
	if err != nil {
		return nil, err
	}
	if cat == nil {
		return nil, nil
	}
	return notesForCategoryID(cat.ID, userGUID)
}

// NoteCategoryMapping is a lightweight row for the bulk mapping endpoint.
type NoteCategoryMapping struct {
	NoteID                int64    `json:"note_id"`
	CategoryID            int64    `json:"category_id"`
	CategoryName          string   `json:"category_name"`
	SelectedSubcategories []string `json:"selected_subcategories,omitempty"`
}

// GetAllNoteCategoryMappings retrieves every note-category link for a
// user's notes across both databases, resolving category names from the
// public catalog. Powers the client-side category filter.
func GetAllNoteCategoryMappings(userGUID string) ([]NoteCategoryMapping, error) {
	// Read the raw links (note_id, category_id, subcategories) for the
	// user's notes from each database (links + notes are co-located).
	type rawMap struct {
		noteID      int64
		categoryID  int64
		subcatsJSON sql.NullString
	}

	raws, err := queryBothNotes(func(en *dbEngine) ([]rawMap, error) {
		rows, err := en.Query(`SELECT nc.note_id, nc.category_id, nc.subcategories
			FROM note_categories nc
			INNER JOIN notes n ON nc.note_id = n.id
			WHERE n.created_by = ? AND n.deleted_at IS NULL
			ORDER BY nc.note_id`, userGUID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []rawMap
		for rows.Next() {
			var r rawMap
			if err := rows.Scan(&r.noteID, &r.categoryID, &r.subcatsJSON); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	})
	if err != nil {
		return nil, serr.Wrap(err, "failed to query note-category mappings")
	}

	// Resolve category names once via a small cache over the public catalog.
	nameCache := map[int64]string{}
	categoryName := func(id int64) string {
		if n, ok := nameCache[id]; ok {
			return n
		}
		cat, _ := getCategoryByID(id)
		n := ""
		if cat != nil {
			n = cat.Name
		}
		nameCache[id] = n
		return n
	}

	var mappings []NoteCategoryMapping
	for _, r := range raws {
		m := NoteCategoryMapping{NoteID: r.noteID, CategoryID: r.categoryID, CategoryName: categoryName(r.categoryID)}
		if r.subcatsJSON.Valid && r.subcatsJSON.String != "" {
			var subcats []string
			if err := json.Unmarshal([]byte(r.subcatsJSON.String), &subcats); err == nil {
				m.SelectedSubcategories = subcats
			}
		}
		mappings = append(mappings, m)
	}

	// Stable order: by note id, then category name.
	sort.SliceStable(mappings, func(i, j int) bool {
		if mappings[i].NoteID != mappings[j].NoteID {
			return mappings[i].NoteID < mappings[j].NoteID
		}
		return mappings[i].CategoryName < mappings[j].CategoryName
	})
	return mappings, nil
}

// GetNotesByCategoryAndSubcategories retrieves a user's notes in a category
// that carry ALL of the given subcategories. The old DuckDB JSON-array
// query is replaced by an in-Go filter: fetch the category's notes with
// their per-note selected subcategories, then keep those that contain
// every requested subcategory.
func GetNotesByCategoryAndSubcategories(categoryName string, subcategories []string, userGUID string) ([]Note, error) {
	if len(subcategories) == 0 {
		return GetNotesByCategoryName(categoryName, userGUID)
	}

	cat, err := GetCategoryByName(categoryName, userGUID)
	if err != nil {
		return nil, err
	}
	if cat == nil {
		return nil, nil
	}

	// Fetch notes joined with their link's selected subcategories, per
	// database, then filter in Go.
	type notePlusSubs struct {
		note      Note
		selected  map[string]struct{}
	}

	collected, err := queryBothNotes(func(en *dbEngine) ([]notePlusSubs, error) {
		q := `SELECT ` + noteColsN + `, nc.subcategories
			FROM notes n
			INNER JOIN note_categories nc ON n.id = nc.note_id
			WHERE nc.category_id = ? AND n.created_by = ? AND n.deleted_at IS NULL
			  AND nc.subcategories IS NOT NULL
			ORDER BY n.created_at DESC`
		rows, err := en.Query(q, cat.ID, userGUID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var out []notePlusSubs
		for rows.Next() {
			var (
				note     Note
				subsJSON sql.NullString
			)
			// This is the ONE note scan that cannot go through scanNoteRow: the
			// projection carries an extra trailing column (nc.subcategories),
			// and the row cursor is positional, so the note's fields and that
			// column have to be read in a single Scan call.
			//
			// That makes it the one place where a column added to noteColsN has
			// to be mirrored BY HAND, in order, before subsJSON. Getting it
			// wrong does not fail to compile — it fails at runtime, on this
			// screen only, with a scan-type error.
			if err := rows.Scan(
				&note.ID, &note.GUID, &note.Title, &note.Description, &note.Body, &note.Tags,
				&note.IsPrivate, &note.IsFlagged, &note.CreatedBy, &note.UpdatedBy,
				&note.CreatedAt, &note.UpdatedAt, &note.AuthoredAt, &note.SyncedAt, &note.DeletedAt,
				&note.Version,
				&subsJSON,
			); err != nil {
				return nil, err
			}
			set := map[string]struct{}{}
			if subsJSON.Valid && subsJSON.String != "" {
				var subs []string
				if err := json.Unmarshal([]byte(subsJSON.String), &subs); err == nil {
					for _, s := range subs {
						set[s] = struct{}{}
					}
				}
			}
			out = append(out, notePlusSubs{note: note, selected: set})
		}
		return out, rows.Err()
	})
	if err != nil {
		return nil, serr.Wrap(err, "failed to get notes by category and subcategories")
	}

	var notes []Note
	for _, nps := range collected {
		hasAll := true
		for _, want := range subcategories {
			if _, ok := nps.selected[want]; !ok {
				hasAll = false
				break
			}
		}
		if hasAll {
			notes = append(notes, nps.note)
		}
	}

	sort.SliceStable(notes, func(i, j int) bool { return notes[i].CreatedAt.After(notes[j].CreatedAt) })
	return notes, nil
}
