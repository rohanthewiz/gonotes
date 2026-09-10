package models

import (
	"database/sql"
	"strconv"
	"strings"
	"time"
)

// query_eval.go runs a parsed query against one note at a time.
//
// Evaluation deliberately cannot fail. Every error the language can produce —
// an unknown field, a literal of the wrong type, a malformed pattern — was
// raised at parse time with a source position, so the walk below is a plain
// bool recursion with no error plumbing threaded through it. The one thing
// that could still surprise is a field added to the catalog with no arm in
// fieldValues; that is why the string/number/time accessors fall through to a
// zero result rather than pretending to match.
//
// The shape of one evaluation:
//
//	Query.Where ──walk──▶ predicate ──▶ noteFacts.<kind>Values(field)
//	                                        │
//	                     notes columns ◀────┤
//	           note_categories links ◀──────┘  (loaded only when the query
//	                                            names a category field)

// evalEnv is everything a predicate needs beyond the note itself: the clock
// relative time literals resolve against, and the identity `me` stands for.
//
// The clock is captured once per query run rather than read per comparison so
// that every predicate in one run agrees on "now" — otherwise a query naming
// two time fields could evaluate them a microsecond apart, which is invisible
// until the day it is not.
type evalEnv struct {
	Now      time.Time
	UserGUID string
}

// noteFacts is one note plus its category links, with the derived
// multi-valued attributes computed at most once.
//
// The laziness matters: a query over ten thousand notes that never mentions
// tags must not split ten thousand tag strings, and one that mentions `text`
// must not rebuild the same six-part slice for every predicate in the
// expression.
type noteFacts struct {
	note  *Note
	links []NoteCategoryMapping

	tagsDone bool
	tags     []string

	catsDone bool
	catNames []string
	subNames []string
	catIDs   []float64

	textDone bool
	textVals []string
}

// tagValues splits the comma-separated tags column, dropping blanks so a
// trailing comma does not produce an empty tag that matches `tags = ”`.
func (f *noteFacts) tagValues() []string {
	if !f.tagsDone {
		f.tagsDone = true
		if f.note.Tags.Valid {
			for _, t := range strings.Split(f.note.Tags.String, ",") {
				if t = strings.TrimSpace(t); t != "" {
					f.tags = append(f.tags, t)
				}
			}
		}
	}
	return f.tags
}

// categoryValues flattens the note's links into the three shapes a query can
// ask about. Subcategory names are de-duplicated across categories: a note
// filed under Work/backend and Home/backend answers `subcategory = 'backend'`
// once, which is all an existential test needs and keeps the slice small.
func (f *noteFacts) categoryValues() {
	if f.catsDone {
		return
	}
	f.catsDone = true
	seenSub := map[string]bool{}
	for _, l := range f.links {
		if l.CategoryName != "" {
			f.catNames = append(f.catNames, l.CategoryName)
		}
		f.catIDs = append(f.catIDs, float64(l.CategoryID))
		for _, s := range l.SelectedSubcategories {
			if s == "" || seenSub[s] {
				continue
			}
			seenSub[s] = true
			f.subNames = append(f.subNames, s)
		}
	}
}

// textValues is the pseudo-field `text`: every place a human-typed word could
// live, as separate values so a CONTAINS scans each in turn and stops early.
// Body is last because it is by far the largest and the least likely to be
// the only hit.
func (f *noteFacts) textValues() []string {
	if !f.textDone {
		f.textDone = true
		f.categoryValues()
		f.textVals = append(f.textVals, f.note.Title)
		if f.note.Description.Valid {
			f.textVals = append(f.textVals, f.note.Description.String)
		}
		f.textVals = append(f.textVals, f.tagValues()...)
		f.textVals = append(f.textVals, f.catNames...)
		f.textVals = append(f.textVals, f.subNames...)
		if f.note.Body.Valid {
			f.textVals = append(f.textVals, f.note.Body.String)
		}
	}
	return f.textVals
}

// stringValues returns every string value the note carries for a field. An
// absent (NULL) value yields an empty slice — see the NULL note on
// predicate.eval.
func (f *noteFacts) stringValues(field *QueryField) []string {
	n := f.note
	switch field.Name {
	case "guid":
		return []string{n.GUID}
	case "title":
		return []string{n.Title}
	case "description":
		return nullStringValues(n.Description)
	case "body":
		return nullStringValues(n.Body)
	case "created_by":
		return nullStringValues(n.CreatedBy)
	case "updated_by":
		return nullStringValues(n.UpdatedBy)
	case "tags":
		return f.tagValues()
	case "category":
		f.categoryValues()
		return f.catNames
	case "subcategory":
		f.categoryValues()
		return f.subNames
	case "text":
		return f.textValues()
	}
	return nil
}

func nullStringValues(ns sql.NullString) []string {
	if !ns.Valid {
		return nil
	}
	return []string{ns.String}
}

func (f *noteFacts) numberValues(field *QueryField) []float64 {
	switch field.Name {
	case "id":
		return []float64{float64(f.note.ID)}
	case "version":
		return []float64{float64(f.note.Version)}
	case "category_id":
		f.categoryValues()
		return f.catIDs
	}
	return nil
}

func (f *noteFacts) boolValue(field *QueryField) (bool, bool) {
	switch field.Name {
	case "is_private":
		return f.note.IsPrivate, true
	case "is_flagged":
		return f.note.IsFlagged, true
	}
	return false, false
}

// timeValues returns the single timestamp a time field holds, or nothing when
// the column is NULL.
func (f *noteFacts) timeValues(field *QueryField) []time.Time {
	n := f.note
	switch field.Name {
	case "created_at":
		return []time.Time{n.CreatedAt}
	case "updated_at":
		return []time.Time{n.UpdatedAt}
	case "authored_at":
		if n.AuthoredAt.Valid {
			return []time.Time{n.AuthoredAt.Time}
		}
	case "synced_at":
		if n.SyncedAt.Valid {
			return []time.Time{n.SyncedAt.Time}
		}
	case "deleted_at":
		if n.DeletedAt.Valid {
			return []time.Time{n.DeletedAt.Time}
		}
	}
	return nil
}

// hasValue reports whether the note carries any value for the field — the
// test behind IS NULL.
func (f *noteFacts) hasValue(field *QueryField) bool {
	switch field.Kind {
	case KindBool:
		_, ok := f.boolValue(field)
		return ok
	case KindNumber:
		return len(f.numberValues(field)) > 0
	case KindTime:
		return len(f.timeValues(field)) > 0
	}
	return len(f.stringValues(field)) > 0
}

// isEmpty is IS EMPTY: no value at all, or nothing but blank ones. It exists
// separately from IS NULL because a description that was cleared to "" and one
// that was never written are the same thing to a person and different rows to
// the database.
func (f *noteFacts) isEmpty(field *QueryField) bool {
	if field.Kind != KindString {
		return !f.hasValue(field)
	}
	for _, s := range f.stringValues(field) {
		if strings.TrimSpace(s) != "" {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Predicate evaluation
// ---------------------------------------------------------------------------

// eval tests one predicate against one note.
//
// NULL HANDLING, and why it is not SQL's. SQL evaluates `description != 'x'`
// on a NULL description to UNKNOWN, which a WHERE clause then drops — so the
// note disappears from both `= 'x'` and `!= 'x'`. That is the correct answer
// for a language whose NULL means "unknown value", and the wrong one for a
// note store, where a missing description means the note does not have one.
// Here a NULL is simply an empty value list: no value satisfies the positive
// test, so `= 'x'` is false and its negation is true, and every note is
// accounted for by exactly one of a predicate and its negation. IS NULL
// remains the way to ask about absence itself.
//
// NEGATION, and why it is one rule. Negation is applied once, on top of the
// positive result, rather than being pushed into each operator. On a
// single-valued field that is the same thing. On a multi-valued one it is what
// gives `category != 'archive'` the meaning people expect — "not filed under
// archive" — instead of SQL's "has some category other than archive", which is
// true for almost every note and would make the operator useless.
func (p *predicate) eval(env *evalEnv, f *noteFacts) bool {
	res := p.evalPositive(env, f)
	if p.Negate {
		return !res
	}
	return res
}

func (p *predicate) evalPositive(env *evalEnv, f *noteFacts) bool {
	switch p.Op {
	case OpNull:
		return !f.hasValue(p.Field)
	case OpEmpty:
		return f.isEmpty(p.Field)
	case OpTruthy:
		b, ok := f.boolValue(p.Field)
		return ok && b
	}

	switch p.Field.Kind {
	case KindBool:
		b, ok := f.boolValue(p.Field)
		if !ok {
			return false
		}
		for _, v := range p.Values {
			if b == v.Bool {
				return true
			}
		}
		return false

	case KindNumber:
		for _, got := range f.numberValues(p.Field) {
			if p.matchNumber(got) {
				return true
			}
		}
		return false

	case KindTime:
		for _, got := range f.timeValues(p.Field) {
			if p.matchTime(env, got) {
				return true
			}
		}
		return false
	}

	for _, got := range f.stringValues(p.Field) {
		if p.matchString(env, got) {
			return true
		}
	}
	return false
}

// matchString compares one of the note's string values against the
// predicate's literals. Equality and CONTAINS fold ASCII case, because a
// category typed as "Airflow" and filed as "airflow" is the same category to
// everyone except a byte comparison. MATCHES does not fold: a regular
// expression carries its own flags, and silently making every pattern
// case-insensitive would take that control away.
func (p *predicate) matchString(env *evalEnv, got string) bool {
	switch p.Op {
	case OpEq, OpIn:
		for _, v := range p.Values {
			if strings.EqualFold(got, p.literalString(env, v)) {
				return true
			}
		}
		return false
	case OpContains:
		for _, v := range p.Values {
			if containsFold(got, foldASCII(p.literalString(env, v))) {
				return true
			}
		}
		return false
	case OpLike, OpMatches:
		for _, v := range p.Values {
			if v.pattern != nil && v.pattern.MatchString(got) {
				return true
			}
		}
		return false
	case OpBetween:
		lo := foldASCII(p.literalString(env, p.Values[0]))
		hi := foldASCII(p.literalString(env, p.Values[1]))
		g := foldASCII(got)
		return g >= lo && g <= hi
	case OpLt, OpLte, OpGt, OpGte:
		g, want := foldASCII(got), foldASCII(p.literalString(env, p.Values[0]))
		return orderResult(strings.Compare(g, want), p.Op)
	}
	return false
}

// literalString resolves a string literal, substituting the querying user's
// GUID for `me`.
func (p *predicate) literalString(env *evalEnv, v queryValue) string {
	if v.isMe {
		return env.UserGUID
	}
	return v.Str
}

func (p *predicate) matchNumber(got float64) bool {
	switch p.Op {
	case OpEq, OpIn:
		for _, v := range p.Values {
			if got == v.Num {
				return true
			}
		}
		return false
	case OpBetween:
		return got >= p.Values[0].Num && got <= p.Values[1].Num
	case OpLt, OpLte, OpGt, OpGte:
		return orderResult(compareFloat(got, p.Values[0].Num), p.Op)
	}
	return false
}

// matchTime compares against a time literal at the precision the literal was
// written with. See timeGranularity: '2026-03-04' is a day-wide window, and
// every operator is defined against that window's edges so the readings stay
// consistent with each other:
//
//	          |<--- the window named by the literal --->|
//	 < D      |                                         |
//	<= D      |·········································|
//	 = D      |·········································|
//	>= D      |·········································|·······▶
//	 > D                                                |·······▶
//
// An instant literal (now, -7d, a full timestamp) has a zero-width window, so
// all five collapse to the ordinary comparisons.
func (p *predicate) matchTime(env *evalEnv, got time.Time) bool {
	if p.Op == OpBetween {
		lo, _ := windowOf(env, p.Values[0])
		_, hi := windowOf(env, p.Values[1])
		if p.Values[1].gran == granInstant {
			return !got.Before(lo) && !got.After(hi)
		}
		return !got.Before(lo) && got.Before(hi)
	}
	for _, v := range p.Values {
		start, end := windowOf(env, v)
		instant := v.gran == granInstant
		var ok bool
		switch p.Op {
		case OpEq, OpIn:
			if instant {
				ok = got.Equal(start)
			} else {
				ok = !got.Before(start) && got.Before(end)
			}
		case OpLt:
			ok = got.Before(start)
		case OpLte:
			if instant {
				ok = !got.After(start)
			} else {
				ok = got.Before(end)
			}
		case OpGt:
			if instant {
				ok = got.After(start)
			} else {
				ok = !got.Before(end)
			}
		case OpGte:
			ok = !got.Before(start)
		}
		if ok {
			return true
		}
	}
	return false
}

func windowOf(env *evalEnv, v queryValue) (time.Time, time.Time) {
	return v.gran.window(v.timeAt(env.Now))
}

// orderResult turns a three-way comparison into the answer for an ordering
// operator, so the four cases are written once instead of once per kind.
func orderResult(cmp int, op CompareOp) bool {
	switch op {
	case OpLt:
		return cmp < 0
	case OpLte:
		return cmp <= 0
	case OpGt:
		return cmp > 0
	case OpGte:
		return cmp >= 0
	}
	return false
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// containsFold is a case-insensitive substring test that does not allocate.
//
// strings.Contains(strings.ToLower(s), sub) would be one line, but it copies
// the haystack — and the haystack here is a note body, scanned once per
// predicate per note. Folding is ASCII-only, matching foldASCII and the rest
// of the language: bytes outside ASCII must match exactly, which is both
// predictable and correct for UTF-8, since a multi-byte rune's bytes are all
// >= 0x80 and can never be confused with an ASCII letter.
//
// needleFold must already be folded.
func containsFold(haystack, needleFold string) bool {
	if needleFold == "" {
		return true
	}
	n := len(haystack) - len(needleFold)
	if n < 0 {
		return false
	}
	first := needleFold[0]
	for i := 0; i <= n; i++ {
		if lowerByte(haystack[i]) != first {
			continue
		}
		if matchesFoldAt(haystack, i, needleFold) {
			return true
		}
	}
	return false
}

func matchesFoldAt(haystack string, at int, needleFold string) bool {
	for j := 0; j < len(needleFold); j++ {
		if lowerByte(haystack[at+j]) != needleFold[j] {
			return false
		}
	}
	return true
}

func lowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// ---------------------------------------------------------------------------
// Canonical rendering
// ---------------------------------------------------------------------------

// String renders the predicate back to source. The output re-parses to an
// equivalent tree, which query_test.go checks by round-tripping every example
// — the cheapest guard there is against the canonical form drifting away from
// the grammar that produced it.
func (p *predicate) String() string {
	f := p.Field.Name

	switch p.Op {
	case OpNull:
		if p.Negate {
			return f + " IS NOT NULL"
		}
		return f + " IS NULL"
	case OpEmpty:
		if p.Negate {
			return f + " IS NOT EMPTY"
		}
		return f + " IS EMPTY"
	case OpTruthy:
		if p.Negate {
			return "NOT " + f
		}
		return f
	case OpIn:
		parts := make([]string, len(p.Values))
		for i, v := range p.Values {
			parts[i] = v.render()
		}
		op := " IN ("
		if p.Negate {
			op = " NOT IN ("
		}
		return f + op + strings.Join(parts, ", ") + ")"
	case OpBetween:
		op := " BETWEEN "
		if p.Negate {
			op = " NOT BETWEEN "
		}
		return f + op + p.Values[0].render() + " AND " + p.Values[1].render()
	case OpLike, OpContains, OpMatches:
		word := string(p.Op)
		if p.Negate {
			word = "NOT " + word
		}
		return f + " " + word + " " + p.Values[0].render()
	case OpEq:
		if p.Negate {
			return f + " != " + p.Values[0].render()
		}
		return f + " = " + p.Values[0].render()
	}
	s := f + " " + string(p.Op) + " " + p.Values[0].render()
	if p.Negate {
		return "NOT (" + s + ")"
	}
	return s
}

// render writes one literal back in a form the parser accepts. Strings are
// single-quoted with embedded quotes doubled; numbers keep their shortest
// exact form; time and boolean literals render as the words they were.
func (v queryValue) render() string {
	switch v.Kind {
	case KindNumber:
		return strconv.FormatFloat(v.Num, 'f', -1, 64)
	case KindBool:
		return strconv.FormatBool(v.Bool)
	case KindTime:
		if v.rel != nil && v.rel.keyword != "" {
			return v.rel.keyword
		}
		if v.rel != nil {
			return v.Str // an offset such as -7d, already bare
		}
		return "'" + strings.ReplaceAll(v.Str, "'", "''") + "'"
	}
	if v.isMe {
		return "me"
	}
	return "'" + strings.ReplaceAll(v.Str, "'", "''") + "'"
}
