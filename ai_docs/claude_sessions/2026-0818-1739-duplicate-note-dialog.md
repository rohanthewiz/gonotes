# Session: Duplicating a Note Keeps Its Filing

**Session ID:** `ca6e8c0c-f32c-4648-a25f-66531b5bf5f3`
**Date:** 2026-08-18
**Branch:** master
**Base commit:** `4ee2807` — Teach the gonotes skill what changed since DuckDB

## Context

Task, as given: "Duplicating a note should duplicate the categories -- hmm maybe
give a quick popup of what we want duplicated with defaults already checked. By
default we want 1) the title duplicated with the COPY prefix 2) Categories and
Subcats." Followed mid-turn by: "Apply this feature to the TUI mode also."

The bug behind the ask: `app.duplicateCurrentNote` copied title, description,
body and `is_private` and stopped there. Categories live in the
`note_categories` junction, not on the note, so a duplicate silently landed in
no category at all — and `tags: null` meant it silently lost those too. The TUI
had no duplicate at all.

---

## The shape of the answer

A **duplicate dialog** rather than a smarter one-shot copy, in both UIs, with
the same rules on both sides:

- The title is an editable field prefilled `COPY <title>` — a default answer,
  not the only one.
- Below it, one checkbox per part the copy can carry: **categories &
  subcategories first**, then body, description, tags, private, follow-up flag.
- Everything shown starts checked. `D`, `enter` is still a full copy.
- A row exists only when the source note has that field. A dead checkbox on
  every note is noise, and in the TUI it would also be a row that shifts focus
  indices when the async category load lands.

### Decisions worth keeping

**`COPY ` leads the title, it does not trail it.** The web UI's old
`" (Copy)"` suffix is the first thing a truncating list drops — which is the
exact layout where two identical-looking rows are hardest to tell apart.

**Categories carry their per-note SELECTION, not just the category.** A copy
filed under `Work` when the original is under `Work/backend` is the
half-working outcome the whole feature exists to avoid. Both sides re-attach
with `AddCategoryToNoteWithSubcategories` / `POST
/notes/<new>/categories/<cat> {"subcategories": [...]}`, which records the
selection on the insert rather than needing an update after.

**No lock is taken.** Duplicating reads the selected note and writes a
different one, so it neither blocks nor is blocked by whoever has the original
open in a form.

**A partial failure returns the note.** If the note is created and a category
link then fails, `noteDuplicatedMsg` carries both. The list must still refresh —
telling a user "duplicate failed" about a note that is already in their list is
the second wrong thing to happen. `serr.Wrap`'s message never reaches
`Error()`, so the phrasing is chosen from the message shape
(`duplicateErrContext`), not by matching error text.

---

## Web UI

`window.app.duplicateCurrentNote` now fetches `/notes/:id/categories`, then
opens the shared modal; `performDuplicate` builds the note from whatever is
still checked and re-attaches the categories one call at a time (a category that
will not attach is a warning toast, not a lost copy).

Three supporting changes to the modal itself, all of which the dialog needed and
none of which existed:

| Change | Why |
|---|---|
| `modalConfirmHandler` | The shared footer's primary button had exactly one behavior: dismiss. A dialog can now own it (relabelled "Duplicate"), and `closeModal` restores the label and the disabled state. The handler closes the modal itself, so a failed create keeps the dialog open with the typed title intact. |
| Escape closes an open modal | And every other shortcut is swallowed while one is up — `n` was previously able to open a new note *behind* the dialog. |
| Title set as a property, not interpolated | `escapeHtml()` escapes element text, not attribute quotes; a note titled with a `"` would have broken out of `value="…"`. |

Assets are embedded (`web/static.go`, `//go:embed all:static`), so a running
`GoNotes.app` needs a rebuild to see any of this. Cache-bust bumped to
`app.js?v=10` / `app.css?v=9`.

## TUI

New screen, `tui/duplicate.go`, pushed by `D` from browse or detail:

```
╭──────────────────────────────────────────╮
│   Duplicate Release checklist            │
│   Title  > COPY Release checklist        │
│   Also copy from the original:           │
│     [x] Categories & subcategories       │
│         Personal, Work/backend           │
│   › [x] Body                             │
│     [x] Description                      │
│     [x] Tags   [x] Private   [x] Flag    │
│   ↑/↓ move • space include • enter duplicate • esc back
╰──────────────────────────────────────────╯
```

- **`D`, shifted.** Every unmodified letter on browse is already a note action,
  and `d` is the one letter it must not share — the neighbour is delete. The
  slip that matters (reaching for `D`, getting `d`) lands on the delete
  *confirmation*, one esc from nothing having happened. A shifted letter
  stringifies as `"D"`, not `"shift+d"` — pinned in `TestKeyNames`.
- **`space` is the third screen to bind the space bar** (after the form's
  private toggle and the subcategory picker) and gets its own `Include` binding
  so this footer names what *this* screen's space does. It is claimed only off
  the title field; on the title it types, the same line `form.go` draws.
- **The category row is always present**, even before its data arrives, so no
  focus index can shift under the cursor when the load lands. Its detail line
  goes `loading…` → the spec list / `(this note is in no categories)` /
  `could not be loaded — the copy will have none`, and the row is dimmed and
  un-toggleable in the last two.
- **Confirming mid-load is refused** with a status line rather than allowed:
  confirming then would produce a copy with no categories and no sign anything
  was dropped. A *failed* load is different — it will not resolve, and the row
  already says so.
- `confirm()` is split into `plan()` + the command, so what survives which
  combination of toggles is assertable without driving a `tea.Sequence` through
  the event loop.
- Browse footer order puts Duplicate late: the list footer elides from the
  right, and edit/delete/flag are the rows that must survive the cut. The detail
  footer renders whole and always shows it.

---

## Files touched

| File | What |
|---|---|
| `tui/duplicate.go` | **new** — the dialog, `dupPlan`, `duplicateErrContext` |
| `tui/duplicate_test.go` | **new** — defaults, row set, toggling, typing vs. space, store writes incl. filing, partial-failure shape |
| `tui/commands.go` | `noteDuplicatedMsg`, `duplicateNoteCmd` |
| `tui/browse.go` | `D` opens the dialog; the reply refreshes the list either way |
| `tui/detail.go` | `D` opens the dialog; success pops back to the list |
| `tui/keymap.go` | `Duplicate`, `Include`, `duplicateHelp()`, browse/detail footers |
| `tui/keymap_test.go`, `tui/tui_test.go` | binding + key-name pins for `D` and the dialog's space |
| `tui/testdata/narrow-browse.golden` | regenerated (style resets for the elided entries; text unchanged) |
| `web/static/js/app.js` | the dialog, `performDuplicate`, modal confirm handler, Escape handling |
| `web/static/css/app.css` | `.dup-options` / `.dup-option` / `.dup-option-detail` |
| `web/pages/landing/page.go` | asset cache-bust |
| `.claude/skills/gonotes/SKILL.md` | `D` in the key list + a paragraph on the dialog |

`go build ./... && go vet ./... && go test ./...` all green (with the server up;
nothing here touches the real data directory).

## Follow-ups not done

- **The web dialog is not keyboard-navigable beyond Enter.** Tab reaches the
  checkboxes because they are native inputs, but there is no ↑/↓ affordance and
  no visible focus ring beyond the browser default.
- **The TUI dialog has no "duplicating…" state.** It pops before the create, the
  confirmScreen pattern, so a slow HTTP store shows nothing until the status
  line lands. Fine locally; visible against a remote hub.
- **Categories are attached one call at a time.** A note in ten categories is
  ten round trips on the HTTP store. An `AddCategoriesToNote` bulk door would
  fix both UIs at once.
- **The copy is created unflagged-by-default only in the sense that the row is
  offered.** Whether a follow-up flag *should* follow a copy is a judgement the
  dialog delegates to the user; if it turns out nobody ever wants it, the row's
  default is a one-line change.
- **No duplicate action in cats-mobile**, which has no notion of the dialog at
  all.
