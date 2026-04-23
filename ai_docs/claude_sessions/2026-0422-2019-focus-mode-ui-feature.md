# Focus Mode UI Feature

**Date:** 2026-04-22 20:19
**Session ID:** 0db521ab-5003-4112-b962-d2b9540604ea

## Summary

Added a "focus mode" to the landing page that expands the right-side note
preview to full width, collapsing the filter panel and note list. A slim
vertical handle pinned to the left edge restores the normal three-pane
layout. Also relaxed the draggable splitter's max constraint so it can be
dragged much farther left.

## User requests

1. "I would like to go full-width with the single note view on the right
   by expanding the single note view horizontally, squishing the notes
   list and filters section on the left. Provide some kind of a handle on
   the left for restoring normal layout"
2. Follow-up: "Also allow the splitter to go farther left"

## Changes

### `web/pages/landing/toolbar.go`
Added a `⇔` icon button (`id="btn-focus-mode"`) in `toolbar-right`, just
after the sync button. Click → `app.toggleFocusMode()`.

### `web/pages/landing/page.go`
- Added `id="app-main"` to the main content `div` so JS can target it.
- Added `<button class="focus-restore-handle" id="focus-restore-handle">`
  as a sibling inside `.app-main`. Hidden by default; revealed only when
  `.app-main` has the `focus-mode` class.
- Bumped cache-bust versions: `app.css?v=6` → `v=7`, `app.js?v=7` → `v=8`.

### `web/static/css/app.css`
- Removed `max-width: 600px` from `.right-panel` so it can expand past
  the old hard cap.
- New rules:
  - `.app-main.focus-mode .left-panel`, `.center-panel`, `.panel-splitter`
    → `display: none`
  - `.app-main.focus-mode .right-panel` → `flex: 1; width: auto;
    max-width: none`
  - `.focus-restore-handle` — absolutely positioned on the left edge,
    14px wide × 72px tall, `primary` background, chevron `›`. Widens to
    20px and brightens on hover. Hidden by default.
  - `.app-main { position: relative }` so the absolute-positioned handle
    anchors to it.

### `web/static/js/app.js`
- `window.app.toggleFocusMode()` — toggles `.focus-mode` on `#app-main`,
  persists state to `localStorage` under key `gonotes-focus-mode`, and
  updates the toolbar button tooltip.
- `initFocusMode()` — restores saved state on page load; wired into
  `init()` right after `initPanelSplitter()`.
- Splitter `onMouseMove` max changed from `containerWidth * 0.6` to
  `Math.max(250, containerWidth - 240)`, so dragging left can now leave
  only ~240px for the list panel instead of 40% of the viewport.

## Verification

- `go build ./...` → clean (no errors).
- Did not start the dev server; the user can reload the browser to see
  the changes. The code-copy-button work from the prior session is
  still uncommitted alongside these changes.

## Files touched

- `web/pages/landing/toolbar.go`
- `web/pages/landing/page.go`
- `web/static/css/app.css`
- `web/static/js/app.js`

## Notes / follow-ups

- The restore handle lives inside `.app-main`, which has `overflow:
  hidden`; since the handle sits at `left: 0` it isn't clipped.
- The toolbar button uses the `⇔` glyph. If a more expressive icon is
  wanted (e.g. `⛶` maximize, `⤢` resize), it's a one-character swap.
- `gonotes-focus-mode` key joins existing keys (`gonotes-theme`,
  `gonotes-splitter-width`) as persisted UI state.