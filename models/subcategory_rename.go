package models

import (
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/rohanthewiz/serr"
)

// RenameSubcategory renames one subcategory of a category everywhere it
// appears: in the category's definition and in the selection of every note
// filed under it. It returns the updated category and how many notes' links
// were rewritten.
//
// A subcategory has no row of its own. It is a string in two places: the
// category's `subcategories` JSON array (the palette the UIs offer) and each
// link's `subcategories` JSON array in note_categories (what that note is
// filed under). Renaming only the definition would orphan every note filed
// under the old name, so both are rewritten.
//
// Renaming onto a name the category already defines MERGES the two: the old
// name leaves the definition, and every note filed under it is filed under
// the existing one instead (once, even if it already had both).
//
// ORDER, since there is no transaction across the two databases:
//
//  1. the links (each note in its own database), then
//  2. the definition.
//
// A failure in between leaves notes filed under a name the definition doesn't
// list yet. The note form merges unknown selections back into the definition
// on its next save, so that state is visible and self-healing. The opposite
// order would leave notes filed under a name no UI offers any more. The
// operation is also safe to re-run: links already rewritten have nothing to
// match, and the definition step is a plain replacement.
//
// Each rewritten link records one note mapping change, and the definition
// update records one category change, so both reach sync peers as ordinary
// edits.
//
// The new name may not contain "/" or ",": those separate subcategories and
// categories in the single-line notation both UIs accept ("Work/backend,
// Personal"), so such a name could be stored but never typed back.
func RenameSubcategory(categoryID int64, from, to, userGUID string) (*Category, int, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	switch {
	case from == "" || to == "":
		return nil, 0, serr.New("subcategory names cannot be empty")
	case from == to:
		return nil, 0, serr.New("the new name is the same as the old one")
	case strings.ContainsAny(to, "/,"):
		return nil, 0, serr.New(`a subcategory name cannot contain "/" or ","`)
	}

	cat, err := GetCategory(categoryID, userGUID)
	if err != nil {
		return nil, 0, err
	}
	defined := cat.SubcategoryList()
	if !slices.Contains(defined, from) {
		return nil, 0, serr.New("subcategory not found")
	}

	notesChanged := 0
	for _, en := range []*dbEngine{pubDB, privDB} {
		n, err := renameSubcategoryInLinks(en, categoryID, from, to)
		notesChanged += n
		if err != nil {
			return nil, notesChanged, err
		}
	}

	input := CategoryInput{Name: cat.Name, Subcategories: renameInList(defined, from, to)}
	if cat.Description.Valid {
		desc := cat.Description.String
		input.Description = &desc
	}
	updated, err := UpdateCategory(categoryID, input, userGUID)
	if err != nil {
		return nil, notesChanged, serr.Wrap(err, "notes were renamed but the category definition was not")
	}
	return updated, notesChanged, nil
}

// renameSubcategoryInLinks rewrites one database's links of a category whose
// selection names `from`, returning how many notes changed.
//
// All the category's links are read and filtered in Go rather than matched
// with a LIKE on the JSON text: a substring match on "api" would also hit
// "rapid", and the Go side has to parse the array to rewrite it anyway. The
// read finishes before the first write, so no cursor is open while writing.
func renameSubcategoryInLinks(en *dbEngine, categoryID int64, from, to string) (int, error) {
	type link struct {
		noteID int64
		subs   []string
	}

	rows, err := en.Query(`SELECT note_id, subcategories FROM note_categories
		WHERE category_id = ? AND subcategories IS NOT NULL`, categoryID)
	if err != nil {
		return 0, serr.Wrap(err, "failed to read note category links")
	}
	var hits []link
	for rows.Next() {
		var noteID int64
		var raw sql.NullString
		if err := rows.Scan(&noteID, &raw); err != nil {
			rows.Close()
			return 0, serr.Wrap(err, "failed to scan note category link")
		}
		var subs []string
		if raw.Valid && raw.String != "" {
			if err := json.Unmarshal([]byte(raw.String), &subs); err != nil {
				// A malformed selection is left alone rather than failing the
				// whole rename; it did not name `from` in any readable way.
				continue
			}
		}
		if slices.Contains(subs, from) {
			hits = append(hits, link{noteID: noteID, subs: subs})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, serr.Wrap(err, "failed to read note category links")
	}

	changed := 0
	for _, h := range hits {
		col, err := subcategoriesColumn(renameInList(h.subs, from, to))
		if err != nil {
			return changed, err
		}
		if _, err := en.Exec(`UPDATE note_categories SET subcategories = ?
			WHERE note_id = ? AND category_id = ?`, col, h.noteID, categoryID); err != nil {
			return changed, serr.Wrap(err, "failed to rewrite note category link", "note_id", strconv.FormatInt(h.noteID, 10))
		}
		recordNoteCategoryMappingChange(h.noteID)
		changed++
	}
	return changed, nil
}

// renameInList replaces `from` with `to` in place, keeping the list's order.
// If `to` is already present, `from` is dropped instead, so the result never
// holds the same name twice.
func renameInList(list []string, from, to string) []string {
	hasTo := slices.Contains(list, to)
	out := make([]string, 0, len(list))
	for _, s := range list {
		switch {
		case s == from && hasTo:
			continue
		case s == from:
			out = append(out, to)
		default:
			out = append(out, s)
		}
	}
	return out
}
