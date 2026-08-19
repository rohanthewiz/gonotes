# Session: What the Batch Delete Wasn't Saying

**Session ID:** `4041a550-2904-4471-ac21-24052441595b`
**Date:** 2026-08-19
**Branch:** master
**Base commit:** `832f182` — Stop auto-capitalizing new categories and subcategories
**Commit:** `f5f5a10` — Report what a batch delete actually deleted

## Context

Task, as given: "Delete not working within the Web UI?", clarified mid-turn
with "this is when multiple notes are selected".

No reproduction steps, no error text — a symptom report. The whole first half
of this session was working out what "not working" meant, because the happy
path turned out to work fine.

---

## Establishing that the happy path works

Before theorising, the batch path was driven end-to-end against a real server
with a real browser. That method is worth recording because it settled several
wrong guesses quickly.

**Scratch server.** `models.DataDir` is `./data`, relative to the working
directory the CLI chdirs into, so a server on a throwaway dataset is just a
different `--dir`:

```
go build -o <scratch>/gonotes .
cd <scratch>/run && GONOTES_PORT=8899 \
  GONOTES_JWT_SECRET=<32+ chars> gonotes --dir <scratch>/run --port 8899
```

Two gotchas: the JWT secret must be **at least 32 characters** or startup dies,
and `POST /api/v1/notes` requires a client-supplied `guid` (`"guid is
required"`).

**Real browser, driven over CDP.** Headless Chrome with
`--remote-debugging-port=9222`, then Python `websockets` (already installed)
speaking `Runtime.evaluate` / `Network.enable` against the page target. Auth is
seeded by navigating to `/login`, setting `localStorage.token`, then navigating
to `/`. `window.confirm = () => true` gets past the confirmation dialog.
`Network.setCacheDisabled` matters — see the embed note below.

What that showed:

| Scenario | Result |
|---|---|
| 5 notes, select-all, Delete | all 5 gone, 200 each |
| 5 notes, three checkboxes clicked individually | those three gone |
| 100 notes, select-all, Delete | all 100 gone |
| curl, 5 sequential `DELETE`s | 200 each |

So the mechanism was sound. Two things looked like bugs and were not:

- **Row order is not what you'd expect.** Notes created within the same second
  share `updated_at`, and the sort is stable over an arbitrary input order, so
  the list came back `22,20,21,18,19,…`. An early test read that as "the wrong
  note got deleted". Instrumenting `dataset.id` before and after each click
  showed selection tracking was exact.
- **`toggleNoteSelection` re-renders the whole list from inside the clicked
  checkbox's own handler**, destroying the element mid-event. Ugly, harmless.

---

## Finding 1 — failures were silent

`DELETE /api/v1/notes/:id` is gated by the note-lock registry
(`web/api/notes.go:512`, `authorizeNoteWrite`). A note another session holds
open — a GoNotes TUI in a cats pane — answers **409** and stays exactly where
it was.

The old handler swallowed that:

```js
for (const noteId of state.selectedNotes) {
  try { await apiRequest(`/notes/${noteId}`, { method: 'DELETE' }); }
  catch (error) { console.error('Failed to delete note:', noteId); }
}
state.selectedNotes.clear();
await loadNotes();
showToast('Notes deleted', 'success');   // unconditional
```

Reproduced by acquiring a lock from a second session and running the batch: two
of six notes stayed, every checkbox was cleared, and the UI said "Notes
deleted". **A batch that deleted nothing was indistinguishable from one that
deleted everything**, and the cleared selection destroyed the only evidence of
which notes survived.

Fixed by removing each id from the selection *as its delete lands*, so a
mid-batch failure leaves exactly the survivors selected, and by reporting the
count and the server's own wording:

```
Deleted 3 of 4; 1 still selected — note is locked by TUI pane w1:p3 since 8s ago
```

The survivors' checkboxes come back ticked after the reload, ready to retry
once the other session lets go.

---

## Finding 2 — `?limit=100` truncated the library

`loadNotes` asked for `/notes?limit=100`. That is not paging, it is
truncation: every filter this UI offers (search, regex, category,
subcategory, flagged, privacy) runs **client-side** in `getFilteredNotes` over
`state.notes`, and there is no pagination control and no server-side search.
Whatever the request left behind was invisible to search, absent from the
result count, and unreachable by select-all.

So with 400 notes, a search combed 100 of them and select-all-then-delete
removed at most 100 — which reads as "delete didn't work".

Paging would have been the wrong repair. `models.ListNotes` (`models/note.go:307`)
reads **both** databases in full, merges, sorts, and only then applies
limit/offset in memory via `paginate`. N paged requests are N full scans for
data one request already holds. Omitting `limit` is how the API spells
unbounded (`web/api/notes.go:210`, `limit := 0 // 0 means no limit`).

Verified with 150 notes: 150 rows, "150 notes" in the count, a search for text
in note #140 finds it, select-all + Delete clears all 150.

---

## Finding 3 — the selection outlived a change of view

`state.selectedNotes` was cleared in exactly two places: `toggleSelectAll(false)`
and the end of `deleteSelected`. Never on a filter change, never on reload.

Pick twenty notes, switch category, and the bar still read "20 selected" over
three rows — so "Delete 20 notes?" removed seventeen notes that were nowhere on
screen. The same trap runs the other way after a reload: an id deleted from
another session lingers as a phantom that every later batch re-attempts and
fails on.

The fix reconciles the selection against the rows about to be drawn, inside
`renderNoteList` — the one choke point every filter, search, sort and reload
path already funnels through. That covers paths added later by construction,
which is why it lives there rather than as a `clear()` sprinkled over a dozen
filter mutation sites.

The resulting rule, stated in the code: **what is selected is what is visible.**
A note that falls out of the filter is deselected, and re-widening does not
bring the tick back — the honest reading of "Delete N notes", where N is what
you can see.

Two supporting bits fell out of it:

- The header checkbox now tracks the selection (`checked` / `indeterminate`).
  Left ticked over a pruned selection, the user's next click registers as
  "deselect all" when they meant "select all".
- `batch-count` is written even while the bar is hidden, so an emptied
  selection cannot flash a stale "20 selected" when the bar returns.

---

## Verification

Final end-to-end pass, one browser session, against 6 notes with one locked by
a simulated TUI:

```
filter 'alpha', select all      → 3 selected, select-all checked
switch filter to 'beta'         → 0 selected, bar hidden      (was: "3 selected")
select all beta, Delete         → "Deleted 3 of 4; 1 still selected —
                                   note is locked by TUI pane w1:p3 since 8s ago"
rows after                      → the locked note only, still ticked
clear the search                → 1 selected among all rows, header indeterminate
```

`go build ./...` and `go test ./...` pass.

### The trap that cost the most time

`web/static.go` embeds the asset tree with `//go:embed all:static`. **A
JS-only edit does nothing until the binary is rebuilt.** A verification run
mid-session reported the old `updateBatchActions` behaviour from a stale build
and briefly looked like the fix had failed. `Network.setCacheDisabled` does not
help — the bytes are in the binary, not the browser.

For the same reason the asset version in `web/pages/landing/page.go` was bumped
`app.js?v=10` → `v=11`, so deployed browsers do not serve a cached copy.

## Files touched

| File | Change |
|---|---|
| `web/static/js/app.js` | `loadNotes` unbounded; `reconcileSelection`; `updateBatchActions` takes a visible count and syncs the header tick; `deleteSelected` reports per-note outcomes |
| `web/pages/landing/page.go` | asset version `v=10` → `v=11` |

## Follow-ups not done

- **`apiRequest` toasts once per failure.** A batch where fifty notes are
  locked stacks fifty toasts plus the summary. Pre-existing behaviour, left
  alone rather than changed globally from inside a delete fix — but the
  summary now makes the per-failure toasts largely redundant.
- **Loading the whole library is now unbounded.** That matches how the client
  already works (all filtering is client-side, `ListNotes` materialises
  everything server-side anyway), but there is no guard and no "this is a very
  large library" signal. If note counts ever get big, the answer is real
  server-side search and paging, not a silent cap — a silent cap is the bug
  that was just removed.
- **The batch bar's other two actions were not audited.** `categorySelected`
  and `togglePrivacySelected` sit next to Delete and may swallow failures the
  same way; only the delete path was examined.
- **Whether this was actually the user's bug is unconfirmed.** The lock
  conflict was reproduced synthetically. If the real symptom is deleted notes
  *reappearing* later, that is the sync path and a separate investigation.
