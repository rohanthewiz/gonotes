# Session: N-012 — all/any switch for subcategory filters

**Session ID:** `eab655dd-1fe3-444a-8b2c-90848d5f5940`
**Date:** 2026-10-03
**Branch:** master
**Base commit:** `667397c` — Close N-034: saved queries, server-side history, note-link completion
**Work commit:** `a065185` — Close N-012: all/any switch for subcategory filters

## The ask

Next list item N-012 (pasted from the cats-todo backlog):

> Toggling several subcategories means AND ("all of them"), in both UIs.
> There's no OR, and no models function for one.

The message was only pasted text, so I confirmed before changing code and asked
how OR should surface. The user chose **an all/any switch, with AND kept as the
default**. The alternatives were replacing AND with OR, doing models + API only,
or not proceeding.

## What was already there

- **Models:** `GetNotesByCategoryAndSubcategories` (`models/category.go`) reads
  the category's links that have any subcategory selected, from both databases,
  and filters in Go with an AND loop.
- **API:** `GET /api/v1/notes?cat=…&subcats[]=…` calls that function.
- **TUI:** `Store.GetCategorySubcategoryNotes`. The local store calls the models
  function, and the HTTP store calls the API endpoint above. The subcategories
  screen builds the set with `space` and applies it with `enter` through
  `categoryPickedMsg`. Browse keeps `subFilter`, and `esc` clears it before the
  category.
- **Web:** filters entirely in the browser (`app.js` `getFilteredNotes`), using
  the preloaded `noteCategoryMap`. It never called the API filter. The chips are
  rendered by `cats_subcats.js` `renderSubcategoryChips`.
- The advanced query language could already express OR
  (`subcategory = 'a' OR subcategory = 'b'`). Only the toggle UIs couldn't.

## What changed

### models (`models/category.go`)
- **`SubcategoryMatch`:** `MatchAllSubcategories` (the zero value, so any
  caller that omits it still gets AND) and `MatchAnySubcategory`. `String()`
  gives `"all"` / `"any"`.
- **`ParseSubcategoryMatch`:** anything other than exactly `"any"` means AND.
  That is the safe direction, because AND returns a subset of OR, so a typo can
  only hide notes.
- **`GetNotesByCategorySubcategoryMatch(name, subs, match, guid)`** is now the
  one implementation. `GetNotesByCategoryAndSubcategories` (same signature as
  before) and the new `GetNotesByCategoryAndAnySubcategory` are thin wrappers
  over it.
  - The per-note test was pulled out into `subcategorySetMatches`. Both modes
    read the same rows, so they can't drift on owner, soft-delete or the
    two-database merge.
- **An empty subcategory list is the whole category in both modes.** Strictly,
  OR over nothing would match nothing, but "nothing toggled" means "no
  narrowing" in both UIs.

### api (`web/api/notes.go`)
- `ListNotes` reads `subcats_mode` with `ParseSubcategoryMatch`, and the doc
  comment lists the new parameter.

### tui
- **Store interface:** `GetCategorySubcategoryNotes` gains a
  `match models.SubcategoryMatch` parameter. It is threaded through the local
  store, the HTTP store, the test fake, `loadCategorySubNotesCmd`, and
  `categoryPickedMsg.match`.
- **HTTP store:** sends `subcats_mode=any` only for OR.
  - For AND the request is byte-identical to before.
  - An older server that ignores the parameter answers an OR request with AND,
    which is the narrower result, never the wider one.
- **Subcategories screen:** `m` (new `keys.SubMatch`) flips the mode and shows
  a status line saying which mode is now on.
  - The title ends in `(any)` whenever OR is on, even with fewer than two
    subcategories toggled, so the key press visibly took effect.
  - The mode resets to AND on each visit, the same way the toggled set starts
    empty.
- **Why `m`:** the screen already uses space, enter, n, r, d and q, and the
  list widget takes `/` and its navigation letters. `a` was rejected because it
  is "all notes" on the category screen one level up.
- **Browse:** a `subMatch` field travels with `subFilter`. It is set from the
  same message, and the first `esc` resets it along with the subcategories.
  - The title gets an `(any)` suffix (`subMatchSuffix`) only for OR with two or
    more subcategories, where the two rules can differ.
  - `filingSpec` (used by capture) ignores the mode. A note filed under every
    subcategory of an OR filter still shows in that list.

### web
- `state.filters.subcatMode` (`'all'` / `'any'`). The browser filter uses
  `.some` for any and `.every` for all.
- **The switch:** `renderSubcategoryChips` puts a button before the chips,
  `#subcat-mode-toggle` labelled `all of` / `any of`, but only when the
  category has two or more subcategories.
  - Changing category, `clearSearchBar` and `clearAllFilters` all reset it to
    `'all'`.
- **Query display:** `buildQueryString` adds `match:any` only for OR with two
  or more subcategories.
- **CSS:** `.subcat-mode-toggle` is dashed and monospace in both states, and
  colored only for "any".
  - The first version switched to a solid border for "any" and was labelled
    just `any`. In a screenshot it looked exactly like a selected chip, as if
    "any" were a subcategory name, so I changed the label and kept the dashes.

### docs
- **README**, TUI subcategories bullet: covers `m`, `(any)`, the web button and
  `subcats_mode=any`.
- **`ai_docs/todo/next-list.md`:** N-012 moved to Closed.

## Tests

- **models** (`models/subcategory_match_test.go`, new):
  - Four notes, one of them private so both databases are read. AND vs OR on
    `backend`+`api`, the value-carrying form, and the empty list in both modes.
  - `ParseSubcategoryMatch` pins `any`, `all`, the empty string, `ANY` and `or`,
    plus a round trip through `String()`.
- **web/api** (`notes_test.go`): `subcats_mode` set to empty, `all`, `bogus`
  and `any` on `pod`+`service` gives 0, 0, 0 and 1 notes.
- **tui:**
  - `TestSubcategoriesOverHTTP` gains `api`+`ops` AND (0 notes) and OR (1 note)
    over the wire. The fake API parses `subcats_mode` the way the real handler
    does.
  - `TestBrowseAppliesTheSubcategoryFilter` covers the OR result, the `(any)`
    title, and a later plain pick not keeping the old OR.
  - `TestSubcategoryMatchKeyFlipsToAny` (new) covers `m`, the title, the pick
    carrying the mode, and a second `m` switching back.
  - `TestBrowseEscPeelsSubcategoryBeforeCategory` checks that `esc` resets the
    mode.
  - Keymap tests pin `m` and add it to the subcategories help and handled list.
  - The fake store's OR logic is written out separately rather than calling the
    models helper, so a bug there isn't copied into the test double.
- `go test ./...` passes, and `gofmt` and `go vet` are clean.

## Live check

I used a scratch server on :8899 with its own data dir, built from the working
tree. It was seeded over the API with notes filed under backend+api, backend,
api and ops.

- **API with curl:** AND gave `[Both]`; `subcats_mode=any` gave
  `[ApiOnly, BackendOnly, Both]`.
- **Web UI in headless Chrome (CDP on :9333, scratch profile):** driven with
  Node 22's built-in `WebSocket`, since Python `websockets` isn't installed
  here. The script is in the session scratchpad and isn't committed.
  - Selecting the category shows the button and the three chips.
  - With backend+api selected, "all" showed `[Both]` and "any" showed all three
    matching notes. The button turned active and the query display showed
    `match:any`.
  - Switching back to "all" restored `[Both]`.
  - Clear reset the mode and removed the chips.
  - No console errors.
- **Embedded static files:** `web/static` is embedded in the binary, so CSS/JS
  changes need a rebuild and restart before a live check sees them.
- **Cleanup:** the server and headless Chrome were stopped by their own PIDs.
- **TUI:** not run live. Its changes are covered by unit tests.

## Next

Closed: N-012. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
