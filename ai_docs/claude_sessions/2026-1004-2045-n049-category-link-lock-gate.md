# Session: Lock-Gate the Per-Note Category Writes (N-049)

**Session ID:** `4d84aea5-2d3c-4540-88ce-aaff8d3930a6`
**Date:** 2026-10-04
**Branch:** master
**Base commit:** `aaf5f48` — Save session doc: N-025 account rename/merge

Picks up N-049 from `ai_docs/todo/next-list.md`, raised during the N-016
bulk-lock-gate work:

> The per-note category writes skip the lock gate. `POST`/`PUT`/`DELETE
> /api/v1/notes/:id/categories[/:category_id]` never call
> `authorizeNoteWrite`, and `localStore`'s category writers never call
> `AuthorizeNoteWrite`. So a second session can refile a note another
> session's form holds, and the holder's save then writes its own set back
> over that change. Gating them with the holder's token, as `UpdateNote` is
> gated, would close it. The holder's form already sends the token.

---

## The gap

A note's category links are part of what its edit form holds. The form loads
them when it opens, and its save sends the whole set back in one
`SetNoteCategories` call. A link write from another session while the form is
open therefore lands, and is then silently undone by that save.

`UpdateNote`, `DeleteNote` and `ToggleNoteFlag` already run the lock gate on
both sides of the Store seam. The link writers did not, on either side:

| Path | Before |
|---|---|
| `POST /notes/:id/categories/:cid` (`AddCategoryToNote`) | ungated |
| `PUT /notes/:id/categories/:cid` (`UpdateNoteCategory`) | ungated |
| `DELETE /notes/:id/categories/:cid` (`RemoveCategoryFromNote`) | ungated |
| `PUT /notes/:id/categories` (`SetNoteCategories`, whole set) | ungated |
| `localStore` link writers (5) | ungated |

The whole-set `PUT` is covered by the item's `[/:category_id]` pattern. It is
also the request the holder's own save makes.

## Checking "the holder's form already sends the token"

This was the premise to verify before gating. If a holder's link write
arrived without its token, the gate would refuse the holder's own save.

- **Web UI:** true. `apiRequest` adds `X-GoNotes-Lock` via
  `leaseHeaderFor(endpoint)`, which matches `/notes/:id` and every
  `/notes/:id/...` path. So `PUT /notes/:id/categories` from
  `cats_subcats.js` and the per-link calls in `app.js` carry the token.
- **TUI over HTTP:** false. `httpStore`'s five link writers used plain
  `s.request(...)` with no headers. Only `UpdateNote`/`DeleteNote`/
  `ToggleNoteFlag` used `requestH(..., s.lockHeaders(id))`. Gating the server
  alone would have broken `syncNoteCategories` at the end of every TUI save
  over HTTP.
- **TUI local:** the token lives in `s.tokens`, which the gate reads directly.

## Changes

### Server — `web/api/categories.go`

Each of the four handlers calls `authorizeNoteWrite(ctx, noteID)` right after
parsing the note id, before the body is decoded or ownership is checked. That
is the same position `UpdateNote` uses. `AddCategoryToNote` carries the full
explanation, and the other three point to it.

### Local store — `tui/store_local.go`

`AddCategoryToNote`, `AddCategoryToNoteWithSubcategories`,
`SetNoteCategorySubcategories`, `SetNoteCategories` and
`RemoveCategoryFromNote` each call
`models.AuthorizeNoteWrite(noteID, s.tokens.get(noteID))` first. One block
comment above them gives the reason.

### HTTP store — `tui/store_http.go`

The five link writers now use `requestH(..., s.lockHeaders(noteID))` and
finish through a new helper, `linkWriteErr(err, msg)`:

```
err == nil                                   → nil
409 with body data.reason == "locked"        → asConflictError(err)  (unwrapped *NoteLockedError)
anything else                                → serr.Wrap(err, msg)
```

The check on `reason` matters. `asConflictError` treats any unrecognized 409
as a lock, which is right on the note endpoints. But attach also returns 409
for "category already added to this note". Without the check, a duplicate
attach would open the one-note contention dialog for a note this session
holds.

### Test doubles

- `tui/fake_store_test.go`: the fakeStore link writers run the same gate
  against `f.tokens`, before touching anything, as its `UpdateNote` does.
  `AddCategoryToNote` delegates to `AddCategoryToNoteWithSubcategories`, so it
  is covered.
- `tui/store_http_test.go`: the fake API's four link handlers call a new
  `gateLinkWrite(w, r, noteID)`. It runs `AuthorizeNoteWrite` with the
  request's `X-GoNotes-Lock` header and writes the real 409 shape
  (`reason: "locked"`, `lock`). On a pass it copies the token into
  `api.data.tokens`. `api.data` is a fakeStore, which now gates on its own
  token table, and here it stands in for the models layer behind the handler.

## Tests

| Test | Pins |
|---|---|
| `web/api` `TestCategoryLinkWritesAreLockGated` | For each of the 4 routes: no token → 409 `locked`, wrong token → 409 `locked`, holder → 2xx. Only the holder's set landed. After release, an unlocked note's links are writable with no token. |
| `tui` `TestLocalStoreLinkWritesAreLockGated` | Two `localStore`s sharing one registry. All 5 writers from the non-holder return `*NoteLockedError`, nothing lands, and the holder's `SetNoteCategories` succeeds. |
| `tui` `TestHTTPStoreLinkWritesAreLockGated` | Two `httpStore`s against the fake API. The non-holder gets the typed error on all 5 writers, the holder's attach and set succeed, and a duplicate attach is a plain error, not a `NoteLockedError`. |

The new web/api test was run against `HEAD`'s `categories.go`. It fails there
(`attach with token "" returned 201, want 409`), so it detects the bug.

`go vet ./...` and `go test ./... -count=1` pass. `gofmt -l` is clean.

## Behavior change to note

The web batch bar (`performAddCategorySelected` /
`performRemoveCategorySelected` in `app.js`) sends one per-link request per
note. A note held by another session now answers 409. Both loops already
push any error other than "already added" or "relationship not found" into
`failures`, so the held note appears in `batchSummary` and the rest of the
batch proceeds. No JS change was needed.

`md_import.go` and sync apply call the `models` functions directly and stay
ungated. They write unheld or new notes, and the lock is a protocol between
sessions that opt in (see `models.AuthorizeNoteWrite`'s doc).

## Files

- `web/api/categories.go`: gate in 4 handlers
- `web/api/locks_test.go`: `TestCategoryLinkWritesAreLockGated`
- `tui/store_local.go`: gate in 5 link writers
- `tui/store_http.go`: token on link writes, `linkWriteErr`
- `tui/fake_store_test.go`: gate in fake link writers
- `tui/store_http_test.go`: `gateLinkWrite` on the fake API's link routes
- `tui/lock_test.go`: local and HTTP store gate tests
- `ai_docs/todo/next-list.md`: N-049 moved to Closed

## Next

Closed: N-049. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
