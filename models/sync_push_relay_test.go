package models

import (
	"testing"
	"time"
)

// A spoke's push batch carries no relays (N-023).
//
// Applying a pulled change records an OperationSync row under the change's
// own GUID (relayChangeGUID). GetUnsentChangesForPeer does not filter by
// operation, and on the hub that is required: the relay rows ARE what other
// spokes pull. The open question was the other direction. Do the relay rows
// a spoke records while pulling come back out in its next push, only for the
// hub to skip them by GUID?
//
// They do not, because the pull path marks each one as already delivered to
// the hub (MarkChangeGUIDSyncedToPeer, called in
// applyChangeWithConflictDetection). This test pins that, so an operation
// filter in GetUnsentChangesForPeer stays unnecessary. It drives the pull
// path the sync client actually runs, minus the HTTP, with every kind of
// change a pull can deliver.
func TestASpokesPushCarriesNoRelaysItPulled(t *testing.T) {
	if err := InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("InitTestDB: %v", err)
	}
	t.Cleanup(func() { CloseDB() })

	const spokePeer = "spoke-peer-n023"
	sc := &SyncClient{config: &SyncConfig{}, peerID: spokePeer}

	str := func(s string) *string { return &s }
	yes := true
	noteChange := func(guid, noteGUID string, op int32, frag *NoteFragmentOutput) SyncChange {
		return SyncChange{GUID: guid, EntityType: "note", EntityGUID: noteGUID, Operation: op,
			AuthoredAt: time.Now().UTC(), User: "n023-user", Fragment: frag}
	}

	// One pull's worth of every shape: a relayed create, a relayed update to
	// it, a relayed privacy flip (its relay row lands in the other database),
	// a delete, and a category create and rename.
	pulled := []SyncChange{
		noteChange("n023-c1", "n023-note-a", OperationSync,
			&NoteFragmentOutput{Bitmask: FragmentTitle | FragmentBody, Title: str("A"), Body: str("one")}),
		noteChange("n023-c2", "n023-note-a", OperationSync,
			&NoteFragmentOutput{Bitmask: FragmentBody, Body: str("two")}),
		noteChange("n023-c3", "n023-note-b", OperationSync,
			&NoteFragmentOutput{Bitmask: FragmentTitle | FragmentBody, Title: str("B"), Body: str("b")}),
		noteChange("n023-c4", "n023-note-b", OperationSync,
			&NoteFragmentOutput{Bitmask: FragmentIsPrivate, IsPrivate: &yes}),
		noteChange("n023-c5", "n023-note-c", OperationCreate,
			&NoteFragmentOutput{Bitmask: FragmentTitle, Title: str("C")}),
		noteChange("n023-c6", "n023-note-c", OperationDelete, nil),
		{GUID: "n023-c7", EntityType: "category", EntityGUID: "n023-cat", Operation: OperationSync,
			Fragment: &CategoryFragmentOutput{Bitmask: CatFragmentName, Name: str("Pulled")}},
		{GUID: "n023-c8", EntityType: "category", EntityGUID: "n023-cat", Operation: OperationSync,
			Fragment: &CategoryFragmentOutput{Bitmask: CatFragmentName, Name: str("Renamed")}},
	}
	for _, ch := range pulled {
		if err := sc.applyChangeWithConflictDetection(ch); err != nil {
			t.Fatalf("applying pulled change %s: %v", ch.GUID, err)
		}
	}

	// Guard against a vacuous pass: the relay rows exist, and are owed to
	// any peer other than the one they came from.
	owedElsewhere, err := GetUnifiedChangesForPeer("some-other-peer", "", 100)
	if err != nil {
		t.Fatalf("GetUnifiedChangesForPeer(other): %v", err)
	}
	relays := 0
	for _, ch := range owedElsewhere.Changes {
		if ch.Operation == OperationSync {
			relays++
		}
	}
	if relays == 0 {
		t.Fatal("applying the pull recorded no relay rows; the test would prove nothing")
	}

	// A local edit, which the push must carry.
	local, err := CreateNote(NoteInput{GUID: "n023-local", Title: "Written here"}, "n023-user")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	// The exact call pushChanges makes.
	batch, err := GetUnifiedChangesForPeer(spokePeer, "", 100)
	if err != nil {
		t.Fatalf("GetUnifiedChangesForPeer(spoke): %v", err)
	}
	sawLocal := false
	for _, ch := range batch.Changes {
		if ch.Operation == OperationSync {
			t.Errorf("the push batch carries relay %s (%s %s), which the hub already has",
				ch.GUID, ch.EntityType, ch.EntityGUID)
		}
		for _, p := range pulled {
			if ch.GUID == p.GUID {
				t.Errorf("the push batch carries pulled change %s back to the hub", ch.GUID)
			}
		}
		if ch.EntityGUID == local.GUID {
			sawLocal = true
		}
	}
	if !sawLocal {
		t.Errorf("the push batch lacks the local edit; batch = %+v", batch.Changes)
	}
}
