# Session: Optional Monaco Editor for Note Body (Vendored, Offline-Capable)

**Session ID:** `e4bd7018-4764-42e2-a251-a7a2119078c8`
**Date:** 2026-08-12
**Branch:** master
**Commit:** `be67ab9` — Add optional Monaco editor for the note body, vendored for offline use

## Context / Decision

The user asked for "an option to use the full programmatic mode of Monaco in the
note body." Monaco was not previously integrated — only anticipated in the CSP
comment (`unsafe-eval`) and an archived plan doc. The note body was a plain
`<textarea id="edit-body" name="body">`.

Key design decisions:

- **Opt-in, textarea stays canonical.** A "Monaco editor" checkbox in the edit
  footer (next to the privacy toggle, reusing `.privacy-toggle` styling) enables
  Monaco. Preference persists in localStorage (`gonotes-editor-mode` =
  `'monaco' | 'plain'`, default plain). The textarea remains in the DOM (hidden
  via `.monaco-active` on `.edit-body-wrapper`) and keeps `name="body"`, so the
  FormData/msgpack save path is untouched. Monaco mirrors every edit into the
  textarea via `onDidChangeModelContent`; `saveNote` also flushes explicitly as
  a safety net.
- **"Full programmatic mode"** = the complete AMD build (`loader.js` +
  `vs/editor/editor.main`), not a slim bundle. Entire API is live at
  `window.monaco`; instance via `app.getMonacoEditor()`. Users can e.g.
  `app.getMonacoEditor().addAction({...})` from the console.
- **Lazy load.** Nothing is fetched until the first activation — users who never
  enable it pay zero cost.
- **Vendored first, CDN fallback** (second request in the session, "in case we
  have no connectivity"): monaco-editor **0.52.2** `min/vs` (13MB, 104 files)
  vendored into `web/static/vendor/monaco/` (+ LICENSE + VERSION marker) and
  embedded via the existing `go:embed all:static`. `MONACO_SOURCES` array is
  tried in order: `/static/vendor/monaco`, then pinned jsdelivr. Binary grew
  ~56M → ~79M.

## What Was Built

### New files

- **`web/static/js/monaco_editor.js`** — the whole integration (IIFE matching
  the other modules; exposes hooks on `window.app`):
  - `loadMonaco()` — idempotent promise chain over `MONACO_SOURCES` via
    `reduce` seeded with `Promise.reject(null)`; resets on total failure to
    allow retry; falls back to plain textarea + toast on failure.
  - Worker creation: `MonacoEnvironment.getWorkerUrl` returns a `data:` URI
    shim that sets `baseUrl` and `importScripts` the real `workerMain.js`
    (needed cross-origin AND for same-origin, since workers from data: URIs
    can't resolve relative paths — local base is expanded with
    `location.origin`).
  - Editor: markdown, `wordWrap:'on'`, minimap off, `automaticLayout:true`
    (handles splitter/focus-mode resizes), theme mapped from `gonotes-theme`
    (`dark-green`→`vs-dark`, else `vs`). Wraps `app.toggleTheme` to call
    `monaco.editor.setTheme` — safe because app.js loads first.
  - Hooks: `app.toggleMonacoEditor(bool)`, `app.getMonacoEditor()`,
    `app._monacoActive()`, `app._monacoInsertText(text, {padNewlines})`
    (uses `executeEdits` to preserve undo), `app._syncBodyToMonaco()`,
    `app._syncMonacoToBody()`, `app._monacoOnEditShown()`.
  - Image paste/drop intercepted on the Monaco container in **capture phase**
    (before Monaco's internal handler), routed to `app._insertImageFile`.
  - `setValue` (not executeEdits) when switching notes — intentionally resets
    undo so undo can't walk into a previous note's content.
- **`scripts/vendor_monaco.sh`** — re-vendors any version straight from the npm
  registry tarball (curl + tar only, no npm). Replaces the vendor dir
  wholesale; writes VERSION. Keep `MONACO_VERSION` in monaco_editor.js in sync.
- **`web/static/vendor/monaco/`** — vendored 0.52.2 min build.

### Modified files

- **`web/pages/landing/preview_panel.go`** — `#monaco-body-container` div beside
  the textarea; Monaco toggle checkbox in the edit footer.
- **`web/pages/landing/page.go`** — script tag for monaco_editor.js (loads after
  app.js/image_embed.js since it wraps their hooks); cache-bust bumps
  (app.css v8, app.js v9, note_links v2, image_embed v2, monaco_editor v2).
- **`web/static/js/app.js`** — guarded hook calls: `_syncBodyToMonaco()` in
  `populateEditForm`/`clearEditForm` (`.value` setter fires no events),
  `_syncMonacoToBody()` at top of `saveNote`, `_monacoOnEditShown()` in
  `showEditMode`.
- **`web/static/js/note_links.js` / `image_embed.js`** — insertion functions
  route through `_monacoInsertText` when Monaco is active; image_embed exposes
  `app._insertImageFile = insertImageAsBase64` so Monaco paste/drop reuses the
  resize dialog.
- **`web/middleware.go`** — CSP: `worker-src 'self' blob: data:` added;
  `font-src` gained cdn.jsdelivr.net (codicon font when on CDN fallback).
- **`web/static.go`** — **bug found during smoke test:** `isAsset` checked
  `Contains(path, "/vendor/")` but paths arrive with `/static/` stripped
  (`vendor/monaco/...`), so vendored files got 1h cache instead of 1y. Added
  `HasPrefix(path, "vendor/")`.
- **`web/static/css/app.css`** — `.monaco-body-container` (flex:1,
  `min-height:0` required so Monaco can measure) + `.monaco-active` visibility
  swap.

## Verification

- `go build ./...`, full `go test ./...` green; `node --check` on all edited JS.
- Smoke test with the built binary on port 18999: vendored loader.js,
  editor.main.js/.css, codicon.ttf, workerMain.js all served 200 with correct
  content types; CSP header verified; vendor cache header confirmed
  `max-age=31536000` after the isAsset fix.
- Gotcha hit while testing: background job `%1` doesn't span Bash tool calls —
  stale server held the port and served old headers; killed by
  `lsof -ti tcp:18999` PID instead.

## Notes / Follow-ups

- Vendored files (13MB, 104 files) are now committed to git. If repo size
  becomes a concern: gitignore `web/static/vendor/monaco/` and run
  `scripts/vendor_monaco.sh` as a build step instead.
- Binary size could be trimmed by dropping `vs/language/typescript` (5.5MB) and
  non-English `nls.messages.*` (~1.7MB) from the vendor dir if "full" mode is
  ever relaxed.
- `scripts/download_vendor.sh` is stale (points at `server/static/vendor`,
  says Monaco must be manual) — left untouched; superseded for Monaco by
  `vendor_monaco.sh`.
- Emulator/browser E2E of the Monaco surface itself (typing, toggle mid-edit,
  image paste inside Monaco) was not exercised — only server-side smoke tests.
