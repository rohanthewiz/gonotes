package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gonotes/models"

	"github.com/google/uuid"
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rutil/fileops"
	"github.com/rohanthewiz/serr"
)

type importMdSummary struct {
	created   int
	updated   int
	unchanged int
	errored   int
	total     int
}

// runImportMd imports every .md file under inPath as a note for username.
// The guid frontmatter field anchors identity: matching notes are updated
// (only when content actually differs), unknown guids are created, and
// files with no guid become new notes — the generated guid is written back
// into the file so re-imports are idempotent.
func runImportMd(dir, inPath, username string) error {
	// Resolve the input path before chdir-ing into the working directory,
	// so a relative --in is anchored to where the user ran the command.
	inAbs, err := filepath.Abs(inPath)
	if err != nil {
		return serr.Wrap(err, "failed to resolve input path", "in", inPath)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return serr.Wrap(err, "failed to create directory", "dir", dir)
	}
	if err := os.Chdir(dir); err != nil {
		return serr.Wrap(err, "failed to change to directory", "dir", dir)
	}

	if issues, err := fileops.EnvFromFile("config/cfg_files/.env"); err != nil {
		for _, issue := range issues {
			logger.Warn("Cfg file issue", serr.StringFromErr(issue))
		}
	}

	if err := models.InitDB(); err != nil {
		return serr.Wrap(err, "failed to initialize database")
	}
	defer models.CloseDB()

	user, err := models.GetUserByUsername(username)
	if err != nil {
		return serr.Wrap(err, "user lookup failed", "username", username)
	}
	if user == nil {
		return fmt.Errorf("user %q not found; create the account first", username)
	}

	paths, err := collectMdFiles(inAbs)
	if err != nil {
		return err
	}

	summary := importMdFiles(paths, inAbs, user.GUID)

	fmt.Printf("import-md: %d created, %d updated, %d unchanged, %d errored (of %d files)\n",
		summary.created, summary.updated, summary.unchanged, summary.errored, summary.total)
	return nil
}

// collectMdFiles walks root gathering .md files, skipping hidden
// directories (notably .obsidian vault metadata).
func collectMdFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, serr.Wrap(err, "failed to walk import directory", "root", root)
	}
	return paths, nil
}

func importMdFiles(paths []string, root, userGUID string) importMdSummary {
	s := importMdSummary{total: len(paths)}
	for _, path := range paths {
		status, err := importMdFile(path, root, userGUID)
		if err != nil {
			logger.LogErr(err, "path", path)
			s.errored++
			continue
		}
		switch status {
		case "created":
			s.created++
		case "updated":
			s.updated++
		default:
			s.unchanged++
		}
	}
	return s
}

// importMdFile imports a single file, returning "created", "updated", or
// "unchanged".
func importMdFile(path, root, userGUID string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", serr.Wrap(err, "failed to read file")
	}

	fm, body, err := parseNoteMd(raw)
	if err != nil {
		return "", err
	}

	title := strings.TrimSpace(fm.Title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	// Frontmatter categories are authoritative; for plain files fall back
	// to the first path segment (the folder layout export-md writes).
	catSpecs := []string(fm.Categories)
	if len(catSpecs) == 0 {
		if rel, relErr := filepath.Rel(root, path); relErr == nil {
			if dir := filepath.Dir(rel); dir != "." {
				catSpecs = []string{strings.Split(dir, string(filepath.Separator))[0]}
			}
		}
	}

	tags := normalizeTags(strings.Join(fm.Tags, ","))
	input := models.NoteInput{
		GUID:        fm.GUID,
		Title:       title,
		Description: nilIfEmpty(strings.TrimSpace(fm.Description)),
		Body:        nilIfEmpty(body),
		Tags:        nilIfEmpty(tags),
		IsPrivate:   fm.Private,
		IsFlagged:   fm.Flagged,
		UpdatedBy:   &userGUID,
	}

	if fm.GUID != "" {
		existing, err := models.GetNoteByGUID(fm.GUID)
		if err != nil {
			return "", serr.Wrap(err, "note lookup failed", "guid", fm.GUID)
		}
		if existing != nil {
			if !existing.CreatedBy.Valid || existing.CreatedBy.String != userGUID {
				return "", serr.New("note with this guid belongs to another user", "guid", fm.GUID)
			}
			if err := ensureNoteCategories(existing.ID, catSpecs, userGUID); err != nil {
				return "", err
			}
			if noteMatchesInput(existing, input) {
				return "unchanged", nil
			}
			if _, err := models.UpdateNote(existing.ID, input, userGUID); err != nil {
				return "", serr.Wrap(err, "failed to update note", "guid", fm.GUID)
			}
			return "updated", nil
		}
	}

	// New note. Files without a guid get one generated here and written
	// back into the file below, so importing the same folder twice does
	// not duplicate notes.
	writeBack := false
	if input.GUID == "" {
		input.GUID = uuid.New().String()
		fm.GUID = input.GUID
		writeBack = true
	}

	// Preserve source timestamps when present. CreateNoteWithTimestamps
	// skips body encryption, so private notes always take the CreateNote
	// path (fresh timestamps) to stay encrypted at rest.
	created, updated := parseMdTime(fm.Created), parseMdTime(fm.Updated)
	var note *models.Note
	if !created.IsZero() && !updated.IsZero() && !input.IsPrivate {
		note, err = models.CreateNoteWithTimestamps(input, userGUID, created, updated, updated)
	} else {
		note, err = models.CreateNote(input, userGUID)
	}
	if err != nil {
		return "", serr.Wrap(err, "failed to create note", "guid", input.GUID)
	}

	if err := ensureNoteCategories(note.ID, catSpecs, userGUID); err != nil {
		return "", err
	}

	if writeBack {
		fm.Title = title
		data, err := renderNoteMd(fm, body)
		if err == nil {
			err = os.WriteFile(path, data, 0o644)
		}
		if err != nil {
			// Note is imported; only the idempotency marker failed to persist
			logger.LogErr(serr.Wrap(err, "failed to write guid back to file"), "path", path)
		}
	}

	return "created", nil
}

// ensureNoteCategories links the note to each category named in specs,
// creating categories that don't exist yet. Existing links are left
// untouched (categories removed from a file are not unlinked).
func ensureNoteCategories(noteID int64, specs []string, userGUID string) error {
	names, subsByName := parseCategorySpecs(specs)
	if len(names) == 0 {
		return nil
	}

	existing, err := models.GetNoteCategories(noteID, userGUID)
	if err != nil {
		return serr.Wrap(err, "failed to get note categories")
	}
	linked := map[string]bool{}
	for _, cat := range existing {
		linked[cat.Name] = true
	}

	for _, name := range names {
		if linked[name] {
			continue
		}
		cat, err := models.GetCategoryByName(name, userGUID)
		if err != nil {
			return serr.Wrap(err, "category lookup failed", "category", name)
		}
		if cat == nil {
			cat, err = models.CreateCategory(models.CategoryInput{
				Name:          name,
				Subcategories: subsByName[name],
			}, userGUID)
			if err != nil {
				return serr.Wrap(err, "failed to create category", "category", name)
			}
		}
		if err := models.AddCategoryToNoteWithSubcategories(noteID, cat.ID, subsByName[name], userGUID); err != nil {
			return serr.Wrap(err, "failed to link category", "category", name)
		}
	}
	return nil
}

// noteMatchesInput reports whether the stored note already reflects the
// file's content, so unchanged files can be skipped. Bodies are compared
// with trailing newlines trimmed and tags in normalized form.
func noteMatchesInput(note *models.Note, input models.NoteInput) bool {
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return note.Title == input.Title &&
		note.Description.String == deref(input.Description) &&
		strings.TrimRight(note.Body.String, "\n") == strings.TrimRight(deref(input.Body), "\n") &&
		normalizeTags(note.Tags.String) == normalizeTags(deref(input.Tags)) &&
		note.IsPrivate == input.IsPrivate &&
		note.IsFlagged == input.IsFlagged
}
