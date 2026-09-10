package models

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/rohanthewiz/serr"
)

// query.go runs an advanced search end to end: load the user's notes (and,
// only when the query needs them, their category links), evaluate the parsed
// filter over each one, then order and page the survivors.
//
//	ParseQuery ──▶ Query ──┐
//	                       ├──▶ RunQuery ──▶ QueryResult
//	  notes (both DBs) ────┤
//	  note_categories  ────┘  (loaded only when NeedsCategories)
//
// WHY EVALUATION HAPPENS IN GO, NOT IN SQL. The obvious alternative is to
// translate the parsed tree into a bytdb WHERE clause and let the engine do
// it. Three things make that the wrong trade here:
//
//   - A user's notes live in TWO databases (public and private), and a
//     category link in one of them references the categories catalog in the
//     other. No single SQL statement spans that; `category = 'airflow'`
//     would have to be a Go-side join anyway.
//   - bytdb holds every row in memory already, so "pushing down" the filter
//     saves no IO — only a walk over rows that are a pointer dereference away.
//   - The semantics this language commits to (case-insensitive equality,
//     absence rather than three-valued NULL, existential-then-negated
//     multi-value matching, date literals as windows) are ours, not the
//     engine's. Implementing them in Go is how they stay identical no matter
//     what the storage layer does next.
//
// The cost is a full scan per query. For a personal note store — the same
// assumption the web UI already makes when it loads every note into the
// browser — that is microseconds, and it is bounded by the same data the app
// already holds in RAM.

// QueryOptions are the run-time knobs a caller sets around a parsed query.
// Where they overlap with the query text (limit, offset, ordering), the
// options win: an API caller's paging is about the transport and must not be
// silently overridden by a LIMIT somebody left in the box.
type QueryOptions struct {
	// Limit caps the returned notes; 0 means no cap (the query's own LIMIT
	// still applies). Offset skips from the front of the ordered result.
	Limit, Offset int

	// SortField overrides ORDER BY with a single field, named as in the
	// catalog. Empty leaves the query's own ordering, or the default.
	SortField string
	SortDesc  bool

	// IncludeDeleted forces soft-deleted notes into the candidate set. A
	// query that mentions deleted_at opts in on its own (see
	// Query.IncludesDeleted), so this is for callers that want them without
	// saying so in the text.
	IncludeDeleted bool

	// Now fixes the clock relative time literals resolve against. Zero means
	// time.Now(). Tests set it; nothing else needs to.
	Now time.Time
}

// QueryResult is what a run produced.
type QueryResult struct {
	// Notes is the page: ordered, then limited and offset.
	Notes []Note
	// Matched is how many notes satisfied the filter BEFORE paging — the
	// number a UI shows as "N results", which a paged Notes slice cannot
	// tell it.
	Matched int
	// Scanned is how many notes were examined, i.e. the size of the user's
	// library at the moment of the run. Matched/Scanned is the selectivity a
	// person is really asking about when a query returns nothing.
	Scanned int
	// Query is the parsed form, for its canonical String and field list.
	Query *Query
	// Elapsed is wall-clock time for load + evaluate + sort.
	Elapsed time.Duration
}

// QueryNotes parses and runs advanced-search text in one call. A syntax error
// comes back as a *QueryError with a position; anything else is a storage
// failure.
func QueryNotes(src, userGUID string, opts QueryOptions) (*QueryResult, error) {
	q, err := ParseQuery(src)
	if err != nil {
		return nil, err
	}
	return RunQuery(q, userGUID, opts)
}

// RunQuery executes an already-parsed query. Splitting it from QueryNotes
// lets a caller parse once and run repeatedly — the TUI re-runs the same
// query on every refresh — and lets the API report parse errors (400) apart
// from storage errors (500).
func RunQuery(q *Query, userGUID string, opts QueryOptions) (*QueryResult, error) {
	started := time.Now()
	env := &evalEnv{Now: opts.Now, UserGUID: userGUID}
	if env.Now.IsZero() {
		env.Now = started
	}

	includeDeleted := opts.IncludeDeleted || q.IncludesDeleted()
	notes, err := loadNotesForQuery(userGUID, includeDeleted)
	if err != nil {
		return nil, err
	}

	// Category links are a second pass over both databases; skip it entirely
	// unless the query can observe them.
	links := map[int64][]NoteCategoryMapping{}
	if q.NeedsCategories() {
		links, err = loadCategoryLinksForQuery(userGUID, includeDeleted)
		if err != nil {
			return nil, err
		}
	}

	matched := make([]Note, 0, len(notes))
	for i := range notes {
		facts := noteFacts{note: &notes[i], links: links[notes[i].ID]}
		if q.Where == nil || q.Where.eval(env, &facts) {
			matched = append(matched, notes[i])
		}
	}

	sortQueryResults(matched, q, opts)

	limit, offset := q.Limit, q.Offset
	if opts.Limit > 0 {
		limit = opts.Limit
	}
	if opts.Offset > 0 {
		offset = opts.Offset
	}

	return &QueryResult{
		Notes:   paginate(matched, limit, offset),
		Matched: len(matched),
		Scanned: len(notes),
		Query:   q,
		Elapsed: time.Since(started),
	}, nil
}

// Matches tests one already-loaded note against the query, without touching
// storage.
//
// RunQuery is the normal door and loads the notes itself. This one exists for
// callers that already hold a note and its category links and only want the
// verdict — a client-side filter over a list it fetched for other reasons, or a
// test that wants the real semantics without a database behind them. links may
// be nil for a note with no categories; a query that reads a category field
// then simply finds none, which is the correct answer for an unfiled note.
//
// The clock is time.Now(): a caller wanting a fixed one for reproducibility
// should go through RunQuery with QueryOptions.Now.
func (q *Query) Matches(n *Note, links []NoteCategoryMapping, userGUID string) bool {
	if q.Where == nil {
		return true
	}
	facts := noteFacts{note: n, links: links}
	return q.Where.eval(&evalEnv{Now: time.Now(), UserGUID: userGUID}, &facts)
}

// loadNotesForQuery reads the user's whole note set from both databases.
//
// It does not reuse ListNotes because that one hard-codes "not deleted" and
// sorts by created_at before returning — work this path would immediately
// redo under the query's own ordering.
func loadNotesForQuery(userGUID string, includeDeleted bool) ([]Note, error) {
	// The completer runs before login and in tests that never opened a
	// database. Both are legitimate — the catalog half of a completion needs
	// no data — so an unopened store is an empty result here rather than the
	// nil-engine panic a raw query would produce.
	if pubDB == nil || privDB == nil || userGUID == "" {
		return nil, nil
	}
	where := `WHERE created_by = ? AND deleted_at IS NULL`
	if includeDeleted {
		where = `WHERE created_by = ?`
	}
	query := `SELECT ` + noteCols + ` FROM notes ` + where

	notes, err := queryBothNotes(func(en *dbEngine) ([]Note, error) {
		return queryNotes(en, query, userGUID)
	})
	if err != nil {
		return nil, serr.Wrap(err, "failed to load notes for query")
	}
	return notes, nil
}

// loadCategoryLinksForQuery builds note id → its category links, resolving
// category names from the public catalog once per category rather than once
// per link.
//
// GetAllNoteCategoryMappings answers almost the same question, but always
// excludes deleted notes and returns a flat slice a caller would have to
// index anyway. Keeping this one separate is what lets a `deleted_at IS NOT
// NULL AND category = 'x'` query work at all.
func loadCategoryLinksForQuery(userGUID string, includeDeleted bool) (map[int64][]NoteCategoryMapping, error) {
	if pubDB == nil || privDB == nil || userGUID == "" {
		return map[int64][]NoteCategoryMapping{}, nil
	}
	join := `INNER JOIN notes n ON nc.note_id = n.id
		WHERE n.created_by = ? AND n.deleted_at IS NULL`
	if includeDeleted {
		join = `INNER JOIN notes n ON nc.note_id = n.id
		WHERE n.created_by = ?`
	}

	type rawLink struct {
		noteID      int64
		categoryID  int64
		subcatsJSON sql.NullString
	}

	raws, err := queryBothNotes(func(en *dbEngine) ([]rawLink, error) {
		rows, err := en.Query(`SELECT nc.note_id, nc.category_id, nc.subcategories
			FROM note_categories nc `+join, userGUID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []rawLink
		for rows.Next() {
			var r rawLink
			if err := rows.Scan(&r.noteID, &r.categoryID, &r.subcatsJSON); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	})
	if err != nil {
		return nil, serr.Wrap(err, "failed to load category links for query")
	}

	names := map[int64]string{}
	byNote := make(map[int64][]NoteCategoryMapping, len(raws))
	for _, r := range raws {
		name, ok := names[r.categoryID]
		if !ok {
			if cat, _ := getCategoryByID(r.categoryID); cat != nil {
				name = cat.Name
			}
			names[r.categoryID] = name
		}
		m := NoteCategoryMapping{NoteID: r.noteID, CategoryID: r.categoryID, CategoryName: name}
		if r.subcatsJSON.Valid && r.subcatsJSON.String != "" {
			var subs []string
			if err := json.Unmarshal([]byte(r.subcatsJSON.String), &subs); err == nil {
				m.SelectedSubcategories = subs
			}
		}
		byNote[r.noteID] = append(byNote[r.noteID], m)
	}
	return byNote, nil
}

// sortQueryResults orders the matched notes.
//
// Precedence: an explicit option, then the query's own ORDER BY, then
// updated_at descending — the same default the note list has always shown, so
// a query with no ordering does not silently reshuffle a familiar list.
//
// Ties break on id descending. Without it, two notes written in the same
// clock tick (an import, a sync apply) could swap places between two runs of
// the same query, which looks like data changing underfoot.
func sortQueryResults(notes []Note, q *Query, opts QueryOptions) {
	order := q.Order
	if opts.SortField != "" {
		if f, ok := LookupQueryField(opts.SortField); ok && !f.Multi {
			order = []QueryOrder{{Field: f, Desc: opts.SortDesc}}
		}
	}
	if len(order) == 0 {
		updated, _ := LookupQueryField("updated_at")
		order = []QueryOrder{{Field: updated, Desc: true}}
	}

	sort.SliceStable(notes, func(i, j int) bool {
		for _, o := range order {
			cmp := compareNotesBy(o.Field, &notes[i], &notes[j])
			if cmp == 0 {
				continue
			}
			if o.Desc {
				return cmp > 0
			}
			return cmp < 0
		}
		return notes[i].ID > notes[j].ID
	})
}

// compareNotesBy is the three-way comparison for one sortable field. Only
// single-valued fields reach it — the parser refuses ORDER BY on a
// multi-valued field, and the options path filters the same way.
//
// A NULL sorts before every value, which puts "never synced" and "never
// edited by anyone else" at the top under ASC and at the bottom under DESC.
func compareNotesBy(f *QueryField, a, b *Note) int {
	fa := noteFacts{note: a}
	fb := noteFacts{note: b}

	switch f.Kind {
	case KindNumber:
		return compareFloat(firstFloat(fa.numberValues(f)), firstFloat(fb.numberValues(f)))
	case KindBool:
		av, _ := fa.boolValue(f)
		bv, _ := fb.boolValue(f)
		switch {
		case av == bv:
			return 0
		case bv:
			return -1
		}
		return 1
	case KindTime:
		at, aok := firstTime(fa.timeValues(f))
		bt, bok := firstTime(fb.timeValues(f))
		if !aok || !bok {
			return boolCompare(aok, bok)
		}
		return at.Compare(bt)
	}
	as, aok := firstString(fa.stringValues(f))
	bs, bok := firstString(fb.stringValues(f))
	if !aok || !bok {
		return boolCompare(aok, bok)
	}
	return strings.Compare(foldASCII(as), foldASCII(bs))
}

// boolCompare orders "has a value" after "has none", so absence sorts first.
func boolCompare(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

func firstFloat(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return v[0]
}

func firstTime(v []time.Time) (time.Time, bool) {
	if len(v) == 0 {
		return time.Time{}, false
	}
	return v[0], true
}

func firstString(v []string) (string, bool) {
	if len(v) == 0 {
		return "", false
	}
	return v[0], true
}
