package models

import (
	"errors"
	"testing"
	"time"
)

// lock_test.go pins the registry's rules. Every test here runs against the
// process-global registry, so each one resets it first — a lease left behind
// would arrive in the next test as a note held by a session that never existed.

func holder(id string) LockHolder {
	return LockHolder{SessionID: id, Label: "pane " + id, PaneHandle: "w1:p" + id, Client: "tui"}
}

func TestAcquireGrantsAnUnheldNote(t *testing.T) {
	ResetNoteLocksForTest()

	lock, err := AcquireNoteLock(42, "user-1", holder("a"), false)
	if err != nil {
		t.Fatalf("acquiring an unheld note failed: %v", err)
	}
	if lock.Token == "" {
		t.Fatal("the lease carries no token; nothing could ever write under it")
	}
	if lock.NoteID != 42 || lock.Holder.SessionID != "a" {
		t.Fatalf("the lease describes note %d held by %q, want 42 held by \"a\"",
			lock.NoteID, lock.Holder.SessionID)
	}
	if !lock.ExpiresAt.After(time.Now()) {
		t.Fatal("the lease is already expired at the moment it was granted")
	}
}

// The refusal is the whole feature: a second session must not be able to open a
// note somebody has open, and must be told who has it.
func TestAcquireRefusesASecondSession(t *testing.T) {
	ResetNoteLocksForTest()

	if _, err := AcquireNoteLock(42, "user-1", holder("a"), false); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	_, err := AcquireNoteLock(42, "user-1", holder("b"), false)
	if !errors.Is(err, ErrNoteLocked) {
		t.Fatalf("a second session got %v, want ErrNoteLocked", err)
	}

	var locked *NoteLockedError
	if !errors.As(err, &locked) {
		t.Fatal("the refusal does not carry the blocking lease; the UI has nobody to name")
	}
	if locked.Lock.Holder.SessionID != "a" {
		t.Fatalf("the refusal names %q as the holder, want \"a\"", locked.Lock.Holder.SessionID)
	}
	// The token is the only thing separating "may not write" from "may". A
	// refusal that leaked it would hand the blocked session exactly what it was
	// just denied.
	if locked.Lock.Token != "" {
		t.Fatal("the refusal leaked the holder's token")
	}
}

// Re-acquiring a note this session already holds is the ordinary case of
// reopening a form, and it must not deadlock against itself or reissue a token
// that invalidates the one in use.
func TestAcquireIsReentrantForTheSameSession(t *testing.T) {
	ResetNoteLocksForTest()

	first, err := AcquireNoteLock(42, "user-1", holder("a"), false)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	second, err := AcquireNoteLock(42, "user-1", holder("a"), false)
	if err != nil {
		t.Fatalf("the holder was refused its own lease: %v", err)
	}
	if second.Token != first.Token {
		t.Fatal("re-acquiring reissued the token; the one the form is already using would stop working")
	}
	if !second.ExpiresAt.After(first.ExpiresAt) && !second.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatal("re-acquiring moved the expiry backwards")
	}
}

func TestStealTakesTheNoteAndRecordsWho(t *testing.T) {
	ResetNoteLocksForTest()

	victim, _ := AcquireNoteLock(42, "user-1", holder("a"), false)

	thief, err := AcquireNoteLock(42, "user-1", holder("b"), true)
	if err != nil {
		t.Fatalf("a steal was refused: %v", err)
	}
	if thief.Token == victim.Token {
		t.Fatal("the steal reused the victim's token; the victim could still write")
	}
	if thief.StolenFrom != "a" {
		t.Fatalf("the steal recorded StolenFrom=%q, want \"a\"", thief.StolenFrom)
	}

	// And the victim finds out the only way it can: its heartbeat stops working.
	if _, err := RenewNoteLock(42, victim.Token); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("the displaced holder renewed successfully (%v); it would never learn it was robbed", err)
	}
}

// A steal of a lease nobody actually held displaced nobody, and must not claim
// otherwise — the audit trail is only worth having if it is true.
func TestStealOfALapsedLeaseRecordsNoVictim(t *testing.T) {
	ResetNoteLocksForTest()

	lock, err := AcquireNoteLock(42, "user-1", holder("b"), true)
	if err != nil {
		t.Fatalf("acquire with steal on an unheld note failed: %v", err)
	}
	if lock.StolenFrom != "" {
		t.Fatalf("a steal of an unheld note recorded StolenFrom=%q, want empty", lock.StolenFrom)
	}
}

func TestRenewExtendsAndRejectsAWrongToken(t *testing.T) {
	ResetNoteLocksForTest()

	lock, _ := AcquireNoteLock(42, "user-1", holder("a"), false)

	renewed, err := RenewNoteLock(42, lock.Token)
	if err != nil {
		t.Fatalf("renewing a held lease failed: %v", err)
	}
	if renewed.ExpiresAt.Before(lock.ExpiresAt) {
		t.Fatal("renewing moved the expiry backwards")
	}

	if _, err := RenewNoteLock(42, "lk_not-the-token"); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("a wrong token renewed the lease (%v)", err)
	}
}

func TestReleaseFreesTheNoteForTheNextSession(t *testing.T) {
	ResetNoteLocksForTest()

	lock, _ := AcquireNoteLock(42, "user-1", holder("a"), false)

	if !ReleaseNoteLock(42, lock.Token) {
		t.Fatal("release reported nothing to release")
	}
	if _, err := AcquireNoteLock(42, "user-1", holder("b"), false); err != nil {
		t.Fatalf("the next session was still blocked after a release: %v", err)
	}
}

// A wrong token on release is deliberately not an error: by then the caller has
// left the form and the lease expires on its own regardless.
func TestReleaseWithAWrongTokenIsQuietlyIgnored(t *testing.T) {
	ResetNoteLocksForTest()

	AcquireNoteLock(42, "user-1", holder("a"), false)
	if ReleaseNoteLock(42, "lk_wrong") {
		t.Fatal("a wrong token released somebody else's lease")
	}
	if GetNoteLock(42) == nil {
		t.Fatal("the lease disappeared despite the release being rejected")
	}
}

func TestReleaseForSessionDropsEveryLeaseItHolds(t *testing.T) {
	ResetNoteLocksForTest()

	AcquireNoteLock(1, "user-1", holder("a"), false)
	AcquireNoteLock(2, "user-1", holder("a"), false)
	AcquireNoteLock(3, "user-1", holder("b"), false)

	if n := ReleaseNoteLocksForSession("a"); n != 2 {
		t.Fatalf("released %d leases for session a, want 2", n)
	}
	if GetNoteLock(3) == nil {
		t.Fatal("another session's lease was released too")
	}
}

// The write gate is the teeth. An unlocked note is writable by anyone (see
// AuthorizeNoteWrite's doc for why); a locked one, only by its holder.
func TestAuthorizeNoteWrite(t *testing.T) {
	ResetNoteLocksForTest()

	if err := AuthorizeNoteWrite(42, ""); err != nil {
		t.Fatalf("an unlocked note refused a write with no token: %v", err)
	}

	lock, _ := AcquireNoteLock(42, "user-1", holder("a"), false)

	if err := AuthorizeNoteWrite(42, lock.Token); err != nil {
		t.Fatalf("the holder was refused its own write: %v", err)
	}
	if err := AuthorizeNoteWrite(42, ""); !errors.Is(err, ErrNoteLocked) {
		t.Fatalf("a write with no token got past a held lock (%v)", err)
	}
	if err := AuthorizeNoteWrite(42, "lk_wrong"); !errors.Is(err, ErrNoteLocked) {
		t.Fatalf("a write with a foreign token got past a held lock (%v)", err)
	}
}

// Expiry is what keeps a crashed pane from holding a note forever. The lease is
// aged by hand rather than by sleeping through LockTTL — a 90-second test is a
// test nobody runs.
func TestALapsedLeaseStopsBlocking(t *testing.T) {
	ResetNoteLocksForTest()

	lock, _ := AcquireNoteLock(42, "user-1", holder("a"), false)

	noteLocks.mu.Lock()
	noteLocks.locks[42].ExpiresAt = time.Now().Add(-time.Second)
	noteLocks.mu.Unlock()

	if GetNoteLock(42) != nil {
		t.Fatal("an expired lease is still reported as held")
	}
	if err := AuthorizeNoteWrite(42, ""); err != nil {
		t.Fatalf("an expired lease still blocked a write: %v", err)
	}
	if _, err := AcquireNoteLock(42, "user-1", holder("b"), false); err != nil {
		t.Fatalf("an expired lease still blocked the next session: %v", err)
	}
	if _, err := RenewNoteLock(42, lock.Token); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("the original holder renewed a lease that had lapsed and been retaken (%v)", err)
	}
}

func TestListNoteLocksIsScopedAndRedacted(t *testing.T) {
	ResetNoteLocksForTest()

	AcquireNoteLock(1, "user-1", holder("a"), false)
	AcquireNoteLock(2, "user-2", holder("b"), false)

	locks := ListNoteLocks("user-1")
	if len(locks) != 1 || locks[0].NoteID != 1 {
		t.Fatalf("listing for user-1 returned %d leases %v, want just note 1", len(locks), locks)
	}
	if locks[0].Token != "" {
		t.Fatal("the listing leaked a lease token")
	}
}

func TestAcquireRequiresASessionID(t *testing.T) {
	ResetNoteLocksForTest()

	if _, err := AcquireNoteLock(42, "user-1", LockHolder{Label: "anonymous"}, false); err == nil {
		t.Fatal("a lease was granted to a holder with no session id; nothing could ever release it")
	}
}
