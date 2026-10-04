# Session: N-048 — save and forget queries from the TUI

**Session ID:** `7f94db89-2c5b-4750-b831-ab8df73b5a3f`
**Date:** 2026-10-03
**Branch:** master
**Base commit:** `68519e2` — Save session doc: N-012 all/any subcategory switch

## The ask

Next list item N-048 (pasted from the cats-todo backlog, with a note that it was
the user's own request):

> The TUI query screen shows saved and recent queries (and runs them), but
> cannot name a query or forget a row. Saving and forgetting are web-only
> (☆ / `Shift+Delete`). The seam would need `SaveQuery` / `DeleteSavedQuery`
> next to `RecordQuery` in `tui/store.go`.

## What was already there

- **Models** (`models/saved_query.go`): `SaveNamedQuery` (upsert by name,
  ignoring case; parses first and returns a `*QueryError`, or a
  `*SavedQueryInputError` for a missing name or text that's too long),
  `DeleteSavedQuery` (`ErrSavedQueryNotFound` when nothing matched),
  `RecordQueryHistory`.
- **API** (`web/api/saved_queries.go`): `POST /api/v1/notes/query/saved` (a parse
  failure → positioned 400 via `writeQueryError`; other input errors → plain
  400), `DELETE /api/v1/notes/query/saved/:id` (404 when not found).
- **Completer**: an empty box leads with saved rows (kind `saved`, label = name)
  then history (kind `history`). Each carries `QuerySuggestion.ID`, which exists
  so a front end can forget from the popup.
- **Web bar**: ⌘S/Ctrl+S opens a name field, prefilled when the box text equals a
  visible saved suggestion; `Shift+Delete` forgets the highlighted saved/recent
  row, treats 404 as success, and re-requests completions.
- **TUI**: `Store.RecordQuery` only. The reusable `promptScreen`
  (`tui/confirm.go`) pops before running its submit command, so the result
  message lands on the screen underneath.

## What changed

### Store seam
- `tui/store.go`: `SaveQuery(name, query, userGUID) (*models.SavedQuery, error)`
  and `DeleteSavedQuery(id, userGUID) error`, documented next to `RecordQuery`.
  Contract: a syntax refusal is a `*models.QueryError`, and an already-gone row
  is not an error.
- `tui/store_local.go`: passes through to models and folds
  `ErrSavedQueryNotFound` into nil.
- `tui/store_http.go`: `saveQueryBody` mirrors the API request. A 400 carrying a
  QueryError is recovered with the existing `asQueryError`, other errors are
  wrapped (`errReason` still finds the server's sentence through serr), and
  `isNotFound` → nil on delete.

### Query screen (`tui/query.go`)
- `ctrl+s` → `startSave`: an empty box sets `errText`. Otherwise it pushes
  `newPromptScreen("Save query as", …)`, prefilled from a visible `saved`
  suggestion whose text equals the box. The query text is captured when ctrl+s
  is pressed, not at submit.
- `querySavedMsg`: a QueryError → `showError` (caret under the mistake); any
  other error → `"could not save: " + errReason(err)`; success → `notice`.
- `shift+delete` / `ctrl+x` → `forget(i)`, only when the popup is open and
  `highlightForgettable()` (kind saved/history **and** ID > 0); otherwise the key
  is swallowed. The row is dropped locally right away (three-index slice so the
  completion reply's backing array isn't mutated), so a second press hits the
  next row rather than the same id. `queryForgotMsg` → notice, then
  `s.complete()` to take the server's list.
- New `notice` field, rendered as a dim `✓ …` line, cleared on any text change,
  run, save start or forget.
- Footer: `keys.queryHelp(forgettable bool)` adds `⇧del forget` only when the
  highlighted row is stored.

### Keymap (`tui/keymap.go`)
- `QuerySave` = `ctrl+s`. Deliberately the same chord as the form's `Save`, so
  the ⌘S accelerator (whose typing twin is ctrl+s in `metakeys.go`) reaches it
  with no new row.
- `QueryForget` = `shift+delete`, `ctrl+x` (help shows `⇧del`). `ctrl+x` is the
  any-terminal twin; nothing else in the TUI binds it, and textinput leaves it
  unbound.

### Tests
- `fake_store_test.go`: `saved`, `forgot`, `nextSaveID`. `SaveQuery` applies the
  checks that don't need a DB (name, empty query, `ParseQuery`) and upserts by
  name.
- `query_test.go`: ctrl+s prompt and save of the captured text, prefill, empty
  refusal, broken query → `qerr`, non-syntax refusal → sentence, both forget keys
  (id, local removal, re-complete, notice), forget ignored on an example row
  (text untouched), footer, and an end-to-end
  `TestSaveAndForgetAgainstTheLocalStore` (real DB: save → ☆ row with ID on an
  empty box → shift+delete → gone; a second delete of the same id → nil).
- `store_http_test.go`: `TestHTTPStoreSaveQueryWire` (body, positioned
  QueryError, a plain 400 keeps the server's sentence) and
  `TestHTTPStoreDeleteSavedQueryWire` (path, 404 → nil, 500 → error).
- `keymap_test.go`: pins the two new bindings; the query footer check uses
  `queryHelp(true)` and lists the new handled keys.

### Docs
- README: the saved-queries section and the TUI key table.
- `.claude/skills/gonotes/SKILL.md`: the TUI key line.
- `models/saved_query.go`: the header diagram names the new TUI calls.

## Verification

`gofmt -l` clean, `go vet ./...` clean, `go test ./...` all pass (models, tui,
web/api, …).

The TUI was **not** driven by hand in a terminal. Whether a given terminal (and
the cats emulator) forwards `shift+delete` as `shift+delete` is untested;
`ctrl+x` exists to cover that case.

## Decisions

- **Reused `promptScreen`** instead of a second inline input on the query screen:
  it matches how categories name things and keeps the query screen's key
  handling flat.
- **No confirm on forget**, matching the web bar. It's one keystroke, but it
  applies only to the highlighted ☆/↺ row, and the notice names what was
  forgotten.
- **Added `ctrl+x`** beyond the ask, as a fallback for terminals that can't tell
  `shift+delete` from delete. Easy to drop.

## Next

Closed: N-048. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
