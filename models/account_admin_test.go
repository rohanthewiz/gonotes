package models

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Account admin tests: the repair for a local account registered under a
// different name from the sync username. The reconcile in sync_identity.go
// refuses to guess across names, so the operator renames or merges, and the
// reconcile takes it from there. Shares setup helpers with
// sync_identity_test.go.

// noteFor creates a note owned by guid, public or private.
func noteFor(t *testing.T, guid, title string, private bool) *Note {
	t.Helper()
	n, err := CreateNote(NoteInput{GUID: uuid.New().String(), Title: title, IsPrivate: private}, guid)
	if err != nil {
		t.Fatalf("failed to create note %q: %v", title, err)
	}
	return n
}

func mustBeVisible(t *testing.T, n *Note, guid string) {
	t.Helper()
	got, err := GetNoteByID(n.ID, guid)
	if err != nil || got == nil {
		t.Fatalf("note %q is not visible to %s (err=%v)", n.Title, guid, err)
	}
}

// TestRenameThenAlignAdoptsHubGUID is the whole N-025 story for the rename
// case: "bob" exists locally, the hub knows this person as idHubUsername.
// Reconcile leaves bob alone (names differ); rename + align fixes it.
func TestRenameThenAlignAdoptsHubGUID(t *testing.T) {
	setupIdentityTestDB(t)

	bob := newTestUser(t, "bob")
	pub := noteFor(t, bob.GUID, "bob public", false)
	priv := noteFor(t, bob.GUID, "bob private", true)

	hubGUID := uuid.New().String()
	recordHub(t, hubGUID)

	// The diagnosed-but-unfixable state: reconcile does nothing.
	if changed, err := ReconcileHubUserGUID(hubGUID, idHubUsername); err != nil || changed {
		t.Fatalf("reconcile across names should be a no-op, got changed=%v err=%v", changed, err)
	}

	renamed, err := RenameLocalUser("bob", idHubUsername)
	if err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	if renamed.Username != idHubUsername || renamed.GUID != bob.GUID {
		t.Fatalf("rename changed the wrong things: %+v", renamed)
	}

	changed, err := AlignLocalUserWithHub(idHubUsername)
	if err != nil || !changed {
		t.Fatalf("align after rename: changed=%v err=%v", changed, err)
	}

	u, _ := GetUserByUsername(idHubUsername)
	if u == nil || u.GUID != hubGUID {
		t.Fatalf("renamed account did not adopt the hub GUID: %+v", u)
	}
	mustBeVisible(t, pub, hubGUID)
	mustBeVisible(t, priv, hubGUID)

	// The password survives a rename; the hash does not depend on the name.
	if _, err := AuthenticateUser(UserLoginInput{Username: idHubUsername, Password: "correct-horse"}); err != nil {
		t.Fatalf("login under the new name failed: %v", err)
	}
}

func TestRenameRefusesATakenName(t *testing.T) {
	setupIdentityTestDB(t)
	newTestUser(t, "bob")
	newTestUser(t, "rob")

	_, err := RenameLocalUser("bob", "rob")
	if err == nil || !strings.Contains(err.Error(), "merge") {
		t.Fatalf("expected a refusal pointing at merge, got %v", err)
	}
}

func TestRenameValidatesTheNewName(t *testing.T) {
	setupIdentityTestDB(t)
	newTestUser(t, "bob")

	if _, err := RenameLocalUser("bob", "no spaces!"); err == nil {
		t.Fatal("expected an invalid username to be refused")
	}
	if _, err := RenameLocalUser("ghost", "rob"); err == nil {
		t.Fatal("expected renaming a missing account to fail")
	}
}

// TestMergeMovesEverythingAndDeletesTheSource is the case the old log advice
// produced: the user re-registered under the sync name, which adopted the
// hub GUID, and the original account's notes were left stranded.
func TestMergeMovesEverythingAndDeletesTheSource(t *testing.T) {
	setupIdentityTestDB(t)

	bob := newTestUser(t, "bob") // first user, so admin
	hubGUID := uuid.New().String()
	recordHub(t, hubGUID)
	rob := newTestUser(t, idHubUsername) // born holding the hub GUID
	if rob.GUID != hubGUID || rob.IsAdmin {
		t.Fatalf("setup: want rob on the hub GUID and not admin, got %+v", rob)
	}

	pub := noteFor(t, bob.GUID, "stranded public", false)
	priv := noteFor(t, bob.GUID, "stranded private", true)
	if _, err := CreateCategory(CategoryInput{Name: "Work"}, bob.GUID); err != nil {
		t.Fatalf("create category: %v", err)
	}
	if _, err := CreateCategory(CategoryInput{Name: "work"}, rob.GUID); err != nil {
		t.Fatalf("create category: %v", err)
	}
	if _, err := SaveNamedQuery(bob.GUID, "mine", `title = "x"`); err != nil {
		t.Fatalf("save query: %v", err)
	}

	collisions, err := CategoryNameCollisions(bob.GUID, rob.GUID)
	if err != nil || len(collisions) != 1 {
		t.Fatalf("want one case-insensitive collision, got %v (err=%v)", collisions, err)
	}

	merged, err := MergeLocalUsers("bob", idHubUsername)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if merged.GUID != hubGUID {
		t.Fatalf("merge changed the surviving GUID: %q", merged.GUID)
	}
	if !merged.IsAdmin {
		t.Fatal("admin rights were dropped by the merge")
	}
	if gone, _ := GetUserByUsername("bob"); gone != nil {
		t.Fatal("source account still exists after merge")
	}

	mustBeVisible(t, pub, hubGUID)
	mustBeVisible(t, priv, hubGUID)

	// saved_queries is in the private database only; the sweep must reach it.
	list, err := ListSavedQueries(hubGUID)
	if err != nil || len(list.Saved) != 1 {
		t.Fatalf("saved query did not follow the merge: %+v (err=%v)", list, err)
	}

	// Nothing at all left under the old GUID.
	counts, err := CountUserGUIDReferences(bob.GUID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range counts {
		if c.Rows != 0 {
			t.Fatalf("%s.%s still has %d rows under the merged GUID", c.Table, c.Column, c.Rows)
		}
	}
}

func TestMergeRefusals(t *testing.T) {
	setupIdentityTestDB(t)
	newTestUser(t, "bob")

	if _, err := MergeLocalUsers("bob", "bob"); err == nil {
		t.Fatal("merge into self should be refused")
	}
	if _, err := MergeLocalUsers("bob", "rob"); err == nil || !strings.Contains(err.Error(), "rename") {
		t.Fatalf("merge into a missing account should point at rename, got %v", err)
	}
	if _, err := MergeLocalUsers("ghost", "bob"); err == nil {
		t.Fatal("merge from a missing account should fail")
	}
}

// TestReconcileSweepsSavedQueries pins the inventory fix: saved_queries
// postdates the identity work, and the original sweep missed it, so a
// reconcile left a user's saved queries behind under the old GUID.
func TestReconcileSweepsSavedQueries(t *testing.T) {
	setupIdentityTestDB(t)

	local := newTestUser(t, idHubUsername)
	if _, err := SaveNamedQuery(local.GUID, "mine", `title = "x"`); err != nil {
		t.Fatalf("save query: %v", err)
	}

	hubGUID := uuid.New().String()
	if _, err := ReconcileHubUserGUID(hubGUID, idHubUsername); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	list, err := ListSavedQueries(hubGUID)
	if err != nil || len(list.Saved) != 1 {
		t.Fatalf("saved query was left under the old GUID: %+v (err=%v)", list, err)
	}
}

func TestListingHelpers(t *testing.T) {
	setupIdentityTestDB(t)
	newTestUser(t, "bob")
	newTestUser(t, "rob")
	hubGUID := uuid.New().String()
	recordHub(t, hubGUID)

	users, err := ListLocalUsers()
	if err != nil || len(users) != 2 || users[0].Username != "bob" {
		t.Fatalf("ListLocalUsers = %+v (err=%v)", users, err)
	}
	ids, err := RecordedHubIdentities()
	if err != nil || len(ids) != 1 || ids[0].UserGUID != hubGUID || ids[0].Username != idHubUsername {
		t.Fatalf("RecordedHubIdentities = %+v (err=%v)", ids, err)
	}
}
