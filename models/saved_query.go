package models

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rohanthewiz/serr"
)

// saved_query.go keeps a user's advanced-search queries on the server: the
// ones they named ("saved") and the ones they recently ran ("history").
//
// Both used to be absent or browser-local — history lived in the web page's
// localStorage, so a second browser, a private window or the TUI started from
// nothing, and there was no way to name a query at all. Moving them here gives
// every front end the same list from one place:
//
//	web query bar ──POST /query/saved, /query/history──┐
//	TUI query screen ──RecordQuery (local or HTTP)─────┤
//	                                                   ▼
//	                                   saved_queries (private DB)
//	                                                   │
//	              CompleteQuery on an empty box ◀──────┘  (both UIs render it)
//
// Surfacing them through the completer rather than through a separate list in
// each UI is the same choice the rest of advanced search makes: one
// implementation of "what can I type here", two thin front ends.
//
// The table lives in the private database; createPrivateOnlySchema explains
// why query text is treated as content.

// SavedQueryKind distinguishes a named query from a history entry.
const (
	SavedQueryKindSaved   = "saved"
	SavedQueryKindHistory = "history"
)

// savedQueryHistoryMax caps history per user. It matches the web UI's old
// localStorage cap, so moving the list server-side changes where it lives,
// not how long it is.
const savedQueryHistoryMax = 25

// Name and text limits. A name is a label in a popup row; a query is typed by
// hand. Anything far past these is a mistake or abuse, and refusing it keeps a
// completion response from carrying a megabyte of someone's paste.
const (
	savedQueryNameMax = 80
	savedQueryTextMax = 4096
)

// ErrSavedQueryNotFound is returned when a delete names an id the user does
// not own (or that does not exist — the two are deliberately not told apart).
var ErrSavedQueryNotFound = errors.New("saved query not found")

// SavedQueryInputError is a save refused for something the user can fix — a
// missing name, an over-long text. It is its own type so the API answers it
// 400 with the message as written, and a storage failure 500 and logged,
// without either side matching on strings.
type SavedQueryInputError struct{ Msg string }

func (e *SavedQueryInputError) Error() string { return e.Msg }

// savedQueryMu serializes writes. Upsert-by-name and history de-duplication
// are read-then-write sequences, and two tabs saving the same name at once
// would otherwise both see "absent" and insert twice. Writes here happen at
// human speed, so a process-wide lock costs nothing.
var savedQueryMu sync.Mutex

// SavedQuery is one row of saved_queries.
type SavedQuery struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name,omitempty"`
	Query      string    `json:"query"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// SavedQueryList is what the list endpoint returns: the named queries in name
// order and the history most-recent first.
type SavedQueryList struct {
	Saved   []SavedQuery `json:"saved"`
	History []SavedQuery `json:"history"`
}

// ListSavedQueries returns the user's named queries (sorted by name, ignoring
// case) and history (most recently used first).
//
// An unopened store or an anonymous caller yields an empty list rather than an
// error, for the same reason loadNotesForQuery does: the completer calls this
// before login and in tests that never opened a database.
func ListSavedQueries(userGUID string) (*SavedQueryList, error) {
	out := &SavedQueryList{Saved: []SavedQuery{}, History: []SavedQuery{}}
	if privDB == nil || userGUID == "" {
		return out, nil
	}
	rows, err := loadSavedQueries(userGUID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Kind == SavedQueryKindSaved {
			out.Saved = append(out.Saved, r)
		} else {
			out.History = append(out.History, r)
		}
	}
	sort.SliceStable(out.Saved, func(i, j int) bool {
		return strings.ToLower(out.Saved[i].Name) < strings.ToLower(out.Saved[j].Name)
	})
	sortHistory(out.History)
	return out, nil
}

// sortHistory orders history newest first. The id tiebreak matters: two runs
// inside one clock tick would otherwise come back in an arbitrary order, and
// the "most recent" row is the one a user expects at the top.
func sortHistory(h []SavedQuery) {
	sort.SliceStable(h, func(i, j int) bool {
		if !h[i].LastUsedAt.Equal(h[j].LastUsedAt) {
			return h[i].LastUsedAt.After(h[j].LastUsedAt)
		}
		return h[i].ID > h[j].ID
	})
}

// loadSavedQueries reads every row for a user. The per-user set is small by
// construction (history is capped; named queries are typed by hand), so the
// filtering and ordering happen in Go — which also sidesteps relying on
// case-insensitive comparison in the SQL layer.
func loadSavedQueries(userGUID string) ([]SavedQuery, error) {
	rows, err := privDB.Query(`
		SELECT id, kind, name, query, created_at, last_used_at
		FROM saved_queries
		WHERE user_guid = ?`, userGUID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to list saved queries")
	}
	defer rows.Close()

	var out []SavedQuery
	for rows.Next() {
		var r SavedQuery
		var name sql.NullString
		if err := rows.Scan(&r.ID, &r.Kind, &name, &r.Query, &r.CreatedAt, &r.LastUsedAt); err != nil {
			return nil, serr.Wrap(err, "failed to scan saved query")
		}
		r.Name = name.String
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "error iterating saved queries")
	}
	return out, nil
}

// SaveNamedQuery stores query under name, replacing the text of an existing
// query with the same name (compared ignoring case). Saving under an existing
// name is how a user edits a saved query, so it is an upsert rather than a
// conflict.
//
// The text must parse. A saved query that cannot run is a trap for later, and
// the parse error comes back as the usual *QueryError so the caller can point
// at the mistake exactly as it does for a run.
func SaveNamedQuery(userGUID, name, query string) (*SavedQuery, error) {
	if userGUID == "" {
		return nil, serr.New("user required")
	}
	name = strings.TrimSpace(name)
	query = strings.TrimSpace(query)
	if name == "" {
		return nil, &SavedQueryInputError{"a saved query needs a name"}
	}
	if len(name) > savedQueryNameMax {
		return nil, &SavedQueryInputError{"saved query name is too long (max " + itoa(savedQueryNameMax) + " bytes)"}
	}
	if query == "" {
		return nil, &SavedQueryInputError{"nothing to save: the query is empty"}
	}
	if len(query) > savedQueryTextMax {
		return nil, &SavedQueryInputError{"query is too long to save (max " + itoa(savedQueryTextMax) + " bytes)"}
	}
	if _, err := ParseQuery(query); err != nil {
		return nil, err
	}

	savedQueryMu.Lock()
	defer savedQueryMu.Unlock()

	rows, err := loadSavedQueries(userGUID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, r := range rows {
		if r.Kind != SavedQueryKindSaved || !strings.EqualFold(r.Name, name) {
			continue
		}
		// The new spelling of the name wins too, so "Airflow" can be
		// corrected to "airflow" by saving over it.
		if _, err := privDB.Exec(`UPDATE saved_queries SET name = ?, query = ?, last_used_at = ? WHERE id = ?`,
			name, query, now, r.ID); err != nil {
			return nil, serr.Wrap(err, "failed to update saved query")
		}
		r.Name, r.Query, r.LastUsedAt = name, query, now
		return &r, nil
	}

	r := &SavedQuery{Kind: SavedQueryKindSaved, Name: name, Query: query, CreatedAt: now, LastUsedAt: now}
	err = privDB.QueryRow(`
		INSERT INTO saved_queries (id, user_guid, kind, name, query, created_at, last_used_at)
		VALUES (nextval('saved_queries_id_seq'), ?, ?, ?, ?, ?, ?)
		RETURNING id`, userGUID, r.Kind, r.Name, r.Query, now, now).Scan(&r.ID)
	if err != nil {
		return nil, serr.Wrap(err, "failed to insert saved query")
	}
	return r, nil
}

// RecordQueryHistory notes that the user ran query. A repeat of an existing
// entry moves it to the top instead of adding a duplicate, and the list is
// pruned to savedQueryHistoryMax afterwards.
//
// Callers record only queries that ran successfully, and only when the user
// ran them — not on the TUI's refresh of an active query, which re-runs the
// same text on every reload and would otherwise churn the table.
func RecordQueryHistory(userGUID, query string) error {
	query = strings.TrimSpace(query)
	if userGUID == "" || query == "" || privDB == nil {
		return nil
	}
	if len(query) > savedQueryTextMax {
		// Not an error: history is a convenience, and a run that succeeded
		// should not be reported as failed because its text was too long to
		// remember.
		return nil
	}

	savedQueryMu.Lock()
	defer savedQueryMu.Unlock()

	rows, err := loadSavedQueries(userGUID)
	if err != nil {
		return err
	}
	now := time.Now()

	var history []SavedQuery
	touched := false
	for _, r := range rows {
		if r.Kind != SavedQueryKindHistory {
			continue
		}
		if r.Query == query && !touched {
			if _, err := privDB.Exec(`UPDATE saved_queries SET last_used_at = ? WHERE id = ?`, now, r.ID); err != nil {
				return serr.Wrap(err, "failed to touch query history")
			}
			r.LastUsedAt = now
			touched = true
		}
		history = append(history, r)
	}

	if !touched {
		var id int64
		err := privDB.QueryRow(`
			INSERT INTO saved_queries (id, user_guid, kind, query, created_at, last_used_at)
			VALUES (nextval('saved_queries_id_seq'), ?, ?, ?, ?, ?)
			RETURNING id`, userGUID, SavedQueryKindHistory, query, now, now).Scan(&id)
		if err != nil {
			return serr.Wrap(err, "failed to record query history")
		}
		history = append(history, SavedQuery{ID: id, Kind: SavedQueryKindHistory, Query: query, LastUsedAt: now})
	}

	// Prune the tail. Done after the insert so the cap is exact, and by id so
	// nothing depends on the SQL layer's handling of ORDER BY + LIMIT in a
	// DELETE.
	sortHistory(history)
	for _, old := range history[min(len(history), savedQueryHistoryMax):] {
		if _, err := privDB.Exec(`DELETE FROM saved_queries WHERE id = ?`, old.ID); err != nil {
			return serr.Wrap(err, "failed to prune query history")
		}
	}
	return nil
}

// DeleteSavedQuery removes one of the user's rows, named or history. The
// user_guid condition is what keeps one user from deleting another's by id.
func DeleteSavedQuery(userGUID string, id int64) error {
	if userGUID == "" || privDB == nil {
		return ErrSavedQueryNotFound
	}
	savedQueryMu.Lock()
	defer savedQueryMu.Unlock()

	res, err := privDB.Exec(`DELETE FROM saved_queries WHERE id = ? AND user_guid = ?`, id, userGUID)
	if err != nil {
		return serr.Wrap(err, "failed to delete saved query")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSavedQueryNotFound
	}
	return nil
}
