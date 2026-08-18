package models_test

import (
	"testing"
	"time"

	"gonotes/models"
)

// Relay tests: what happens to a change AFTER it has been applied once.
//
// A hub does not store a spoke's push as a create — it stores what it did,
// which is an OperationSync row. That row is the hub's only account of the
// edit, so it is also what every other spoke pulls. Two things therefore have
// to be true, and neither was before:
//
//   - a relayed change must be applicable, or a change reaches the hub and
//     stops there;
//   - a relayed change must be recognizable as one this machine has already
//     seen, or the hub hands every spoke back its own change under a new name
//     and the log grows forever.
//
// The second is what the origin-change-GUID threading buys, and it is the one
// worth pinning hardest: an infinite ping-pong is silent, and it is only
// visible as a database that will not stop growing.

const relayUserGUID = "relay-test-user-guid"

func setupRelayTestDB(t *testing.T) {
	t.Helper()
	if err := models.InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}
	t.Cleanup(func() { models.CloseDB() })
}

// incomingNote builds the envelope a peer would send for a whole note.
func incomingNote(changeGUID, noteGUID, title, body string, op int32) models.SyncChange {
	return models.SyncChange{
		GUID:       changeGUID,
		EntityType: "note",
		EntityGUID: noteGUID,
		Operation:  op,
		AuthoredAt: time.Now().UTC(),
		User:       relayUserGUID,
		Fragment: &models.NoteFragmentOutput{
			Bitmask: models.FragmentTitle | models.FragmentBody,
			Title:   &title,
			Body:    &body,
		},
	}
}

// TestAnAppliedChangeKeepsItsIdentity is the property the whole mechanism
// rests on: after applying change X, this machine's own log says X. Without
// it, every "have I seen this?" check downstream compares against a name only
// this machine has ever used.
func TestAnAppliedChangeKeepsItsIdentity(t *testing.T) {
	setupRelayTestDB(t)

	const changeGUID = "relay-change-0001"
	if err := models.ApplyIncomingSyncChange(
		incomingNote(changeGUID, "relay-note-a", "From a peer", "text", models.OperationCreate),
	); err != nil {
		t.Fatalf("failed to apply incoming change: %v", err)
	}

	changes, err := models.GetUnsentChangesForPeer("someone-else", "", 0)
	if err != nil {
		t.Fatalf("failed to read the local change log: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 recorded change, got %d", len(changes))
	}
	if changes[0].GUID != changeGUID {
		t.Errorf("recorded change GUID = %q, want %q — a fresh identity here is what makes the echo endless",
			changes[0].GUID, changeGUID)
	}
	if changes[0].Operation != models.OperationSync {
		t.Errorf("recorded operation = %d, want sync (%d)", changes[0].Operation, models.OperationSync)
	}
}

// TestTheEchoOfOurOwnChangeIsIgnored is the ping-pong test. The hub relays a
// change back to the machine it came from, dressed as operation 9; that
// machine must recognize it and record nothing.
func TestTheEchoOfOurOwnChangeIsIgnored(t *testing.T) {
	setupRelayTestDB(t)

	const changeGUID = "relay-change-0002"
	original := incomingNote(changeGUID, "relay-note-b", "Round trip", "text", models.OperationCreate)
	if err := models.ApplyIncomingSyncChange(original); err != nil {
		t.Fatalf("failed to apply incoming change: %v", err)
	}

	before, err := models.CountUnsentChangesForPeer("hub", "")
	if err != nil {
		t.Fatalf("failed to count changes: %v", err)
	}

	// The same edit comes back from the hub as a relay.
	echo := incomingNote(changeGUID, "relay-note-b", "Round trip", "text", models.OperationSync)
	if err := models.ApplyIncomingSyncChange(echo); err != nil {
		t.Fatalf("applying our own change back failed: %v", err)
	}

	after, err := models.CountUnsentChangesForPeer("hub", "")
	if err != nil {
		t.Fatalf("failed to re-count changes: %v", err)
	}
	if after != before {
		t.Errorf("the echo recorded %d new change(s); it must record none, or hub and spoke trade the same edit forever",
			after-before)
	}
}

// TestARelayedChangeReachesASecondMachine is fan-out: a machine that has never
// seen the note gets it from the hub's relay row, not from the original create
// it never saw.
func TestARelayedChangeReachesASecondMachine(t *testing.T) {
	setupRelayTestDB(t)

	relayed := incomingNote("relay-change-0003", "relay-note-c", "Written on another spoke", "its body",
		models.OperationSync)
	if err := models.ApplyIncomingSyncChange(relayed); err != nil {
		t.Fatalf("failed to apply a relayed change: %v", err)
	}

	note, err := models.GetNoteByGUID("relay-note-c")
	if err != nil {
		t.Fatalf("failed to read the note back: %v", err)
	}
	if note == nil {
		t.Fatal("a relayed change created no note; a second spoke would never see the first spoke's work")
	}
	if note.Title != "Written on another spoke" {
		t.Errorf("title = %q, want %q", note.Title, "Written on another spoke")
	}
	if note.Body.String != "its body" {
		t.Errorf("body = %q, want %q", note.Body.String, "its body")
	}
}

// TestARelayedChangeUpdatesANoteWeAlreadyHave covers the other half of the
// upsert: the operation code says "sync", and what decides create-or-update is
// what is on disk.
func TestARelayedChangeUpdatesANoteWeAlreadyHave(t *testing.T) {
	setupRelayTestDB(t)

	if _, err := models.CreateNote(models.NoteInput{
		GUID:  "relay-note-d",
		Title: "Local title",
		Body:  strp("local body"),
	}, relayUserGUID); err != nil {
		t.Fatalf("failed to create the local note: %v", err)
	}

	relayed := incomingNote("relay-change-0004", "relay-note-d", "Edited elsewhere", "edited body",
		models.OperationSync)
	if err := models.ApplyIncomingSyncChange(relayed); err != nil {
		t.Fatalf("failed to apply a relayed change: %v", err)
	}

	note, err := models.GetNoteByGUID("relay-note-d")
	if err != nil || note == nil {
		t.Fatalf("failed to read the note back: %v", err)
	}
	if note.Title != "Edited elsewhere" || note.Body.String != "edited body" {
		t.Errorf("note = %q / %q, want the relayed values", note.Title, note.Body.String)
	}
}

// TestARelayedBodyIsTextNotADiff is why the update path snapshots rather than
// forwarding what it received. A body diff is expressed against the SENDER's
// previous body; passing it on asks a third machine to patch a base it may
// never have had.
func TestARelayedBodyIsTextNotADiff(t *testing.T) {
	setupRelayTestDB(t)

	if _, err := models.CreateNote(models.NoteInput{
		GUID:  "relay-note-e",
		Title: "Diffed",
		Body:  strp("line one\nline two\nline three\n"),
	}, relayUserGUID); err != nil {
		t.Fatalf("failed to create the local note: %v", err)
	}

	// An incoming update carrying a diff rather than a snapshot.
	patch, _ := models.ComputeBodyDiffForTest(
		"line one\nline two\nline three\n",
		"line one\nline two CHANGED\nline three\n",
	)
	diffChange := models.SyncChange{
		GUID:       "relay-change-0005",
		EntityType: "note",
		EntityGUID: "relay-note-e",
		Operation:  models.OperationUpdate,
		AuthoredAt: time.Now().UTC(),
		Fragment: &models.NoteFragmentOutput{
			Bitmask:    models.FragmentBody,
			Body:       &patch,
			BodyIsDiff: true,
		},
	}
	if err := models.ApplyIncomingSyncChange(diffChange); err != nil {
		t.Fatalf("failed to apply the diffed update: %v", err)
	}

	// What this machine will hand onward must be the resolved text.
	changes, err := models.GetUnsentChangesForPeer("downstream", "", 0)
	if err != nil {
		t.Fatalf("failed to read the change log: %v", err)
	}
	var relayFragment *models.NoteFragment
	for _, c := range changes {
		if c.GUID != "relay-change-0005" || !c.NoteFragmentID.Valid {
			continue
		}
		relayFragment, err = models.GetNoteFragment(c.NoteFragmentID.Int64)
		if err != nil {
			t.Fatalf("failed to load the relayed fragment: %v", err)
		}
	}
	if relayFragment == nil {
		t.Fatal("no relayed change was recorded for the diffed update")
	}
	if relayFragment.BodyIsDiff {
		t.Error("the relayed fragment still carries a diff; a third machine would have to patch a base it never had")
	}
	if relayFragment.Body.String != "line one\nline two CHANGED\nline three\n" {
		t.Errorf("relayed body = %q, want the resolved text", relayFragment.Body.String)
	}
}
