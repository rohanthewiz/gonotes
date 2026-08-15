package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"gonotes/models"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
)

// These are the TUI's first tests, added alongside the Bubble Tea v2
// migration. Their job is not to pin pixel-exact output (that comes with the
// Phase 3 goldens) but to catch the two ways a v1→v2 port fails silently:
//
//  1. Key names drift. Every screen dispatches on msg.String(), so a rename
//     upstream turns a binding into dead code with no compile error and no
//     test failure — the key simply stops working. v2 already did this once:
//     the space bar went from " " to "space". TestKeyNames pins every string
//     the screens match on.
//  2. Rendering stops. v2 moved the alternate screen and the view content
//     into a returned tea.View; getting that wrong yields a program that runs
//     happily and draws nothing. The boot tests assert real text arrives.
//
// Note on the module path, because it is a genuine trap: the four charm
// libraries live at charm.land/<lib>/v2, but teatest does NOT — it is still
// github.com/charmbracelet/x/exp/teatest/v2. The go.mod of each declares
// which, and requiring the wrong one fails with a module-path mismatch.

// ---- Key names -------------------------------------------------------------

// keyPress builds the KeyPressMsg a terminal would produce for a printable
// character, mirroring what the input parser fills in: Code is the rune and
// Text carries the printable form.
func keyPress(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// TestKeyNames pins the stringification of every key the screens bind, so a
// future upstream rename fails here instead of quietly disabling a feature.
//
// The expectations are grouped by the file whose switch statement consumes
// them; adding a binding means adding a line here.
func TestKeyNames(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		// Global (tui.go)
		{"ctrl+c quits from anywhere", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "ctrl+c"},

		// browse.go / categories.go / detail.go / confirm.go single letters
		{"quit", keyPress('q'), "q"},
		{"new", keyPress('n'), "n"},
		{"edit", keyPress('e'), "e"},
		{"delete", keyPress('d'), "d"},
		{"flag", keyPress('f'), "f"},
		{"categories", keyPress('c'), "c"},
		{"all notes", keyPress('a'), "a"},
		{"confirm yes", keyPress('y'), "y"},

		// confirm.go also accepts the shifted forms. A shifted letter arrives
		// with Text already uppercased, which is what String() returns.
		{"confirm yes uppercase", tea.KeyPressMsg{Code: 'y', ShiftedCode: 'Y', Text: "Y", Mod: tea.ModShift}, "Y"},
		{"confirm no uppercase", tea.KeyPressMsg{Code: 'n', ShiftedCode: 'N', Text: "N", Mod: tea.ModShift}, "N"},

		// Navigation and submission, shared by login.go and form.go
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}, "esc"},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, "enter"},
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}, "tab"},
		{"shift+tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "shift+tab"},
		{"up", tea.KeyPressMsg{Code: tea.KeyUp}, "up"},
		{"down", tea.KeyPressMsg{Code: tea.KeyDown}, "down"},

		// form.go control chords
		{"save", tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}, "ctrl+s"},
		{"external editor", tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}, "ctrl+e"},

		// capture.go: the door into the agent-pane picker.
		{"capture from an agent pane", tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}, "ctrl+g"},

		// The one that actually changed in v2. Space is the only printable
		// character with an invisible literal form, so v2 names it rather
		// than emitting " ". form.go's private-toggle depends on this.
		{"space toggles the private checkbox", tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, "space"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.msg.String(); got != tc.want {
				t.Errorf("key %q stringifies as %q, want %q — a binding that matches %q is now dead code",
					tc.name, got, tc.want, tc.want)
			}
		})
	}
}

// TestSpaceIsNotLiteralSpace states the v1→v2 break directly, so the reason
// this suite exists survives even if the table above is refactored.
func TestSpaceIsNotLiteralSpace(t *testing.T) {
	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	if space.String() == " " {
		t.Fatal(`space stringified as " " (the v1 form); form.go matches "space"`)
	}
}

// ---- Boot flows ------------------------------------------------------------

// setupTestDB opens a throwaway database in a temp dir and registers the
// cleanup. Returns the created user so tests can skip the login screen.
func setupTestDB(t *testing.T) *models.User {
	t.Helper()

	if err := models.InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("InitTestDB: %v", err)
	}
	t.Cleanup(func() { models.CloseDB() })

	user, err := models.CreateUser(models.UserRegisterInput{
		Username: "tui_tester",
		Password: "test-password-123",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return user
}

// storeFixture builds one of the Store implementations the boot flows below
// run against.
//
// Running each flow twice is the point of the Phase 4 seam. The local case
// proves the pass-through wrappers really reach bytdb; the fake case proves
// the screens depend on the *interface* and nothing else — if a screen ever
// reaches around the Store back into models.*, the fake run is where it
// shows, because there is no database open at all.
type storeFixture struct {
	name  string
	build func(t *testing.T) (Store, *models.User)
}

func storeFixtures() []storeFixture {
	return []storeFixture{
		{
			name: "local",
			build: func(t *testing.T) (Store, *models.User) {
				return NewLocalStore(), setupTestDB(t)
			},
		},
		{
			name: "fake",
			build: func(t *testing.T) (Store, *models.User) {
				fs := newFakeStore()
				return fs, fs.addUser("tui_tester", "test-password-123")
			},
		},
	}
}

// waitForAll blocks until everything in want has appeared in the program's
// output. A substring check over the accumulated bytes is the reliable
// assertion here — the alternate-screen renderer positions the cursor per
// line and repaints single cells as the input cursor blinks, so whole frames
// never appear intact.
//
// This must be ONE call per program, not one per string. teatest's Output()
// hands back a single consumable stream: a second WaitFor starts accumulating
// from wherever the first one stopped reading, so text that was already
// pulled into the first call's buffer is simply gone. That failure looks
// exactly like a rendering bug, which cost a debugging round to work out.
func waitForAll(t *testing.T, tm *teatest.TestModel, want ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(),
		func(b []byte) bool {
			for _, w := range want {
				if !bytes.Contains(b, []byte(w)) {
					return false
				}
			}
			return true
		},
		teatest.WithCheckInterval(20*time.Millisecond),
		teatest.WithDuration(5*time.Second),
	)
}

func TestLoginScreenRenders(t *testing.T) {
	for _, fx := range storeFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			st, _ := fx.build(t)

			tm := teatest.NewTestModel(t, newAppModel(st), teatest.WithInitialTermSize(100, 40))

			// The store reports exactly one user, so the login screen prefills
			// the username and jumps focus to the password field — a code path
			// worth exercising because it depends on a command's result
			// reaching Update.
			waitForAll(t, tm, "Sign in", "tui_tester", "Password")

			tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
		})
	}
}

func TestBrowseScreenRendersSeededNotes(t *testing.T) {
	for _, fx := range storeFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			st, user := fx.build(t)

			// Seed through the Store rather than models.* so the same lines
			// work for both fixtures — which is itself a small proof that the
			// interface covers what the TUI needs.
			body := "# Heading\n\nSome body text."
			for _, title := range []string{"Alpha note", "Beta note"} {
				if _, err := st.CreateNote(models.NoteInput{
					GUID:  "tui-" + title,
					Title: title,
					Body:  &body,
				}, user.GUID); err != nil {
					t.Fatalf("CreateNote %q: %v", title, err)
				}
			}

			tm := teatest.NewTestModel(t, newAppModel(st), teatest.WithInitialTermSize(100, 40))

			// Skip the credential dance: loggedInMsg is exactly what a
			// successful loginCmd emits, and the root handles it by replacing
			// the stack with the browse screen. This keeps the test about
			// rendering, not bcrypt.
			tm.Send(loggedInMsg{user: user})
			// ...then re-assert the size. WithInitialTermSize delivers one
			// WindowSizeMsg at startup, and nothing orders it against a message
			// we inject this early; if loggedInMsg wins the race the browse
			// list sizes itself to 0x0 and draws nothing. In a real terminal
			// the resize always arrives seconds before a login, so this is
			// harness bookkeeping rather than a behavior the app needs.
			tm.Send(tea.WindowSizeMsg{Width: 100, Height: 40})

			// "notes" is the plural the list widget puts in its own status bar,
			// so matching it proves the widget sized itself and drew rows
			// rather than the screen merely printing a title.
			waitForAll(t, tm, "GoNotes", "Alpha note", "Beta note", "notes")

			tm.Send(tea.KeyPressMsg{Code: 'q'})
			tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
		})
	}
}

// TestResumedSessionSkipsTheLoginScreen covers the HTTP-mode shortcut: a store
// that can answer ResumeSession (a cached token, or credentials in the
// environment) must land on the notes list without the login screen ever
// taking a keystroke.
//
// The fake expresses that with a field rather than a token, which is the point
// — the behavior belongs to the boot sequence in bootstrapCmd, not to HTTP.
func TestResumedSessionSkipsTheLoginScreen(t *testing.T) {
	fs := newFakeStore()
	user := fs.addUser("tui_tester", "test-password-123")
	fs.resume = user
	fs.seedNote(user.GUID, "Resumed note", "body")

	tm := teatest.NewTestModel(t, newAppModel(fs), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.WindowSizeMsg{Width: 100, Height: 40})

	// The note's title can only be on screen if the browse list loaded, which
	// can only happen if loggedInMsg arrived without a password being typed.
	waitForAll(t, tm, "GoNotes", "Resumed note")

	tm.Send(tea.KeyPressMsg{Code: 'q'})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestLoginPrefillsFromEnvWhenUsernamesAreUnavailable pins the HTTP-mode
// degradation in login.go: ErrNoUserList is not an error to display, and it
// must not flip the screen into first-run registration mode.
func TestLoginPrefillsFromEnvWhenUsernamesAreUnavailable(t *testing.T) {
	t.Setenv(envUser, "env_user")

	fs := newFakeStore()
	fs.failWith = ErrNoUserList // ListUsernames now declines, as httpStore does

	tm := teatest.NewTestModel(t, newAppModel(fs), teatest.WithInitialTermSize(100, 40))

	// "Sign in" (not "Create your account") plus the env-supplied username.
	waitForAll(t, tm, "Sign in", "env_user")

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestBadPasswordClearsTheBusyFlag guards a path that had no test and did not
// work: the local store reports invalid credentials as (nil user, nil error),
// so a screen that only checked err left `busy` set — every subsequent key was
// ignored and the screen sat on "Signing in..." forever. loginCmd now turns
// that into a loginFailedMsg.
func TestBadPasswordClearsTheBusyFlag(t *testing.T) {
	fs := newFakeStore()
	fs.addUser("tui_tester", "correct-password")

	s := newLoginScreen(&session{store: fs, width: 80, height: 24})
	s.username.SetValue("tui_tester")
	s.password.SetValue("wrong-password")

	cmd := s.submit()
	if cmd == nil {
		t.Fatal("submit returned no command")
	}
	if !s.busy {
		t.Fatal("submit should mark the screen busy while the attempt is in flight")
	}

	msg := cmd()
	failed, ok := msg.(loginFailedMsg)
	if !ok {
		t.Fatalf("a wrong password produced %T, want loginFailedMsg", msg)
	}

	updated, _ := s.Update(failed)
	if updated.(*loginScreen).busy {
		t.Error("busy must clear on failure, or the screen stops accepting keys")
	}
}

// TestAltScreenIsRequested guards the v2 change that is easiest to lose: the
// alternate screen moved from the tea.WithAltScreen() program option to a
// field on the returned tea.View. Bubble Tea emits the smcup escape sequence
// (CSI ? 1049 h) only if the view asks for it, so the byte sequence is
// asserted in the same stream as the content — reading FinalOutput separately
// would find the enter-altscreen bytes already consumed.
func TestAltScreenIsRequested(t *testing.T) {
	// No database: this is a rendering assertion, and the fake store is enough
	// to get a login screen on the wire. It needs an account, though — an
	// empty store means a fresh install, and the screen says "Create your
	// account" instead.
	fs := newFakeStore()
	fs.addUser("tui_tester", "test-password-123")

	tm := teatest.NewTestModel(t, newAppModel(fs), teatest.WithInitialTermSize(100, 40))
	waitForAll(t, tm, "\x1b[?1049h", "Sign in")

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestRootViewCarriesContent asserts the root model puts the screen stack's
// output on the View rather than dropping it — the failure mode of a
// half-finished View() string → View() tea.View conversion is a program that
// renders an empty frame forever.
func TestRootViewCarriesContent(t *testing.T) {
	fs := newFakeStore()
	fs.addUser("tui_tester", "test-password-123")
	m := newAppModel(fs)

	// Before any WindowSizeMsg there is no size, so the view is deliberately
	// blank — but it must still request the alternate screen.
	if v := m.View(); v.Content != "" {
		t.Errorf("expected empty content before the first resize, got %q", v.Content)
	} else if !v.AltScreen {
		t.Error("AltScreen must be set even on the pre-resize view")
	}

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	v := updated.View()
	if !strings.Contains(v.Content, "Sign in") {
		t.Errorf("root view is missing the login screen's content, got %q", v.Content)
	}
	if !v.AltScreen {
		t.Error("AltScreen must be set on the rendered view")
	}
}
