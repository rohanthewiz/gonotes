package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gonotes/models"
)

func TestExportImportMd_Roundtrip(t *testing.T) {
	cleanup, userGUID := setupImportTest(t)
	defer cleanup()

	body := "First line\n\nSecond paragraph with [[Wiki Link]]"
	desc := "a note about things"
	tags := "go, notes"
	note, err := models.CreateNote(models.NoteInput{
		GUID:        "md-001",
		Title:       "Hello World",
		Description: &desc,
		Body:        &body,
		Tags:        &tags,
		IsFlagged:   true,
	}, userGUID)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	cat, err := models.CreateCategory(models.CategoryInput{
		Name:          "Work",
		Subcategories: []string{"proj-x", "proj-y"},
	}, userGUID)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if err := models.AddCategoryToNoteWithSubcategories(note.ID, cat.ID, []string{"proj-x"}, userGUID); err != nil {
		t.Fatalf("AddCategoryToNote: %v", err)
	}

	if _, err := models.CreateNote(models.NoteInput{
		GUID:  "md-002",
		Title: "Uncategorized",
	}, userGUID); err != nil {
		t.Fatalf("CreateNote md-002: %v", err)
	}

	outDir := t.TempDir()
	notes, err := models.ListNotes(userGUID, 0, 0)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	summary := exportNotesMd(notes, userGUID, outDir, false)
	if summary.exported != 2 || summary.errored != 0 {
		t.Fatalf("unexpected export summary: %+v", summary)
	}

	// Categorized note lands in its category folder; frontmatter carries identity
	notePath := filepath.Join(outDir, "Work", "Hello World.md")
	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("expected exported file at %s: %v", notePath, err)
	}
	content := string(data)
	for _, want := range []string{"guid: md-001", "Work/proj-x", "flagged: true", "[[Wiki Link]]"} {
		if !strings.Contains(content, want) {
			t.Errorf("exported file missing %q:\n%s", want, content)
		}
	}
	if _, err := os.Stat(filepath.Join(outDir, "Uncategorized.md")); err != nil {
		t.Errorf("uncategorized note should export to vault root: %v", err)
	}

	// Re-import unmodified: everything should be recognized as unchanged
	paths, err := collectMdFiles(outDir)
	if err != nil {
		t.Fatalf("collectMdFiles: %v", err)
	}
	imp := importMdFiles(paths, outDir, userGUID)
	if imp.unchanged != 2 || imp.created != 0 || imp.updated != 0 || imp.errored != 0 {
		t.Fatalf("re-import of unmodified export should be all-unchanged: %+v", imp)
	}

	// Edit a file body (as an Obsidian user would) and import again
	edited := strings.Replace(content, "Second paragraph", "Edited paragraph", 1)
	if err := os.WriteFile(notePath, []byte(edited), 0o644); err != nil {
		t.Fatalf("write edited file: %v", err)
	}
	imp = importMdFiles(paths, outDir, userGUID)
	if imp.updated != 1 || imp.unchanged != 1 || imp.errored != 0 {
		t.Fatalf("expected 1 updated after edit: %+v", imp)
	}

	got, err := models.GetNoteByGUID("md-001")
	if err != nil || got == nil {
		t.Fatalf("GetNoteByGUID after update: %v %v", got, err)
	}
	if !strings.Contains(got.Body.String, "Edited paragraph") {
		t.Errorf("body edit did not import: %q", got.Body.String)
	}
	if got.Title != "Hello World" || !got.IsFlagged {
		t.Errorf("unedited fields must survive the update: title=%q flagged=%v", got.Title, got.IsFlagged)
	}
}

func TestImportMd_PlainMarkdownGetsGuidWriteback(t *testing.T) {
	cleanup, userGUID := setupImportTest(t)
	defer cleanup()

	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, "Recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	notePath := filepath.Join(vault, "Recipes", "Pasta.md")
	if err := os.WriteFile(notePath, []byte("Boil water.\nAdd pasta.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := collectMdFiles(vault)
	if err != nil {
		t.Fatalf("collectMdFiles: %v", err)
	}
	imp := importMdFiles(paths, vault, userGUID)
	if imp.created != 1 || imp.errored != 0 {
		t.Fatalf("expected 1 created: %+v", imp)
	}

	// Title from filename, category from folder
	notes, err := models.ListNotes(userGUID, 0, 0)
	if err != nil || len(notes) != 1 {
		t.Fatalf("ListNotes: %v (%d notes)", err, len(notes))
	}
	if notes[0].Title != "Pasta" {
		t.Errorf("title should come from filename, got %q", notes[0].Title)
	}
	cats, err := models.GetNoteCategories(notes[0].ID, userGUID)
	if err != nil || len(cats) != 1 || cats[0].Name != "Recipes" {
		t.Errorf("folder should become category: %v %v", cats, err)
	}

	// The generated guid must be written back so re-imports are idempotent
	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "guid: "+notes[0].GUID) {
		t.Errorf("guid not written back to file:\n%s", data)
	}
	if !strings.Contains(string(data), "Boil water.") {
		t.Errorf("body lost during writeback:\n%s", data)
	}

	imp = importMdFiles(paths, vault, userGUID)
	if imp.unchanged != 1 || imp.created != 0 {
		t.Fatalf("second import must be idempotent: %+v", imp)
	}
}

func TestExportMd_SkipPrivate(t *testing.T) {
	cleanup, userGUID := setupImportTest(t)
	defer cleanup()

	body := "secret"
	if _, err := models.CreateNote(models.NoteInput{
		GUID: "md-priv", Title: "Secret", Body: &body, IsPrivate: true,
	}, userGUID); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if _, err := models.CreateNote(models.NoteInput{
		GUID: "md-pub", Title: "Public",
	}, userGUID); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	notes, err := models.ListNotes(userGUID, 0, 0)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}

	outDir := t.TempDir()
	summary := exportNotesMd(notes, userGUID, outDir, true)
	if summary.exported != 1 || summary.skipped != 1 {
		t.Fatalf("skip-private summary: %+v", summary)
	}
	if _, err := os.Stat(filepath.Join(outDir, "Secret.md")); !os.IsNotExist(err) {
		t.Error("private note must not be exported with --skip-private")
	}

	// Default: private notes ARE exported, marked private in frontmatter
	outDir2 := t.TempDir()
	summary = exportNotesMd(notes, userGUID, outDir2, false)
	if summary.exported != 2 || summary.private != 1 {
		t.Fatalf("default export summary: %+v", summary)
	}
	data, err := os.ReadFile(filepath.Join(outDir2, "Secret.md"))
	if err != nil {
		t.Fatalf("private note file: %v", err)
	}
	if !strings.Contains(string(data), "private: true") {
		t.Errorf("private flag must roundtrip in frontmatter:\n%s", data)
	}
}
