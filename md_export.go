package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gonotes/models"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rutil/fileops"
	"github.com/rohanthewiz/serr"
)

type exportMdSummary struct {
	exported int
	private  int // private notes included in the export (decrypted)
	skipped  int // private notes excluded via --skip-private
	errored  int
}

// runExportMd writes every non-deleted note owned by username into outPath
// as Markdown files with YAML frontmatter, one file per note, grouped into
// folders by primary category. Private notes are exported DECRYPTED unless
// skipPrivate is set — exporting is the deliberate act that takes them out
// of encrypted storage.
func runExportMd(dir, outPath, username string, skipPrivate bool) error {
	// Resolve the output path before chdir-ing into the working directory,
	// so a relative --out is anchored to where the user ran the command.
	outAbs, err := filepath.Abs(outPath)
	if err != nil {
		return serr.Wrap(err, "failed to resolve output path", "out", outPath)
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
		return fmt.Errorf("user %q not found", username)
	}

	notes, err := models.ListNotes(user.GUID, 0, 0)
	if err != nil {
		return serr.Wrap(err, "failed to list notes")
	}

	if err := os.MkdirAll(outAbs, 0o755); err != nil {
		return serr.Wrap(err, "failed to create output directory", "out", outAbs)
	}

	summary := exportNotesMd(notes, user.GUID, outAbs, skipPrivate)

	privateInfo := fmt.Sprintf("%d private included", summary.private)
	if skipPrivate {
		privateInfo = fmt.Sprintf("%d private excluded", summary.skipped)
	}
	fmt.Printf("export-md: %d exported (%s), %d errored → %s\n",
		summary.exported, privateInfo, summary.errored, outAbs)
	return nil
}

// exportNotesMd writes the given notes under outDir. Bodies come from the
// cache (via ListNotes), so private notes are already decrypted.
func exportNotesMd(notes []models.Note, userGUID, outDir string, skipPrivate bool) exportMdSummary {
	var s exportMdSummary
	// Track written paths case-insensitively so title collisions (or
	// case-only differences on APFS/NTFS) don't silently overwrite.
	usedPaths := map[string]bool{}

	for _, note := range notes {
		if note.IsPrivate && skipPrivate {
			s.skipped++
			continue
		}

		catDetails, err := models.GetNoteCategoryDetails(note.ID, userGUID)
		if err != nil {
			logger.LogErr(serr.Wrap(err, "failed to get note categories"), "guid", note.GUID)
			s.errored++
			continue
		}

		var catSpecs []string
		for _, cd := range catDetails {
			if len(cd.SelectedSubcategories) == 0 {
				catSpecs = append(catSpecs, cd.Name)
			} else {
				for _, sub := range cd.SelectedSubcategories {
					catSpecs = append(catSpecs, cd.Name+"/"+sub)
				}
			}
		}

		fm := mdFrontmatter{
			GUID:        note.GUID,
			Title:       note.Title,
			Description: note.Description.String,
			Tags:        splitAndTrim(note.Tags.String, ","),
			Categories:  catSpecs,
			Private:     note.IsPrivate,
			Flagged:     note.IsFlagged,
			Created:     note.CreatedAt.UTC().Format(time.RFC3339),
			Updated:     note.UpdatedAt.UTC().Format(time.RFC3339),
		}

		// File goes in a folder named after the first category (they are
		// ordered by name); the frontmatter remains authoritative on import.
		folder := ""
		if len(catDetails) > 0 {
			folder = sanitizeFileName(catDetails[0].Name)
		}
		relPath := filepath.Join(folder, sanitizeFileName(note.Title)+".md")
		if usedPaths[strings.ToLower(relPath)] {
			suffix := note.GUID
			if len(suffix) > 8 {
				suffix = suffix[:8]
			}
			relPath = strings.TrimSuffix(relPath, ".md") + "-" + suffix + ".md"
		}
		usedPaths[strings.ToLower(relPath)] = true

		data, err := renderNoteMd(fm, note.Body.String)
		if err != nil {
			logger.LogErr(err, "guid", note.GUID)
			s.errored++
			continue
		}

		fullPath := filepath.Join(outDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			logger.LogErr(serr.Wrap(err, "failed to create note folder"), "path", fullPath)
			s.errored++
			continue
		}
		if err := os.WriteFile(fullPath, data, 0o644); err != nil {
			logger.LogErr(serr.Wrap(err, "failed to write note file"), "path", fullPath)
			s.errored++
			continue
		}

		s.exported++
		if note.IsPrivate {
			s.private++
		}
	}
	return s
}
