# Session: Monaco End to End, and the Image-Paste Fix (N-007)

**Session ID:** `4d84aea5-2d3c-4540-88ce-aaff8d3930a6`
**Date:** 2026-10-04
**Branch:** master
**Base commit:** `aa3efdd` — Update N-015: mobile parity premise checked

Same session as the N-049, N-026, N-023, N-022 and N-015 docs earlier today
(latest: [2026-1004-2322-n015-mobile-parity-check](2026-1004-2322-n015-mobile-parity-check.md)).

Picks up N-007 from `ai_docs/todo/next-list.md`, first raised in
[2026-0812-1306-monaco-editor-option](2026-0812-1306-monaco-editor-option.md):

> The Monaco surface has never been exercised end to end in a browser: typing,
> toggling mid-edit, and image paste inside Monaco. The CDP harness (see
> memory) makes this cheap now.

---

## The harness had to be rebuilt

The item points at a "CDP harness" memory, but
`~/.claude/projects/-Users-ro-projs-go-gonotes/memory/` was **empty**. The
only trace was in session docs (`2026-1003-1628-n012-…`: "headless Chrome,
CDP on :9333, scratch profile, driven with Node 22's built-in `WebSocket`;
the script is in the session scratchpad and isn't committed"). It was
rebuilt, and is now saved as the reference memory `cdp-browser-harness`
(indexed in `MEMORY.md`).

Setup used:

- **Server:** a scratch binary (`go build -o <scratchpad>/gonotes-e2e .`) run
  with `-d <scratchpad>/e2e-data -p 18999` and a throwaway
  `GONOTES_JWT_SECRET`. The shell had no `GONOTES_*` variables, so sync and
  data stayed isolated. A test user was registered through the API.
- **Chrome 154:** headless, `--remote-debugging-port=9333`, scratch profile.
- **Driver:** `<scratchpad>/monaco_e2e.mjs`. It talks CDP with
  `Runtime.evaluate`, `Input.insertText`, `Input.dispatchMouseEvent` and
  key events, and records `Network.requestWillBeSent` and console errors
  and exceptions. API calls cross-check what the server actually saved. The
  script is not committed; the user was offered `scripts/e2e/`.

## The bug the run found

**Image paste inside Monaco had never worked.** The handler claimed the
paste (`defaultPrevented`), but the resize dialog never opened.

Narrowing it down:

1. In a synthetic `ClipboardEvent`, `getAsFile()` does return the file, so the
   test was not at fault.
2. Calling `app._insertImageFile(file, textarea)` directly opens the dialog,
   so image_embed.js was fine.
3. A spy on `app._insertImageFile`, with paste dispatched on Monaco's
   `textarea.inputarea`: zero calls. A capture listener added to the
   container afterwards never fired either.
4. `DOMDebugger.getEventListeners` on every ancestor showed **two capture
   paste listeners on `#monaco-body-container`**: Monaco's own
   (`CopyPasteController`, `addDisposableListener(z,"paste",…,!0)`) and then
   the app's.

Capture listeners on one node fire in registration order, and
`monaco.editor.create(container, …)` runs before `setupImageHandlers`.
Monaco's handler calls `preventDefault()` and `stopImmediatePropagation()`
on a paste carrying a file, so the app's handler never ran. A real Cmd+V
takes the same path. Drop was not affected: Monaco's drop and dragover
listeners on the container are bubble-phase, so the app's capture handler
already ran first.

## The fix

`web/static/js/monaco_editor.js`, `setupImageHandlers`:

```
document ─► … ─► .edit-body-wrapper (app: capture) ─► #monaco-body-container (Monaco: capture)
```

- Paste, dragover and drop are registered on `container.parentElement`
  (`.edit-body-wrapper`) in the capture phase. Capture on an ancestor
  always runs before anything on the container, whatever order Monaco
  registers in.
- The wrapper also holds the plain `#edit-body` textarea, which has its own
  handlers in image_embed.js. So each handler starts with
  `if (!container.contains(e.target)) return;`.
- The handler bodies are unchanged. Drop and dragover moved too, so all
  three follow one rule and can't break if Monaco ever makes its drop
  listener capture-phase.

Also:

- `web/pages/landing/page.go`: cache-buster `monaco_editor.js?v=2` → `v=3`.
- `web/pages/landing/preview_panel.go`: the toggle's tooltip said "loaded
  from CDN on demand". It is the vendored copy first with the CDN as
  fallback (since `be67ab9`), so the tooltip now says so.

## Results (after the fix, fresh data dir)

| # | Scenario | Result |
|---|---|---|
| 1 | A checkbox click activates Monaco, hides the textarea and stores `gonotes-editor-mode=monaco`. Monaco loads from `/static/vendor/monaco/`, with no Monaco URL on jsdelivr. The app's own marked, DOMPurify and highlight.js fetches from jsdelivr are expected. | pass |
| 2 | Real typing (insertText + Enter) is mirrored into the textarea on every keystroke, and the saved body matches. | pass |
| 3 | Reopening a note shows Monaco. Edit in Monaco → toggle off: the textarea has the edit. Edit in the textarea → toggle on: Monaco has it. Saving keeps both. | pass |
| 4 | Image paste in Monaco: claimed, the dialog opens, Monaco inserts nothing itself, "Original Size" puts `![pasted](data:image/png;base64,…)` on its own line, the textarea mirrors it, one undo removes it, redo, and it saves. | pass (**failed before the fix**: timed out waiting for the dialog) |
| 4b | Image drop in Monaco goes through the dialog and is inserted. With Monaco off, paste into the plain textarea is claimed, opens exactly one dialog, and is inserted. | pass |
| 5 | Note A, then note B: Monaco shows B, and undo can't walk back into A. Neither note is modified. | pass |
| 6 | After a reload, editing opens in Monaco with the box checked. | pass |
| — | No console errors or uncaught exceptions after the signed-in reload. The signed-out first load's `/auth/me` 401 is by design and excluded. | pass |

Two failures on the way were bugs in the test script, not the app: an
unparenthesised `a === b && c` comparison, and counting the signed-out 401.

`go build ./...` and `go vet ./web/...` are clean, and the `web/api` tests
pass. The scratch server and Chrome were stopped by their own PIDs, and
:18999 and :9333 were confirmed free.

## Files

- `web/static/js/monaco_editor.js`: image handlers moved to the parent, with
  the explanation
- `web/pages/landing/page.go`: `monaco_editor.js?v=3`
- `web/pages/landing/preview_panel.go`: tooltip wording
- `ai_docs/todo/next-list.md`: N-007 moved to Closed
- Outside the repo: memory `cdp-browser-harness.md` and the `MEMORY.md` index

## Next

Closed: N-007. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
