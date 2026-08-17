package models_test

import (
	"errors"
	"testing"

	"gonotes/models"
)

// note_version_test.go covers the optimistic-concurrency guard — the backstop
// that makes a lost update impossible rather than merely unlikely.
//
// The lock (lock.go) keeps a second EDITOR from starting; this keeps a second
// WRITE from landing, and it is the half that still works when the lock has
// expired, been stolen, or was never taken by whatever else writes to notes.

const versionTestUser = "version-test-user-guid"

func setupVersionTestDB(t *testing.T) func() {
	t.Helper()
	if err := models.InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}
	return func() { models.CloseDB() }
}

func seedVersionNote(t *testing.T, guid, title string) *models.Note {
	t.Helper()
	note, err := models.CreateNote(models.NoteInput{GUID: guid, Title: title}, versionTestUser)
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}
	return note
}

func TestNewNotesStartAtVersionOne(t *testing.T) {
	defer setupVersionTestDB(t)()

	note := seedVersionNote(t, "version-start", "Fresh note")
	if note.Version != 1 {
		t.Fatalf("a new note has version %d, want 1", note.Version)
	}
}

func TestUpdateBumpsTheVersion(t *testing.T) {
	defer setupVersionTestDB(t)()

	note := seedVersionNote(t, "version-bump", "Original")

	updated, err := models.UpdateNote(note.ID,
		models.NoteInput{GUID: note.GUID, Title: "Edited"}, versionTestUser)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Version != note.Version+1 {
		t.Fatalf("after one update the version is %d, want %d", updated.Version, note.Version+1)
	}
}

// The core case: two editors load the same note, both save, and the second one
// must be told rather than silently winning.
func TestTheSecondWriterIsRefused(t *testing.T) {
	defer setupVersionTestDB(t)()

	note := seedVersionNote(t, "version-race", "Shared note")
	loadedVersion := note.Version // both editors loaded this

	// Editor A saves first and wins.
	if _, err := models.UpdateNote(note.ID, models.NoteInput{
		GUID: note.GUID, Title: "A's title", ExpectedVersion: loadedVersion,
	}, versionTestUser); err != nil {
		t.Fatalf("the first writer was refused: %v", err)
	}

	// Editor B saves against the version it loaded, which has moved.
	_, err := models.UpdateNote(note.ID, models.NoteInput{
		GUID: note.GUID, Title: "B's title", ExpectedVersion: loadedVersion,
	}, versionTestUser)
	if !errors.Is(err, models.ErrStaleWrite) {
		t.Fatalf("the second writer got %v, want ErrStaleWrite", err)
	}

	var stale *models.StaleWriteError
	if !errors.As(err, &stale) {
		t.Fatal("the refusal does not carry the current note; the loser cannot see what it lost to")
	}
	if stale.Current == nil || stale.Current.Title != "A's title" {
		t.Fatalf("the refusal reports the stored title as %v, want \"A's title\"", stale.Current)
	}

	// And, crucially, nothing was written.
	current, _ := models.GetNoteByID(note.ID, versionTestUser)
	if current.Title != "A's title" {
		t.Fatalf("the refused write landed anyway: the note now says %q", current.Title)
	}
}

// After losing, the loser can save by naming the version that beat it — which
// is what the TUI's "overwrite theirs" does.
func TestNamingTheWinningVersionLetsTheWriteThrough(t *testing.T) {
	defer setupVersionTestDB(t)()

	note := seedVersionNote(t, "version-retry", "Shared note")

	winner, err := models.UpdateNote(note.ID, models.NoteInput{
		GUID: note.GUID, Title: "A's title", ExpectedVersion: note.Version,
	}, versionTestUser)
	if err != nil {
		t.Fatalf("the first writer was refused: %v", err)
	}

	final, err := models.UpdateNote(note.ID, models.NoteInput{
		GUID: note.GUID, Title: "B's title", ExpectedVersion: winner.Version,
	}, versionTestUser)
	if err != nil {
		t.Fatalf("the retry against the winning version was refused: %v", err)
	}
	if final.Title != "B's title" {
		t.Fatalf("the retry stored %q, want \"B's title\"", final.Title)
	}
}

// Zero means "do not check", and it has to keep meaning that: the Markdown
// importer, sync apply, and every client older than this field all write
// without a version and must not start failing.
func TestAnUnguardedWriteIsNeverRefused(t *testing.T) {
	defer setupVersionTestDB(t)()

	note := seedVersionNote(t, "version-unguarded", "Shared note")

	// Move the note underneath, twice, so no stale version could accidentally
	// match.
	models.UpdateNote(note.ID, models.NoteInput{GUID: note.GUID, Title: "moved once"}, versionTestUser)
	models.UpdateNote(note.ID, models.NoteInput{GUID: note.GUID, Title: "moved twice"}, versionTestUser)

	updated, err := models.UpdateNote(note.ID,
		models.NoteInput{GUID: note.GUID, Title: "unguarded"}, versionTestUser)
	if err != nil {
		t.Fatalf("an unguarded write was refused: %v", err)
	}
	if updated.Title != "unguarded" {
		t.Fatalf("the unguarded write stored %q", updated.Title)
	}
}

// Flagging and deleting are writes too, and an editor holding the pre-flag
// version must not be able to save over them.
func TestFlagAndDeleteBumpTheVersion(t *testing.T) {
	defer setupVersionTestDB(t)()

	note := seedVersionNote(t, "version-flag", "Flag me")

	flagged, err := models.ToggleNoteFlag(note.ID, versionTestUser)
	if err != nil {
		t.Fatalf("toggling the flag failed: %v", err)
	}
	if flagged.Version != note.Version+1 {
		t.Fatalf("flagging left the version at %d, want %d", flagged.Version, note.Version+1)
	}

	// An editor that loaded the note before the flag is now stale, which is the
	// point: its save carries the OLD is_flagged and would silently revert it.
	_, err = models.UpdateNote(note.ID, models.NoteInput{
		GUID: note.GUID, Title: "stale editor", ExpectedVersion: note.Version,
	}, versionTestUser)
	if !errors.Is(err, models.ErrStaleWrite) {
		t.Fatalf("a save built on the pre-flag note was accepted (%v)", err)
	}
}

// A privacy flip physically moves the note between the two databases. The
// version has to survive that move and advance, or the flip would be a hole in
// the guard big enough to drive an editor through.
func TestPrivacyFlipCarriesAndBumpsTheVersion(t *testing.T) {
	defer setupVersionTestDB(t)()

	note := seedVersionNote(t, "version-flip", "Public note")

	flipped, err := models.UpdateNote(note.ID, models.NoteInput{
		GUID: note.GUID, Title: "Now private", IsPrivate: true, ExpectedVersion: note.Version,
	}, versionTestUser)
	if err != nil {
		t.Fatalf("the privacy flip was refused: %v", err)
	}
	if !flipped.IsPrivate {
		t.Fatal("the note did not actually become private")
	}
	if flipped.Version != note.Version+1 {
		t.Fatalf("after the flip the version is %d, want %d", flipped.Version, note.Version+1)
	}

	// The pre-flip version must no longer be writable.
	_, err = models.UpdateNote(note.ID, models.NoteInput{
		GUID: note.GUID, Title: "stale", IsPrivate: true, ExpectedVersion: note.Version,
	}, versionTestUser)
	if !errors.Is(err, models.ErrStaleWrite) {
		t.Fatalf("a save built on the pre-flip note was accepted (%v)", err)
	}
}
