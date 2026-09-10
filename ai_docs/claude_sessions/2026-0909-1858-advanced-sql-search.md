# Session: advanced search — autocompleted SQL over every note attribute

**Session ID:** `7ece3665-7dde-4021-ab53-cea5a8d2b7d9`
**Date:** 2026-09-09
**Branch:** master
**Base commit:** `d845129` — Add session doc for the bytdb v0.11.0 bump

## The ask

> Create an advanced search option that allows me to use autocompleted SQL to
> query for notes, for example where "category = 'airflow' and subcategory =
> 'conversion'". Ensure all attributes are queryable.

Delivered as one language with three doors: the API, the web UI, and the TUI.

## Shape of it

```
                        models/query_fields.go          ← the catalog
                                  │
        ┌─────────────────────────┼──────────────────────────┐
   query_parse.go            query_eval.go            query_complete.go
   (+ _lex, _value)          (per-note verdict)       (what fits the cursor)
        └─────────────────────────┼──────────────────────────┘
                             query.go  (load both DBs, evaluate, order, page)
                                  │
        ┌─────────────────────────┴──────────────────────────┐
   web/api/query.go                                    tui/store.go seam
   /notes/query{,/complete,/schema}                    QueryNotes/CompleteQuery
        │                                                    │
   advanced_search.js  ({ } in the toolbar)            tui/query.go  (":")
```

The catalog is the single source of truth. A column added to `models.Note`
becomes a row in `queryFields` and the parser, evaluator, autocompleter, schema
endpoint, web help panel and TUI field list all pick it up together. A field
present in the catalog with no arm in `noteFacts` is caught by
`TestEveryCatalogFieldIsQueryable`, which probes every kind's accessor directly
— the one failure mode a user could not diagnose is a field that parses and
then silently matches nothing.

## Decisions worth remembering

**Evaluation is in Go, not pushed into SQL.** Three reasons, all specific to
this codebase: a user's notes live in *two* bytdb databases and a category link
in one references the catalog in the other, so `category = 'x'` needs a Go-side
join no matter what; bytdb already holds every row in RAM, so pushing the
filter down saves no IO; and the semantics below are ours, not the engine's.
Full comment at the top of `models/query.go`.

**Where the language departs from SQL** — all of it deliberate, all of it
shipped as data in `/notes/query/schema` so the UIs explain it without
hard-coding it:

- string comparison ignores case (`MATCHES` excepted — a regex carries its own
  flags)
- a bare quoted string is a free-text term over title/description/body/tags/
  categories/subcategories, so the fast thing stays one word
- on a multi-valued field a positive operator is existential and its negation
  universal, so `category != 'archive'` means "not filed under archive" rather
  than SQL's "has some category that is not archive" (true of nearly every
  note, i.e. useless)
- a missing value is *absence*, not SQL's unknown: `description != 'x'` is true
  for a note with no description, and every note is accounted for by exactly
  one of a predicate and its negation. `IS NULL` asks about absence itself.
- a date literal covers the precision it was written at, and ordering
  comparisons use the window's edges — so `= '2026-03-04'` is the whole day,
  `>= '2026-03-04'` includes it, `> '2026-03-04'` starts the day after
- soft-deleted notes are excluded unless the query names `deleted_at`

**Completion is server-side.** Half of what is worth suggesting *is* the user's
data — their categories, subcategories, tags, note titles — which no
client-side list can know, and which in HTTP mode belongs to the hub rather
than to the process running the UI. One implementation in
`models/query_complete.go`, two front ends. The completer never parses (a query
being typed is invalid most of the time and that is exactly when help is
wanted); it re-lexes the text left of the cursor and runs a small state machine
over the tokens. The lexer therefore reports an unterminated trailing string as
a *flagged token* rather than an error, because `category = 'air` is a normal
state there.

**Accepting a suggestion is a pure splice.** The completer returns the byte
range to replace and the exact replacement text — quoting and trailing space
included — so neither front end decides how to quote a value. That is what
keeps the web popup and the TUI list from drifting.

**A query result is an ordinary note list.** The TUI's query screen pops on
enter and hands the text to `browseScreen.queryFilter`, which `reloadNotes()`
treats as a third way of loading the list. The preview pane, lock badges, `/`
within the results, edit/delete/duplicate all keep working with no second code
path. Same idea in the web UI: the query *replaces* the client-side filter
pipeline (`state.advanced` in app.js) rather than intersecting with it, because
the server has already done the selecting.

**Errors point at themselves.** Every parse failure is a `*models.QueryError`
with a byte offset, length and a "did you mean" for a near-miss field name. The
API returns it as the envelope's `data` on a 400; the web UI *selects* the range
in the input; the TUI draws a caret line under it. `asQueryError` in
store_http.go recovers the structure from the 400 so HTTP mode behaves
identically to local mode.

## Bugs found by actually running it

- **A stale completion re-opened the popup over the results.** The keystroke
  that finished a query issues a completion; it landed after the run. Fixed by
  bumping the sequence counter in `runAdvancedQuery` (the TUI had the same
  guard from the start, via `queryCompletionMsg.seq`). Found in a screenshot.
- **`ctrl+h` was the wrong key for the field catalog.** ^H is what several
  terminals still send for Backspace, and bubbles' textinput binds `ctrl+h` to
  delete-backward for that reason. Moved to `ctrl+t`; `TestCtrlHStillDeletes`
  pins it.
- **`BETWEEN a AND b` completed wrong.** The AND belongs to the operator, not
  the expression, and the completer's original lookback fixup could not see an
  AND that had already been consumed — so `id BETWEEN 1 AND ` suggested fields
  instead of a value. Replaced with an explicit `betweenStage`.
- **A category pick under an active query appeared to do nothing**, since the
  query outranks the category filter in `reloadNotes`. A pick now retires the
  query.
- **`:` did not open the query screen** in the first pty run — the scratch
  binary predated the TUI work. Same class as the go:embed trap: rebuild before
  believing a live run.

## Verification

- `go vet ./... && go test -race ./...` — all packages green.
- **models**: `query_test.go` (semantics, no DB — parser, evaluator, errors,
  round-trip of every advertised example), `query_complete_test.go` (the state
  machine, including a property test that splicing any suggestion leaves text
  that still parses), `query_run_test.go` (both databases, the category join,
  user scoping, the deleted opt-in, paging, real completion values).
- **web/api**: `query_test.go` over the real HTTP stack — the request's own
  query, paging vs `matched`, the positioned 400, 401 on all three endpoints,
  the schema document's shape.
- **tui**: `query_test.go` with fakeStore running the *real* parser, so screen
  tests cannot pass against semantics the store does not have.
- **Live web run**: scratch server on 8899 + headless Chrome over CDP, 11
  checks — open, complete a field, tab-accept, real category values, run,
  positioned error with the range selected, help panel, click-an-example, close
  and restore, ⌘⇧F. All pass.
- **Live TUI run**: own pty (120x40, answering OSC 11), in **HTTP mode** so the
  wire path is what gets exercised — `:`, completion over the wire, operator
  list, the server's subcategory values, run + title + "2 notes match",
  the caret line, `ctrl+t`, and esc peeling back to the full list. All pass.

Harness scripts are in the session scratchpad, not the repo.

## Files

New: `models/query_fields.go`, `query_lex.go`, `query_value.go`,
`query_parse.go`, `query_eval.go`, `query.go`, `query_complete.go` (+ three
test files); `web/api/query.go` (+ test); `web/pages/landing/advanced_search.go`;
`web/static/js/advanced_search.js`; `tui/query.go` (+ test).

Touched: `web/routes.go` (three routes ahead of `/notes/:id`), `toolbar.go`
(the `{ }` button), `page.go` (the row + script, cache-bust bumps), `app.css`,
`app.js` (`state.advanced`, `applyNoteSort` extracted, query-aware result
count, Clear clears the query), `models/category.go`
(`Category.SubcategoryList`), `tui/store.go` / `store_local.go` /
`store_http.go` / `fake_store_test.go` (two new seam methods),
`tui/browse.go` (`queryFilter`), `keymap.go`, `styles.go`, README, skill doc.

## Left undone

- No CLI subcommand (`gonotes query "…"`). It would have to open the databases
  directly, so it only works with the server stopped — the TUI's HTTP fallback
  is the better door and already exists.
- The web UI's query bar is not wired to the note-link autocomplete or to
  saved/named queries. History is per-browser in `localStorage`.
