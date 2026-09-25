package tui

import (
	"strings"
	"testing"

	"gonotes/models"

	tea "charm.land/bubbletea/v2"
)

// The v1 envelope as cats-todo writes it, with every key set.
const fullEnvelope = `<!-- cats-note v1 -->
---
title: pane.list quirk
description: from cats-todo · cats
tags: [cats-todo, info]
categories: [Work/backend]
---

First paragraph.

Second paragraph.
`

// ---- parsing ---------------------------------------------------------------

func TestParseIntakeReadsEveryV1Key(t *testing.T) {
	note, ok, err := parseIntake(fullEnvelope)
	if !ok || err != nil {
		t.Fatalf("parseIntake = ok %v, err %v; want an envelope", ok, err)
	}
	want := intakeNote{
		title:      "pane.list quirk",
		desc:       "from cats-todo · cats",
		tags:       "cats-todo,info",
		categories: "Work/backend",
		// The blank line after the frontmatter and the trailing newline are
		// envelope padding; the blank line between paragraphs is content.
		body: "First paragraph.\n\nSecond paragraph.",
	}
	if note != want {
		t.Errorf("parseIntake =\n  %+v\nwant\n  %+v", note, want)
	}
}

// A pasted newline is typed as CR by a terminal. Whichever form arrives, the
// envelope must parse the same — the sentinel is line-anchored, so a CR left on
// it would make every paste read as "not an envelope".
func TestParseIntakeAcceptsTerminalLineEndings(t *testing.T) {
	for name, text := range map[string]string{
		"crlf": strings.ReplaceAll(fullEnvelope, "\n", "\r\n"),
		"cr":   strings.ReplaceAll(fullEnvelope, "\n", "\r"),
	} {
		note, ok, err := parseIntake(text)
		if !ok || err != nil || note.title != "pane.list quirk" || note.body != "First paragraph.\n\nSecond paragraph." {
			t.Errorf("%s: parseIntake = %+v, ok %v, err %v", name, note, ok, err)
		}
	}
}

func TestParseIntakeWithoutFrontmatterIsAllBody(t *testing.T) {
	note, ok, err := parseIntake("<!-- cats-note v1 -->\nJust a body.\n")
	if !ok || err != nil || note.body != "Just a body." || note.title != "" {
		t.Errorf("parseIntake = %+v, ok %v, err %v; want body only", note, ok, err)
	}
	// An opening rule with no closing one is a body that starts with a rule,
	// the reading import-md gives the same file.
	note, ok, err = parseIntake("<!-- cats-note v1 -->\n---\nnot: frontmatter\n")
	if !ok || err != nil || note.body != "---\nnot: frontmatter" {
		t.Errorf("unterminated: parseIntake = %+v, ok %v, err %v; want it kept as body", note, ok, err)
	}
}

// Only a paste that STARTS with the sentinel is an envelope. Everything else
// must fall through to the focused widget untouched — that is every ordinary
// paste the TUI has ever received.
func TestParseIntakeIgnoresOrdinaryPastes(t *testing.T) {
	for _, text := range []string{
		"",
		"hello",
		"some notes\n<!-- cats-note v1 -->\n---\ntitle: x\n---\n", // a mention, not a header
		"<!-- cats-note -->\nno version",
	} {
		if _, ok, _ := parseIntake(text); ok {
			t.Errorf("parseIntake(%q) claimed an ordinary paste as an envelope", text)
		}
	}
}

func TestParseIntakeRefusesWhatItCannotRead(t *testing.T) {
	if _, ok, err := parseIntake("<!-- cats-note v2 -->\nbody"); !ok || err == nil {
		t.Errorf("a newer envelope: ok %v, err %v; want it claimed and refused", ok, err)
	}
	if _, ok, err := parseIntake("<!-- cats-note v1 -->\n---\ntags: [unclosed\n---\nbody"); !ok || err == nil {
		t.Errorf("broken YAML: ok %v, err %v; want it claimed and refused", ok, err)
	}
}

// ---- delivery --------------------------------------------------------------

// The note opens OVER whatever the user was doing, as its own unsaved form. The
// form underneath — here, a half-typed note — must not receive a byte of it.
func TestIntakeOpensItsOwnFormOverTheActiveScreen(t *testing.T) {
	sess := testSession(newFakeStore())
	underneath := newFormScreen(sess, nil)
	underneath.title.SetValue("half-typed")
	m := appWith(sess, underneath)

	next, cmd := m.Update(tea.PasteMsg{Content: fullEnvelope})
	if got := next.(appModel).top(); got != screen(underneath) {
		t.Fatalf("the envelope was routed to the active screen (%T)", got)
	}
	if underneath.title.Value() != "half-typed" || underneath.body.Value() != "" {
		t.Error("the form underneath was edited by the envelope")
	}

	f, ok := pushedScreen(cmd).(*formScreen)
	if !ok {
		t.Fatal("the envelope did not push a note form")
	}
	v := f.values()
	if v.title != "pane.list quirk" || v.desc != "from cats-todo · cats" || v.tags != "cats-todo,info" ||
		v.categories != "Work/backend" || v.body != "First paragraph.\n\nSecond paragraph." {
		t.Errorf("form values = %+v", v)
	}
	if f.editing != nil {
		t.Error("an intake form must save as a new note")
	}
	if !f.dirty() {
		t.Error("an intake form must count as unsaved work, or a stray esc discards it silently")
	}
}

func TestOrdinaryPasteStillReachesTheActiveScreen(t *testing.T) {
	sess := testSession(newFakeStore())
	form := newFormScreen(sess, nil) // title field focused
	m := appWith(sess, form)

	m.Update(tea.PasteMsg{Content: "pasted title"})
	if form.title.Value() != "pasted title" {
		t.Errorf("title = %q; an ordinary paste no longer reaches the focused field", form.title.Value())
	}
}

// Before login there is no user to file under, so the note waits and opens over
// the browser the moment someone logs in.
func TestIntakeBeforeLoginIsHeldUntilLogin(t *testing.T) {
	fs := newFakeStore()
	m := newAppModel(fs)
	// Login starts the sync clock with a tea.Tick, which drainCmd would run —
	// and sleep through. Already polling means login schedules no tick.
	m.sess.sync.polling = true

	next, cmd := m.Update(tea.PasteMsg{Content: fullEnvelope})
	m = next.(appModel)
	if pushedScreen(cmd) != nil {
		t.Fatal("a form was opened with nobody logged in")
	}
	if len(m.heldIntake) != 1 {
		t.Fatalf("held %d notes, want 1", len(m.heldIntake))
	}

	next, cmd = m.Update(loggedInMsg{user: &models.User{GUID: "u"}})
	m = next.(appModel)
	if len(m.heldIntake) != 0 {
		t.Error("the held note was not released at login")
	}
	var opened *formScreen
	for _, msg := range drainCmd(cmd) {
		if p, ok := msg.(pushMsg); ok {
			if f, ok := p.s.(*formScreen); ok {
				opened = f
			}
		}
	}
	if opened == nil || opened.title.Value() != "pane.list quirk" {
		t.Error("login did not open the held note")
	}
}

// TestParseIntakeReadsWhatCatsTodoWrites pins the other end of the contract:
// this is cats-todo's notesEnvelope output byte for byte (see its
// TestNotesEnvelopeShape), JSON-quoted values and all. If either side changes
// shape, one of the two tests has to change with it.
func TestParseIntakeReadsWhatCatsTodoWrites(t *testing.T) {
	const sent = "<!-- cats-note v1 -->\n---\n" +
		`title: "say \"hi\": <now>"` + "\n" +
		`description: "from cats-todo · cats"` + "\n" +
		`tags: ["cats-todo"]` + "\n" +
		"---\n\nHandles are w1:p3.\n\nNot numeric.\n\nAttached images:\n- /tmp/a.png\n"
	note, ok, err := parseIntake(sent)
	if !ok || err != nil {
		t.Fatalf("parseIntake: ok %v, err %v", ok, err)
	}
	want := intakeNote{
		title: `say "hi": <now>`,
		desc:  "from cats-todo · cats",
		tags:  "cats-todo",
		body:  "Handles are w1:p3.\n\nNot numeric.\n\nAttached images:\n- /tmp/a.png",
	}
	if note != want {
		t.Errorf("parseIntake =\n  %+v\nwant\n  %+v", note, want)
	}
}
