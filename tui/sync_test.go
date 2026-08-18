package tui

import (
	"strings"
	"testing"
	"time"

	"gonotes/models"

	tea "charm.land/bubbletea/v2"
)

// The TUI half of prompt-mode sync has two jobs, and each fails silently in
// its own direction:
//
//   - a quit guard that does not fire loses work — the whole reason the guard
//     exists is a laptop closed on an afternoon of notes that never left it;
//   - a guard that fires when there is nothing pending is a toll on every
//     exit, which trains the user to answer without reading.
//
// So the tests pin both, plus the thing that makes the "no" answer honest:
// declining at the quit dialog has to reach the exit path, or the process
// syncs anyway and the question was theatre.

// syncFixture builds a browse screen over a fake store, with the session's
// sync state set to whatever the test is about.
func syncFixture(t *testing.T, status *models.SyncClientStatus) (*browseScreen, *fakeStore) {
	t.Helper()
	fs := newFakeStore()
	user := fs.addUser("sync_tester", "test-password-123")
	fs.seedNote(user.GUID, "A note", "body")
	fs.setSyncStatus(status)

	sess := &session{
		store:  fs,
		cats:   newCatsState(),
		user:   user,
		width:  100,
		height: 40,
		sync:   &syncState{status: status},
	}
	return newBrowseScreen(sess), fs
}

// dueStatus is a spoke that is overdue with work waiting.
func dueStatus(pending int) *models.SyncClientStatus {
	lastSync := time.Now().Add(-3 * time.Hour)
	return &models.SyncClientStatus{
		Enabled:         true,
		Mode:            models.SyncModePrompt,
		Due:             true,
		Pending:         pending,
		LastSync:        &lastSync,
		PromptAfterSecs: int64((2 * time.Hour).Seconds()),
	}
}

// ---- The quit guard --------------------------------------------------------

func TestQuittingWithPendingChangesAsksFirst(t *testing.T) {
	browse, _ := syncFixture(t, dueStatus(7))

	_, cmd := press(t, browse, runeKey('q'))
	pushed := pushedScreen(cmd)
	if pushed == nil {
		t.Fatal("q with 7 unsynced changes quit outright; the sync dialog should have stopped it")
	}
	sync, ok := pushed.(*syncScreen)
	if !ok {
		t.Fatalf("q pushed a %T, want *syncScreen", pushed)
	}
	if sync.purpose != syncQuitting {
		t.Errorf("dialog purpose = %v, want syncQuitting", sync.purpose)
	}
}

func TestQuittingWithNothingPendingJustQuits(t *testing.T) {
	// Due, but with an empty outbox: a session where the user only read
	// notes. There is nothing to lose by leaving.
	browse, _ := syncFixture(t, dueStatus(0))

	_, cmd := press(t, browse, runeKey('q'))
	if pushed := pushedScreen(cmd); pushed != nil {
		t.Errorf("q with nothing pending pushed a %T; it should have quit", pushed)
	}
}

func TestQuittingWithoutSyncConfiguredJustQuits(t *testing.T) {
	browse, _ := syncFixture(t, nil)

	_, cmd := press(t, browse, runeKey('q'))
	if pushed := pushedScreen(cmd); pushed != nil {
		t.Errorf("q on an installation with no hub pushed a %T; it should have quit", pushed)
	}
}

// ---- The door --------------------------------------------------------------

func TestTheSyncKeyOpensTheDialog(t *testing.T) {
	browse, _ := syncFixture(t, dueStatus(2))

	_, cmd := press(t, browse, runeKey('S'))
	pushed := pushedScreen(cmd)
	sync, ok := pushed.(*syncScreen)
	if !ok {
		t.Fatalf("S pushed a %T, want *syncScreen", pushed)
	}
	if sync.purpose != syncAsked {
		t.Errorf("dialog purpose = %v, want syncAsked", sync.purpose)
	}
}

func TestTheSyncKeySaysSoWhenThereIsNoHub(t *testing.T) {
	browse, _ := syncFixture(t, nil)

	_, cmd := press(t, browse, runeKey('S'))
	if pushed := pushedScreen(cmd); pushed != nil {
		t.Fatalf("S opened a %T on an installation with no sync configured", pushed)
	}
	// It should say why rather than doing nothing at all.
	var said bool
	for _, msg := range drainCmd(cmd) {
		if note, ok := msg.(statusNote); ok && strings.Contains(note.text, "not configured") {
			said = true
		}
	}
	if !said {
		t.Error("S with no sync configured produced no explanation")
	}
}

// ---- The answers -----------------------------------------------------------

func TestSyncNowRunsACycle(t *testing.T) {
	_, fs := syncFixture(t, dueStatus(4))
	sess := &session{store: fs, cats: newCatsState(), width: 100, height: 40, sync: &syncState{status: dueStatus(4)}}
	dialog := newSyncScreen(sess, syncDue)

	_, cmd := press(t, dialog, runeKey('s'))
	drainCmd(cmd)

	if fs.syncCalls != 1 {
		t.Errorf("SyncNow called %d times, want 1", fs.syncCalls)
	}
	if fs.syncCompacted {
		t.Error("plain sync asked the store to compact; only the c answer does that")
	}
	if !dialog.busy {
		t.Error("the dialog is not marked busy while the cycle runs; a second press would start another")
	}
}

func TestCompactAndSyncAsksForBoth(t *testing.T) {
	_, fs := syncFixture(t, dueStatus(30))
	sess := &session{store: fs, cats: newCatsState(), width: 100, height: 40, sync: &syncState{status: dueStatus(30)}}
	dialog := newSyncScreen(sess, syncDue)

	_, cmd := press(t, dialog, runeKey('c'))
	drainCmd(cmd)

	if fs.syncCalls != 1 || !fs.syncCompacted {
		t.Errorf("compact & sync produced syncCalls=%d compacted=%v, want 1/true",
			fs.syncCalls, fs.syncCompacted)
	}
}

func TestCompactOnlyDoesNotSync(t *testing.T) {
	_, fs := syncFixture(t, dueStatus(30))
	sess := &session{store: fs, cats: newCatsState(), width: 100, height: 40, sync: &syncState{status: dueStatus(30)}}
	dialog := newSyncScreen(sess, syncDue)

	_, cmd := press(t, dialog, runeKey('p'))
	drainCmd(cmd)

	if fs.compactCalls != 1 {
		t.Errorf("CompactChanges called %d times, want 1", fs.compactCalls)
	}
	if fs.syncCalls != 0 {
		t.Error("compact-only ran a sync cycle; the hub may be exactly what is unreachable")
	}
}

func TestLaterDefersTheClock(t *testing.T) {
	_, fs := syncFixture(t, dueStatus(3))
	sess := &session{store: fs, cats: newCatsState(), width: 100, height: 40, sync: &syncState{status: dueStatus(3)}}
	dialog := newSyncScreen(sess, syncDue)

	_, cmd := press(t, dialog, escKey)
	drainCmd(cmd)

	if fs.snoozeCalls != 1 {
		t.Errorf("SnoozeSync called %d times, want 1 — esc on a dialog the clock raised defers it", fs.snoozeCalls)
	}
}

func TestCancellingADialogTheUserOpenedDoesNotDefer(t *testing.T) {
	_, fs := syncFixture(t, dueStatus(3))
	sess := &session{store: fs, cats: newCatsState(), width: 100, height: 40, sync: &syncState{status: dueStatus(3)}}
	dialog := newSyncScreen(sess, syncAsked)

	_, cmd := press(t, dialog, escKey)
	drainCmd(cmd)

	if fs.snoozeCalls != 0 {
		t.Error("cancelling a dialog the user opened deferred the clock; they were not being asked")
	}
}

func TestQuitAnywayTellsTheExitPath(t *testing.T) {
	_, fs := syncFixture(t, dueStatus(5))
	sess := &session{store: fs, cats: newCatsState(), width: 100, height: 40, sync: &syncState{status: dueStatus(5)}}
	dialog := newSyncScreen(sess, syncQuitting)

	_, cmd := press(t, dialog, runeKey('q'))
	drainCmd(cmd)

	if fs.declineCalls != 1 {
		t.Errorf("DeclineExitSync called %d times, want 1 — otherwise the exit path syncs anyway", fs.declineCalls)
	}
	if fs.syncCalls != 0 {
		t.Error("quit-without-syncing ran a sync cycle")
	}
}

func TestQuitAnywayIsNotOfferedOutsideTheQuitDialog(t *testing.T) {
	_, fs := syncFixture(t, dueStatus(5))
	sess := &session{store: fs, cats: newCatsState(), width: 100, height: 40, sync: &syncState{status: dueStatus(5)}}
	dialog := newSyncScreen(sess, syncDue)

	_, cmd := press(t, dialog, runeKey('q'))
	drainCmd(cmd)

	if fs.declineCalls != 0 {
		t.Error("q on the due dialog declined the exit sync; there is no exit happening")
	}
}

// TestAFailedSyncKeepsTheDialogUp: when the user was quitting, a failure means
// the choice they made is no longer available, so the dialog has to stay and
// say so rather than popping and letting them think it worked.
func TestAFailedSyncKeepsTheDialogUp(t *testing.T) {
	_, fs := syncFixture(t, dueStatus(5))
	fs.syncFailWith = errSyncNotConfigured
	sess := &session{store: fs, cats: newCatsState(), width: 100, height: 40, sync: &syncState{status: dueStatus(5)}}
	dialog := newSyncScreen(sess, syncQuitting)

	press(t, dialog, runeKey('s'))
	updated, cmd := dialog.Update(syncDoneMsg{err: errSyncNotConfigured, quitting: true})

	if _, quits := anyQuit(cmd); quits {
		t.Error("a failed sync still quit; the user's changes would be gone with no warning")
	}
	if !strings.Contains(updated.(*syncScreen).note, "Sync failed") {
		t.Errorf("the dialog does not report the failure; note = %q", updated.(*syncScreen).note)
	}
}

// anyQuit reports whether a command produces bubbletea's quit message.
func anyQuit(cmd tea.Cmd) (tea.Msg, bool) {
	for _, msg := range drainCmd(cmd) {
		if _, ok := msg.(tea.QuitMsg); ok {
			return msg, true
		}
	}
	return nil, false
}

// ---- The banner ------------------------------------------------------------

func TestTheBannerNamesTheCountAndTheKey(t *testing.T) {
	state := &syncState{status: dueStatus(12)}
	banner := state.banner()

	if !strings.Contains(banner, "12 changes") {
		t.Errorf("banner does not say how much is waiting: %q", banner)
	}
	if !strings.Contains(banner, keys.Sync.Help().Key) {
		t.Errorf("banner does not name the key that acts on it: %q", banner)
	}
}

func TestTheBannerIsSilentWhenNothingIsDue(t *testing.T) {
	notDue := dueStatus(4)
	notDue.Due = false

	for name, state := range map[string]*syncState{
		"no sync configured": {},
		"not due yet":        {status: notDue},
	} {
		if got := (state).banner(); got != "" {
			t.Errorf("%s: banner = %q, want empty", name, got)
		}
	}
}
