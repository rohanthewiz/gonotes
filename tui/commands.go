package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gonotes/models"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"github.com/rohanthewiz/serr"
)

// This file holds every asynchronous operation the TUI performs, expressed as
// tea.Cmd functions plus the message types they resolve to.
//
// Design choice: all data access happens inside tea.Cmd closures, never in
// Update(). Bubble Tea runs commands on separate goroutines, so storage
// latency (bytdb disk writes, or a round trip to the server in HTTP mode)
// never blocks rendering or key handling. Each message carries its own err
// field; screens surface failures through the shared status bar rather than
// crashing the program.
//
// Every command takes the Store as its first argument rather than reaching for
// models.* directly. Callers pass s.sess.store, which is set once at startup —
// see store.go for why the seam exists and tui.go for where it is threaded in.

// ---- Navigation messages -------------------------------------------------
// Screens never mutate the root's stack directly; they emit these messages
// and the root model performs the push/pop. This keeps navigation ordering
// deterministic (via tea.Sequence) and screens decoupled from the root.

type pushMsg struct{ s screen }

type popMsg struct {
	refresh bool // when true, the newly exposed screen is asked to reload its data
}

func push(s screen) tea.Cmd {
	return func() tea.Msg { return pushMsg{s: s} }
}

func pop(refresh bool) tea.Cmd {
	return func() tea.Msg { return popMsg{refresh: refresh} }
}

// statusNote sets the one-line feedback message at the bottom of the screen.
type statusNote struct {
	text  string
	isErr bool
}

func status(text string) tea.Cmd {
	return func() tea.Msg { return statusNote{text: text} }
}

func statusErr(err error, context string) tea.Cmd {
	return func() tea.Msg { return statusNote{text: context + ": " + err.Error(), isErr: true} }
}

// ---- Auth ------------------------------------------------------------------

type usernamesLoadedMsg struct {
	names []string
	err   error
}

// bootstrapCmd is the login screen's opening move. It asks the store whether
// the user is already authenticated — a cached token or environment
// credentials in HTTP mode — and only falls back to loading the username list
// for the prefill when they are not.
//
// The two live in one command because they are alternatives, not a pair: if
// the session resumes, the login screen is replaced outright and the username
// list would be answering a question nobody asked. Sequencing them here also
// means the resume decision is made before the screen renders anything the
// user could type into.
//
// A resume error is swallowed deliberately. "Could not resume" is not a
// failure the user needs to read; it just means the login screen stays.
func bootstrapCmd(st Store) tea.Cmd {
	return func() tea.Msg {
		if user, err := st.ResumeSession(); err == nil && user != nil {
			return loggedInMsg{user: user}
		}
		names, err := st.ListUsernames()
		return usernamesLoadedMsg{names: names, err: err}
	}
}

// loggedInMsg is handled by the root model: it stores the user in the shared
// session and swaps the login screen for the notes browser.
type loggedInMsg struct{ user *models.User }

type loginFailedMsg struct{ err error }

func loginCmd(st Store, username, password string) tea.Cmd {
	return func() tea.Msg {
		user, err := st.AuthenticateUser(username, password)
		if err != nil {
			return loginFailedMsg{err: err}
		}
		if user == nil {
			// The local store reports bad credentials this way (nil user, nil
			// error); without this the screen would sit on "Signing in..."
			// forever after a typo.
			return loginFailedMsg{err: serr.New("invalid username or password")}
		}
		return loggedInMsg{user: user}
	}
}

func registerCmd(st Store, username, password string) tea.Cmd {
	return func() tea.Msg {
		user, err := st.CreateUser(username, password)
		if err != nil {
			return loginFailedMsg{err: err}
		}
		return loggedInMsg{user: user}
	}
}

// ---- Notes -----------------------------------------------------------------

type notesLoadedMsg struct {
	notes []models.Note
	err   error
}

// loadNotesCmd fetches all of the user's notes — see Store.ListNotes for why
// there is no pagination.
func loadNotesCmd(st Store, userGUID string) tea.Cmd {
	return func() tea.Msg {
		notes, err := st.ListNotes(userGUID)
		return notesLoadedMsg{notes: notes, err: err}
	}
}

// loadCategoryNotesCmd fetches only the notes attached to one category —
// used when a category filter is active in the browser.
func loadCategoryNotesCmd(st Store, categoryID int64, userGUID string) tea.Cmd {
	return func() tea.Msg {
		notes, err := st.GetCategoryNotes(categoryID, userGUID)
		return notesLoadedMsg{notes: notes, err: err}
	}
}

type noteLoadedMsg struct {
	note *models.Note
	err  error
}

func loadNoteCmd(st Store, id int64, userGUID string) tea.Cmd {
	return func() tea.Msg {
		note, err := st.GetNoteByID(id, userGUID)
		return noteLoadedMsg{note: note, err: err}
	}
}

type noteSavedMsg struct {
	note *models.Note
	err  error
}

// saveNoteCmd creates or updates a note, then reconciles its category
// assignments. noteID == 0 means create. categoriesCSV is the raw
// comma-separated names from the form; unknown categories are created on the
// fly so assigning a brand-new category doesn't require a separate trip to
// the category screen.
func saveNoteCmd(st Store, noteID int64, input models.NoteInput, categoriesCSV string, userGUID string) tea.Cmd {
	return func() tea.Msg {
		var note *models.Note
		var err error

		if noteID == 0 {
			// Both stores expect the caller to supply the GUID (the web API
			// does the same), so we generate one here.
			input.GUID = uuid.New().String()
			note, err = st.CreateNote(input, userGUID)
		} else {
			note, err = st.UpdateNote(noteID, input, userGUID)
		}
		if err != nil {
			return noteSavedMsg{err: err}
		}
		if note == nil {
			return noteSavedMsg{err: serr.New("note not found or not owned by you")}
		}

		if err = syncNoteCategories(st, note.ID, categoriesCSV, userGUID); err != nil {
			// The note itself saved fine; report the category issue but
			// still return the note so the UI reflects the save.
			return noteSavedMsg{note: note, err: serr.Wrap(err, "note saved, but category update failed")}
		}
		return noteSavedMsg{note: note}
	}
}

// syncNoteCategories makes the note's category set match the given
// comma-separated names: missing links are added (creating categories as
// needed) and links no longer named are removed.
func syncNoteCategories(st Store, noteID int64, categoriesCSV string, userGUID string) error {
	// Parse the desired set, preserving nothing but trimmed, non-empty names.
	desired := map[string]bool{}
	for _, raw := range strings.Split(categoriesCSV, ",") {
		if name := strings.TrimSpace(raw); name != "" {
			desired[name] = true
		}
	}

	current, err := st.GetNoteCategories(noteID, userGUID)
	if err != nil {
		return serr.Wrap(err, "failed to load current note categories")
	}

	// Remove links that are no longer desired.
	currentByName := map[string]models.Category{}
	for _, c := range current {
		currentByName[c.Name] = c
		if !desired[c.Name] {
			if err = st.RemoveCategoryFromNote(noteID, c.ID); err != nil {
				return serr.Wrap(err, "failed to remove category "+c.Name)
			}
		}
	}

	// Add links that don't exist yet, creating unknown categories on the fly.
	for name := range desired {
		if _, exists := currentByName[name]; exists {
			continue
		}
		cat, err := st.GetCategoryByName(name, userGUID)
		if err != nil {
			return serr.Wrap(err, "failed to look up category "+name)
		}
		if cat == nil {
			cat, err = st.CreateCategory(name, userGUID)
			if err != nil {
				return serr.Wrap(err, "failed to create category "+name)
			}
		}
		if err = st.AddCategoryToNote(noteID, cat.ID, userGUID); err != nil {
			return serr.Wrap(err, "failed to attach category "+name)
		}
	}
	return nil
}

type noteDeletedMsg struct{ err error }

func deleteNoteCmd(st Store, id int64, userGUID string) tea.Cmd {
	return func() tea.Msg {
		ok, err := st.DeleteNote(id, userGUID)
		if err == nil && !ok {
			err = serr.New("note not found or not owned by you")
		}
		return noteDeletedMsg{err: err}
	}
}

type flagToggledMsg struct {
	note *models.Note
	err  error
}

func toggleFlagCmd(st Store, id int64, userGUID string) tea.Cmd {
	return func() tea.Msg {
		note, err := st.ToggleNoteFlag(id, userGUID)
		return flagToggledMsg{note: note, err: err}
	}
}

// ---- Categories ------------------------------------------------------------

type categoriesLoadedMsg struct {
	cats []models.Category
	err  error
}

func loadCategoriesCmd(st Store, userGUID string) tea.Cmd {
	return func() tea.Msg {
		cats, err := st.ListCategories(userGUID)
		return categoriesLoadedMsg{cats: cats, err: err}
	}
}

// categoryPickedMsg is emitted by the category screen after it pops itself;
// the browse screen (newly on top) receives it and applies the filter.
// A nil cat means "show all notes" (clear the filter).
type categoryPickedMsg struct{ cat *models.Category }

type categoryCreatedMsg struct{ err error }

func createCategoryCmd(st Store, name string, userGUID string) tea.Cmd {
	return func() tea.Msg {
		_, err := st.CreateCategory(name, userGUID)
		return categoryCreatedMsg{err: err}
	}
}

type categoryDeletedMsg struct{ err error }

func deleteCategoryCmd(st Store, id int64, userGUID string) tea.Cmd {
	return func() tea.Msg {
		return categoryDeletedMsg{err: st.DeleteCategory(id, userGUID)}
	}
}

// noteCatsLoadedMsg carries the categories attached to a single note
// (used by the detail header and to prefill the form's category field).
type noteCatsLoadedMsg struct {
	cats []models.Category
	err  error
}

func loadNoteCategoriesCmd(st Store, noteID int64, userGUID string) tea.Cmd {
	return func() tea.Msg {
		cats, err := st.GetNoteCategories(noteID, userGUID)
		return noteCatsLoadedMsg{cats: cats, err: err}
	}
}

// ---- External editor ---------------------------------------------------------

type editorFinishedMsg struct {
	body string
	err  error
}

// openEditorCmd writes the current body to a temp markdown file, suspends the
// TUI, and hands the terminal to $VISUAL/$EDITOR (vim as a last resort).
// When the editor exits we read the file back and restore the TUI.
// This gives full-power editing (syntax highlighting, macros, etc.) for long
// notes, which a textarea widget can't match.
//
// This is the longest span the TUI has — the user is in another program, for
// as long as they like — which makes it the one worth telling the host about:
// cats badges the pane while it runs and turns the working→idle edge into a
// "finished" toast and phone push. cs is the reporter seam (nil-safe, inert
// outside cats); noteTitle names the note in that badge.
//
// The span is opened HERE rather than at the call site because only this
// function knows whether an editor is actually going to run: the temp-file
// write can fail, and a "working" report for an editor that never launched
// would leave the pane badged until the next transition. The closing edge is
// in form.go, on editorFinishedMsg — which every path below resolves to.
//
// Note that openEditorCmd runs on the event loop (it is called from a screen's
// Update); only the tea.Cmd it returns runs on its own goroutine. That is what
// makes the cs.working call safe — see the threading rule in cats_glue.go.
func openEditorCmd(cs *catsState, currentBody, noteTitle string) tea.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vim"
	}

	tmp := filepath.Join(os.TempDir(), "gonotes-edit-"+uuid.New().String()+".md")
	if err := os.WriteFile(tmp, []byte(currentBody), 0o600); err != nil {
		wrapped := serr.Wrap(err, "failed to create temp file for editor")
		return func() tea.Msg { return editorFinishedMsg{err: wrapped} }
	}

	cs.working(editorActivity(noteTitle))

	c := exec.Command(editor, tmp)
	return tea.ExecProcess(c, func(execErr error) tea.Msg {
		defer os.Remove(tmp) // best-effort cleanup; the file may hold decrypted text
		if execErr != nil {
			return editorFinishedMsg{err: serr.Wrap(execErr, "editor exited with error")}
		}
		content, err := os.ReadFile(tmp)
		if err != nil {
			return editorFinishedMsg{err: serr.Wrap(err, "failed to read editor output")}
		}
		return editorFinishedMsg{body: string(content)}
	})
}

// editorActivity is the phrase the host shows while the editor is open. An
// untitled note still gets a phrase rather than a bare "editing:", because the
// status is read on a phone with no other context.
//
// It is not truncated here: the reporter clamps to what cats will actually
// display (32 bytes, on a rune boundary), and doing it twice would mean two
// places that have to agree about the limit.
func editorActivity(noteTitle string) string {
	if t := strings.TrimSpace(noteTitle); t != "" {
		return "editing: " + t
	}
	return "editing a note"
}
