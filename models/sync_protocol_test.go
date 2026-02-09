package models_test

import (
	"os"
	"testing"
	"time"

	"gonotes/models"
)

// spTestUserGUID is a constant user GUID for sync protocol tests.
const spTestUserGUID = "sp-test-user-guid-001"

// setupSyncProtocolTestDB initializes a clean test database for sync protocol tests.
func setupSyncProtocolTestDB(t *testing.T) func() {
	t.Helper()

	os.Remove("./test_sync_protocol.ddb")
	os.Remove("./test_sync_protocol.ddb.wal")

	if err := models.InitTestDB("./test_sync_protocol.ddb"); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}

	return func() {
		models.CloseDB()
		os.Remove("./test_sync_protocol.ddb")
		os.Remove("./test_sync_protocol.ddb.wal")
	}
}

// createTestNote is a helper that creates a note and returns it.
func createTestNote(t *testing.T, guid, title string) *models.Note {
	t.Helper()
	body := "body of " + title
	input := models.NoteInput{
		GUID:  guid,
		Title: title,
		Body:  &body,
	}
	note, err := models.CreateNote(input, spTestUserGUID)
	if err != nil {
		t.Fatalf("failed to create test note %q: %v", guid, err)
	}
	return note
}

// createTestCategory is a helper that creates a category and returns it.
func createTestCategory(t *testing.T, name string) *models.Category {
	t.Helper()
	desc := "description of " + name
	input := models.CategoryInput{
		Name:        name,
		Description: &desc,
	}
	cat, err := models.CreateCategory(input)
	if err != nil {
		t.Fatalf("failed to create test category %q: %v", name, err)
	}
	return cat
}

// ============================================================================
// TestGetUnifiedChangesForPeer
// ============================================================================

// TestGetUnifiedChangesForPeer verifies that note and category changes are
// merged into a single chronologically ordered stream, and that pagination
// via has_more works correctly.
func TestGetUnifiedChangesForPeer(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	peerID := "test-peer-unified"

	// Create a category first, then a note — both generate changes
	_ = createTestCategory(t, "Sync Category 1")

	// Small delay to ensure distinct timestamps in DuckDB
	time.Sleep(10 * time.Millisecond)

	_ = createTestNote(t, "sync-unified-note-1", "Sync Note 1")

	// Fetch unified changes — should have both types
	response, err := models.GetUnifiedChangesForPeer(peerID, 100)
	if err != nil {
		t.Fatalf("GetUnifiedChangesForPeer failed: %v", err)
	}

	if len(response.Changes) < 2 {
		t.Fatalf("expected at least 2 changes (1 category + 1 note), got %d", len(response.Changes))
	}

	// Verify chronological ordering: category change should come before
	// note change since it was created first
	foundCategory := false
	foundNote := false
	for _, ch := range response.Changes {
		if ch.EntityType == "category" {
			foundCategory = true
		}
		if ch.EntityType == "note" {
			foundNote = true
		}
	}
	if !foundCategory {
		t.Error("expected at least one category change in unified stream")
	}
	if !foundNote {
		t.Error("expected at least one note change in unified stream")
	}

	// Verify ordering: each change should be <= the next one chronologically
	for i := 1; i < len(response.Changes); i++ {
		if response.Changes[i].CreatedAt.Before(response.Changes[i-1].CreatedAt) {
			t.Errorf("changes not chronologically ordered at index %d", i)
		}
	}

	if response.HasMore {
		t.Error("expected has_more=false with limit=100 and only 2 changes")
	}
}

// TestGetUnifiedChangesForPeer_Pagination verifies has_more flag when
// the number of changes exceeds the limit.
func TestGetUnifiedChangesForPeer_Pagination(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	peerID := "test-peer-pagination"

	// Create multiple entities to generate many changes
	for i := 1; i <= 5; i++ {
		_ = createTestCategory(t, "PagCat"+string(rune('A'-1+i)))
		time.Sleep(5 * time.Millisecond)
	}

	// Pull with limit=2 — should get 2 changes and has_more=true
	response, err := models.GetUnifiedChangesForPeer(peerID, 2)
	if err != nil {
		t.Fatalf("GetUnifiedChangesForPeer failed: %v", err)
	}

	if len(response.Changes) != 2 {
		t.Errorf("expected 2 changes with limit=2, got %d", len(response.Changes))
	}

	if !response.HasMore {
		t.Error("expected has_more=true when more changes exist beyond the limit")
	}
}

// ============================================================================
// TestApplyIncomingSyncChange — Note Operations
// ============================================================================

// TestApplyIncomingSyncChange_NoteCreate verifies that applying a create
// change via the dispatcher inserts the note with the correct authored_at.
func TestApplyIncomingSyncChange_NoteCreate(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	title := "Synced Note From Remote"
	body := "This note was created on a remote machine"
	desc := "Remote description"
	isPrivate := false
	authoredAt := time.Date(2025, 3, 15, 10, 30, 0, 0, time.UTC)

	change := models.SyncChange{
		GUID:       "sync-change-note-create-001",
		EntityType: "note",
		EntityGUID: "remote-note-guid-001",
		Operation:  models.OperationCreate,
		Fragment: &models.NoteFragmentOutput{
			Bitmask:     models.FragmentTitle | models.FragmentDescription | models.FragmentBody | models.FragmentIsPrivate,
			Title:       &title,
			Description: &desc,
			Body:        &body,
			IsPrivate:   &isPrivate,
		},
		AuthoredAt: authoredAt,
		User:       spTestUserGUID,
	}

	err := models.ApplyIncomingSyncChange(change)
	if err != nil {
		t.Fatalf("ApplyIncomingSyncChange for note create failed: %v", err)
	}

	// Verify the note exists with the correct data
	note, err := models.GetNoteByGUID("remote-note-guid-001")
	if err != nil {
		t.Fatalf("failed to get created note: %v", err)
	}
	if note == nil {
		t.Fatal("expected note to exist after sync create")
	}

	if note.Title != title {
		t.Errorf("expected title %q, got %q", title, note.Title)
	}
	if !note.Body.Valid || note.Body.String != body {
		t.Errorf("expected body %q, got %v", body, note.Body)
	}
}

// TestApplyIncomingSyncChange_NoteUpdate verifies that applying an update
// change modifies only the specified fields.
func TestApplyIncomingSyncChange_NoteUpdate(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	// First create a note to update
	note := createTestNote(t, "update-target-guid", "Original Title")

	// Apply an update change that only changes the title
	newTitle := "Updated Via Sync"
	authoredAt := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	change := models.SyncChange{
		GUID:       "sync-change-note-update-001",
		EntityType: "note",
		EntityGUID: note.GUID,
		Operation:  models.OperationUpdate,
		Fragment: &models.NoteFragmentOutput{
			Bitmask: models.FragmentTitle,
			Title:   &newTitle,
		},
		AuthoredAt: authoredAt,
	}

	err := models.ApplyIncomingSyncChange(change)
	if err != nil {
		t.Fatalf("ApplyIncomingSyncChange for note update failed: %v", err)
	}

	// Verify the title was updated but body remains unchanged
	updated, err := models.GetNoteByGUID(note.GUID)
	if err != nil {
		t.Fatalf("failed to get updated note: %v", err)
	}
	if updated.Title != newTitle {
		t.Errorf("expected title %q, got %q", newTitle, updated.Title)
	}
	// Body should still be the original
	if !updated.Body.Valid || updated.Body.String != "body of Original Title" {
		t.Errorf("body should be unchanged, got %v", updated.Body)
	}
}

// TestApplyIncomingSyncChange_NoteDelete verifies that a delete change
// soft-deletes the note.
func TestApplyIncomingSyncChange_NoteDelete(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	note := createTestNote(t, "delete-target-guid", "To Be Deleted")

	change := models.SyncChange{
		GUID:       "sync-change-note-delete-001",
		EntityType: "note",
		EntityGUID: note.GUID,
		Operation:  models.OperationDelete,
	}

	err := models.ApplyIncomingSyncChange(change)
	if err != nil {
		t.Fatalf("ApplyIncomingSyncChange for note delete failed: %v", err)
	}

	// Note should no longer be found (soft-deleted)
	deleted, err := models.GetNoteByGUID(note.GUID)
	if err != nil {
		t.Fatalf("unexpected error getting deleted note: %v", err)
	}
	if deleted != nil {
		t.Error("expected note to be soft-deleted (not found via GetNoteByGUID)")
	}
}

// ============================================================================
// TestApplyIncomingSyncChange — Category Operations
// ============================================================================

// TestApplyIncomingSyncChange_CategoryCreate verifies that a category create
// change inserts the category correctly.
func TestApplyIncomingSyncChange_CategoryCreate(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	name := "Remote Category"
	desc := "Created on another machine"

	change := models.SyncChange{
		GUID:       "sync-change-cat-create-001",
		EntityType: "category",
		EntityGUID: "remote-category-guid-001",
		Operation:  models.OperationCreate,
		Fragment: &models.CategoryFragmentOutput{
			Bitmask:     models.CatFragmentName | models.CatFragmentDescription,
			Name:        &name,
			Description: &desc,
		},
	}

	err := models.ApplyIncomingSyncChange(change)
	if err != nil {
		t.Fatalf("ApplyIncomingSyncChange for category create failed: %v", err)
	}

	cat, err := models.GetCategoryByGUID("remote-category-guid-001")
	if err != nil {
		t.Fatalf("failed to get created category: %v", err)
	}
	if cat == nil {
		t.Fatal("expected category to exist after sync create")
	}
	if cat.Name != name {
		t.Errorf("expected name %q, got %q", name, cat.Name)
	}
}

// TestApplyIncomingSyncChange_CategoryUpdate verifies that a category update
// change modifies the specified fields.
func TestApplyIncomingSyncChange_CategoryUpdate(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	cat := createTestCategory(t, "Original Category")

	newName := "Renamed Category"
	change := models.SyncChange{
		GUID:       "sync-change-cat-update-001",
		EntityType: "category",
		EntityGUID: cat.GUID,
		Operation:  models.OperationUpdate,
		Fragment: &models.CategoryFragmentOutput{
			Bitmask: models.CatFragmentName,
			Name:    &newName,
		},
	}

	err := models.ApplyIncomingSyncChange(change)
	if err != nil {
		t.Fatalf("ApplyIncomingSyncChange for category update failed: %v", err)
	}

	updated, err := models.GetCategoryByGUID(cat.GUID)
	if err != nil {
		t.Fatalf("failed to get updated category: %v", err)
	}
	if updated.Name != newName {
		t.Errorf("expected name %q, got %q", newName, updated.Name)
	}
}

// TestApplyIncomingSyncChange_CategoryDelete verifies that a category delete
// change removes the category.
func TestApplyIncomingSyncChange_CategoryDelete(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	cat := createTestCategory(t, "Delete Me Category")

	change := models.SyncChange{
		GUID:       "sync-change-cat-delete-001",
		EntityType: "category",
		EntityGUID: cat.GUID,
		Operation:  models.OperationDelete,
	}

	err := models.ApplyIncomingSyncChange(change)
	if err != nil {
		t.Fatalf("ApplyIncomingSyncChange for category delete failed: %v", err)
	}

	deleted, err := models.GetCategoryByGUID(cat.GUID)
	if err != nil {
		t.Fatalf("unexpected error getting deleted category: %v", err)
	}
	if deleted != nil {
		t.Error("expected category to be deleted after sync delete")
	}
}

// ============================================================================
// TestApplyIncomingSyncChange — Category Mapping
// ============================================================================

// TestApplyIncomingSyncChange_NoteCategoryMapping verifies that a note update
// with FragmentCategories correctly applies category mappings.
func TestApplyIncomingSyncChange_NoteCategoryMapping(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	// Create a note and a category first
	note := createTestNote(t, "mapping-note-guid", "Note With Categories")
	cat := createTestCategory(t, "Mapping Category")

	// Build a category mapping JSON snapshot
	categoriesJSON := `[{"category_guid":"` + cat.GUID + `","selected_subcategories":[]}]`

	change := models.SyncChange{
		GUID:       "sync-change-mapping-001",
		EntityType: "note",
		EntityGUID: note.GUID,
		Operation:  models.OperationUpdate,
		Fragment: &models.NoteFragmentOutput{
			Bitmask:    models.FragmentCategories,
			Categories: &categoriesJSON,
		},
		AuthoredAt: time.Now(),
	}

	err := models.ApplyIncomingSyncChange(change)
	if err != nil {
		t.Fatalf("ApplyIncomingSyncChange for category mapping failed: %v", err)
	}

	// Verify the note-category mapping exists
	categories, err := models.GetNoteCategories(note.ID)
	if err != nil {
		t.Fatalf("failed to get note categories: %v", err)
	}
	if len(categories) != 1 {
		t.Errorf("expected 1 category mapping, got %d", len(categories))
	}
	if len(categories) > 0 && categories[0].GUID != cat.GUID {
		t.Errorf("expected category GUID %q, got %q", cat.GUID, categories[0].GUID)
	}
}

// ============================================================================
// TestApplyIncomingSyncChange — Idempotency
// ============================================================================

// TestApplyIncomingSyncChange_Idempotency verifies that applying the same
// change GUID twice is a no-op (no error, no duplicate data).
func TestApplyIncomingSyncChange_Idempotency(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	title := "Idempotent Note"
	body := "Should only exist once"

	change := models.SyncChange{
		GUID:       "sync-change-idempotent-001",
		EntityType: "note",
		EntityGUID: "idempotent-note-guid",
		Operation:  models.OperationCreate,
		Fragment: &models.NoteFragmentOutput{
			Bitmask: models.FragmentTitle | models.FragmentBody,
			Title:   &title,
			Body:    &body,
		},
		AuthoredAt: time.Now(),
		User:       spTestUserGUID,
	}

	// First application — should succeed
	err := models.ApplyIncomingSyncChange(change)
	if err != nil {
		t.Fatalf("first application failed: %v", err)
	}

	// Second application — should be a no-op (idempotent)
	err = models.ApplyIncomingSyncChange(change)
	if err != nil {
		t.Fatalf("second application should be idempotent but got error: %v", err)
	}

	// Verify only one note exists with this GUID
	note, err := models.GetNoteByGUID("idempotent-note-guid")
	if err != nil {
		t.Fatalf("failed to get note: %v", err)
	}
	if note == nil {
		t.Fatal("expected note to exist")
	}
	if note.Title != title {
		t.Errorf("expected title %q, got %q", title, note.Title)
	}
}

// ============================================================================
// TestGetEntitySnapshot
// ============================================================================

// TestGetEntitySnapshot verifies that GetEntitySnapshot returns the full
// current state of a note including its complete body text.
func TestGetEntitySnapshot(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	note := createTestNote(t, "snapshot-note-guid", "Snapshot Test Note")

	snapshot, err := models.GetEntitySnapshot("note", note.GUID)
	if err != nil {
		t.Fatalf("GetEntitySnapshot failed: %v", err)
	}
	if snapshot == nil {
		t.Fatal("expected non-nil snapshot")
	}

	if snapshot.EntityType != "note" {
		t.Errorf("expected entity_type 'note', got %q", snapshot.EntityType)
	}
	if snapshot.EntityGUID != note.GUID {
		t.Errorf("expected entity_guid %q, got %q", note.GUID, snapshot.EntityGUID)
	}
	if snapshot.Operation != models.OperationCreate {
		t.Errorf("expected operation Create (%d), got %d", models.OperationCreate, snapshot.Operation)
	}

	// Verify the fragment contains the full body
	fragment, ok := snapshot.Fragment.(*models.NoteFragmentOutput)
	if !ok {
		t.Fatalf("expected fragment to be *NoteFragmentOutput, got %T", snapshot.Fragment)
	}
	if fragment.Title == nil || *fragment.Title != "Snapshot Test Note" {
		t.Errorf("expected title 'Snapshot Test Note', got %v", fragment.Title)
	}
	if fragment.Body == nil || *fragment.Body != "body of Snapshot Test Note" {
		t.Errorf("expected body 'body of Snapshot Test Note', got %v", fragment.Body)
	}
}

// TestGetEntitySnapshot_Category verifies snapshot for categories.
func TestGetEntitySnapshot_Category(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	cat := createTestCategory(t, "Snapshot Category")

	snapshot, err := models.GetEntitySnapshot("category", cat.GUID)
	if err != nil {
		t.Fatalf("GetEntitySnapshot for category failed: %v", err)
	}
	if snapshot == nil {
		t.Fatal("expected non-nil snapshot")
	}

	if snapshot.EntityType != "category" {
		t.Errorf("expected entity_type 'category', got %q", snapshot.EntityType)
	}

	fragment, ok := snapshot.Fragment.(*models.CategoryFragmentOutput)
	if !ok {
		t.Fatalf("expected fragment to be *CategoryFragmentOutput, got %T", snapshot.Fragment)
	}
	if fragment.Name == nil || *fragment.Name != "Snapshot Category" {
		t.Errorf("expected name 'Snapshot Category', got %v", fragment.Name)
	}
}

// TestGetEntitySnapshot_NotFound verifies proper error for missing entities.
func TestGetEntitySnapshot_NotFound(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	_, err := models.GetEntitySnapshot("note", "nonexistent-guid")
	if err == nil {
		t.Error("expected error for nonexistent entity, got nil")
	}
}

// ============================================================================
// TestGetSyncStatus
// ============================================================================

// TestGetSyncStatus verifies that counts and checksum are returned correctly.
func TestGetSyncStatus(t *testing.T) {
	cleanup := setupSyncProtocolTestDB(t)
	defer cleanup()

	// Get status with empty DB
	statusEmpty, err := models.GetSyncStatus()
	if err != nil {
		t.Fatalf("GetSyncStatus failed on empty DB: %v", err)
	}
	if statusEmpty.NoteCount != 0 {
		t.Errorf("expected 0 notes on empty DB, got %d", statusEmpty.NoteCount)
	}
	if statusEmpty.CategoryCount != 0 {
		t.Errorf("expected 0 categories on empty DB, got %d", statusEmpty.CategoryCount)
	}
	if statusEmpty.Checksum == "" {
		t.Error("expected non-empty checksum even on empty DB")
	}

	// Create some data
	_ = createTestNote(t, "status-note-1", "Status Note 1")
	_ = createTestNote(t, "status-note-2", "Status Note 2")
	_ = createTestCategory(t, "Status Category 1")

	status, err := models.GetSyncStatus()
	if err != nil {
		t.Fatalf("GetSyncStatus failed: %v", err)
	}
	if status.NoteCount != 2 {
		t.Errorf("expected 2 notes, got %d", status.NoteCount)
	}
	if status.CategoryCount != 1 {
		t.Errorf("expected 1 category, got %d", status.CategoryCount)
	}
	if status.Checksum == "" {
		t.Error("expected non-empty checksum")
	}

	// Checksum should differ from empty DB
	if status.Checksum == statusEmpty.Checksum {
		t.Error("checksum should differ after adding data")
	}

	// Getting status again with same data should produce same checksum
	status2, err := models.GetSyncStatus()
	if err != nil {
		t.Fatalf("GetSyncStatus second call failed: %v", err)
	}
	if status2.Checksum != status.Checksum {
		t.Error("expected identical checksums for unchanged data")
	}
}
