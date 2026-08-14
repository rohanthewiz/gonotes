package models

import (
	"database/sql"
	"database/sql/driver"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rohanthewiz/bytdb"
	bsql "github.com/rohanthewiz/bytdb/sql"
	"github.com/rohanthewiz/serr"
)

// store.go is the persistence foundation for the bytdb-backed store.
//
// GoNotes keeps TWO bytdb databases side by side:
//
//	privDB  — private notes, their category links, and their sync
//	          change-tracking. Opened with WithEncryptionKey so the
//	          write-ahead log (and therefore every backup/replica) is
//	          AES-256-GCM ciphertext at rest. Rows are plaintext in RAM,
//	          so reads/scans pay no crypto cost.
//	pubDB   — everything else: non-private notes + their links +
//	          change-tracking, plus the shared/system tables (categories
//	          catalog, users, invite_tokens, sync_state, sync_conflicts).
//	          Plaintext on disk.
//
// bytdb is not a database/sql driver — it returns rows as [][]any and
// binds $1-style placeholders. Rather than rewrite every query by hand,
// this file provides a thin *sql.DB-shaped shim (dbEngine) so the model
// code keeps using Exec / Query / QueryRow / Scan and the familiar
// database/sql sentinel (sql.ErrNoRows) and null types (sql.NullString,
// …). Two adaptations happen transparently in the shim:
//
//   - Placeholders: model SQL is written with '?' (its historical
//     DuckDB style); rebind() rewrites them to $1,$2,… (bytdb's style),
//     skipping any '?' inside single-quoted string literals.
//   - Arguments: sql.NullString/NullInt64/NullTime/NullBool implement
//     driver.Valuer, so unwrapArgs() reduces them to the plain value (or
//     nil) that bytdb's coercion layer understands. time.Time is passed
//     through — bytdb coerces it to timestamp micros for timestamp
//     columns.
//
// Reads never block in bytdb (snapshot reads), so the "divide and
// conquer" pattern — fan a note read out to both databases on separate
// goroutines and merge — is safe and is implemented in queryBothNotes
// and friends elsewhere. Writes serialize (one writer per engine), so
// write transactions are kept short.

// dbEngine wraps one bytdb engine and its SQL executor behind a
// database/sql-like surface. The zero value is not usable; construct
// via openEngine.
type dbEngine struct {
	eng *bytdb.Engine
	db  *bsql.DB
	// encrypted records whether this engine's WAL is key-encrypted at
	// rest — surfaced for logging/status, not used in the hot path.
	encrypted bool
}

// openEngine opens (creating if absent) a bytdb database at path. When
// key is non-nil it must be exactly 32 bytes; the WAL is then encrypted
// with AES-256-GCM. Opening an existing encrypted db without its key —
// or a plaintext db with a key — fails inside bytdb with a clear error,
// which we wrap and return.
func openEngine(path string, key []byte) (*dbEngine, error) {
	var eng *bytdb.Engine
	var err error
	if len(key) > 0 {
		eng, err = bytdb.Open(path, bytdb.WithEncryptionKey(key))
	} else {
		eng, err = bytdb.Open(path)
	}
	if err != nil {
		return nil, serr.Wrap(err, "failed to open bytdb engine", "path", path)
	}
	return &dbEngine{eng: eng, db: bsql.New(eng), encrypted: len(key) > 0}, nil
}

// Close releases the engine's resources. Safe to call on a nil receiver
// so shutdown paths need not guard.
func (en *dbEngine) Close() error {
	if en == nil || en.eng == nil {
		return nil
	}
	return en.eng.Close()
}

// hasTable reports whether a table already exists in this engine. Used
// to make schema creation idempotent (bytdb has no CREATE TABLE IF NOT
// EXISTS — only sequences take IF NOT EXISTS).
func (en *dbEngine) hasTable(name string) bool {
	return en.eng.Table(name) != nil
}

// Ping verifies the engine is live. bytdb has no network round-trip, so
// a non-nil executor is sufficient; kept for parity with the old code.
func (en *dbEngine) Ping() error {
	if en == nil || en.db == nil {
		return serr.New("engine not initialized")
	}
	return nil
}

// execResult mirrors the sliver of sql.Result the model code uses.
type execResult struct{ rowsAffected int64 }

func (r execResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }

// LastInsertId is unsupported (bytdb has no implicit rowid); callers use
// RETURNING instead. Present only to satisfy the sql.Result shape.
func (r execResult) LastInsertId() (int64, error) {
	return 0, serr.New("LastInsertId is unsupported; use RETURNING")
}

// Exec runs a non-SELECT (or a RETURNING-less statement) and reports
// rows affected. Placeholders are rebound and args unwrapped first.
func (en *dbEngine) Exec(query string, args ...any) (execResult, error) {
	ua, err := unwrapArgs(args)
	if err != nil {
		return execResult{}, err
	}
	res, err := en.db.Exec(rebind(query), ua...)
	if err != nil {
		return execResult{}, err
	}
	// A RETURNING statement reports len(Rows); a plain DML reports
	// RowsAffected. Prefer the latter when set, else fall back to rows.
	n := int64(res.RowsAffected)
	if n == 0 && len(res.Rows) > 0 {
		n = int64(len(res.Rows))
	}
	return execResult{rowsAffected: n}, nil
}

// Query runs a SELECT (or RETURNING) and returns a cursor over the
// result rows.
func (en *dbEngine) Query(query string, args ...any) (*shimRows, error) {
	ua, err := unwrapArgs(args)
	if err != nil {
		return nil, err
	}
	res, err := en.db.Exec(rebind(query), ua...)
	if err != nil {
		return nil, err
	}
	return &shimRows{cols: res.Cols, rows: res.Rows, idx: -1}, nil
}

// QueryRow runs a statement expected to yield at most one row. Its Scan
// returns sql.ErrNoRows when the result is empty — matching *sql.Row so
// existing `err == sql.ErrNoRows` checks keep working.
func (en *dbEngine) QueryRow(query string, args ...any) *shimRow {
	ua, err := unwrapArgs(args)
	if err != nil {
		return &shimRow{err: err}
	}
	res, err := en.db.Exec(rebind(query), ua...)
	if err != nil {
		return &shimRow{err: err}
	}
	if len(res.Rows) == 0 {
		return &shimRow{err: sql.ErrNoRows}
	}
	return &shimRow{vals: res.Rows[0]}
}

// shimRows is a forward-only cursor over a materialized bytdb result. It
// mirrors the *sql.Rows methods the model code uses (Next/Scan/Close/Err).
type shimRows struct {
	cols []string
	rows [][]any
	idx  int
	err  error
}

func (r *shimRows) Next() bool {
	if r.err != nil {
		return false
	}
	r.idx++
	return r.idx < len(r.rows)
}

func (r *shimRows) Scan(dest ...any) error {
	if r.idx < 0 || r.idx >= len(r.rows) {
		return serr.New("Scan called out of range")
	}
	return scanRow(r.rows[r.idx], dest)
}

// Close is a no-op: the result is already fully materialized in memory.
// Present for parity with *sql.Rows so `defer rows.Close()` compiles.
func (r *shimRows) Close() error { return nil }

func (r *shimRows) Err() error { return r.err }

// shimRow is the single-row analogue of *sql.Row.
type shimRow struct {
	vals []any
	err  error
}

func (r *shimRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanRow(r.vals, dest)
}

// scanRow assigns each source value into the matching destination
// pointer, converting between bytdb's runtime representation and the Go
// types the model structs use.
func scanRow(vals []any, dest []any) error {
	if len(dest) > len(vals) {
		return serr.New("Scan destination count exceeds column count",
			"dest", strconv.Itoa(len(dest)), "cols", strconv.Itoa(len(vals)))
	}
	for i, d := range dest {
		if err := assignValue(d, vals[i]); err != nil {
			return serr.Wrap(err, "failed to scan column", "index", strconv.Itoa(i))
		}
	}
	return nil
}

// assignValue converts one bytdb value (int64/float64/bool/string/[]byte/
// nil, with timestamps as int64 microseconds) into the concrete pointer
// destination. Timestamp handling keys off the destination type: an
// int64 landing in a *time.Time / *sql.NullTime is read as micros since
// the Unix epoch.
func assignValue(dest any, src any) error {
	switch d := dest.(type) {
	case *string:
		if src == nil {
			*d = ""
			return nil
		}
		s, ok := src.(string)
		if !ok {
			return typeErr("string", src)
		}
		*d = s
	case *sql.NullString:
		if src == nil {
			*d = sql.NullString{}
			return nil
		}
		s, ok := src.(string)
		if !ok {
			return typeErr("string", src)
		}
		*d = sql.NullString{String: s, Valid: true}
	case *int64:
		if src == nil {
			*d = 0
			return nil
		}
		n, err := toInt64(src)
		if err != nil {
			return err
		}
		*d = n
	case *int:
		if src == nil {
			*d = 0
			return nil
		}
		n, err := toInt64(src)
		if err != nil {
			return err
		}
		*d = int(n)
	case *int32:
		if src == nil {
			*d = 0
			return nil
		}
		n, err := toInt64(src)
		if err != nil {
			return err
		}
		*d = int32(n)
	case *int16:
		if src == nil {
			*d = 0
			return nil
		}
		n, err := toInt64(src)
		if err != nil {
			return err
		}
		*d = int16(n)
	case *sql.NullInt64:
		if src == nil {
			*d = sql.NullInt64{}
			return nil
		}
		n, err := toInt64(src)
		if err != nil {
			return err
		}
		*d = sql.NullInt64{Int64: n, Valid: true}
	case *sql.NullInt32:
		if src == nil {
			*d = sql.NullInt32{}
			return nil
		}
		n, err := toInt64(src)
		if err != nil {
			return err
		}
		*d = sql.NullInt32{Int32: int32(n), Valid: true}
	case *bool:
		if src == nil {
			*d = false
			return nil
		}
		b, ok := src.(bool)
		if !ok {
			return typeErr("bool", src)
		}
		*d = b
	case *sql.NullBool:
		if src == nil {
			*d = sql.NullBool{}
			return nil
		}
		b, ok := src.(bool)
		if !ok {
			return typeErr("bool", src)
		}
		*d = sql.NullBool{Bool: b, Valid: true}
	case *float64:
		if src == nil {
			*d = 0
			return nil
		}
		f, ok := src.(float64)
		if !ok {
			return typeErr("float64", src)
		}
		*d = f
	case *sql.NullFloat64:
		if src == nil {
			*d = sql.NullFloat64{}
			return nil
		}
		f, ok := src.(float64)
		if !ok {
			return typeErr("float64", src)
		}
		*d = sql.NullFloat64{Float64: f, Valid: true}
	case *time.Time:
		// Nullable timestamps are scanned into *sql.NullTime; a nil here
		// is tolerated (zero time) rather than erroring, so a column that
		// turns out empty never crashes a read.
		if src == nil {
			*d = time.Time{}
			return nil
		}
		t, err := toTime(src)
		if err != nil {
			return err
		}
		*d = t
	case *sql.NullTime:
		if src == nil {
			*d = sql.NullTime{}
			return nil
		}
		t, err := toTime(src)
		if err != nil {
			return err
		}
		*d = sql.NullTime{Time: t, Valid: true}
	case *[]byte:
		if src == nil {
			*d = nil
			return nil
		}
		b, ok := src.([]byte)
		if !ok {
			return typeErr("[]byte", src)
		}
		*d = b
	case *any:
		*d = src
	default:
		return serr.New("unsupported Scan destination type")
	}
	return nil
}

// toInt64 widens any of bytdb's integer representations to int64.
func toInt64(src any) (int64, error) {
	switch v := src.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case int32:
		return int64(v), nil
	default:
		return 0, typeErr("int", src)
	}
}

// toTime reads a bytdb timestamp (int64 micros since epoch, UTC) — or a
// time.Time if one somehow arrives — as a time.Time.
func toTime(src any) (time.Time, error) {
	switch v := src.(type) {
	case int64:
		return time.UnixMicro(v).UTC(), nil
	case time.Time:
		return v, nil
	default:
		return time.Time{}, typeErr("timestamp", src)
	}
}

func typeErr(want string, src any) error {
	return serr.New("unexpected value kind while scanning", "want", want)
}

// rebind rewrites '?' positional placeholders to bytdb's $1,$2,… form,
// leaving any '?' inside single-quoted string literals untouched. SQL
// escapes a quote by doubling it ('') — handled so the literal scan
// stays correct.
func rebind(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	inStr := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		if inStr {
			b.WriteByte(c)
			if c == '\'' {
				if i+1 < len(query) && query[i+1] == '\'' {
					b.WriteByte('\'')
					i++
				} else {
					inStr = false
				}
			}
			continue
		}
		switch c {
		case '\'':
			inStr = true
			b.WriteByte(c)
		case '?':
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// unwrapArgs reduces driver.Valuer arguments (sql.NullString and the
// other sql.Null* types) to the plain value or nil that bytdb binds.
// Everything else — including time.Time, which bytdb coerces to
// timestamp micros — passes through unchanged.
func unwrapArgs(args []any) ([]any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make([]any, len(args))
	for i, a := range args {
		if a == nil {
			out[i] = nil
			continue
		}
		if v, ok := a.(driver.Valuer); ok {
			dv, err := v.Value()
			if err != nil {
				return nil, serr.Wrap(err, "failed to unwrap driver.Valuer arg")
			}
			out[i] = dv
			continue
		}
		out[i] = a
	}
	return out, nil
}

// --- Divide-and-conquer fan-out across the two note databases ---

// queryBothNotes runs fn against both note databases concurrently and
// concatenates the results. Reads never block in bytdb, so the two
// scans proceed in parallel and the wall-clock cost is the slower of the
// two, not their sum. Order within each engine's slice is preserved;
// callers that need a global order re-sort the merged slice.
func queryBothNotes[T any](fn func(en *dbEngine) ([]T, error)) ([]T, error) {
	engines := []*dbEngine{pubDB, privDB}
	results := make([][]T, len(engines))
	errs := make([]error, len(engines))

	var wg sync.WaitGroup
	for i, en := range engines {
		wg.Add(1)
		go func(i int, en *dbEngine) {
			defer wg.Done()
			results[i], errs[i] = fn(en)
		}(i, en)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	var merged []T
	for _, r := range results {
		merged = append(merged, r...)
	}
	return merged, nil
}

// firstFromBothNotes runs fn against both note databases concurrently
// and returns the first non-nil, no-error result — the divide-and-conquer
// form of "find this note wherever it lives". fn should return
// (nil, sql.ErrNoRows) when the engine does not hold the row. If neither
// engine has it, sql.ErrNoRows is returned.
func firstFromBothNotes[T any](fn func(en *dbEngine) (*T, error)) (*T, error) {
	engines := []*dbEngine{pubDB, privDB}
	type outcome struct {
		val *T
		err error
	}
	outs := make([]outcome, len(engines))

	var wg sync.WaitGroup
	for i, en := range engines {
		wg.Add(1)
		go func(i int, en *dbEngine) {
			defer wg.Done()
			v, err := fn(en)
			outs[i] = outcome{val: v, err: err}
		}(i, en)
	}
	wg.Wait()

	// A hard error (anything other than "not found") from either engine
	// is surfaced; a found row wins over a not-found sibling.
	for _, o := range outs {
		if o.err != nil && o.err != sql.ErrNoRows {
			return nil, o.err
		}
	}
	for _, o := range outs {
		if o.err == nil && o.val != nil {
			return o.val, nil
		}
	}
	return nil, sql.ErrNoRows
}
