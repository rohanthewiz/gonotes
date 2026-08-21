# Session: A Summarize Door in the Web UI and the TUI

**Session ID:** `f158f004-bd53-4c00-819a-646b58d26edb`
**Date:** 2026-08-21
**Branch:** master
**Base commit:** `01f748e` — Summarize the clipboard on the way into a note

## Context

Follow-on to the previous session, which put clipboard summarization behind
`gn-clip.sh -s`. The ask this time: *"I would like some UI element (Web/TUI) for
using the summarize into note feature."*

Two things would have produced materially different work, so they were asked:

1. **Which surface** — web, TUI, or both.
2. **What gets summarized** — the clipboard, the text in the editor, or both.

Answers: **both surfaces, both entry points**. So five doors in total, counting
the script that already existed.

## The shape that fell out

The script's summarizer is a shell function. Two UIs cannot share a shell
function, so the first move was porting it to Go — prompt, flags and all — and
then deciding *where that Go code runs*.

That question has one right answer and it is not "in the browser": the
summarizer is the local `claude` CLI, which lives on the machine the SERVER runs
on. Which makes the whole design follow from one line:

```
package summarize  ── the CLI call, the prompt, the parsing
      ├── web/api/summarize.go  ── POST /api/v1/summarize   (browser, TUI-over-HTTP)
      └── tui Store.Summarize   ── localStore calls it directly
                                   httpStore posts to the endpoint
```

A TUI attached to a hub on another machine therefore summarizes with *that*
machine's CLI and credentials — the same answer GoNotes already gives for notes
themselves, so nothing new to explain.

Three consequences worth naming, because each one shows up in the code:

- **Availability is a server property.** `GET /api/v1/summarize` answers
  `{available, default_model}`. Both web buttons ship `display:none` and are
  revealed only by that probe — revealing rather than hiding, so a button never
  flashes on and off on a page load.
- **The clipboard is a client property.** The TUI shells out to
  `pbpaste`/`wl-paste`/`xclip`/`xsel`; the browser uses
  `navigator.clipboard.readText`. Neither ever asks the server for a clipboard.
- **The 15-second HTTP client would abort every call.** `httpStore` gained a
  second client (4 min) used by exactly one method. Raising the shared timeout
  instead would have turned a dead server into a TUI that appears to hang.

## Design decisions

**Nothing is ever saved.** Every door lands in an unsaved form and waits for
`ctrl+s` / **Save**. This is the same rule capture-to-note keeps, for the same
reason: what a model wrote about a text is a proposal, and the person who pasted
the text is the one who decides whether it is worth keeping. A note written
without that decision is one the user finds days later and cannot check against
a source that is long gone from the clipboard.

**A title you typed is never overwritten.** Only an empty title or description
is filled in. This is `-t` outranking the generated title, restated: the flag —
or the field — is the user's own words, and the request was only ever about
condensing the body.

**The body IS replaced, and the status line says so.** That is the request. It
is safe because nothing is saved, but a replaced body is exactly the kind of
change a user has to be told about rather than notice, so every path ends in a
line naming it ("Body replaced with the summary (title filled in) — ctrl+s to
save, esc to discard").

**Fail loudly, in the right vocabulary.** An empty clipboard is reported as an
empty clipboard, not as "Summarize failed" — wrapping it would send the user
looking at the summarizer for a problem that is one ⌘C away from fixed. A
refused browser clipboard says so and opens an empty note, because the other
door reaches the same place. A blank POST body earns a 400, not the 502 it would
get by reaching the summarizer and being refused there.

**`ctrl+r` on both TUI screens.** A bare letter was unavailable on the form
(every unmodified key is text), and one chord meaning "condense the text at
hand" — the clipboard in the list, the body on the form — is one thing to learn
instead of two. Two *bindings* though, not one, so each footer names what that
screen actually summarizes; the same reason `SelectSub` and `TogglePrivate`
share the space bar apart.

**Results are delivered at the root.** Like `captureDoneMsg`, and for the same
reason: a model call takes ~10 seconds and the user is free to navigate while it
runs. A summary that lands after the form closed is reported and dropped, never
routed onto whatever screen replaced it.

**Both CLI streams ride along in the error.** First live run against the scratch
server failed with a bare `exit status 1` — useless. The CLI does not
consistently pick a stream (auth problems go to stderr, usage errors to stdout
with a non-zero exit), so both are captured and truncated into the `serr`.

## Implementation

New:

- `summarize/summarize.go` — `Result`, `Available()`, `Summarize(ctx, text,
  model)`, `ErrUnavailable`, the shared system prompt (kept verbatim in step
  with `gn-clip.sh`), fence stripping, title cap, size cap. `runner` and
  `lookPath` are vars so tests describe a machine with or without the CLI.
- `summarize/summarize_test.go` — 9 tests.
- `web/api/summarize.go` — `Summarize` + `SummarizeStatus`; 3-minute ceiling;
  503 for a missing CLI (the request was fine, the capability is absent).
- `tui/summarize.go` — messages, both commands, `appModel.summarizeDone`, the
  `summary` tag.
- `tui/clipboard.go` — per-platform readers, in preference order (Wayland before
  xclip, which on a Wayland session answers from a clipboard nobody copied to).
- `tui/summarize_test.go`, plus 2 wire tests appended to `store_http_test.go`.
- `web/static/js/summarize.js` — the probe, both doors, the Monaco-aware body
  read/write, busy-state handling.

Changed: `Store` + both stores + the fake; `keymap.go` (two bindings, two help
sets); `browse.go`, `form.go` (`summarize()`, `applySummary()`, a `summarizing`
flag kept separate from `busy` — a summary leaves every field editable),
`tui.go`; `routes.go`; `toolbar.go`, `preview_panel.go`, `page.go`;
`keymap_test.go`; the narrow-browse golden (the new footer row elides on a
narrow terminal, as designed — the diff is invisible style resets).

`request`/`requestH`/`requestOnce` gained `…HC` variants taking a client, so the
slow call keeps the same auth, envelope and silent-re-login path as everything
else. No existing call site changed.

## Verification

`go vet ./...` clean. `tui`, `summarize`, `web/api` suites pass. `gofmt` clean on
every touched file (the repo has pre-existing unformatted files elsewhere; they
were left alone).

Live work ran against a **scratch server on :8477** with its own data dir. The
user's `GoNotes.app` on :8444 was never touched, and every scratch process was
stopped afterwards.

| Check | Result |
|---|---|
| `GET /api/v1/summarize`, authed / unauthed | `{available:true, default_model:"haiku"}` / 401 |
| Real POST, standup paste | ~10s, structured Markdown, apt title, **empty** description, `notes stored: 0` |
| `{"text":""}` / `{"text":"  "}` | 400 both (the second only after the trim fix) |
| Web, CDP: both buttons | revealed by the probe; `summarize.js` loaded |
| Web, **Summarize** button | disabled during the call, body replaced and shorter, title filled, one toast, still in edit mode |
| Web, clipboard denied | error toast + empty new note (the fallback) |
| Web, clipboard empty | info toast, no model call |
| Web, clipboard real | new note, GUID minted, `edit-id` empty (a create) |
| TUI over a pty, `ctrl+r` in the list | "Summarizing the clipboard…" → prefilled form: title, tag `summary`, bulleted body; footer reads `ctrl+r summarize body`; **0 notes stored** |

The web check drove headless Chrome over CDP (`--remote-allow-origins='*'` is
required now — without it the WebSocket handshake 403s). The TUI check used
`pty.fork` + `TIOCSWINSZ` + an OSC 11 answer, with a stub `pbpaste` first on
`PATH` and `GONOTES_URL` pointed at the scratch port.

## Flagged to the user

- **Web summaries are untagged.** The web note form has no tags field and its
  save path sends `tags: null`, so only the TUI's clipboard door applies the
  `summary` tag. Documented in SKILL.md rather than papered over; adding a
  hidden tags field to the web form was out of scope.
- **The form door replaces the body of an existing note.** Recoverable only by
  cancelling — which is why the status line names the replacement.

## Files touched

New: `summarize/summarize.go`, `summarize/summarize_test.go`,
`web/api/summarize.go`, `tui/summarize.go`, `tui/clipboard.go`,
`tui/summarize_test.go`, `web/static/js/summarize.js`.

Changed: `web/routes.go`, `web/pages/landing/{toolbar,preview_panel,page}.go`,
`tui/{store,store_local,store_http,keymap,browse,form,tui}.go`,
`tui/{keymap_test,store_http_test,fake_store_test}.go`,
`tui/testdata/narrow-browse.golden`, `.claude/skills/gonotes/SKILL.md`.
