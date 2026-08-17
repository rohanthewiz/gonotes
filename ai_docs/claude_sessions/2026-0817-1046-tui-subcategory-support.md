# Session: Subcategory Support in TUI Mode

**Session ID:** `8e8638d5-94f4-4647-8b8b-657a0b435749`
**Date:** 2026-08-17
**Branch:** master
**Base commit:** `98b761d` — Fix TUI mouse and filter, and verify which dataset the TUI attaches to
**Commits:** `e6e090d` (feature), `385206d` (README walkthrough)

## Context

Task, from the cats todo list: "TUI mode needs subcategory support."

Subcategories already existed everywhere else. `models` has the full two-level
model — `categories.subcategories` (a JSON array: the *definition*, i.e. the
palette of names a category offers) and `note_categories.subcategories` (the
per-note *selection*) — the API exposes every operation on both, and the web UI
renders them as chips with checkboxes (`web/static/js/cats_subcats.js`). Only the
TUI was blind to them: `categories.go` said so out loud in a comment
("Renames and subcategories stay in the web UI"), the note form wrote names only,
and the detail header showed `Work` for a note actually filed under
`Work/backend`.

That last one is the part that made this more than a missing feature: the form
**prefilled** the field with the plain name, so opening a note in the TUI and
saving it unchanged silently *deselected* its subcategories.

**Nothing was needed server-side.** Every endpoint required already existed,
including the subcategory filter. That was the main discovery of the exploration
phase and it shaped the whole design.

---

## The one-line notation

The central decision: a subcategory has to be typeable into a **single-line text
field**, because that is what the note form is (a nested picker is not an option
there). The notation already existed in two places — Markdown frontmatter
(`categories: [Work/backend]`) and `gn-clip.sh -c "Work/backend"` — so it was
adopted rather than invented:

```
Work              category only
Work/backend      category Work, subcategory backend
Work/backend/api  category Work, subcategories backend and api
Work/backend, Work/api    the same thing, one entry per subcategory
                          (this is the form a Markdown export writes)
```

`md_format.go` held the only parser. It moved to `models/category_spec.go` as
`ParseCategorySpecs`, with `md_format.go:parseCategorySpecs` reduced to a
one-line delegate so the Markdown code reads as before. Two dialects of the same
string was the failure being avoided — the TUI now parses exactly what the
importer does.

Also in that file:

- `ParseCategorySpecCSV` — the single-line-field entry point (splits on commas).
- `FormatCategorySpec` / `FormatCategorySpecCSV` — the inverse, used for the
  form's prefill, the detail header, the browse title, and the subcategory
  screen's heading. The *compact* form (`Work/backend/api`) is used rather than
  the exporter's repeated form, because it reads better in a narrow field; the
  parser accepts either.
- `MergeSubcategories` — grows a definition, never shrinks it (see below).
- `SameSubcategories` — order-insensitive comparison, so a re-save with the
  subcategories retyped in a different order writes nothing.

The round trip between `FormatCategorySpec` and `ParseCategorySpecs` is the
contract that keeps prefill-then-save from rewriting a note's filing, and it is
pinned by `TestCategorySpecRoundTrip`.

---

## Definition vs selection

The distinction that drove most of the design, and the source of the two rules
that are easy to get backwards:

```
categories.subcategories        the DEFINITION — what Work offers
                                (backend, api, ops)
note_categories.subcategories   the SELECTION — what THIS note chose
                                (backend)
```

1. **Filing a note under a new subcategory adds it to the definition.** Typing
   `Work/ops` on a note where `ops` has never been used registers `ops` on Work,
   so it becomes a chip in the web UI and a row on the TUI's subcategory screen.
   Without this, "type it and it exists" would work for categories but not for
   subcategories — and the name would be invisible to every other UI.
2. **Editing one note never shrinks the definition.** `MergeSubcategories` only
   adds, because another note may be filed under a name this form never mentioned.
   Removing a name from the definition is a separate, explicit act (`d` on the
   subcategory screen), and even that does not refile notes — they keep the
   selection until edited. The confirm prompt says so, and a test pins it.

---

## The Store seam

Five methods added to `tui.Store` (implemented over both bytdb and the API):

| Method | Local | HTTP |
|---|---|---|
| `GetNoteCategoryDetails` | `models.GetNoteCategoryDetails` | `GET /notes/:id/categories` (already returned the detail shape) |
| `AddCategoryToNoteWithSubcategories` | `models.AddCategoryToNoteWithSubcategories` | `POST /notes/:id/categories/:cid` with `{"subcategories":[…]}` |
| `SetNoteCategorySubcategories` | `models.UpdateNoteCategorySubcategories` | `PUT /notes/:id/categories/:cid` |
| `SetCategorySubcategories` | `models.UpdateCategory` | `PUT /categories/:id` |
| `GetCategorySubcategoryNotes` | `models.GetNotesByCategoryAndSubcategories` | `GET /notes?cat=<name>&subcats[]=…` |

Two shapes worth recording:

**`SetCategorySubcategories` takes the whole `models.Category`, not an id.** Both
sides are whole-object updates — `models.UpdateCategory` rewrites name,
description and subcategories in one statement, and the API's PUT rejects an
empty name outright — so the fields the call is *not* about have to be carried
through or they get blanked. Callers always have the category in hand anyway,
since they needed its current subcategories.

**`GetCategorySubcategoryNotes` is name-keyed, not id-keyed**, which is the one
asymmetry in the seam. `/categories/:id/notes` takes no parameters; the
subcategory filter exists only on `/notes?cat=<name>&subcats[]=…` (the same
endpoint the web UI's chips use), and the models function is name-keyed too. So
the TUI passes `catFilter.Name`. `subcats[]` is repeated per subcategory rather
than comma-joined, because the handler reads `url.Values["subcats[]"]` and a
comma would arrive as one subcategory whose name contains a comma — which, under
AND semantics, matches nothing. Checked: `ListNotes`' default limit is 0 (no
limit), so the filtered read is not silently truncated.

`GetNoteCategories` (the plain, selection-less read) was kept alongside the new
detail method: it answers a different question and is still the right call when
the selection is irrelevant.

---

## Where subcategories now appear

**Note form** (`tui/form.go`). The Categories field takes specs; the placeholder
became `Work/backend, Personal — created if new`. Prefill goes through
`noteCatSpecs`, shared with the detail header so the two cannot spell an
assignment differently.

**`syncNoteCategories`** (`tui/commands.go`) — the rewrite, and the only real
logic in the file. Per category, three possible writes plus one that is not about
this note at all:

```
                    ┌ not named any more ─────────────→ remove the link
current link ───────┤
                    └ named, selection changed ───────→ update the link (PUT)
no link yet ─────────→ find-or-create the category ──→ attach with subs
                                                    └→ merge new names into
                                                       the definition
```

Iteration follows the *parsed order* (not map order) so writes happen in the
order the user typed and any error names them in that order. An unchanged
selection is **not** rewritten — every write becomes a sync change record, so a
plain re-save must produce none. `fakeStore.linkWrites` exists purely so a test
can assert that zero.

**Detail header** — `in Reading, Work/ops`. `detailScreen.cats` changed from
`[]models.Category` to `[]models.NoteCategoryDetailOutput`, and so did
`noteCatsLoadedMsg`.

**Categories screen** (`tui/categories.go`) — rows show `▸ backend, api, ops`
when a category defines any (falling back to description, then creation date),
and `FilterValue` includes the subcategory names so the fuzzy filter finds a
category by something it contains. `s` opens the new screen. `enter` still means
"the whole category" — narrowing is one level down, which is what keeps the
screen's primary verb intact.

**Subcategories screen** (`tui/subcategories.go`, new) — the category's defined
names as rows:

- `space` toggles a row into the filter, `enter` applies it. Several toggled →
  notes carrying **all** of them (AND, the web UI's rule). Nothing toggled →
  `enter` filters by the highlighted row alone, so the common case is one key.
- `n` adds a name to the definition (a duplicate is a status line, not a second
  row); `d` removes one, behind a confirm that states notes keep it.
- The pending filter lives in the list **title** (`filter: Work/backend/api`).
  First attempt appended it as a line below the list and it was invisible: the
  list is sized to the whole screen, so `clampPane` cut it. Caught by rendering
  the screen, not by the test — the test had asserted only that the string was
  somewhere in the view.
- Toggling rebuilds the items, which resets the widget cursor, so the index is
  restored afterwards; without that, multi-select jumps home on every press.
- `pick` pops **two** screens (itself and the category list) before delivering
  `categoryPickedMsg`, via `tea.Sequence` — same pattern as
  `categoriesScreen.pick`, one level deeper.
- Empty definition gets its own view (a category with nothing defined is the
  normal first visit, not an error) with a reduced footer: `n new • esc back`,
  since there is nothing to select or filter by.
- `esc` pops with `refresh` only when the definition actually changed (`dirty`),
  so a look-and-leave costs no reload.

**Browse** (`tui/browse.go`) — `subFilter []string` beside `catFilter`; title
reads `GoNotes — Work/backend` (via `FormatCategorySpec`, so the title is a
string the user could type back into a note); `refresh` routes to the
subcategory-aware read when it is set; `categoryPickedMsg` sets both filters
together so a stale subcategory cannot narrow a newly picked category. `esc`
peels **narrowest first**: fuzzy filter → subcategory → category. Dropping the
last two together would put `Work/backend` one keystroke from every note.

**Keymap** — `Subcats` (`s`, free on the category screen and not a chord cats
forwards) and `SelectSub` (`space`). `SelectSub` shares the space bar with
`TogglePrivate` but stays a separate binding so each footer names what that
screen actually toggles; both are pinned separately in `keymap_test.go`.

---

## Tests

- `models/category_spec_test.go` — the grammar (compact and repeated forms,
  duplicates, whitespace/empty segments), the format↔parse round trip, and the
  merge/compare rules.
- `tui/subcategories_test.go` — the form flows (spec → link + definition +
  prefill; swap a subcategory; drop to the bare category with the link intact; a
  bystander note's filing surviving all of it; **zero** link writes on an
  edit-free re-save), the screen (rows, empty state, two-pop pick, multi-select
  filter, add/remove, refresh-only-after-edit), the `s` door, and browse's AND
  filter plus esc peel order.
- `tui/store_http_test.go` — `TestSubcategoriesOverHTTP` runs the whole feature
  across the wire, because three of the shapes involved (attach body, link PUT,
  category PUT) and the `subcats[]` query exist only in HTTP mode: a store that
  got any of them wrong would still pass every `fakeStore` test in the package.
  The fake API grew those endpoints and now enforces the real handler's
  name-required rule on the category PUT.
- `fakeStore` gained per-link subcategories (`fakeLink`), `linkWrites`, and the
  five new methods — with AND semantics mirrored deliberately, since a lenient
  double would make a broken filter look like a working one.

Test-only note: `drainSequence` reads `tea.Sequence`'s messages via reflection.
Its type is unexported but its underlying type is `[]tea.Cmd`, so the *elements*
are an exported type and can be read back out — no unexported field is touched.
This is what let the two-pop-then-message ordering be asserted without driving a
real program (which is how `capture_test.go` had to handle Sequence before).

`go build ./...`, `go vet ./...`, `go test ./...` all pass. The suites use temp
data dirs, so the user's running server was no obstacle.

## Verified by rendering

A throwaway test in `tui/` printed the real views (then deleted): category rows
with their subcategory lists, the subcategory screen with two rows toggled, the
empty-definition state, browse under `GoNotes — Work/backend`, the form's
prefilled field (`"Reading, Work/ops"`), and the detail header. This caught the
clamped filter line and prompted two polish changes: the per-row hint
("space selects…") repeated on every row was replaced with a quiet
`in the filter` marker on selected rows only, and the empty state's footer was
reduced to the keys that do something.

## Files

| File | Change |
|---|---|
| `models/category_spec.go` | **new** — the shared `Name/Sub` grammar, format, merge, compare |
| `models/category_spec_test.go` | **new** |
| `md_format.go` | `parseCategorySpecs` delegates to `models` |
| `tui/subcategories.go` | **new** — the subcategory screen |
| `tui/subcategories_test.go` | **new** |
| `tui/store.go` | five new methods, documented at the seam |
| `tui/store_local.go` | bytdb implementations (`SetCategorySubcategories` preserves name/description) |
| `tui/store_http.go` | API implementations (`subcats[]` query, bodies, whole-object PUT) |
| `tui/commands.go` | spec-aware `syncNoteCategories`, `registerSubcategories`, details message, sub-notes and definition commands |
| `tui/form.go` | spec placeholder and prefill |
| `tui/detail.go` | header shows `Cat/Sub`; `cats` is now details |
| `tui/categories.go` | subcategory rows, richer `FilterValue`, the `s` door |
| `tui/browse.go` | `subFilter`, title, refresh routing, esc peel order |
| `tui/keymap.go` | `Subcats`, `SelectSub`, `subcategoriesHelp` |
| `tui/fake_store_test.go` | per-link subcategories, `linkWrites`, new methods |
| `tui/store_http_test.go` | new endpoints on the fake API, HTTP-mode feature test |
| `README.md` | key table rows plus a "Categories and subcategories" walkthrough |
| `.claude/skills/gonotes/SKILL.md` | TUI keys mention the subcategory path and the field notation |

## Follow-ups not done

- **Renames still stay in the web UI.** The category screen has no rename, and
  the subcategory screen cannot rename a name either (only add/remove) — a
  rename would have to rewrite every note's selection, which is a models-level
  operation that does not exist yet.
- **No subcategory-aware capture.** `ctrl+g` pane capture still files notes with
  no category at all; it could take a spec.
- **The AND-only filter.** Multiple toggled subcategories mean "all of them",
  matching the web UI. There is no OR, and no models function for one.
- The subcategory screen's rows show no note counts (would cost a query per row).
- `.cats-todo/todos.json` item `ad45f3f2…` is marked done; `b5ac0100…`
  ("Bump / Create GoNotes tag when at a good point") is still open and this may be
  a reasonable point for it.
