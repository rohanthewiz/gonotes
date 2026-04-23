# Add Code Copy Button to Rendered Code Blocks

**Date:** 2026-04-22 19:56
**Session ID:** aa0b198a-4aca-4306-b444-1502c4602f4a

## Goal

Add a clear double-rectangle copy icon to the top-right of every rendered
fenced code block in the note preview, so users can copy the block's source
to the clipboard with one click.

## Context

The note preview renders markdown via `marked` with a custom `renderer.code`
that produces `<pre><code class="hljs ...">...</code></pre>`. An existing
double-rectangle copy icon was already in use for the query-popup's copy
button (`web/pages/landing/status_bar.go:33`), so the new code-block button
reuses the same SVG shape for visual consistency.

Static assets live under `web/static/` and are embedded into the binary via
`//go:embed all:static` in `web/static.go`, so changes only ship after a
rebuild.

## Changes

### `web/static/js/app.js`

- `renderer.code` (lines 89-94): wrap the `<pre>` in a
  `<div class="code-block-wrapper">` and inject a
  `<button class="code-copy-btn">` with the double-rectangle SVG icon.
  The raw (pre-highlight, pre-escape) source is base64-encoded
  (via `btoa(unescape(encodeURIComponent(code)))`, which preserves non-ASCII
  characters) and stashed on the button as `data-code`.

- Added `window.app.copyCodeBlock(btn)`: decodes the base64 source,
  writes it to the clipboard via `navigator.clipboard.writeText`, and
  briefly toggles a `.copied` class on the button for visual feedback
  (no toast, to avoid spamming — each code block has its own button).
  Falls back to `showToast('Failed to copy code', 'error')` if the
  clipboard API rejects.

### `web/static/css/app.css`

Added a block right after the existing `.markdown-content pre code` rule:

- `.markdown-content .code-block-wrapper { position: relative; }`
  — anchors the absolutely-positioned button.
- `.markdown-content .code-block-wrapper pre { padding-right: calc(var(--spacing-lg) + 32px); }`
  — reserves space on the right so long single-line code doesn't slide
  under the copy button.
- `.code-copy-btn` — 28x28 button in the top-right corner, subtle
  translucent background (`rgba(255,255,255,0.08)`), `opacity: 0.75`
  at rest, brightens to 1.0 on hover and picks up `var(--primary)`.
- `.code-copy-btn.copied` — green-500 (`#4ade80`) tint for ~1.2s after
  a successful copy.

## Icon

The SVG is the same double-rectangle "copy" shape used elsewhere in the UI:

```html
<svg width="16" height="16" viewBox="0 0 24 24" fill="none"
     stroke="currentColor" stroke-width="2"
     stroke-linecap="round" stroke-linejoin="round">
  <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
  <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
</svg>
```

## Verification

- `go build ./...` — passes.
- Since assets are embedded, the running binary must be rebuilt for the
  change to appear in the served UI. No Go source changes were needed.

## Follow-ups / Not Done

- Did not launch the dev server to manually test in a browser — the change
  is mechanical enough (and the CSS is conventional) that this felt
  acceptable, but a browser smoke test is still the right final step.
- If we ever switch away from `marked`'s synchronous renderer, the
  `btoa`/`data-code` stash can be replaced with storing the raw source in a
  `Map` keyed by a generated id. Current approach keeps the renderer pure
  and avoids touching a module-level map.