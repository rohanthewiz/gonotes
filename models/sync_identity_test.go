package models

import (
	"testing"

	"github.com/google/uuid"
)

// Identity tests: a spoke's local account and its hub account are the same
// person, so they must be the same GUID. Ownership travels with every synced
// change (change_user → created_by) and every read filters on it, so a
// mismatch is not cosmetic — it is "sync worked and I still can't see my
// notes".
//
// Each test fails against the pre-fix code: registration minted a fresh GUID
// unconditionally, and nothing ever reconciled an existing account.

const (
	idHubURL      = "http://hub.example:8981"
	idHubUsername = "spokeuser"
)

func setupIdentityTestDB(t *testing.T) {
	t.Helper()
	if err := InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}
	t.Cleanup(func() { CloseDB() })
}

// recordHub creates the sync_state row a spoke would have and stamps the hub
// identity onto it — the state after one successful login.
func recordHub(t *testing.T, hubUserGUID string) {
	t.Helper()
	if _, err := GetOrCreateSyncState(idHubURL); err != nil {
		t.Fatalf("failed to create sync state: %v", err)
	}
	if err := RecordHubIdentity(idHubURL, hubUserGUID, idHubUsername); err != nil {
		t.Fatalf("failed to record hub identity: %v", err)
	}
}

func newTestUser(t *testing.T, username string) *User {
	t.Helper()
	u, err := CreateUser(UserRegisterInput{Username: username, Password: "correct-horse"})
	if err != nil {
		t.Fatalf("failed to create user %q: %v", username, err)
	}
	return u
}

// TestNewLocalUserAdoptsRecordedHubGUID is the path that keeps the mismatch
// from ever forming: a spoke that has synced already knows which GUID its
// notes arrive under, so the account created next is born holding it.
func TestNewLocalUserAdoptsRecordedHubGUID(t *testing.T) {
	setupIdentityTestDB(t)

	hubGUID := uuid.New().String()
	recordHub(t, hubGUID)

	user := newTestUser(t, idHubUsername)
	if user.GUID != hubGUID {
		t.Fatalf("new local user got GUID %q, want the recorded hub GUID %q", user.GUID, hubGUID)
	}
}

// TestAdoptionRequiresAMatchingUsername pins the safety catch. A GUID is an
// account, and the only local evidence that two accounts are the same account
// is that they share a name. Without the name, mint a fresh one.
func TestAdoptionRequiresAMatchingUsername(t *testing.T) {
	setupIdentityTestDB(t)

	hubGUID := uuid.New().String()
	recordHub(t, hubGUID)

	other := newTestUser(t, "someone_else")
	if other.GUID == hubGUID {
		t.Fatal("an unrelated username adopted the hub GUID; adoption must be name-matched")
	}
}

// TestAdoptionDoesNotStealAGUIDAlreadyHeld guards the uniqueness constraint:
// offering a GUID that another local row already holds would turn a
// registration into a constraint violation.
func TestAdoptionDoesNotStealAGUIDAlreadyHeld(t *testing.T) {
	setupIdentityTestDB(t)

	hubGUID := uuid.New().String()
	recordHub(t, hubGUID)

	// Someone else is already sitting on that GUID.
	first := newTestUser(t, "incumbent")
	if _, err := pubDB.Exec(`UPDATE users SET guid = ? WHERE id = ?`, hubGUID, first.ID); err != nil {
		t.Fatalf("failed to plant the incumbent GUID: %v", err)
	}

	second, err := CreateUser(UserRegisterInput{Username: idHubUsername, Password: "correct-horse"})
	if err != nil {
		t.Fatalf("registration should still succeed with a fresh GUID: %v", err)
	}
	if second.GUID == hubGUID {
		t.Fatal("registration adopted a GUID another user already holds")
	}
}

// TestReconcileAdoptsHubGUIDAndRepointsNotes is the repair path, and the one
// that matters for databases that already carry the mismatch. The assertion
// that counts is the last one: the notes are readable by the account that is
// now the hub account.
func TestReconcileAdoptsHubGUIDAndRepointsNotes(t *testing.T) {
	setupIdentityTestDB(t)

	// The pre-fix ordering: a local account exists first, with its own GUID.
	local := newTestUser(t, idHubUsername)
	localGUID := local.GUID

	pub, err := CreateNote(NoteInput{GUID: uuid.New().String(), Title: "public note"}, localGUID)
	if err != nil {
		t.Fatalf("failed to create public note: %v", err)
	}
	priv, err := CreateNote(NoteInput{GUID: uuid.New().String(), Title: "private note", IsPrivate: true}, localGUID)
	if err != nil {
		t.Fatalf("failed to create private note: %v", err)
	}

	hubGUID := uuid.New().String()
	if hubGUID == localGUID {
		t.Fatal("test setup produced identical GUIDs")
	}

	changed, err := ReconcileHubUserGUID(hubGUID, idHubUsername)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if !changed {
		t.Fatal("reconcile reported no change, but the GUIDs differed")
	}

	reloaded, err := GetUserByUsername(idHubUsername)
	if err != nil || reloaded == nil {
		t.Fatalf("failed to reload user: %v", err)
	}
	if reloaded.GUID != hubGUID {
		t.Fatalf("user GUID is %q, want the hub GUID %q", reloaded.GUID, hubGUID)
	}

	// Both databases hold notes; the sweep has to reach both.
	for _, n := range []*Note{pub, priv} {
		got, err := GetNoteByID(n.ID, hubGUID)
		if err != nil {
			t.Fatalf("note %q not readable as the hub user: %v", n.Title, err)
		}
		if got == nil {
			t.Fatalf("note %q is invisible to the hub user after reconciliation", n.Title)
		}
	}

	// And nothing is left behind under the old identity.
	if old, err := GetNoteByID(pub.ID, localGUID); err == nil && old != nil {
		t.Fatal("public note is still owned by the abandoned local GUID")
	}
}

// TestReconcileIsIdempotent matters because reconcile runs on every login and
// every startup, and because a crashed half-sweep is only recoverable if the
// re-run is harmless.
func TestReconcileIsIdempotent(t *testing.T) {
	setupIdentityTestDB(t)

	newTestUser(t, idHubUsername)
	hubGUID := uuid.New().String()

	if changed, err := ReconcileHubUserGUID(hubGUID, idHubUsername); err != nil || !changed {
		t.Fatalf("first reconcile: changed=%v err=%v", changed, err)
	}
	changed, err := ReconcileHubUserGUID(hubGUID, idHubUsername)
	if err != nil {
		t.Fatalf("second reconcile errored: %v", err)
	}
	if changed {
		t.Fatal("second reconcile reported a change; it should be a no-op")
	}
}

// TestReconcileRefusesWhenAnotherUserHoldsTheHubGUID: two accounts claiming
// the same identity is not something this code can adjudicate, and forcing it
// would violate users.guid uniqueness. Refuse loudly, change nothing.
func TestReconcileRefusesWhenAnotherUserHoldsTheHubGUID(t *testing.T) {
	setupIdentityTestDB(t)

	hubGUID := uuid.New().String()
	incumbent := newTestUser(t, "incumbent")
	if _, err := pubDB.Exec(`UPDATE users SET guid = ? WHERE id = ?`, hubGUID, incumbent.ID); err != nil {
		t.Fatalf("failed to plant the incumbent GUID: %v", err)
	}
	sync := newTestUser(t, idHubUsername)

	if _, err := ReconcileHubUserGUID(hubGUID, idHubUsername); err == nil {
		t.Fatal("reconcile should refuse when the hub GUID is already held locally")
	}

	after, err := GetUserByUsername(idHubUsername)
	if err != nil || after == nil {
		t.Fatalf("failed to reload user: %v", err)
	}
	if after.GUID != sync.GUID {
		t.Fatalf("a refused reconcile still changed the user GUID: %q -> %q", sync.GUID, after.GUID)
	}
}

// TestReconcileWithNoLocalAccountIsANoOp: a spoke that has synced but where
// nobody has registered yet is not broken — the next registration adopts.
func TestReconcileWithNoLocalAccountIsANoOp(t *testing.T) {
	setupIdentityTestDB(t)

	changed, err := ReconcileHubUserGUID(uuid.New().String(), idHubUsername)
	if err != nil {
		t.Fatalf("reconcile errored with no local user: %v", err)
	}
	if changed {
		t.Fatal("reconcile reported a change with no local user to change")
	}
}

// TestHubIdentityFromTokenReadsAnUnverifiableToken is the upgrade path: a
// spoke holding a week-long token from before sync_state recorded the hub
// identity must be able to recover it without waiting for the token to
// expire. The token is signed with the HUB's key, which this spoke need not
// have — so the read has to work when the signature cannot be checked.
func TestHubIdentityFromTokenReadsAnUnverifiableToken(t *testing.T) {
	setupIdentityTestDB(t)

	t.Setenv(JWTSecretEnvVar, "a-hub-signing-secret-of-sufficient-length")
	if err := InitJWT(); err != nil {
		t.Fatalf("failed to init JWT: %v", err)
	}
	hubUser := &User{GUID: uuid.New().String(), Username: idHubUsername}
	token, err := GenerateToken(hubUser)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	// Re-key: this process no longer holds the secret the token was signed
	// with, exactly as a spoke with its own JWT secret would not.
	t.Setenv(JWTSecretEnvVar, "a-completely-different-spoke-side-secret")
	if err := InitJWT(); err != nil {
		t.Fatalf("failed to re-init JWT: %v", err)
	}

	guid, username := hubIdentityFromToken(token)
	if guid != hubUser.GUID {
		t.Fatalf("recovered GUID %q, want %q", guid, hubUser.GUID)
	}
	if username != idHubUsername {
		t.Fatalf("recovered username %q, want %q", username, idHubUsername)
	}

	if g, u := hubIdentityFromToken("not-a-jwt"); g != "" || u != "" {
		t.Fatalf("garbage token yielded (%q, %q), want empty", g, u)
	}
}

// TestSyncClientStartupRecoversIdentityFromACachedToken covers the upgrade
// path as it actually presents: an existing spoke has a valid week-long hub
// token and a local account under the wrong GUID, and sync_state has no
// recorded identity because it predates the column. Construction alone has to
// repair it — waiting for the next login would mean waiting out the token, and
// in prompt mode waiting for a cycle can mean waiting for the user.
func TestSyncClientStartupRecoversIdentityFromACachedToken(t *testing.T) {
	setupIdentityTestDB(t)

	t.Setenv(JWTSecretEnvVar, "a-hub-signing-secret-of-sufficient-length")
	if err := InitJWT(); err != nil {
		t.Fatalf("failed to init JWT: %v", err)
	}
	hubGUID := uuid.New().String()
	token, err := GenerateToken(&User{GUID: hubGUID, Username: idHubUsername})
	if err != nil {
		t.Fatalf("failed to generate hub token: %v", err)
	}

	local := newTestUser(t, idHubUsername)
	if local.GUID == hubGUID {
		t.Fatal("test setup produced identical GUIDs")
	}

	// The pre-upgrade state: a peer row and a cached token, nothing else.
	if _, err := GetOrCreateSyncState(idHubURL); err != nil {
		t.Fatalf("failed to create sync state: %v", err)
	}
	if err := UpdateSyncAuthToken(idHubURL, token); err != nil {
		t.Fatalf("failed to cache the auth token: %v", err)
	}

	if _, err := NewSyncClient(&SyncConfig{
		Enabled:     true,
		HubURL:      idHubURL,
		Username:    idHubUsername,
		Password:    "correct-horse",
		Interval:    defaultSyncInterval,
		Mode:        SyncModePrompt,
		PromptAfter: defaultPromptAfter,
	}); err != nil {
		t.Fatalf("failed to construct sync client: %v", err)
	}
	t.Cleanup(func() { syncClientInstance = nil })

	reloaded, err := GetUserByUsername(idHubUsername)
	if err != nil || reloaded == nil {
		t.Fatalf("failed to reload user: %v", err)
	}
	if reloaded.GUID != hubGUID {
		t.Fatalf("startup left the local GUID at %q, want the hub GUID %q from the cached token",
			reloaded.GUID, hubGUID)
	}
}
