package models_test

import (
	"os"
	"testing"

	"gonotes/models"
)

// setupCategoryTestDB initializes a clean test database for category tests
func setupCategoryTestDB(t *testing.T) func() {
	t.Helper()

	// Remove existing test database
	os.Remove("./test_category_cache.ddb")
	os.Remove("./test_category_cache.ddb.wal")

	// Initialize test database
	if err := models.InitTestDB("./test_category_cache.ddb"); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}

	// Return cleanup function
	return func() {
		models.CloseDB()
		os.Remove("./test_category_cache.ddb")
		os.Remove("./test_category_cache.ddb.wal")
	}
}

// TestCategoryCacheSync verifies that category writes to disk are reflected in cache
func TestCategoryCacheSync(t *testing.T) {
	cleanup := setupCategoryTestDB(t)
	defer cleanup()

	t.Run("create and retrieve category", func(t *testing.T) {
		// Create a category
		desc := "Test category description"
		input := models.CategoryInput{
			Name:          "Test Category",
			Description:   &desc,
			Subcategories: []string{"Subcat1", "Subcat2"},
		}

		category, err := models.CreateCategory(input)
		if err != nil {
			t.Fatalf("failed to create category: %v", err)
		}

		// Verify category is readable (from cache)
		retrieved, err := models.GetCategory(category.ID)
		if err != nil {
			t.Fatalf("failed to get category by ID: %v", err)
		}

		if retrieved == nil {
			t.Fatal("expected category to be found in cache")
		}

		if retrieved.Name != "Test Category" {
			t.Errorf("expected name 'Test Category', got '%s'", retrieved.Name)
		}

		if !retrieved.Description.Valid {
			t.Error("expected description to be valid")
		} else if retrieved.Description.String != desc {
			t.Errorf("expected description '%s', got '%s'", desc, retrieved.Description.String)
		}

		// Verify subcategories
		output := retrieved.ToOutput()
		if len(output.Subcategories) != 2 {
			t.Errorf("expected 2 subcategories, got %d", len(output.Subcategories))
		}
	})

	t.Run("list categories", func(t *testing.T) {
		// Create multiple categories
		for i := 1; i <= 3; i++ {
			input := models.CategoryInput{
				Name: "Category " + string(rune('A'+i-1)),
			}
			_, err := models.CreateCategory(input)
			if err != nil {
				t.Fatalf("failed to create category: %v", err)
			}
		}

		// List all categories
		categories, err := models.ListCategories(0, 0)
		if err != nil {
			t.Fatalf("failed to list categories: %v", err)
		}

		if len(categories) < 3 {
			t.Errorf("expected at least 3 categories, got %d", len(categories))
		}
	})
}

// TestCategoryUpdate verifies that category updates are reflected in cache
func TestCategoryUpdate(t *testing.T) {
	cleanup := setupCategoryTestDB(t)
	defer cleanup()

	t.Run("update category name", func(t *testing.T) {
		// Create a category
		input := models.CategoryInput{
			Name: "Original Name",
		}

		category, err := models.CreateCategory(input)
		if err != nil {
			t.Fatalf("failed to create category: %v", err)
		}

		// Update the category
		desc := "Updated description"
		updateInput := models.CategoryInput{
			Name:          "Updated Name",
			Description:   &desc,
			Subcategories: []string{"New1", "New2", "New3"},
		}

		updated, err := models.UpdateCategory(category.ID, updateInput)
		if err != nil {
			t.Fatalf("failed to update category: %v", err)
		}

		if updated.Name != "Updated Name" {
			t.Errorf("expected name 'Updated Name', got '%s'", updated.Name)
		}

		// Verify update is in cache
		retrieved, err := models.GetCategory(category.ID)
		if err != nil {
			t.Fatalf("failed to get updated category: %v", err)
		}

		if retrieved.Name != "Updated Name" {
			t.Errorf("cache: expected name 'Updated Name', got '%s'", retrieved.Name)
		}

		output := retrieved.ToOutput()
		if len(output.Subcategories) != 3 {
			t.Errorf("expected 3 subcategories, got %d", len(output.Subcategories))
		}
	})
}

// TestCategoryDelete verifies that category deletes are reflected in cache
func TestCategoryDelete(t *testing.T) {
	cleanup := setupCategoryTestDB(t)
	defer cleanup()

	t.Run("delete category", func(t *testing.T) {
		// Create a category
		input := models.CategoryInput{
			Name: "Category to Delete",
		}

		category, err := models.CreateCategory(input)
		if err != nil {
			t.Fatalf("failed to create category: %v", err)
		}

		// Delete the category
		err = models.DeleteCategory(category.ID)
		if err != nil {
			t.Fatalf("failed to delete category: %v", err)
		}

		// Verify category is not in cache
		retrieved, err := models.GetCategory(category.ID)
		if err == nil {
			t.Error("expected error when getting deleted category")
		}
		if retrieved != nil {
			t.Error("expected category to be nil after deletion")
		}
	})
}

// TestNoteCategoryRelationshipSync verifies note-category relationships are synced
func TestNoteCategoryRelationshipSync(t *testing.T) {
	cleanup := setupCategoryTestDB(t)
	defer cleanup()

	t.Run("add category to note", func(t *testing.T) {
		// Create a note
		noteInput := models.NoteInput{
			GUID:  "test-note-001",
			Title: "Test Note",
		}
		note, err := models.CreateNote(noteInput)
		if err != nil {
			t.Fatalf("failed to create note: %v", err)
		}

		// Create a category
		catInput := models.CategoryInput{
			Name: "Test Category",
		}
		category, err := models.CreateCategory(catInput)
		if err != nil {
			t.Fatalf("failed to create category: %v", err)
		}

		// Add category to note
		err = models.AddCategoryToNote(note.ID, category.ID)
		if err != nil {
			t.Fatalf("failed to add category to note: %v", err)
		}

		// Verify relationship in cache
		categories, err := models.GetNoteCategories(note.ID)
		if err != nil {
			t.Fatalf("failed to get note categories: %v", err)
		}

		if len(categories) != 1 {
			t.Errorf("expected 1 category, got %d", len(categories))
		}

		if len(categories) > 0 && categories[0].Name != "Test Category" {
			t.Errorf("expected category name 'Test Category', got '%s'", categories[0].Name)
		}
	})

	t.Run("remove category from note", func(t *testing.T) {
		// Create a note
		noteInput := models.NoteInput{
			GUID:  "test-note-002",
			Title: "Test Note 2",
		}
		note, err := models.CreateNote(noteInput)
		if err != nil {
			t.Fatalf("failed to create note: %v", err)
		}

		// Create a category
		catInput := models.CategoryInput{
			Name: "Category to Remove",
		}
		category, err := models.CreateCategory(catInput)
		if err != nil {
			t.Fatalf("failed to create category: %v", err)
		}

		// Add category to note
		err = models.AddCategoryToNote(note.ID, category.ID)
		if err != nil {
			t.Fatalf("failed to add category to note: %v", err)
		}

		// Remove category from note
		err = models.RemoveCategoryFromNote(note.ID, category.ID)
		if err != nil {
			t.Fatalf("failed to remove category from note: %v", err)
		}

		// Verify relationship is removed in cache
		categories, err := models.GetNoteCategories(note.ID)
		if err != nil {
			t.Fatalf("failed to get note categories: %v", err)
		}

		if len(categories) != 0 {
			t.Errorf("expected 0 categories, got %d", len(categories))
		}
	})

	t.Run("get category notes", func(t *testing.T) {
		// Create a category
		catInput := models.CategoryInput{
			Name: "Shared Category",
		}
		category, err := models.CreateCategory(catInput)
		if err != nil {
			t.Fatalf("failed to create category: %v", err)
		}

		// Create multiple notes and add to category
		for i := 1; i <= 3; i++ {
			noteInput := models.NoteInput{
				GUID:  "test-note-shared-" + string(rune('0'+i)),
				Title: "Shared Note " + string(rune('0'+i)),
			}
			note, err := models.CreateNote(noteInput)
			if err != nil {
				t.Fatalf("failed to create note: %v", err)
			}

			err = models.AddCategoryToNote(note.ID, category.ID)
			if err != nil {
				t.Fatalf("failed to add category to note: %v", err)
			}
		}

		// Get all notes for category
		notes, err := models.GetCategoryNotes(category.ID)
		if err != nil {
			t.Fatalf("failed to get category notes: %v", err)
		}

		if len(notes) != 3 {
			t.Errorf("expected 3 notes, got %d", len(notes))
		}
	})

	t.Run("prevent duplicate category assignment", func(t *testing.T) {
		// Create a note
		noteInput := models.NoteInput{
			GUID:  "test-note-dup",
			Title: "Test Note Dup",
		}
		note, err := models.CreateNote(noteInput)
		if err != nil {
			t.Fatalf("failed to create note: %v", err)
		}

		// Create a category
		catInput := models.CategoryInput{
			Name: "Duplicate Test Category",
		}
		category, err := models.CreateCategory(catInput)
		if err != nil {
			t.Fatalf("failed to create category: %v", err)
		}

		// Add category to note
		err = models.AddCategoryToNote(note.ID, category.ID)
		if err != nil {
			t.Fatalf("failed to add category to note: %v", err)
		}

		// Try to add same category again
		err = models.AddCategoryToNote(note.ID, category.ID)
		if err == nil {
			t.Error("expected error when adding duplicate category")
		}
		if err != nil && err.Error() != "category already added to this note" {
			t.Errorf("expected 'category already added to this note' error, got: %v", err)
		}
	})
}

// TestCategoryEdgeCases tests edge cases and error conditions
func TestCategoryEdgeCases(t *testing.T) {
	cleanup := setupCategoryTestDB(t)
	defer cleanup()

	t.Run("get non-existent category", func(t *testing.T) {
		_, err := models.GetCategory(99999)
		if err == nil {
			t.Error("expected error when getting non-existent category")
		}
	})

	t.Run("update non-existent category", func(t *testing.T) {
		input := models.CategoryInput{
			Name: "Does Not Exist",
		}
		_, err := models.UpdateCategory(99999, input)
		if err == nil {
			t.Error("expected error when updating non-existent category")
		}
	})

	t.Run("delete non-existent category", func(t *testing.T) {
		err := models.DeleteCategory(99999)
		if err == nil {
			t.Error("expected error when deleting non-existent category")
		}
	})

	t.Run("add category to non-existent note", func(t *testing.T) {
		// Create a category
		catInput := models.CategoryInput{
			Name: "Test Category",
		}
		category, err := models.CreateCategory(catInput)
		if err != nil {
			t.Fatalf("failed to create category: %v", err)
		}

		err = models.AddCategoryToNote(99999, category.ID)
		if err == nil {
			t.Error("expected error when adding category to non-existent note")
		}
	})

	t.Run("add non-existent category to note", func(t *testing.T) {
		// Create a note
		noteInput := models.NoteInput{
			GUID:  "test-note-edge",
			Title: "Test Note Edge",
		}
		note, err := models.CreateNote(noteInput)
		if err != nil {
			t.Fatalf("failed to create note: %v", err)
		}

		err = models.AddCategoryToNote(note.ID, 99999)
		if err == nil {
			t.Error("expected error when adding non-existent category to note")
		}
	})

	t.Run("remove non-existent relationship", func(t *testing.T) {
		// Create a note and category but don't link them
		noteInput := models.NoteInput{
			GUID:  "test-note-nolink",
			Title: "Test Note No Link",
		}
		note, err := models.CreateNote(noteInput)
		if err != nil {
			t.Fatalf("failed to create note: %v", err)
		}

		catInput := models.CategoryInput{
			Name: "Unlinked Category",
		}
		category, err := models.CreateCategory(catInput)
		if err != nil {
			t.Fatalf("failed to create category: %v", err)
		}

		err = models.RemoveCategoryFromNote(note.ID, category.ID)
		if err == nil {
			t.Error("expected error when removing non-existent relationship")
		}
	})

	t.Run("create category without name", func(t *testing.T) {
		input := models.CategoryInput{
			Name: "",
		}
		_, err := models.CreateCategory(input)
		if err == nil {
			t.Error("expected error when creating category without name")
		}
	})

	t.Run("update category without name", func(t *testing.T) {
		// Create a valid category first
		input := models.CategoryInput{
			Name: "Valid Category",
		}
		category, err := models.CreateCategory(input)
		if err != nil {
			t.Fatalf("failed to create category: %v", err)
		}

		// Try to update with empty name
		updateInput := models.CategoryInput{
			Name: "",
		}
		_, err = models.UpdateCategory(category.ID, updateInput)
		if err == nil {
			t.Error("expected error when updating category with empty name")
		}
	})

	t.Run("pagination", func(t *testing.T) {
		// Create several categories
		for i := 1; i <= 5; i++ {
			input := models.CategoryInput{
				Name: "Pagination Test " + string(rune('0'+i)),
			}
			_, err := models.CreateCategory(input)
			if err != nil {
				t.Fatalf("failed to create category: %v", err)
			}
		}

		// Test limit
		categories, err := models.ListCategories(2, 0)
		if err != nil {
			t.Fatalf("failed to list categories: %v", err)
		}
		if len(categories) != 2 {
			t.Errorf("expected 2 categories with limit=2, got %d", len(categories))
		}

		// Test offset
		categories, err = models.ListCategories(2, 2)
		if err != nil {
			t.Fatalf("failed to list categories: %v", err)
		}
		if len(categories) != 2 {
			t.Errorf("expected 2 categories with offset=2, got %d", len(categories))
		}
	})
}
