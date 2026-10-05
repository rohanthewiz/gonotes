package models_test

import (
	"encoding/json"
	"testing"
	"time"

	"gonotes/models"
)

// Relay tests for a note's FILING. A hub relays an edit by recording what it
// now holds, and filing lives in link rows rather than on the note row. That
// separation made three distinct ways for a filing to reach the hub and stop
// there:
//
//   - a create carrying categories relayed only the plain fields;
//   - a categories-only update was never relayed at all;
//   - an update relayed the filing as it was BEFORE the change, because the
//     relay was recorded before the links were written.
//
// Each test applies one incoming change and then reads the relay row a second
// spoke would pull, which is the only place any of the three ever showed.

// relayedFilingFor applies change and returns the categories its relay row
// carries, failing when the relay does not claim the categories bit.
func relayedFilingFor(t *testing.T, change models.SyncChange) []models.NoteCategoryMappingSnapshot {
	t.Helper()
	if err := models.ApplyIncomingSyncChange(change); err != nil {
		t.Fatalf("failed to apply incoming change: %v", err)
	}

	// Read it as a pull would, through the same envelope a peer receives.
	for _, c := range noteChangesFor(t, "second-spoke", change.EntityGUID) {
		if c.GUID != change.GUID {
			continue
		}
		frag, ok := c.Fragment.(*models.NoteFragmentOutput)
		if !ok || frag == nil {
			t.Fatalf("relay %s has no note fragment", c.GUID)
		}
		if frag.Bitmask&models.FragmentCategories == 0 || frag.Categories == nil {
			t.Fatalf("relay bitmask = %#x with categories %v; a peer pulling it learns nothing about the filing",
				frag.Bitmask, frag.Categories)
		}
		var mappings []models.NoteCategoryMappingSnapshot
		if err := json.Unmarshal([]byte(*frag.Categories), &mappings); err != nil {
			t.Fatalf("relay categories %q are not a mapping snapshot: %v", *frag.Categories, err)
		}
		return mappings
	}
	t.Fatalf("no relay recorded for change %s; the hub would keep this change to itself", change.GUID)
	return nil
}

func relayCategory(t *testing.T, name string) *models.Category {
	t.Helper()
	cat, err := models.CreateCategory(models.CategoryInput{Name: name}, relayUserGUID)
	if err != nil {
		t.Fatalf("failed to create category %q: %v", name, err)
	}
	return cat
}

func filingJSON(categoryGUIDs ...string) string {
	mappings := make([]models.NoteCategoryMappingSnapshot, 0, len(categoryGUIDs))
	for _, g := range categoryGUIDs {
		mappings = append(mappings, models.NoteCategoryMappingSnapshot{CategoryGUID: g})
	}
	b, _ := json.Marshal(mappings)
	return string(b)
}

func assertFiledUnder(t *testing.T, got []models.NoteCategoryMappingSnapshot, categoryGUID string) {
	t.Helper()
	if len(got) != 1 || got[0].CategoryGUID != categoryGUID {
		t.Errorf("relayed filing = %+v, want exactly %s", got, categoryGUID)
	}
}

// TestACreateRelaysItsFiling: a note filed at birth must arrive filed.
func TestACreateRelaysItsFiling(t *testing.T) {
	setupRelayTestDB(t)
	cat := relayCategory(t, "Filed at birth")

	change := incomingNote("relay-cat-0001", "relay-cat-note-a", "Born filed", "body", models.OperationCreate)
	frag := change.Fragment.(*models.NoteFragmentOutput)
	filing := filingJSON(cat.GUID)
	frag.Bitmask |= models.FragmentCategories
	frag.Categories = &filing

	assertFiledUnder(t, relayedFilingFor(t, change), cat.GUID)
}

// TestACategoriesOnlyUpdateIsRelayed: refiling a note changes no field on
// the note row, and is still an edit every peer needs.
func TestACategoriesOnlyUpdateIsRelayed(t *testing.T) {
	setupRelayTestDB(t)
	cat := relayCategory(t, "Refiled")
	if _, err := models.CreateNote(models.NoteInput{GUID: "relay-cat-note-b", Title: "Unfiled"}, relayUserGUID); err != nil {
		t.Fatalf("failed to create the local note: %v", err)
	}

	filing := filingJSON(cat.GUID)
	change := models.SyncChange{
		GUID:       "relay-cat-0002",
		EntityType: "note",
		EntityGUID: "relay-cat-note-b",
		Operation:  models.OperationUpdate,
		AuthoredAt: time.Now().UTC(),
		User:       relayUserGUID,
		Fragment: &models.NoteFragmentOutput{
			Bitmask:    models.FragmentCategories,
			Categories: &filing,
		},
	}

	before, err := models.GetNoteByGUID("relay-cat-note-b")
	if err != nil || before == nil {
		t.Fatalf("failed to read the note: %v", err)
	}
	assertFiledUnder(t, relayedFilingFor(t, change), cat.GUID)

	// The note row is not this change's business: a version bump here would
	// tell an open editor its base moved over a field it never edits.
	after, err := models.GetNoteByGUID("relay-cat-note-b")
	if err != nil || after == nil {
		t.Fatalf("failed to re-read the note: %v", err)
	}
	if after.Version != before.Version {
		t.Errorf("version %d -> %d; a categories-only apply must leave the note row alone",
			before.Version, after.Version)
	}
}

// TestAnUpdateRelaysTheNewFilingNotTheOld: the relay must snapshot the links
// after this change wrote them. Before, it carried the previous filing —
// "null" for a first filing, which peers apply as "unfile".
func TestAnUpdateRelaysTheNewFilingNotTheOld(t *testing.T) {
	setupRelayTestDB(t)
	oldCat := relayCategory(t, "Old home")
	newCat := relayCategory(t, "New home")

	// Arrive filed under the old category, then move with a title edit
	// alongside — the shape a spoke sends when both change in one save.
	first := incomingNote("relay-cat-0003", "relay-cat-note-c", "Moving", "body", models.OperationCreate)
	firstFrag := first.Fragment.(*models.NoteFragmentOutput)
	oldFiling := filingJSON(oldCat.GUID)
	firstFrag.Bitmask |= models.FragmentCategories
	firstFrag.Categories = &oldFiling
	if err := models.ApplyIncomingSyncChange(first); err != nil {
		t.Fatalf("failed to apply the create: %v", err)
	}

	title := "Moved"
	newFiling := filingJSON(newCat.GUID)
	move := models.SyncChange{
		GUID:       "relay-cat-0004",
		EntityType: "note",
		EntityGUID: "relay-cat-note-c",
		Operation:  models.OperationUpdate,
		AuthoredAt: time.Now().UTC(),
		User:       relayUserGUID,
		Fragment: &models.NoteFragmentOutput{
			Bitmask:    models.FragmentTitle | models.FragmentCategories,
			Title:      &title,
			Categories: &newFiling,
		},
	}
	assertFiledUnder(t, relayedFilingFor(t, move), newCat.GUID)
}
