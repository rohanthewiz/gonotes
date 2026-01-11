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

// TestCategorySubcategoryQueries tests querying notes by category and subcategories
func TestCategorySubcategoryQueries(t *testing.T) {
	cleanup := setupCategoryTestDB(t)
	defer cleanup()

	// Setup: Create categories and notes for testing
	var k8sCategory, awsCategory *models.Category
	var note1, note2, note3 *models.Note

	t.Run("setup: create categories and notes", func(t *testing.T) {
		// Create categories
		k8sInput := models.CategoryInput{
			Name:          "k8s",
			Subcategories: []string{"pod", "service", "deployment", "replicaset"},
		}
		var err error
		k8sCategory, err = models.CreateCategory(k8sInput)
		if err != nil {
			t.Fatalf("failed to create k8s category: %v", err)
		}

		awsInput := models.CategoryInput{
			Name:          "aws",
			Subcategories: []string{"ec2", "s3", "lambda"},
		}
		awsCategory, err = models.CreateCategory(awsInput)
		if err != nil {
			t.Fatalf("failed to create aws category: %v", err)
		}

		// Create notes - Body is a pointer type
		body1 := "A pod is the smallest deployable unit in Kubernetes"
		note1Input := models.NoteInput{
			GUID:  "k8s-pod-note",
			Title: "Kubernetes Pod Basics",
			Body:  &body1,
		}
		note1, err = models.CreateNote(note1Input)
		if err != nil {
			t.Fatalf("failed to create note1: %v", err)
		}

		body2 := "Deployments manage ReplicaSets"
		note2Input := models.NoteInput{
			GUID:  "k8s-deployment-note",
			Title: "Kubernetes Deployments",
			Body:  &body2,
		}
		note2, err = models.CreateNote(note2Input)
		if err != nil {
			t.Fatalf("failed to create note2: %v", err)
		}

		body3 := "EC2 provides virtual servers"
		note3Input := models.NoteInput{
			GUID:  "aws-ec2-note",
			Title: "AWS EC2 Instances",
			Body:  &body3,
		}
		note3, err = models.CreateNote(note3Input)
		if err != nil {
			t.Fatalf("failed to create note3: %v", err)
		}

		// Add categories to notes with subcategories
		err = models.AddCategoryToNoteWithSubcategories(note1.ID, k8sCategory.ID, []string{"pod"})
		if err != nil {
			t.Fatalf("failed to add k8s/pod to note1: %v", err)
		}

		err = models.AddCategoryToNoteWithSubcategories(note2.ID, k8sCategory.ID, []string{"deployment", "replicaset"})
		if err != nil {
			t.Fatalf("failed to add k8s/deployment,replicaset to note2: %v", err)
		}

		err = models.AddCategoryToNoteWithSubcategories(note3.ID, awsCategory.ID, []string{"ec2"})
		if err != nil {
			t.Fatalf("failed to add aws/ec2 to note3: %v", err)
		}
	})

	t.Run("query notes by category name only", func(t *testing.T) {
		notes, err := models.GetNotesByCategoryName("k8s")
		if err != nil {
			t.Fatalf("failed to get notes by category name: %v", err)
		}

		if len(notes) != 2 {
			t.Errorf("expected 2 notes in k8s category, got %d", len(notes))
		}

		// Verify notes are the expected ones
		noteIDs := make(map[int64]bool)
		for _, n := range notes {
			noteIDs[n.ID] = true
		}
		if !noteIDs[note1.ID] || !noteIDs[note2.ID] {
			t.Error("expected note1 and note2 in k8s category")
		}
	})

	t.Run("query notes by category name - aws", func(t *testing.T) {
		notes, err := models.GetNotesByCategoryName("aws")
		if err != nil {
			t.Fatalf("failed to get notes by category name: %v", err)
		}

		if len(notes) != 1 {
			t.Errorf("expected 1 note in aws category, got %d", len(notes))
		}

		if len(notes) > 0 && notes[0].ID != note3.ID {
			t.Errorf("expected note3 in aws category, got note ID %d", notes[0].ID)
		}
	})

	t.Run("query notes by non-existent category", func(t *testing.T) {
		notes, err := models.GetNotesByCategoryName("nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(notes) != 0 {
			t.Errorf("expected 0 notes for non-existent category, got %d", len(notes))
		}
	})

	t.Run("query notes by category and single subcategory", func(t *testing.T) {
		notes, err := models.GetNotesByCategoryAndSubcategories("k8s", []string{"pod"})
		if err != nil {
			t.Fatalf("failed to get notes by category and subcategory: %v", err)
		}

		if len(notes) != 1 {
			t.Errorf("expected 1 note with k8s/pod, got %d", len(notes))
		}

		if len(notes) > 0 && notes[0].ID != note1.ID {
			t.Errorf("expected note1 with k8s/pod, got note ID %d", notes[0].ID)
		}
	})

	t.Run("query notes by category and multiple subcategories", func(t *testing.T) {
		notes, err := models.GetNotesByCategoryAndSubcategories("k8s", []string{"deployment", "replicaset"})
		if err != nil {
			t.Fatalf("failed to get notes by category and subcategories: %v", err)
		}

		if len(notes) != 1 {
			t.Errorf("expected 1 note with k8s/deployment+replicaset, got %d", len(notes))
		}

		if len(notes) > 0 && notes[0].ID != note2.ID {
			t.Errorf("expected note2 with k8s/deployment+replicaset, got note ID %d", notes[0].ID)
		}
	})

	t.Run("query notes by category and partial subcategory match", func(t *testing.T) {
		// note2 has deployment and replicaset, query for deployment only
		notes, err := models.GetNotesByCategoryAndSubcategories("k8s", []string{"deployment"})
		if err != nil {
			t.Fatalf("failed to get notes by category and subcategory: %v", err)
		}

		if len(notes) != 1 {
			t.Errorf("expected 1 note with k8s/deployment, got %d", len(notes))
		}

		if len(notes) > 0 && notes[0].ID != note2.ID {
			t.Errorf("expected note2 with k8s/deployment, got note ID %d", notes[0].ID)
		}
	})

	t.Run("query notes by category and non-matching subcategory", func(t *testing.T) {
		notes, err := models.GetNotesByCategoryAndSubcategories("k8s", []string{"service"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(notes) != 0 {
			t.Errorf("expected 0 notes with k8s/service (none assigned), got %d", len(notes))
		}
	})

	t.Run("query with empty subcategories returns all category notes", func(t *testing.T) {
		notes, err := models.GetNotesByCategoryAndSubcategories("k8s", []string{})
		if err != nil {
			t.Fatalf("failed to get notes: %v", err)
		}

		if len(notes) != 2 {
			t.Errorf("expected 2 notes for k8s with empty subcategories, got %d", len(notes))
		}
	})

	t.Run("update note subcategories", func(t *testing.T) {
		// Update note1's subcategories from ["pod"] to ["pod", "service"]
		err := models.UpdateNoteCategorySubcategories(note1.ID, k8sCategory.ID, []string{"pod", "service"})
		if err != nil {
			t.Fatalf("failed to update subcategories: %v", err)
		}

		// Now query for service should return note1
		notes, err := models.GetNotesByCategoryAndSubcategories("k8s", []string{"service"})
		if err != nil {
			t.Fatalf("failed to get notes: %v", err)
		}

		if len(notes) != 1 {
			t.Errorf("expected 1 note with k8s/service after update, got %d", len(notes))
		}

		if len(notes) > 0 && notes[0].ID != note1.ID {
			t.Errorf("expected note1 with k8s/service, got note ID %d", notes[0].ID)
		}
	})

	t.Run("get category by name", func(t *testing.T) {
		category, err := models.GetCategoryByName("k8s")
		if err != nil {
			t.Fatalf("failed to get category by name: %v", err)
		}

		if category == nil {
			t.Fatal("expected k8s category, got nil")
		}

		if category.Name != "k8s" {
			t.Errorf("expected category name 'k8s', got '%s'", category.Name)
		}
	})

	t.Run("get non-existent category by name", func(t *testing.T) {
		category, err := models.GetCategoryByName("nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if category != nil {
			t.Errorf("expected nil for non-existent category, got %v", category)
		}
	})
}
