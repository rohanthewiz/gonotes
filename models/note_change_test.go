package models_test

import (
	"os"
	"testing"

	"gonotes/models"
)

// setupNoteChangeTestDB initializes a clean test database for note change tests
func setupNoteChangeTestDB(t *testing.T) func() {
	t.Helper()

	// Remove existing test database files
	os.Remove("./test_note_change.ddb")
	os.Remove("./test_note_change.ddb.wal")

	// Initialize test database
	if err := models.InitTestDB("./test_note_change.ddb"); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}

	// Return cleanup function
	return func() {
		models.CloseDB()
		os.Remove("./test_note_change.ddb")
		os.Remove("./test_note_change.ddb.wal")
	}
}

// TestNoteChangeOnCreate verifies that a change is recorded when creating a note
func TestNoteChangeOnCreate(t *testing.T) {
	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	// Create a note
	descr := "Test description"
	body := "Test body"
	tags := "test,note"
	input := models.NoteInput{
		GUID:        "note-change-create-test",
		Title:       "Test Note",
		Description: &descr,
		Body:        &body,
		Tags:        &tags,
		IsPrivate:   false,
	}

	note, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Retrieve unsent changes for a peer
	changes, err := models.GetUnsentChangesForPeer("peer1", 10)
	if err != nil {
		t.Fatalf("failed to get unsent changes: %v", err)
	}

	// Should have exactly one change
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	change := changes[0]

	// Verify change properties
	if change.NoteGUID != note.GUID {
		t.Errorf("expected note GUID %s, got %s", note.GUID, change.NoteGUID)
	}

	if change.Operation != models.OperationCreate {
		t.Errorf("expected operation %d (Create), got %d", models.OperationCreate, change.Operation)
	}

	// Create operation should have a fragment
	if !change.NoteFragmentID.Valid {
		t.Error("expected fragment ID to be valid for create operation")
	}

	// Verify fragment contains the data
	if change.NoteFragmentID.Valid {
		fragment, err := models.GetNoteFragment(change.NoteFragmentID.Int64)
		if err != nil {
			t.Fatalf("failed to get note fragment: %v", err)
		}

		if fragment == nil {
			t.Fatal("expected fragment to exist")
		}

		// Check that bitmask includes all fields
		expectedBitmask := int16(models.FragmentTitle | models.FragmentDescription |
			models.FragmentBody | models.FragmentTags | models.FragmentIsPrivate)
		if fragment.Bitmask != expectedBitmask {
			t.Errorf("expected bitmask %d, got %d", expectedBitmask, fragment.Bitmask)
		}

		// Verify fragment data
		if !fragment.Title.Valid || fragment.Title.String != "Test Note" {
			t.Errorf("expected title 'Test Note', got %v", fragment.Title)
		}
		if !fragment.Description.Valid || fragment.Description.String != descr {
			t.Errorf("expected description '%s', got %v", descr, fragment.Description)
		}
		if !fragment.Body.Valid || fragment.Body.String != body {
			t.Errorf("expected body '%s', got %v", body, fragment.Body)
		}
		if !fragment.Tags.Valid || fragment.Tags.String != tags {
			t.Errorf("expected tags '%s', got %v", tags, fragment.Tags)
		}
		if !fragment.IsPrivate.Valid || fragment.IsPrivate.Bool != false {
			t.Errorf("expected is_private false, got %v", fragment.IsPrivate)
		}
	}
}

// TestNoteChangeOnUpdate verifies that only changed fields are tracked in the delta
func TestNoteChangeOnUpdate(t *testing.T) {
	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	// Create a note first
	descr := "Original description"
	body := "Original body"
	input := models.NoteInput{
		GUID:        "note-change-update-test",
		Title:       "Original Title",
		Description: &descr,
		Body:        &body,
		IsPrivate:   false,
	}

	note, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Mark the create change as synced to clear it
	createChanges, _ := models.GetUnsentChangesForPeer("peer1", 10)
	if len(createChanges) > 0 {
		models.MarkChangeSyncedToPeer(createChanges[0].ID, "peer1")
	}

	// Update only the title and body
	newBody := "Updated body"
	updateInput := models.NoteInput{
		Title:       "Updated Title",
		Description: &descr, // Same description
		Body:        &newBody,
		IsPrivate:   false, // Same privacy
	}

	_, err = models.UpdateNote(note.ID, updateInput)
	if err != nil {
		t.Fatalf("failed to update note: %v", err)
	}

	// Get the update change
	changes, err := models.GetUnsentChangesForPeer("peer1", 10)
	if err != nil {
		t.Fatalf("failed to get unsent changes: %v", err)
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	change := changes[0]

	// Verify it's an update operation
	if change.Operation != models.OperationUpdate {
		t.Errorf("expected operation %d (Update), got %d", models.OperationUpdate, change.Operation)
	}

	// Get the fragment
	if !change.NoteFragmentID.Valid {
		t.Fatal("expected fragment ID to be valid for update operation")
	}

	fragment, err := models.GetNoteFragment(change.NoteFragmentID.Int64)
	if err != nil {
		t.Fatalf("failed to get note fragment: %v", err)
	}

	// Bitmask should only include Title and Body (changed fields)
	expectedBitmask := int16(models.FragmentTitle | models.FragmentBody)
	if fragment.Bitmask != expectedBitmask {
		t.Errorf("expected bitmask %d (Title+Body), got %d", expectedBitmask, fragment.Bitmask)
	}

	// Verify only changed fields are in fragment
	if !fragment.Title.Valid || fragment.Title.String != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got %v", fragment.Title)
	}
	if !fragment.Body.Valid || fragment.Body.String != newBody {
		t.Errorf("expected body '%s', got %v", newBody, fragment.Body)
	}

	// Unchanged fields should NOT be in fragment (or should be null)
	if fragment.Description.Valid {
		t.Error("expected description to be null in delta fragment (unchanged)")
	}
	if fragment.IsPrivate.Valid {
		t.Error("expected is_private to be null in delta fragment (unchanged)")
	}
}

// TestNoteChangeOnDelete verifies that delete changes are recorded without a fragment
func TestNoteChangeOnDelete(t *testing.T) {
	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	// Create a note
	input := models.NoteInput{
		GUID:  "note-change-delete-test",
		Title: "To Be Deleted",
	}

	note, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Mark the create change as synced
	createChanges, _ := models.GetUnsentChangesForPeer("peer1", 10)
	if len(createChanges) > 0 {
		models.MarkChangeSyncedToPeer(createChanges[0].ID, "peer1")
	}

	// Delete the note
	deleted, err := models.DeleteNote(note.ID)
	if err != nil {
		t.Fatalf("failed to delete note: %v", err)
	}
	if !deleted {
		t.Fatal("expected note to be deleted")
	}

	// Get the delete change
	changes, err := models.GetUnsentChangesForPeer("peer1", 10)
	if err != nil {
		t.Fatalf("failed to get unsent changes: %v", err)
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	change := changes[0]

	// Verify it's a delete operation
	if change.Operation != models.OperationDelete {
		t.Errorf("expected operation %d (Delete), got %d", models.OperationDelete, change.Operation)
	}

	// Delete operation should NOT have a fragment
	if change.NoteFragmentID.Valid {
		t.Error("expected fragment ID to be null for delete operation")
	}
}

// TestUnsentChangesForPeer verifies peer-specific filtering of changes
func TestUnsentChangesForPeer(t *testing.T) {
	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	// Create multiple notes
	for i := 1; i <= 3; i++ {
		input := models.NoteInput{
			GUID:  "note-" + string(rune('0'+i)),
			Title: "Test Note " + string(rune('0'+i)),
		}
		_, err := models.CreateNote(input)
		if err != nil {
			t.Fatalf("failed to create note %d: %v", i, err)
		}
	}

	// Get unsent changes for peer1 (should get all 3)
	peer1Changes, err := models.GetUnsentChangesForPeer("peer1", 10)
	if err != nil {
		t.Fatalf("failed to get unsent changes for peer1: %v", err)
	}
	if len(peer1Changes) != 3 {
		t.Errorf("expected 3 changes for peer1, got %d", len(peer1Changes))
	}

	// Get unsent changes for peer2 (should also get all 3)
	peer2Changes, err := models.GetUnsentChangesForPeer("peer2", 10)
	if err != nil {
		t.Fatalf("failed to get unsent changes for peer2: %v", err)
	}
	if len(peer2Changes) != 3 {
		t.Errorf("expected 3 changes for peer2, got %d", len(peer2Changes))
	}

	// Mark first change as synced to peer1
	err = models.MarkChangeSyncedToPeer(peer1Changes[0].ID, "peer1")
	if err != nil {
		t.Fatalf("failed to mark change as synced to peer1: %v", err)
	}

	// Get unsent changes for peer1 again (should get 2)
	peer1ChangesAfter, err := models.GetUnsentChangesForPeer("peer1", 10)
	if err != nil {
		t.Fatalf("failed to get unsent changes for peer1 after sync: %v", err)
	}
	if len(peer1ChangesAfter) != 2 {
		t.Errorf("expected 2 changes for peer1 after sync, got %d", len(peer1ChangesAfter))
	}

	// peer2 should still have all 3
	peer2ChangesAfter, err := models.GetUnsentChangesForPeer("peer2", 10)
	if err != nil {
		t.Fatalf("failed to get unsent changes for peer2 after peer1 sync: %v", err)
	}
	if len(peer2ChangesAfter) != 3 {
		t.Errorf("expected 3 changes for peer2 (unaffected by peer1 sync), got %d", len(peer2ChangesAfter))
	}
}

// TestMarkChangeSyncedToPeer verifies sync tracking functionality
func TestMarkChangeSyncedToPeer(t *testing.T) {
	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	// Create a note
	input := models.NoteInput{
		GUID:  "sync-test-note",
		Title: "Sync Test",
	}

	_, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Get the change
	changes, err := models.GetUnsentChangesForPeer("peer1", 10)
	if err != nil {
		t.Fatalf("failed to get unsent changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	changeID := changes[0].ID

	// Mark as synced to peer1
	err = models.MarkChangeSyncedToPeer(changeID, "peer1")
	if err != nil {
		t.Fatalf("failed to mark change as synced: %v", err)
	}

	// Should now be empty for peer1
	changesAfter, err := models.GetUnsentChangesForPeer("peer1", 10)
	if err != nil {
		t.Fatalf("failed to get unsent changes after sync: %v", err)
	}
	if len(changesAfter) != 0 {
		t.Errorf("expected 0 changes after marking as synced, got %d", len(changesAfter))
	}

	// Mark as synced to peer2
	err = models.MarkChangeSyncedToPeer(changeID, "peer2")
	if err != nil {
		t.Fatalf("failed to mark change as synced to peer2: %v", err)
	}

	// Should be empty for peer2 as well
	peer2Changes, err := models.GetUnsentChangesForPeer("peer2", 10)
	if err != nil {
		t.Fatalf("failed to get unsent changes for peer2: %v", err)
	}
	if len(peer2Changes) != 0 {
		t.Errorf("expected 0 changes for peer2 after marking as synced, got %d", len(peer2Changes))
	}
}

// TestChangeBitmaskComputation tests the bitmask computation logic directly
func TestChangeBitmaskComputation(t *testing.T) {
	// This is a unit test of the internal computeChangeBitmask function
	// We'll test it indirectly through the update operation

	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	// Create a note with all fields populated
	descr := "Description"
	body := "Body"
	tags := "tag1,tag2"
	input := models.NoteInput{
		GUID:        "bitmask-test",
		Title:       "Title",
		Description: &descr,
		Body:        &body,
		Tags:        &tags,
		IsPrivate:   false,
	}

	note, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Clear the create change
	createChanges, _ := models.GetUnsentChangesForPeer("peer1", 10)
	if len(createChanges) > 0 {
		models.MarkChangeSyncedToPeer(createChanges[0].ID, "peer1")
	}

	// Test case 1: Change only title
	updateInput1 := models.NoteInput{
		Title:       "New Title",
		Description: &descr,
		Body:        &body,
		Tags:        &tags,
		IsPrivate:   false,
	}
	_, err = models.UpdateNote(note.ID, updateInput1)
	if err != nil {
		t.Fatalf("failed to update note (case 1): %v", err)
	}

	changes1, _ := models.GetUnsentChangesForPeer("peer1", 10)
	if len(changes1) > 0 {
		fragment1, _ := models.GetNoteFragment(changes1[0].NoteFragmentID.Int64)
		if fragment1.Bitmask != models.FragmentTitle {
			t.Errorf("expected bitmask %d (Title only), got %d", models.FragmentTitle, fragment1.Bitmask)
		}
		models.MarkChangeSyncedToPeer(changes1[0].ID, "peer1")
	}

	// Test case 2: Change privacy flag
	updateInput2 := models.NoteInput{
		Title:       "New Title",
		Description: &descr,
		Body:        &body,
		Tags:        &tags,
		IsPrivate:   true, // Changed
	}
	_, err = models.UpdateNote(note.ID, updateInput2)
	if err != nil {
		t.Fatalf("failed to update note (case 2): %v", err)
	}

	changes2, _ := models.GetUnsentChangesForPeer("peer1", 10)
	if len(changes2) > 0 {
		fragment2, _ := models.GetNoteFragment(changes2[0].NoteFragmentID.Int64)
		if fragment2.Bitmask != models.FragmentIsPrivate {
			t.Errorf("expected bitmask %d (IsPrivate only), got %d", models.FragmentIsPrivate, fragment2.Bitmask)
		}
		models.MarkChangeSyncedToPeer(changes2[0].ID, "peer1")
	}

	// Test case 3: Change multiple fields
	newDescr := "New Description"
	newBody := "New Body"
	updateInput3 := models.NoteInput{
		Title:       "New Title", // Same
		Description: &newDescr,   // Changed
		Body:        &newBody,    // Changed
		Tags:        &tags,       // Same
		IsPrivate:   true,        // Same
	}
	_, err = models.UpdateNote(note.ID, updateInput3)
	if err != nil {
		t.Fatalf("failed to update note (case 3): %v", err)
	}

	changes3, _ := models.GetUnsentChangesForPeer("peer1", 10)
	if len(changes3) > 0 {
		fragment3, _ := models.GetNoteFragment(changes3[0].NoteFragmentID.Int64)
		expectedBitmask := int16(models.FragmentDescription | models.FragmentBody)
		if fragment3.Bitmask != expectedBitmask {
			t.Errorf("expected bitmask %d (Description+Body), got %d", expectedBitmask, fragment3.Bitmask)
		}
	}
}

// TestGetNoteChangeWithFragment verifies the output type for API responses
func TestGetNoteChangeWithFragment(t *testing.T) {
	cleanup := setupNoteChangeTestDB(t)
	defer cleanup()

	// Create a note
	body := "Test body"
	input := models.NoteInput{
		GUID:  "output-test",
		Title: "Output Test",
		Body:  &body,
	}

	_, err := models.CreateNote(input)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Get the change
	changes, err := models.GetUnsentChangesForPeer("peer1", 10)
	if err != nil {
		t.Fatalf("failed to get unsent changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	// Get full change with fragment
	changeOutput, err := models.GetNoteChangeWithFragment(changes[0].ID)
	if err != nil {
		t.Fatalf("failed to get note change with fragment: %v", err)
	}

	if changeOutput == nil {
		t.Fatal("expected change output to exist")
	}

	// Verify the output includes the fragment
	if changeOutput.Fragment == nil {
		t.Error("expected fragment to be included in output")
	}

	// Verify fragment data
	if changeOutput.Fragment.Title.Valid && changeOutput.Fragment.Title.String != "Output Test" {
		t.Errorf("expected fragment title 'Output Test', got %s", changeOutput.Fragment.Title.String)
	}
}
