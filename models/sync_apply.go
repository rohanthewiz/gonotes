package models

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// sync_apply.go applies changes received from a peer. Notes are routed to
// the database matching their privacy (a synced privacy flip moves the note
// between databases, mirroring UpdateNote); categories, being a public-only
// table, always land in the public database. There is no cache and no
// per-note encryption — the private database is encrypted as a whole.

// originChangeGUID is the identity of the change being applied, and every
// ApplySync* function takes one.
//
// It is what the local change row records as its OWN guid, rather than a fresh
// one, and that single decision is what makes a hub-and-two-spokes topology
// converge:
//
//	spoke A ──push(X)──► hub records X ──pull──► spoke A sees X, already has it
//	                          │                  (changeGUIDExists → skip)
//	                          └────pull────────► spoke B applies X, records X
//	                                             ──push(X)──► hub already has X
//
// With a fresh guid at each hop, none of those "already has it" checks fire:
// the hub hands every spoke back its own change under a new name, each side
// applies it and records another change, and the log grows without end. The
// idempotency check in ApplyIncomingSyncChange has always been written for
// this; it just never had a stable identity to check against.
//
// An empty originChangeGUID means "no incoming change to inherit from" and
// generates one, which is what a caller outside the sync path gets.

// ApplySyncNoteCreate inserts a note from sync data with an explicit
// authored_at (preserving the source authoring time). Records an
// OperationSync change so peers don't re-propagate it.
func ApplySyncNoteCreate(noteGUID, title string, fragment NoteFragment, authoredAt time.Time, userGUID, originChangeGUID string) (*Note, error) {
	description := fragment.Description
	body := fragment.Body
	tags := fragment.Tags
	isPrivate := false
	if fragment.IsPrivate.Valid {
		isPrivate = fragment.IsPrivate.Bool
	}

	// A create should never carry a diff — there is no base to apply it to.
	if fragment.BodyIsDiff {
		return nil, serr.New("cannot apply body diff for note creation — need full body snapshot")
	}

	en := noteEngine(isPrivate)
	createdBy := sql.NullString{String: userGUID, Valid: userGUID != ""}

	// version starts at 1, stated rather than defaulted. Every INSERT into
	// notes names it explicitly for the same reason: on a database that
	// predates the column, the value comes from the ALTER's default, and a
	// note that arrived by sync with a NULL version would sit outside the
	// optimistic-concurrency guard entirely (0 means "unchecked").
	query := `
		INSERT INTO notes (id, guid, title, description, body, tags, is_private, created_by, updated_by,
		                   authored_at, synced_at, version)
		VALUES (nextval('notes_id_seq'), ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, 1)
		RETURNING ` + noteCols

	note := &Note{}
	err := scanNoteRow(en.QueryRow(query,
		noteGUID, title, description, body, tags, isPrivate, createdBy, createdBy, authoredAt,
	), note)
	if err != nil {
		return nil, serr.Wrap(err, "failed to insert synced note")
	}

	// Record OperationSync so it won't be pushed back to the originator.
	syncFragment := createFragmentFromInput(NoteInput{
		Title:       title,
		Description: nullStringToPtr(description),
		Body:        nullStringToPtr(body),
		Tags:        nullStringToPtr(tags),
		IsPrivate:   isPrivate,
	}, FragmentTitle|FragmentDescription|FragmentBody|FragmentTags|FragmentIsPrivate)
	if fragmentID, err := insertNoteFragment(en, syncFragment); err != nil {
		logger.LogErr(err, "failed to record sync note create fragment", "note_guid", noteGUID)
	} else {
		if err := insertNoteChange(en, relayChangeGUID(originChangeGUID), noteGUID, OperationSync,
			sql.NullInt64{Int64: fragmentID, Valid: true}, userGUID); err != nil {
			logger.LogErr(err, "failed to record sync note create change", "note_guid", noteGUID)
		}
	}

	return note, nil
}

// ApplySyncNoteUpdate updates a note from sync data, preserving the source
// authored_at. It resolves the incoming fragment (applying a body diff when
// present) against the current note, and — if the privacy bit flips — moves
// the note between the two databases.
func ApplySyncNoteUpdate(noteGUID string, fragment NoteFragment, authoredAt time.Time, originChangeGUID string) error {
	existing, err := GetNoteByGUID(noteGUID)
	if err != nil {
		return serr.Wrap(err, "failed to get existing note for sync update")
	}
	if existing == nil {
		return serr.New("note not found for sync update: " + noteGUID)
	}

	// If no mutable field bits are set, there is nothing to update.
	const mutableBits = FragmentTitle | FragmentDescription | FragmentBody | FragmentTags | FragmentIsPrivate
	if fragment.Bitmask&mutableBits == 0 {
		return nil
	}

	// Resolve the target field values from existing + fragment.
	resolved := *existing
	if fragment.Bitmask&FragmentTitle != 0 && fragment.Title.Valid {
		resolved.Title = fragment.Title.String
	}
	if fragment.Bitmask&FragmentDescription != 0 {
		resolved.Description = fragment.Description
	}
	if fragment.Bitmask&FragmentBody != 0 {
		if fragment.BodyIsDiff && fragment.Body.Valid {
			currentBody := ""
			if existing.Body.Valid {
				currentBody = existing.Body.String
			}
			newBody, derr := applyBodyDiff(currentBody, fragment.Body.String)
			if derr != nil {
				return serr.Wrap(derr, "failed to apply body diff during sync update")
			}
			resolved.Body = sql.NullString{String: newBody, Valid: true}
		} else {
			resolved.Body = fragment.Body
		}
	}
	if fragment.Bitmask&FragmentTags != 0 {
		resolved.Tags = fragment.Tags
	}
	if fragment.Bitmask&FragmentIsPrivate != 0 && fragment.IsPrivate.Valid {
		resolved.IsPrivate = fragment.IsPrivate.Bool
	}

	src := noteEngine(existing.IsPrivate)
	dst := noteEngine(resolved.IsPrivate)
	now := time.Now().UTC()

	// Sync bumps the version but never GUARDS on it. That asymmetry is the
	// point: sync has already resolved this conflict by its own rules (see
	// sync_conflict.go) and its job is to land the result, not to ask again.
	// Bumping still matters — a local form that had the pre-sync note open must
	// be told its base moved, which is exactly what the counter is for.
	if src == dst {
		_, err = src.Exec(`
			UPDATE notes SET title = ?, description = ?, body = ?, tags = ?, is_private = ?,
			    authored_at = ?, synced_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP,
			    version = version + 1
			WHERE guid = ? AND deleted_at IS NULL`,
			resolved.Title, resolved.Description, resolved.Body, resolved.Tags, resolved.IsPrivate,
			authoredAt, noteGUID,
		)
		if err != nil {
			return serr.Wrap(err, "failed to update note from sync")
		}
	} else {
		// Privacy flip: move the note (and its links) to the other database,
		// preserving id/guid/created_at.
		insertQuery := `
			INSERT INTO notes (id, guid, title, description, body, tags, is_private, is_flagged,
			                   created_by, updated_by, created_at, updated_at, authored_at, synced_at, deleted_at, version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?, ?)`
		if _, err = dst.Exec(insertQuery,
			existing.ID, existing.GUID, resolved.Title, resolved.Description, resolved.Body, resolved.Tags,
			resolved.IsPrivate, existing.IsFlagged, existing.CreatedBy, existing.UpdatedBy,
			existing.CreatedAt, now, authoredAt, existing.DeletedAt, existing.Version+1,
		); err != nil {
			return serr.Wrap(err, "failed to insert moved note during sync privacy flip")
		}
		if err := moveNoteCategories(src, dst, existing.ID); err != nil {
			logger.LogErr(err, "failed to move note category links during sync privacy flip", "note_id", existing.ID)
		}
		if _, err = src.Exec(`DELETE FROM notes WHERE id = ?`, existing.ID); err != nil {
			return serr.Wrap(err, "failed to delete note from source database during sync privacy flip")
		}
	}

	// Record OperationSync in the destination engine — from the RESOLVED
	// state, not from the incoming fragment.
	//
	// The difference only shows when the incoming fragment carried a body
	// diff: that diff is expressed against the SENDER's previous body, so
	// relaying it verbatim to a third machine asks it to patch a base it may
	// never have had. A snapshot of what this note now says cannot fail that
	// way. (Same reasoning as the compactor — see sync_compact.go.)
	relay := noteRelayFragment(&resolved, fragment.Bitmask)
	if fragmentID, err := insertNoteFragment(dst, relay); err != nil {
		logger.LogErr(err, "failed to record sync update fragment", "note_guid", noteGUID)
	} else {
		if err := insertNoteChange(dst, relayChangeGUID(originChangeGUID), noteGUID, OperationSync,
			sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
			logger.LogErr(err, "failed to record sync update change", "note_guid", noteGUID)
		}
	}

	return nil
}

// ApplySyncNoteDelete soft-deletes a note received via sync (idempotent).
func ApplySyncNoteDelete(noteGUID, originChangeGUID string) error {
	existing, err := GetNoteByGUID(noteGUID)
	if err != nil {
		return serr.Wrap(err, "failed to resolve note for sync delete")
	}
	if existing == nil {
		return nil // Already deleted or unknown — idempotent.
	}
	en := noteEngine(existing.IsPrivate)

	result, err := en.Exec(
		`UPDATE notes SET deleted_at = CURRENT_TIMESTAMP, synced_at = CURRENT_TIMESTAMP, version = version + 1 WHERE guid = ? AND deleted_at IS NULL`,
		noteGUID,
	)
	if err != nil {
		return serr.Wrap(err, "failed to soft-delete synced note")
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil
	}

	if err := insertNoteChange(en, relayChangeGUID(originChangeGUID), noteGUID, OperationDelete,
		sql.NullInt64{}, ""); err != nil {
		logger.LogErr(err, "failed to record sync delete change", "note_guid", noteGUID)
	}
	return nil
}

// ApplySyncCategoryCreate creates a category from sync data (public database).
func ApplySyncCategoryCreate(categoryGUID, name string, fragment CategoryFragment, userGUID, originChangeGUID string) (*Category, error) {
	description := fragment.Description
	subcategories := fragment.Subcategories

	createdBy := sql.NullString{}
	if userGUID != "" {
		createdBy = sql.NullString{String: userGUID, Valid: true}
	}

	query := `INSERT INTO categories (id, guid, name, description, subcategories, created_by)
		VALUES (nextval('categories_id_seq'), ?, ?, ?, ?, ?)
		RETURNING ` + categoryCols

	var category Category
	err := scanCategory(pubDB.QueryRow(query, categoryGUID, name, description, subcategories, createdBy), &category)
	if err != nil {
		return nil, serr.Wrap(err, "failed to insert synced category")
	}

	if fragmentID, err := insertCategoryFragment(fragment); err != nil {
		logger.LogErr(err, "failed to record sync category create fragment", "category_guid", categoryGUID)
	} else {
		if err := insertCategoryChange(relayChangeGUID(originChangeGUID), categoryGUID, OperationSync,
			sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
			logger.LogErr(err, "failed to record sync category create change", "category_guid", categoryGUID)
		}
	}

	return &category, nil
}

// ApplySyncCategoryUpdate updates a category from sync data (public database).
func ApplySyncCategoryUpdate(categoryGUID string, fragment CategoryFragment, originChangeGUID string) error {
	setClauses := []string{}
	args := []any{}

	if fragment.Bitmask&CatFragmentName != 0 && fragment.Name.Valid {
		setClauses = append(setClauses, "name = ?")
		args = append(args, fragment.Name.String)
	}
	if fragment.Bitmask&CatFragmentDescription != 0 {
		setClauses = append(setClauses, "description = ?")
		args = append(args, fragment.Description)
	}
	if fragment.Bitmask&CatFragmentSubcategories != 0 {
		setClauses = append(setClauses, "subcategories = ?")
		args = append(args, fragment.Subcategories)
	}
	if len(setClauses) == 0 {
		return nil
	}
	setClauses = append(setClauses, "updated_at = CURRENT_TIMESTAMP")

	query := "UPDATE categories SET " + joinStrings(setClauses, ", ") + " WHERE guid = ?"
	args = append(args, categoryGUID)

	if _, err := pubDB.Exec(query, args...); err != nil {
		return serr.Wrap(err, "failed to update category from sync")
	}

	if fragmentID, err := insertCategoryFragment(fragment); err != nil {
		logger.LogErr(err, "failed to record sync category update fragment", "category_guid", categoryGUID)
	} else {
		if err := insertCategoryChange(relayChangeGUID(originChangeGUID), categoryGUID, OperationSync,
			sql.NullInt64{Int64: fragmentID, Valid: true}, ""); err != nil {
			logger.LogErr(err, "failed to record sync category update change", "category_guid", categoryGUID)
		}
	}
	return nil
}

// ApplySyncCategoryDelete deletes a category from sync (public database).
func ApplySyncCategoryDelete(categoryGUID, originChangeGUID string) error {
	if _, err := pubDB.Exec(`DELETE FROM categories WHERE guid = ?`, categoryGUID); err != nil {
		return serr.Wrap(err, "failed to delete synced category")
	}

	if err := insertCategoryChange(relayChangeGUID(originChangeGUID), categoryGUID, OperationDelete,
		sql.NullInt64{}, ""); err != nil {
		logger.LogErr(err, "failed to record sync category delete change", "category_guid", categoryGUID)
	}
	return nil
}

// ApplySyncNoteCategoryMapping replaces a note's entire category set from a
// sync snapshot (category GUIDs resolved to local ids). The link rows live
// in the note's database.
func ApplySyncNoteCategoryMapping(noteGUID string, mappingsJSON string) error {
	note, err := GetNoteByGUID(noteGUID)
	if err != nil {
		return serr.Wrap(err, "failed to resolve note GUID for category mapping sync")
	}
	if note == nil {
		return serr.New("note not found for category mapping sync: " + noteGUID)
	}
	en := noteEngine(note.IsPrivate)

	var mappings []NoteCategoryMappingSnapshot
	if err := json.Unmarshal([]byte(mappingsJSON), &mappings); err != nil {
		return serr.Wrap(err, "failed to parse category mapping snapshot")
	}

	// Replace all existing links for this note in its database.
	if _, err = en.Exec(`DELETE FROM note_categories WHERE note_id = ?`, note.ID); err != nil {
		return serr.Wrap(err, "failed to clear existing note-category mappings")
	}

	insertQuery := `INSERT INTO note_categories (note_id, category_id, subcategories, created_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)`

	for _, mapping := range mappings {
		cat, err := GetCategoryByGUID(mapping.CategoryGUID)
		if err != nil || cat == nil {
			// Category not present locally yet — a later sync pass resolves it.
			logger.LogErr(serr.New("category not found locally during mapping sync"),
				"skipping mapping", "category_guid", mapping.CategoryGUID)
			continue
		}

		var subcatsJSON sql.NullString
		if len(mapping.SelectedSubcategories) > 0 {
			jsonBytes, err := json.Marshal(mapping.SelectedSubcategories)
			if err == nil {
				subcatsJSON = sql.NullString{String: string(jsonBytes), Valid: true}
			}
		}

		if _, err := en.Exec(insertQuery, note.ID, cat.ID, subcatsJSON); err != nil {
			logger.LogErr(err, "failed to insert synced note-category mapping",
				"note_id", note.ID, "category_guid", mapping.CategoryGUID)
		}
	}

	return nil
}

// --- Helper functions ---

// nullStringToPtr converts a sql.NullString to a *string.
func nullStringToPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

// joinStrings joins a slice of strings with a separator.
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}

// getNoteByGUIDFromDisk returns the canonical note by GUID from whichever
// database holds it. Retained under its historical name for sync callers;
// with whole-database encryption there is no decrypt step, so it is simply
// a fan-out read.
func getNoteByGUIDFromDisk(guid string) (*Note, error) {
	return GetNoteByGUID(guid)
}

// relayChangeGUID returns the identity a locally recorded change should carry:
// the incoming change's, so the same edit keeps one name across every machine
// it reaches, or a fresh one when there is no incoming change to inherit from.
func relayChangeGUID(originChangeGUID string) string {
	if originChangeGUID != "" {
		return originChangeGUID
	}
	return GenerateChangeGUID()
}

// noteRelayFragment snapshots the fields a bitmask names from a note as it now
// stands, for the change row an apply records on the way through. Body is
// literal text with BodyIsDiff false — see the note at its call site.
func noteRelayFragment(note *Note, bitmask int16) NoteFragment {
	fragment := NoteFragment{Bitmask: bitmask}
	if bitmask&FragmentTitle != 0 {
		fragment.Title = sql.NullString{String: note.Title, Valid: true}
	}
	if bitmask&FragmentDescription != 0 {
		fragment.Description = note.Description
	}
	if bitmask&FragmentBody != 0 {
		fragment.Body = note.Body
		fragment.BodyIsDiff = false
	}
	if bitmask&FragmentTags != 0 {
		fragment.Tags = note.Tags
	}
	if bitmask&FragmentIsPrivate != 0 {
		fragment.IsPrivate = sql.NullBool{Bool: note.IsPrivate, Valid: true}
	}
	if bitmask&FragmentCategories != 0 {
		fragment.Categories = note.categoriesSnapshotForRelay()
	}
	return fragment
}

// categoriesSnapshotForRelay re-reads the note's category links so a relayed
// fragment that claims the categories bit carries the current filing rather
// than the sender's view of it. A failure leaves the field absent, which the
// receiver reads as "this change says nothing about categories" — the safe
// reading, since the alternative is unfiling the note.
func (n *Note) categoriesSnapshotForRelay() sql.NullString {
	mappingsJSON, err := noteCategoryMappingsJSON(n.ID)
	if err != nil {
		logger.LogErr(err, "failed to snapshot categories for relayed change", "note_guid", n.GUID)
		return sql.NullString{}
	}
	return sql.NullString{String: mappingsJSON, Valid: true}
}
