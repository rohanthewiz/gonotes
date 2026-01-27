# Plan: Query Notes by Category and Subcategory

## Goal
Enable querying notes by category name and subcategories, using a query like `{"category": "k8s", "subcategories": ["pod", "replicaset"]}`.

## Approach
Extend the `note_categories` join table to include an optional subcategories field (JSON array), then add query functions and tests.

## Changes Required

### 1. Schema Changes (`models/category.go`)

Update `CreateNoteCategoriesTableSQL` to add optional subcategories column:
```sql
CREATE TABLE IF NOT EXISTS note_categories (
    note_id       BIGINT NOT NULL,
    category_id   BIGINT NOT NULL,
    subcategories VARCHAR,  -- NEW: JSON array of subcategories, e.g. '["pod", "replicaset"]'
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (note_id, category_id),
    FOREIGN KEY (note_id) REFERENCES notes(id),
    FOREIGN KEY (category_id) REFERENCES categories(id)
);
```

Update `NoteCategory` struct:
```go
type NoteCategory struct {
    NoteID        int64          `json:"note_id"`
    CategoryID    int64          `json:"category_id"`
    Subcategories sql.NullString `json:"subcategories,omitempty"` // JSON array
    CreatedAt     time.Time      `json:"created_at"`
}
```

### 2. Model Functions (`models/category.go`)

Add new query functions:
- `GetNotesByCategoryName(categoryName string) ([]Note, error)` - Query by category name
- `GetNotesByCategoryAndSubcategories(categoryName string, subcategories []string) ([]Note, error)` - Query notes that match category AND have ALL specified subcategories
- `AddCategoryToNoteWithSubcategories(noteID, categoryID int64, subcategories []string) error` - Add with subcategories
- `UpdateNoteCategorySubcategories(noteID, categoryID int64, subcategories []string) error` - Update subcategories for existing relationship

Update existing:
- Modify `AddCategoryToNote` signature to accept optional subcategories slice, or keep separate function

### 3. Cache Sync (`models/db.go`)

Update `syncNoteCategoriesFromDisk` to include the subcategory column.

### 4. API Endpoints (`web/api/notes.go`)

Extend existing `GET /api/v1/notes` endpoint with query params:
- `GET /api/v1/notes?cat=k8s` - Query notes by category name
- `GET /api/v1/notes?cat=k8s&subcat=pod` - Query notes by category with single subcategory
- `GET /api/v1/notes?cat=k8s&subcat=pod&subcat=replicaset` - Multiple subcategories (repeated param)

Modify `ListNotesHandler` to check for `cat` and `subcat` query params and call appropriate model functions. The `subcat` param can be repeated for multiple subcategories.

### 5. Tests (`models/category_cache_test.go`)

Add tests for:
- Creating note with category and multiple subcategories
- Querying notes by category name only
- Querying notes by category name and single subcategory
- Querying notes by category name and multiple subcategories
- Edge cases (non-existent category, partial subcategory match)

## Files to Modify

1. `models/category.go` - Schema, struct, query functions
2. `models/db.go` - Cache sync for subcategory column
3. `web/api/notes.go` - Extend ListNotesHandler with cat/subcat query params
4. `models/category_cache_test.go` - New tests for category/subcategory queries

## Verification

1. Run existing tests to ensure no regression: `go test ./models/...`
2. Run new tests specifically: `go test ./models/... -run TestCategorySubcategory`
3. Verify API endpoints work with manual testing or API tests
