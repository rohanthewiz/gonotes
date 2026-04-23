package main

import (
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonotes/models"
)

func setupImportTest(t *testing.T) (cleanup func(), userGUID string) {
	t.Helper()

	os.Remove("./test_import.ddb")
	os.Remove("./test_import.ddb.wal")

	if err := models.InitTestDB("./test_import.ddb"); err != nil {
		t.Fatalf("InitTestDB: %v", err)
	}

	user, err := models.CreateUser(models.UserRegisterInput{
		Username: "importer",
		Password: "test-password-123",
	})
	if err != nil {
		models.CloseDB()
		os.Remove("./test_import.ddb")
		t.Fatalf("CreateUser: %v", err)
	}

	cleanup = func() {
		models.CloseDB()
		os.Remove("./test_import.ddb")
		os.Remove("./test_import.ddb.wal")
	}
	return cleanup, user.GUID
}

func TestImportGob_Roundtrip(t *testing.T) {
	cleanup, userGUID := setupImportTest(t)
	defer cleanup()

	src := []legacyNote{
		{
			Guid:        "leg-001",
			Title:       "Hello",
			Description: "desc",
			Body:        "body content",
			Tag:         "a,b",
			Public:      true,
			CreatedAt:   time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC),
			UpdatedAt:   time.Date(2021, 6, 7, 8, 9, 10, 0, time.UTC),
		},
		{
			Guid:      "leg-002",
			Title:     "Empty body",
			CreatedAt: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	path := filepath.Join(t.TempDir(), "notes.gob")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create gob file: %v", err)
	}
	if err := gob.NewEncoder(f).Encode(src); err != nil {
		f.Close()
		t.Fatalf("gob encode: %v", err)
	}
	f.Close()

	rf, err := os.Open(path)
	if err != nil {
		t.Fatalf("open gob file: %v", err)
	}
	var decoded []legacyNote
	if err := gob.NewDecoder(rf).Decode(&decoded); err != nil {
		rf.Close()
		t.Fatalf("gob decode: %v", err)
	}
	rf.Close()

	summary := importNotes(decoded, userGUID)
	if summary.imported != 2 || summary.skipped != 0 || summary.errored != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	n, err := models.GetNoteByGUID("leg-001")
	if err != nil {
		t.Fatalf("GetNoteByGUID: %v", err)
	}
	if n == nil {
		t.Fatal("note leg-001 not found after import")
	}
	if !n.CreatedAt.Equal(src[0].CreatedAt) {
		t.Errorf("CreatedAt: got %v, want %v", n.CreatedAt, src[0].CreatedAt)
	}
	if !n.UpdatedAt.Equal(src[0].UpdatedAt) {
		t.Errorf("UpdatedAt: got %v, want %v", n.UpdatedAt, src[0].UpdatedAt)
	}
	if n.IsPrivate {
		t.Error("IsPrivate must default to false; source Public must NOT map to IsPrivate")
	}
	if !n.CreatedBy.Valid || n.CreatedBy.String != userGUID {
		t.Errorf("CreatedBy: got %+v, want %s", n.CreatedBy, userGUID)
	}
	if !n.UpdatedBy.Valid || n.UpdatedBy.String != userGUID {
		t.Errorf("UpdatedBy: got %+v, want %s", n.UpdatedBy, userGUID)
	}
	if !n.Tags.Valid || n.Tags.String != "a,b" {
		t.Errorf("Tags: got %+v, want a,b", n.Tags)
	}
	if !n.Description.Valid || n.Description.String != "desc" {
		t.Errorf("Description: got %+v, want desc", n.Description)
	}
	if !n.Body.Valid || n.Body.String != "body content" {
		t.Errorf("Body: got %+v, want body content", n.Body)
	}

	n2, err := models.GetNoteByGUID("leg-002")
	if err != nil || n2 == nil {
		t.Fatalf("GetNoteByGUID leg-002: note=%v err=%v", n2, err)
	}
	if n2.Body.Valid {
		t.Errorf("empty source body should map to NULL, got %+v", n2.Body)
	}
	if n2.Description.Valid {
		t.Errorf("empty source description should map to NULL, got %+v", n2.Description)
	}
	if n2.Tags.Valid {
		t.Errorf("empty source tag should map to NULL, got %+v", n2.Tags)
	}
}

func TestImportGob_DuplicateSkipped(t *testing.T) {
	cleanup, userGUID := setupImportTest(t)
	defer cleanup()

	if _, err := models.CreateNote(models.NoteInput{
		GUID:  "leg-dup",
		Title: "Pre-existing",
	}, userGUID); err != nil {
		t.Fatalf("seed CreateNote: %v", err)
	}

	src := []legacyNote{
		{
			Guid:      "leg-dup",
			Title:     "Should be skipped",
			Body:      "new body",
			CreatedAt: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			Guid:      "leg-fresh",
			Title:     "Fresh import",
			CreatedAt: time.Date(2022, 2, 2, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2022, 2, 2, 0, 0, 0, 0, time.UTC),
		},
	}

	summary := importNotes(src, userGUID)
	if summary.imported != 1 || summary.skipped != 1 || summary.errored != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	dup, err := models.GetNoteByGUID("leg-dup")
	if err != nil || dup == nil {
		t.Fatalf("GetNoteByGUID: %v %v", dup, err)
	}
	if dup.Title != "Pre-existing" {
		t.Errorf("existing note must not be overwritten on skip: title=%q", dup.Title)
	}
}

func TestImportGob_AuthoredAtFromDisk(t *testing.T) {
	cleanup, userGUID := setupImportTest(t)
	defer cleanup()

	src := []legacyNote{{
		Guid:      "leg-authored",
		Title:     "Authored time check",
		CreatedAt: time.Date(2018, 5, 5, 5, 5, 5, 0, time.UTC),
		UpdatedAt: time.Date(2019, 9, 9, 9, 9, 9, 0, time.UTC),
	}}

	summary := importNotes(src, userGUID)
	if summary.imported != 1 {
		t.Fatalf("expected 1 imported, got %+v", summary)
	}

	var authoredAt time.Time
	err := models.DB().QueryRow(
		`SELECT authored_at FROM notes WHERE guid = ?`, "leg-authored",
	).Scan(&authoredAt)
	if err != nil {
		t.Fatalf("disk authored_at query: %v", err)
	}
	if !authoredAt.Equal(src[0].UpdatedAt) {
		t.Errorf("authored_at: got %v, want %v (source UpdatedAt)", authoredAt, src[0].UpdatedAt)
	}
}
