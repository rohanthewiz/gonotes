# Session: Comma-separated categories in the web UI, and seeding the Next list

Session ID: `67fc7795-935b-478a-ac72-cb1e88969ab2`
Date: 2026-09-23

## The ask

1. "On the Web UI (not sure if this would work for the TUI also) when I type a
   category input with a comma in it, I mean to make two categories there."
2. `/next-list seed`: build the living follow-up list from session history.

## Part 1: comma-separated category input

### Survey

- **The TUI already did this.** Its category field is one line of
  comma-separated specs parsed by `models.ParseCategorySpecCSV`
  (`models/category_spec.go`), the same grammar used by Markdown frontmatter
  and `gn-clip.sh -c`. No TUI change was needed.
- **The web UI did not.** `app.addCategoryEntry` in
  `web/static/js/cats_subcats.js` took the whole trimmed input as one name, so
  `Work, Personal` became a single category literally named that.

### What changed

- **`addCategoryEntry`** splits on `,`, trims each piece, and drops empty
  pieces, so a trailing or doubled comma is harmless. It adds one entry card
  per name, still matching existing categories case-insensitively. Names
  already on the note, or repeated within the input, are skipped silently; the
  "already added" warning fires only when nothing new was added.
- **`onCategoryChange`** (the "(new)" indicator) now checks only the segment
  after the last comma, which is the name being typed. Before, it checked the
  whole string.
- **Placeholder** in `web/pages/landing/preview_panel.go`: "Type or select
  category (comma-separate several)...".

### Decisions

- **Commas only, not the TUI's `/` subcategory notation.** The ask was about
  commas. Adopting `Work/backend` in the web input would also change how
  existing web names containing `/` are read, which is a separate call. It is
  logged as N-043.
- **No server change.** Save already loops over `categoryEntries`, creating new
  categories and linking each, so more entries simply means more of the same
  calls. That makes the one-request-per-link cost (N-020) more visible; the
  item text now says so.
- A comma can no longer appear inside a web category name, which matches the
  TUI and Markdown formats.

### Verification

- `go build ./...` and `go vet ./web/...` pass; `node --check` passes on
  `cats_subcats.js`.
- **Not exercised in a browser.** The JS is embedded with `go:embed`, so
  rebuild the binary before trying it (see the CDP-harness memory).

## Part 2: `/next-list seed`

Created `ai_docs/todo/next-list.md` from the 15 newest session docs
(`2026-0722-1558` … `2026-0909-1858`). For each item, the first appearance
was looked up across all 25 docs, and every premise was checked against the
code at `e0c2a39`. Result: 34 Open, empty Roadmap, 2 Non-goals, 5 Closed.

What the check turned up:

- **N-029 is a real defect, and worse than written.** The batch bar's **Set
  Category** and **Toggle Privacy** buttons (`note_list.go:14-15`) call
  `app.categorySelected` / `app.togglePrivacySelected`, which have never been
  defined in any JS file since the first landing-page commit (`e1540fd`).
  Clicking either throws a TypeError.
- **Already done, moved to Closed:** the web "Compact only" button, the
  TUI's idle sync-poll back-off, and persisting sync mode (all in `2bdfb0c`);
  the spoke user-GUID mismatch (fixed in the `2026-0818-1934` session); and the
  v0.2.0 release tag.
- **Nothing lapsed silently:** only the three sync docs from 08-18 carried
  items forward, and every item they dropped had been finished. All other docs
  wrote per-feature follow-ups that were never carried forward. The list is
  the first place they are all collected.
- **Premises updated:** N-023 (operation-9 rows are correct behavior, only a
  wasted entry per batch), N-015 (the mobile app was rebuilt since; needs
  checking in that repo), N-020 (raised in importance by Part 1).
- N-001 (TUI category rename) was first raised in
  `2026-0710-1656-tui-implementation`, outside the window, and raised again on
  08-17.

## Files

- `web/static/js/cats_subcats.js`: comma splitting in `addCategoryEntry`,
  last-segment check in `onCategoryChange`
- `web/pages/landing/preview_panel.go`: placeholder text
- `ai_docs/todo/next-list.md`: new living list

## Next

Closed: None. Declined: None. Raised: N-042, N-043.
Deferred: None. Promoted: None.
Updated: N-020. Full list: `ai_docs/todo/next-list.md` (seeded this session).
