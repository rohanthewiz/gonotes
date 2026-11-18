# Session: whole-app review for reliability, performance and usability; plan and first fixes

**Session ID:** `ead430f2-549f-42f1-9f42-d72789fd41e0`
**Date:** 2026-11-18
**Branch:** master
**Base commit:** `66c3670`, "Clean look-alike spaces out of text pasted into the note textarea"

## The ask

Take a deep look at GoNotes, say what can be improved in reliability,
performance and usability, turn that into a plan (Next-list items), and start
on it.

## How the review was done

The data layer (`models/db.go`, `store.go`, `note.go`, `category.go`,
`schema.go`, the query runner) was read directly. Four read-only reviews ran
in parallel over the sync layer, the HTTP server, the web front end and the
TUI, each asked for at most ten to twelve findings with file, scenario,
quoted evidence and confidence. Their top claims were then re-checked in the
code, and where a browser could settle it, in a browser, before anything was
listed as fact or fixed.

Two findings changed shape under that re-check:

- The web front end's "saving a large note erases its body" was real code but
  had never run. The msgpack library's script URL was a 404 from the day it
  was added (msgpack 3.0 moved the bundle eleven months earlier), so the
  browser always sent plain JSON. The defect was a trap for whoever fixed the
  URL, not a live loss. It was fixed anyway, and the encoding switched off.
- The same discovery meant the stale-write guard *was* live on the plain path,
  which made the reviewer's follow-on scenario a current bug: flag a note,
  then save it from the same tab, and the save was refused as stale on every
  retry.

## The plan

`ai_docs/todo/next-list.md`, N-055 to N-087: 33 items, in order of data
safety, then performance, then usability. Six were closed here; 27 are open.

## Done

| Item | What |
|---|---|
| N-055 | Scheduled, rotating backups of both databases (`models/backup.go`): online copy, private copy stays encrypted, unchanged data is not backed up again. Server and local TUI; `gonotes backup [list]`; admin `POST /admin/backup`, `GET /admin/backups`. |
| N-056 | Web save path: server accepts a plain `body` under the msgpack header and carries the version both ways; encoder chunked; msgpack encoding switched off and its dead script tag removed; flag-then-save no longer refused as stale; `editNote` re-reads the note; a stale 409 offers overwrite or keep editing. |
| N-057 | `POST /api/v1/setup/apply` needs an admin once accounts exist, rejects multi-line values, and merges into `.env` instead of overwriting it (which deleted a stored encryption key). |
| N-058 | At 1024px wide and below, the preview and edit pane was off-screen with nothing to open it. It now opens on select, New and Edit, with a back button. |
| N-060 | Push sends the whole backlog in batches of 100 instead of the first 100. A push retried after token expiry no longer goes out with an empty body. Hub rejections are recorded in the status and shown in the web and TUI summaries. |
| N-061 | TUI: a half-failed save no longer creates a duplicate note on retry, and a failed or pending category prefill no longer strips the note's categories on save. |

N-059 (changes marked delivered when the hub refused them) is half done: the
refusals are now visible, the retry policy is not designed. N-087 lost its
401-retry item, fixed under N-060.

## Verification

- `go vet ./...` clean, `gofmt -l` empty, `go test -race ./...` green before
  and after.
- New tests: `models/backup_test.go` (7), `models/sync_client_push_test.go`
  (3), `web/api/backup_test.go`, `web/api/msgpack_body_test.go` (2),
  `web/api/setup_apply_test.go` (2), three in `web/api/config_env_test.go`,
  `tui/form_partial_save_test.go` (6), one in `tui/sync_test.go`.
- Tests shown to bite: the msgpack tests fail on the old server code ("stored
  body is 0 bytes"); the push retry test fails with the rewind disabled; three
  TUI tests fail with the two fixes disabled ("categories after the save =
  []"). The backup rotation test caught a real bug while it was being
  written: a set taken in the same second as another could be named so that
  its own rotation pass removed it.
- Browser, per the web UI harness (scratch server on 8899, own `--dir`,
  headless Chrome over CDP): 18 checks on the save path (a 300 KB body saved
  through the form and read back intact; the form opening on text changed
  behind the tab; 409 then 200 after one prompt; flag-then-save first time),
  19 checks on the pane at 1400, 1000 and 700px, and a screenshot at 900px.
- Backups end to end on the scratch server: startup backup, both endpoints,
  `backup list` beside a running server, `backup` refused beside it and
  working once stopped, relative `-o`.
- The scratch server and browser were stopped by PID. The live server on 8444
  was never stopped, written to or read from beyond `/api/v1/health`.

One hazard met while verifying: the test browser served a cached
`app.js?v=20` from earlier in the same session, so a rebuilt asset looked
unchanged until the cache was cleared. That is N-071 (hand-bumped asset
versions) in miniature.

## Not done, and worth knowing

- The running server has none of this until it is rebuilt: it runs from the
  cats plugin's `bin/gonotes`, which only refreshes on `catctl plugin link .`.
  A build check during the session did overwrite the git-ignored `./gonotes`
  in the repo root with a current build.
- Once a rebuilt server starts, it takes its first backup at startup into
  `~/.gonotes/data/backups/` and then one a day.
- Restoring a backup onto a sync **spoke** is untested (noted in the README
  and in N-085).
- N-064 (ownership possibly lost in transit) is the open item most worth a
  runtime check before trusting it either way.

## Next

Closed: N-055, N-056, N-057, N-058, N-060, N-061. Declined: None.
Raised: N-055 to N-087. Deferred: None. Promoted: None. Moved: None.
Updated: N-059 (rejections now visible), N-086 and N-087 (narrowed).
Full list: `ai_docs/todo/next-list.md`.
