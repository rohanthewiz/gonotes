# Session: In-Progress Feedback for the Web Summarize Doors

**Session ID:** `d308dcb8-09ed-4822-90f9-90c22999b316`
**Date:** 2026-08-26
**Branch:** master
**Base commit:** `f98641a` — Fold away Description and Categories while editing a note

## Context

Started as a question, not a task: *"How do I access the 'Summarize the
clipboard into a new note' feature in the TUI?"* — answered from the code as
**ctrl+r on the browse screen** (`tui/keymap.go:225`, `tui/browse.go:624`), with
the note that the same chord on the edit form means the other thing entirely:
summarize the body on screen, in place (`tui/keymap.go:229`).

Then the real report: *"On the GUI I see no indication that the summarization is
in-progress."*

That turned out to be true, and specific to one of the two web doors.

## The diagnosis

`summarize.js` has a `busy()` helper that disables a button and optionally swaps
its label. Both doors call it; only one of them gets anything visible out of it.

| Door | Call | What the user saw |
|---|---|---|
| Edit footer (`summarizeBody`) | `busy(btn, true, 'Summarizing…')` | Label swaps. Works. |
| Toolbar (`summarizeClipboard`) | `busy(btn, true)` | **Nothing.** |

Two independent reasons the clipboard door showed nothing:

1. **No label was passed.** And passing one would not have helped — the toolbar
   button is an icon-only SVG (`web/pages/landing/toolbar.go:102`), so
   `btn.textContent.trim()` is empty and the guard on `busy()`'s label line
   drops the swap anyway.
2. **`disabled` was styled nowhere.** `.btn` had no `:disabled` rule in
   `app.css` at all — the only one in the file was `.auth-submit:disabled`. A
   `<button>` wrapping an SVG that inherits `currentColor` barely dims under the
   UA default.

Net effect: click, nothing, then seconds later a new note simply appears.

Neither door announced the *start* either. The TUI does — `status("Summarizing
the clipboard…")` at `tui/browse.go:629` — and that asymmetry was the tell.

## What was considered

Three options were put up, roughly increasing effort:

1. A start toast on both doors — matches the TUI exactly.
2. A `.btn:disabled` rule — fixes every disabled button in the app, not just
   this one.
3. A real busy state on the icon button (spinner / `aria-busy` + CSS pulse).

Chosen: **1 + 2 together**, on the reasoning that the toast says it started and
the dimming covers the rest of the wait. 3 was left on the table.

## What was done

### 1. Start toasts — `web/static/js/summarize.js`

- Clipboard door: `toast('Summarizing the clipboard…', 'info')` immediately
  before `busy()`. Same words as the TUI's status line.
- Body door: `toast('Summarizing the note body…', 'info')` **alongside** the
  existing label swap — the footer button can be scrolled out of view, and this
  is the door that *replaces* text the user wrote, so the replacement landing
  should be an expected event rather than a surprise.

Both sit *after* the empty-clipboard / clipboard-refused guards, so a no-op
click still gets only its own message and never a "starting…" that was a lie.

**Known limit, deliberately accepted:** `showToast` auto-dismisses at 3s
(`app.js:1912`) and a model call can outlast that. Past the 3s mark the dimmed
button is the entire signal — which is exactly why part 2 was not optional.

### 2. `.btn:disabled` — `web/static/css/app.css`

```css
.btn:disabled { opacity: 0.55; cursor: not-allowed; }
```

Plus three `:disabled:hover` rules pinning `.btn-primary`, `.btn-secondary` and
`.btn-icon` back to their resting colors. Without those, the existing `:hover`
rules repaint a disabled button on pointer-over and it reads as live *despite*
the dimming. Each is one class more specific than the `:hover` it answers, so it
wins regardless of position in the file — no existing selector had to be edited
to make room.

The `transition: all` already on `.btn` makes it fade rather than blink.

## Two things worth remembering

- **This is app-wide, not summarize-only.** The Save button (`app.js:501`
  disables it and swaps to "Saving...") now dims too, as does anything else that
  disables a `.btn`. That was the point of choosing the shared rule over a
  per-button one, but it is a visual change beyond the reported bug.
- **`.auth-submit:disabled` is untouched** and keeps its own `opacity: 0.7`.
  Login/register/setup buttons carry `auth-submit` without `btn`, so the two
  rules never meet.

## Verification

- `node --check web/static/js/summarize.js` — OK
- `go build ./...` — OK
- `go test ./web/... ./summarize/...` — OK (`web/api` 8.9s, `summarize` 0.7s)

Diff: 2 files, +42 lines, no deletions.

## Not done

**Option 3** — a spinner or `aria-busy` pulse on the icon button. The toolbar
button is still motionless while it works: dimmed and inert, but not animated.
That is the nicer version if the toolbar should feel finished, and it is the
obvious next pickup here.
