package models_test

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"gonotes/models"
)

// testUserGUID is a constant user GUID used for tests to simulate an authenticated user.
// All note operations require a user GUID for ownership filtering.
const testUserGUID = "test-user-guid-001"

// setupTestDB initializes a clean test database for each test
func setupTestDB(t *testing.T) func() {
	t.Helper()

	// Remove existing test database
	os.Remove("./test_cache.ddb")
	os.Remove("./test_cache.ddb.wal")

	// Initialize test database
	if err := models.InitTestDB("./test_cache.ddb"); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}

	// Return cleanup function
	return func() {
		models.CloseDB()
		os.Remove("./test_cache.ddb")
		os.Remove("./test_cache.ddb.wal")
	}
}

// TestCacheSync verifies that writes to disk are reflected in cache
func TestCacheSync(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Create a note
	input := models.NoteInput{
		GUID:  "cache-test-001",
		Title: "Cache Test Note",
	}

	note, err := models.CreateNote(input, testUserGUID)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Verify note is readable (from cache)
	retrieved, err := models.GetNoteByID(note.ID, testUserGUID)
	if err != nil {
		t.Fatalf("failed to get note by ID: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected note to be found in cache")
	}

	if retrieved.Title != "Cache Test Note" {
		t.Errorf("expected title 'Cache Test Note', got '%s'", retrieved.Title)
	}

	// Verify GUID lookup works (from cache)
	retrievedByGUID, err := models.GetNoteByGUID("cache-test-001")
	if err != nil {
		t.Fatalf("failed to get note by GUID: %v", err)
	}

	if retrievedByGUID == nil {
		t.Fatal("expected note to be found in cache by GUID")
	}

	if retrievedByGUID.ID != note.ID {
		t.Errorf("expected ID %d, got %d", note.ID, retrievedByGUID.ID)
	}
}

// TestCacheUpdate verifies that updates are reflected in cache
func TestCacheUpdate(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Create a note
	input := models.NoteInput{
		GUID:  "cache-update-test",
		Title: "Original Title",
	}

	note, err := models.CreateNote(input, testUserGUID)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Update the note
	updateInput := models.NoteInput{
		Title: "Updated Title",
	}

	updated, err := models.UpdateNote(note.ID, updateInput, testUserGUID)
	if err != nil {
		t.Fatalf("failed to update note: %v", err)
	}

	if updated.Title != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got '%s'", updated.Title)
	}

	// Verify the update is in cache
	retrieved, err := models.GetNoteByID(note.ID, testUserGUID)
	if err != nil {
		t.Fatalf("failed to get updated note: %v", err)
	}

	if retrieved.Title != "Updated Title" {
		t.Errorf("cache not updated: expected title 'Updated Title', got '%s'", retrieved.Title)
	}
}

// TestCacheDelete verifies that soft deletes are reflected in cache
func TestCacheDelete(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Create a note
	input := models.NoteInput{
		GUID:  "cache-delete-test",
		Title: "To Be Deleted",
	}

	note, err := models.CreateNote(input, testUserGUID)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Delete the note
	deleted, err := models.DeleteNote(note.ID, testUserGUID)
	if err != nil {
		t.Fatalf("failed to delete note: %v", err)
	}

	if !deleted {
		t.Error("expected note to be deleted")
	}

	// Verify the note is not retrievable from cache
	retrieved, err := models.GetNoteByID(note.ID, testUserGUID)
	if err != nil {
		t.Fatalf("failed to query deleted note: %v", err)
	}

	if retrieved != nil {
		t.Error("expected deleted note to not be retrievable from cache")
	}
}

// TestCacheHardDelete verifies that hard deletes are reflected in cache
func TestCacheHardDelete(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Create a note
	input := models.NoteInput{
		GUID:  "cache-hard-delete-test",
		Title: "To Be Hard Deleted",
	}

	note, err := models.CreateNote(input, testUserGUID)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Hard delete the note
	deleted, err := models.HardDeleteNote(note.ID)
	if err != nil {
		t.Fatalf("failed to hard delete note: %v", err)
	}

	if !deleted {
		t.Error("expected note to be hard deleted")
	}

	// Verify the note is not retrievable from cache
	retrieved, err := models.GetNoteByID(note.ID, testUserGUID)
	if err != nil {
		t.Fatalf("failed to query hard deleted note: %v", err)
	}

	if retrieved != nil {
		t.Error("expected hard deleted note to not be retrievable from cache")
	}
}

// TestCacheList verifies that list operations work from cache
func TestCacheList(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Create multiple notes
	for i := 1; i <= 5; i++ {
		input := models.NoteInput{
			GUID:  fmt.Sprintf("list-test-%d", i),
			Title: "List Test Note",
		}
		_, err := models.CreateNote(input, testUserGUID)
		if err != nil {
			t.Fatalf("failed to create note %d: %v", i, err)
		}
	}

	// List all notes
	notes, err := models.ListNotes(testUserGUID, 0, 0)
	if err != nil {
		t.Fatalf("failed to list notes: %v", err)
	}

	if len(notes) != 5 {
		t.Errorf("expected 5 notes, got %d", len(notes))
	}

	// Test pagination
	limited, err := models.ListNotes(testUserGUID, 2, 0)
	if err != nil {
		t.Fatalf("failed to list notes with limit: %v", err)
	}

	if len(limited) != 2 {
		t.Errorf("expected 2 notes with limit=2, got %d", len(limited))
	}

	// Test offset
	offset, err := models.ListNotes(testUserGUID, 2, 2)
	if err != nil {
		t.Fatalf("failed to list notes with offset: %v", err)
	}

	if len(offset) != 2 {
		t.Errorf("expected 2 notes with offset=2, got %d", len(offset))
	}

	// Verify offset returns different notes
	if limited[0].ID == offset[0].ID {
		t.Error("offset should return different notes")
	}
}

// TestCachePrimaryKeySync verifies that primary keys stay synchronized
func TestCachePrimaryKeySync(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Create multiple notes and verify IDs are sequential and consistent
	var lastID int64
	for i := 1; i <= 3; i++ {
		input := models.NoteInput{
			GUID:  fmt.Sprintf("pk-test-%d", i),
			Title: "Primary Key Test",
		}

		note, err := models.CreateNote(input, testUserGUID)
		if err != nil {
			t.Fatalf("failed to create note %d: %v", i, err)
		}

		// Verify ID is greater than last
		if note.ID <= lastID {
			t.Errorf("expected ID > %d, got %d", lastID, note.ID)
		}
		lastID = note.ID

		// Verify note can be retrieved with the same ID
		retrieved, err := models.GetNoteByID(note.ID, testUserGUID)
		if err != nil {
			t.Fatalf("failed to retrieve note %d: %v", i, err)
		}

		if retrieved.ID != note.ID {
			t.Errorf("ID mismatch: created with %d, retrieved with %d", note.ID, retrieved.ID)
		}
	}
}

// TestCacheEdgeCases tests edge cases for cache synchronization
func TestCacheEdgeCases(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Test 1: Get non-existent note
	t.Run("GetNonExistent", func(t *testing.T) {
		note, err := models.GetNoteByID(999, testUserGUID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if note != nil {
			t.Error("expected nil for non-existent note")
		}
	})

	// Test 2: Update non-existent note
	t.Run("UpdateNonExistent", func(t *testing.T) {
		input := models.NoteInput{
			Title: "Update Non-Existent",
		}
		updated, err := models.UpdateNote(999, input, testUserGUID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated != nil {
			t.Error("expected nil when updating non-existent note")
		}
	})

	// Test 3: Delete non-existent note
	t.Run("DeleteNonExistent", func(t *testing.T) {
		deleted, err := models.DeleteNote(999, testUserGUID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if deleted {
			t.Error("expected false when deleting non-existent note")
		}
	})

	// Test 4: Duplicate GUID
	t.Run("DuplicateGUID", func(t *testing.T) {
		input1 := models.NoteInput{
			GUID:  "duplicate-test",
			Title: "First Note",
		}
		_, err := models.CreateNote(input1, testUserGUID)
		if err != nil {
			t.Fatalf("failed to create first note: %v", err)
		}

		// Try to create with same GUID
		input2 := models.NoteInput{
			GUID:  "duplicate-test",
			Title: "Second Note",
		}
		_, err = models.CreateNote(input2, testUserGUID)
		if err == nil {
			t.Error("expected error when creating note with duplicate GUID")
		}
	})

	// Test 5: Update then delete
	t.Run("UpdateThenDelete", func(t *testing.T) {
		input := models.NoteInput{
			GUID:  "update-delete-test",
			Title: "Original",
		}
		note, err := models.CreateNote(input, testUserGUID)
		if err != nil {
			t.Fatalf("failed to create note: %v", err)
		}

		// Update
		updateInput := models.NoteInput{
			Title: "Updated",
		}
		updated, err := models.UpdateNote(note.ID, updateInput, testUserGUID)
		if err != nil {
			t.Fatalf("failed to update note: %v", err)
		}
		if updated.Title != "Updated" {
			t.Errorf("expected title 'Updated', got '%s'", updated.Title)
		}

		// Delete
		deleted, err := models.DeleteNote(note.ID, testUserGUID)
		if err != nil {
			t.Fatalf("failed to delete note: %v", err)
		}
		if !deleted {
			t.Error("expected note to be deleted")
		}

		// Verify not retrievable
		retrieved, err := models.GetNoteByID(note.ID, testUserGUID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if retrieved != nil {
			t.Error("expected deleted note to not be retrievable")
		}
	})
}

// TestAuthoredAtBehavior verifies that authored_at tracks human authoring time
// and is stored only on disk (not in cache)
func TestAuthoredAtBehavior(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	t.Run("SetOnCreate", func(t *testing.T) {
		input := models.NoteInput{
			GUID:  "authored-at-create-test",
			Title: "Authored At Create Test",
		}

		note, err := models.CreateNote(input, testUserGUID)
		if err != nil {
			t.Fatalf("failed to create note: %v", err)
		}

		// Verify authored_at is set (populated from disk RETURNING clause)
		if !note.AuthoredAt.Valid {
			t.Error("expected authored_at to be set on creation")
		}

		// Verify authored_at is close to now (within 5 seconds)
		now := time.Now()
		diff := now.Sub(note.AuthoredAt.Time)
		if diff > 5*time.Second || diff < -5*time.Second {
			t.Errorf("authored_at should be close to now, diff: %v", diff)
		}

		// Verify authored_at matches created_at for new notes
		if note.AuthoredAt.Time.Sub(note.CreatedAt) > time.Second {
			t.Errorf("authored_at should match created_at on new notes, authored_at: %v, created_at: %v",
				note.AuthoredAt.Time, note.CreatedAt)
		}
	})

	t.Run("UpdatedOnEdit", func(t *testing.T) {
		input := models.NoteInput{
			GUID:  "authored-at-update-test",
			Title: "Original Title",
		}

		note, err := models.CreateNote(input, testUserGUID)
		if err != nil {
			t.Fatalf("failed to create note: %v", err)
		}

		originalAuthoredAt := note.AuthoredAt.Time

		// Wait a moment to ensure timestamp difference
		time.Sleep(50 * time.Millisecond)

		// Update the note
		updateInput := models.NoteInput{
			Title: "Updated Title",
		}

		// Note: UpdateNote doesn't return authored_at since it reads from cache
		_, err = models.UpdateNote(note.ID, updateInput, testUserGUID)
		if err != nil {
			t.Fatalf("failed to update note: %v", err)
		}

		// Read authored_at directly from disk to verify it changed
		newAuthoredAt := readAuthoredAtFromDisk(t, note.ID)

		if !newAuthoredAt.After(originalAuthoredAt) {
			t.Errorf("authored_at should be updated after edit. original: %v, new: %v",
				originalAuthoredAt, newAuthoredAt)
		}
	})

	t.Run("NotInCacheSchema", func(t *testing.T) {
		// This test verifies that authored_at is not in the cache schema
		// by confirming that reading from cache returns zero value for AuthoredAt
		input := models.NoteInput{
			GUID:  "authored-at-cache-test",
			Title: "Cache Schema Test",
		}

		note, err := models.CreateNote(input, testUserGUID)
		if err != nil {
			t.Fatalf("failed to create note: %v", err)
		}

		// GetNoteByID reads from cache, which doesn't have authored_at column
		retrieved, err := models.GetNoteByID(note.ID, testUserGUID)
		if err != nil {
			t.Fatalf("failed to get note: %v", err)
		}

		// When reading from cache, AuthoredAt should be zero/invalid
		// since cache schema doesn't include authored_at column
		if retrieved.AuthoredAt.Valid {
			t.Error("expected AuthoredAt to be invalid when reading from cache (cache lacks authored_at column)")
		}

		// But we can still read it directly from disk
		diskAuthoredAt := readAuthoredAtFromDisk(t, note.ID)
		if diskAuthoredAt.IsZero() {
			t.Error("expected authored_at to exist on disk")
		}
	})
}

// readAuthoredAtFromDisk reads authored_at directly from the disk database,
// bypassing the cache. This is used to verify authored_at behavior since
// the cache schema doesn't include the authored_at column.
func readAuthoredAtFromDisk(t *testing.T, id int64) time.Time {
	t.Helper()

	var authoredAtNull sql.NullTime
	err := models.DB().QueryRow(
		"SELECT authored_at FROM notes WHERE id = ?", id,
	).Scan(&authoredAtNull)
	if err != nil {
		t.Fatalf("failed to query authored_at from disk: %v", err)
	}

	if !authoredAtNull.Valid {
		t.Fatalf("authored_at is NULL on disk for note ID %d", id)
	}

	return authoredAtNull.Time
}
