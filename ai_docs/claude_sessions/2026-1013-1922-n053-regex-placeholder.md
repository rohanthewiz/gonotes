# Session: N-053, regex placeholder left behind by Clear/Reset

**Session ID:** `203d12fd-c1c9-40f8-ace8-37ace93429d5`
**Date:** 2026-10-13
**Branch:** master
**Base commit:** `c2723dd`, "Session doc: UI reorder, Next-list merge; raise N-053, N-054"

## The ask

Fix Next-list item N-053, dropped in from the cats-todo backlog: after regex
mode was switched off by the toolbar's Clear (`clearSearchBar`) or the filter
bar's Reset (`clearAllFilters`), the search input kept the "Search by regex
pattern..." placeholder.

## Cause

The regex button and the search placeholder are one piece of UI state, but
only `toggleRegex` updated both. `clearSearchBar` and `clearAllFilters` each
reset `state.filters.regex` and removed the button's `active` class by hand,
and neither touched the placeholder.

## Fix

- `web/static/js/app.js`: new `syncRegexControls()` sets the button's
  `active` class and the input's placeholder from `state.filters.regex`.
  `toggleRegex`, `clearSearchBar` and `clearAllFilters` all call it instead
  of touching the controls themselves, so a future path that changes the flag
  has one call to make. Its comment notes that the text-mode string must match
  the server-rendered default in `web/pages/landing/toolbar.go`.
- `web/pages/landing/page.go`: `app.js?v=17` → `v=18`.

## Verification

Scratch server (`gonotes --dir <scratch> --port 8899`) plus headless Chrome
over CDP, per the web UI harness. The binary was rebuilt after the JS edit
(`go:embed`), and the page loaded `app.js?v=18`.

| Step | Placeholder | Button active |
|---|---|---|
| Initial | Search by text or ID... | no |
| Regex on → Clear | Search by text or ID... | no |
| Regex on → Reset | Search by text or ID... | no |
| Toggle twice | Search by text or ID... | no |

With regex on, each step showed "Search by regex pattern..." and an active
button before the reset. `node --check` passes. Both scratch processes were
stopped by PID afterwards.

## Next

Closed: N-053. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
