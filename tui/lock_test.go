package tui

import (
	"strings"
	"testing"

	"gonotes/models"

	tea "charm.land/bubbletea/v2"
)

// lock_test.go covers the TUI half of contention: what the user sees and can do
// when a note is already open somewhere else, and what the form does with a
// lease while it holds one.
//
// The scenarios all need two sessions, and there is only one process. The
// second session is an identity, not a program — fakeStore.lockAsOther takes a
// lease under a different session id, which is indistinguishable to everything
// under test from a real GoNotes in another pane.

// lockFixture is a session with one note in it, ready to be edited.
func lockFixture(t *testing.T) (*session, *fakeStore, models.Note) {
	t.Helper()
	fs := newFakeStore()
	user := fs.addUser("lock_tester", "pw")
	sess := &session{store: fs, user: user, width: 100, height: 30, cats: newCatsState()}
	note := fs.seedNote(user.GUID, "Contested note", "the original body")
	return sess, fs, note
}

// resolve runs a command and returns its message, failing the test if there is
// no command at all — the shape almost every assertion here starts with.
func resolve(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command, got none")
	}
	return cmd()
}

// ---- Acquiring --------------------------------------------------------------

// The headline case: pressing "e" on a note somebody else has open must NOT
// open the form. Everything else in this file is downstream of that.
func TestEditingAHeldNoteOpensTheContentionDialog(t *testing.T) {
	sess, fs, note := lockFixture(t)
	fs.lockAsOther(note.ID, "pane w2:p1")

	msg := resolve(t, acquireLockCmd(fs, &note, sess.user.GUID, false))
	acquired, ok := msg.(lockAcquiredMsg)
	if !ok {
		t.Fatalf("the acquire produced %T, want lockAcquiredMsg", msg)
	}
	if acquired.lock != nil {
		t.Fatal("a note held by another session was handed over anyway")
	}
	if acquired.blockedBy == nil {
		t.Fatal("the refusal names nobody; the dialog would have no holder to show")
	}
	if acquired.blockedBy.Holder.Label != "pane w2:p1" {
		t.Fatalf("the refusal names %q as the holder", acquired.blockedBy.Holder.Label)
	}

	// And the browse screen turns that into the dialog rather than the form.
	browse := newBrowseScreen(sess)
	next, cmd := browse.Update(acquired)
	if _, ok := next.(*browseScreen); !ok {
		t.Fatalf("browse became %T", next)
	}
	push, ok := resolve(t, cmd).(pushMsg)
	if !ok {
		t.Fatalf("a refused lock pushed %T, want a pushMsg", resolve(t, cmd))
	}
	if _, ok := push.s.(*lockedScreen); !ok {
		t.Fatalf("a refused lock pushed %T, want *lockedScreen", push.s)
	}
}

func TestEditingAFreeNoteOpensTheForm(t *testing.T) {
	sess, fs, note := lockFixture(t)

	acquired := resolve(t, acquireLockCmd(fs, &note, sess.user.GUID, false)).(lockAcquiredMsg)
	if acquired.lock == nil {
		t.Fatalf("an unheld note was refused: err=%v blocked=%v", acquired.err, acquired.blockedBy)
	}

	browse := newBrowseScreen(sess)
	_, cmd := browse.Update(acquired)
	push := resolve(t, cmd).(pushMsg)
	if _, ok := push.s.(*formScreen); !ok {
		t.Fatalf("a granted lock pushed %T, want *formScreen", push.s)
	}
}

// ---- The contention dialog ---------------------------------------------------

func TestTheContentionDialogOffersReadOnlyAndSteal(t *testing.T) {
	sess, fs, note := lockFixture(t)
	held := fs.lockAsOther(note.ID, "pane w2:p1")

	readOnlyRan := false
	s := newLockedScreen(sess, &note, held, func() tea.Cmd {
		readOnlyRan = true
		return nil
	})

	// The view has to name the holder — that is the information the whole
	// dialog exists to deliver.
	view := s.View()
	if !strings.Contains(view, "pane w2:p1") {
		t.Fatalf("the dialog does not name the holder:\n%s", view)
	}
	if !strings.Contains(view, note.Title) {
		t.Fatalf("the dialog does not name the note:\n%s", view)
	}

	// r → read-only, via the caller's callback.
	_, cmd := s.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	resolve(t, cmd)
	if !readOnlyRan {
		t.Fatal("\"r\" did not run the read-only action")
	}
}

// Stealing is two keystrokes on purpose — t, then the confirmation — because it
// is the one action here that can cost somebody their work.
func TestStealingOpensTheFormAndDisplacesTheHolder(t *testing.T) {
	sess, fs, note := lockFixture(t)
	held := fs.lockAsOther(note.ID, "pane w2:p1")

	s := newLockedScreen(sess, &note, held, func() tea.Cmd { return nil })

	// "t" asks first.
	_, cmd := s.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	push, ok := resolve(t, cmd).(pushMsg)
	if !ok {
		t.Fatalf("\"t\" produced %T, want a pushMsg carrying a confirmation", resolve(t, cmd))
	}
	confirm, ok := push.s.(*confirmScreen)
	if !ok {
		t.Fatalf("\"t\" pushed %T, want *confirmScreen — a steal must be confirmed", push.s)
	}

	// Confirming performs the takeover.
	acquired, ok := resolve(t, confirm.onYes).(lockAcquiredMsg)
	if !ok {
		t.Fatalf("confirming produced %T, want lockAcquiredMsg", resolve(t, confirm.onYes))
	}
	if acquired.lock == nil {
		t.Fatalf("the steal was refused: err=%v blocked=%v", acquired.err, acquired.blockedBy)
	}
	if acquired.lock.StolenFrom != held.Holder.SessionID {
		t.Fatalf("the steal recorded StolenFrom=%q, want %q",
			acquired.lock.StolenFrom, held.Holder.SessionID)
	}

	// The displaced session's token is dead — which is exactly how it will find
	// out, on its next heartbeat.
	if _, err := models.RenewNoteLock(note.ID, held.Token); err == nil {
		t.Fatal("the displaced holder can still renew; it would never learn it was robbed")
	}

	// And the thief lands in the form.
	_, cmd = s.Update(acquired)
	if !pushesA[*formScreen](cmd) {
		t.Fatal("a successful steal never opened the form")
	}
}

// pushesA reports whether a command — flattened through any batch or sequence —
// pushes a screen of type T. The steal path returns a Sequence (pop, push,
// status), so the assertion has to look inside rather than at the outer message.
func pushesA[T screen](cmd tea.Cmd) bool {
	for _, msg := range drainSequence(cmd) {
		if p, ok := msg.(pushMsg); ok {
			if _, ok := p.s.(T); ok {
				return true
			}
		}
	}
	return false
}

// ---- The form's lease --------------------------------------------------------

// Leaving a form must give the note back immediately. Otherwise a colleague who
// opened a note and changed their mind holds it for the rest of the TTL.
func TestLeavingTheFormReleasesTheLock(t *testing.T) {
	sess, fs, note := lockFixture(t)

	if _, err := fs.AcquireNoteLock(note.ID, sess.user.GUID, sessionIdentity(), false); err != nil {
		t.Fatalf("setting up the lease failed: %v", err)
	}
	if models.GetNoteLock(note.ID) == nil {
		t.Fatal("the fixture never took the lease")
	}

	form := newFormScreen(sess, &note)
	// Drain the abandon path the way the event loop would.
	drain(t, form.abandon())

	if lock := models.GetNoteLock(note.ID); lock != nil {
		t.Fatalf("esc left the note locked by %q", lock.Holder.Label)
	}
}

func TestSavingReleasesTheLock(t *testing.T) {
	sess, fs, note := lockFixture(t)

	if _, err := fs.AcquireNoteLock(note.ID, sess.user.GUID, sessionIdentity(), false); err != nil {
		t.Fatalf("setting up the lease failed: %v", err)
	}

	form := newFormScreen(sess, &note)
	form.title.SetValue("Edited title")

	saved := resolve(t, form.save()).(noteSavedMsg)
	if saved.err != nil {
		t.Fatalf("the save failed: %v", saved.err)
	}
	drain(t, secondReturn(form.Update(saved)))

	if lock := models.GetNoteLock(note.ID); lock != nil {
		t.Fatalf("a successful save left the note locked by %q", lock.Holder.Label)
	}
}

// The form's save must name the version it loaded, or the whole optimistic
// guard is dead code on the path that matters most.
func TestTheFormSavesAgainstTheVersionItLoaded(t *testing.T) {
	sess, fs, note := lockFixture(t)
	fs.AcquireNoteLock(note.ID, sess.user.GUID, sessionIdentity(), false)

	// Somebody else saves first, moving the version out from under this form.
	models.ResetNoteLocksForTest() // let the other writer through the gate
	if _, err := fs.UpdateNote(note.ID, models.NoteInput{
		GUID: note.GUID, Title: "their title",
	}, sess.user.GUID); err != nil {
		t.Fatalf("the competing write failed: %v", err)
	}

	form := newFormScreen(sess, &note)
	form.title.SetValue("my title")

	saved := resolve(t, form.save()).(noteSavedMsg)
	stale, ok := staleWrite(saved.err)
	if !ok {
		t.Fatalf("the save was accepted (err=%v); the version guard never fired", saved.err)
	}
	if stale.Current == nil || stale.Current.Title != "their title" {
		t.Fatalf("the refusal reports %v as the stored note", stale.Current)
	}

	// The form turns that into the fork dialog, not an error message.
	_, cmd := form.Update(saved)
	push, ok := resolve(t, cmd).(pushMsg)
	if !ok {
		t.Fatalf("a stale save produced %T, want a pushMsg", resolve(t, cmd))
	}
	if _, ok := push.s.(*staleScreen); !ok {
		t.Fatalf("a stale save pushed %T, want *staleScreen", push.s)
	}
}

// "Overwrite theirs" has to actually land — a fork whose answers do not work is
// worse than no fork.
func TestOverwritingAfterAStaleSaveLands(t *testing.T) {
	sess, fs, note := lockFixture(t)

	if _, err := fs.UpdateNote(note.ID, models.NoteInput{
		GUID: note.GUID, Title: "their title",
	}, sess.user.GUID); err != nil {
		t.Fatalf("the competing write failed: %v", err)
	}
	current, _ := fs.GetNoteByID(note.ID, sess.user.GUID)

	form := newFormScreen(sess, &note) // still holding the pre-write version
	form.title.SetValue("my title")

	saved := resolve(t, form.overwriteTheirs(current)).(noteSavedMsg)
	if saved.err != nil {
		t.Fatalf("the overwrite was refused: %v", saved.err)
	}
	final, _ := fs.GetNoteByID(note.ID, sess.user.GUID)
	if final.Title != "my title" {
		t.Fatalf("after the overwrite the note says %q, want \"my title\"", final.Title)
	}
}

// "Load theirs" replaces the form and leaves it clean, so esc does not then stop
// to ask about edits the user just chose to abandon.
func TestLoadingTheirVersionReplacesTheFormAndClearsDirty(t *testing.T) {
	sess, fs, note := lockFixture(t)

	fs.UpdateNote(note.ID, models.NoteInput{GUID: note.GUID, Title: "their title"}, sess.user.GUID)
	current, _ := fs.GetNoteByID(note.ID, sess.user.GUID)

	form := newFormScreen(sess, &note)
	form.title.SetValue("my title")
	if !form.dirty() {
		t.Fatal("the fixture's form is not dirty; the test proves nothing")
	}

	resolve(t, form.adoptTheirs(current))

	if form.title.Value() != "their title" {
		t.Fatalf("the form still says %q after loading theirs", form.title.Value())
	}
	if form.dirty() {
		t.Fatal("the form is dirty right after adopting the stored version")
	}
}

// Losing the lock mid-edit must be visible immediately, and must not touch the
// user's text.
func TestLosingTheLockRaisesTheBannerAndKeepsTheText(t *testing.T) {
	sess, _, note := lockFixture(t)

	form := newFormScreen(sess, &note)
	form.body.SetValue("half a paragraph the user is in the middle of")

	thief := &models.NoteLock{
		NoteID: note.ID,
		Holder: models.LockHolder{SessionID: "other", Label: "pane w4:p2"},
	}
	next, _ := form.Update(lockLostMsg{noteID: note.ID, lock: thief})
	form = next.(*formScreen)

	if !form.lockLost {
		t.Fatal("losing the lease did not raise the banner")
	}
	if !strings.Contains(form.View(), "pane w4:p2") {
		t.Fatalf("the banner does not name who took the note:\n%s", form.View())
	}
	if !strings.Contains(form.body.Value(), "half a paragraph") {
		t.Fatal("losing the lease disturbed the user's text")
	}
}

// ---- Badges ------------------------------------------------------------------

// The badge answers "can I edit this right now", so it must appear for other
// sessions' locks and NOT for this session's own.
func TestTheListBadgesOtherSessionsButNotItself(t *testing.T) {
	sess, fs, note := lockFixture(t)
	mine := fs.seedNote(sess.user.GUID, "My open note", "body")

	fs.lockAsOther(note.ID, "pane w2:p1")
	fs.AcquireNoteLock(mine.ID, sess.user.GUID, sessionIdentity(), false)

	browse := newBrowseScreen(sess)
	loaded := resolve(t, loadLocksCmd(fs, sess.user.GUID)).(locksLoadedMsg)
	if loaded.err != nil {
		t.Fatalf("loading locks failed: %v", loaded.err)
	}
	next, _ := browse.Update(loaded)
	browse = next.(*browseScreen)

	if got := browse.heldBy(note.ID); got != "pane w2:p1" {
		t.Fatalf("a note held elsewhere reports heldBy=%q, want \"pane w2:p1\"", got)
	}
	if got := browse.heldBy(mine.ID); got != "" {
		t.Fatalf("this session's own lease badged the row as held by %q", got)
	}
}

// ---- helpers ------------------------------------------------------------------

// drain runs a command and every arm of any batch or sequence it produces, the
// way the event loop eventually would.
//
// It has to see through BOTH wrappers, because the release rides inside one: a
// form's exit is a tea.Sequence of release-then-pop, so a test that ran only the
// outer command would find the lease still held and conclude the wrong thing.
// drainSequence (subcategories_test.go) already does the flattening, including
// the reflection tea.Sequence's unexported type requires.
func drain(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	drainSequence(cmd)
}

// secondReturn discards a screen and keeps the command, so an Update call can be
// fed straight to drain.
func secondReturn(_ screen, cmd tea.Cmd) tea.Cmd { return cmd }
