package main

import (
	"encoding/gob"
	"fmt"
	"os"
	"time"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rutil/fileops"
	"github.com/rohanthewiz/serr"
)

// legacyNote mirrors the Note struct from the legacy go_notes project for
// gob decoding. gob matches struct fields by exported name, so the receiving
// struct does not need to share a package or import path with the encoder.
// Fields kept verbatim from the source even when discarded so that an
// extra field added on the encoder side does not cause a decode error.
type legacyNote struct {
	Id          uint64
	Guid        string
	Title       string
	Description string
	Body        string
	Tag         string
	User        string
	Creator     string
	SharedBy    string
	Public      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type importSummary struct {
	imported int
	skipped  int
	errored  int
}

func runImportGob(dir, gobPath, username string) error {
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

	f, err := os.Open(gobPath)
	if err != nil {
		return serr.Wrap(err, "failed to open gob file", "path", gobPath)
	}
	defer f.Close()

	var legacy []legacyNote
	if err := gob.NewDecoder(f).Decode(&legacy); err != nil {
		return serr.Wrap(err, "failed to decode gob", "path", gobPath)
	}

	summary := importNotes(legacy, user.GUID)

	fmt.Printf("import-gob: %d imported, %d skipped (duplicate GUID), %d errored (of %d total)\n",
		summary.imported, summary.skipped, summary.errored, len(legacy))
	return nil
}

func importNotes(legacy []legacyNote, userGUID string) importSummary {
	var s importSummary
	for _, ln := range legacy {
		existing, err := models.GetNoteByGUID(ln.Guid)
		if err != nil {
			logger.LogErr(serr.Wrap(err, "duplicate-check lookup failed"), "guid", ln.Guid)
			s.errored++
			continue
		}
		if existing != nil {
			s.skipped++
			continue
		}

		input := mapLegacyToInput(ln)
		if _, err := models.CreateNoteWithTimestamps(
			input, userGUID,
			ln.CreatedAt, ln.UpdatedAt, ln.UpdatedAt,
		); err != nil {
			logger.LogErr(serr.Wrap(err, "import insert failed"),
				"guid", ln.Guid, "title", ln.Title)
			s.errored++
			continue
		}
		s.imported++
	}
	return s
}

func mapLegacyToInput(ln legacyNote) models.NoteInput {
	return models.NoteInput{
		GUID:        ln.Guid,
		Title:       ln.Title,
		Description: nilIfEmpty(ln.Description),
		Body:        nilIfEmpty(ln.Body),
		Tags:        nilIfEmpty(ln.Tag),
		IsPrivate:   false,
		IsFlagged:   false,
	}
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
