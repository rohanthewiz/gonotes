package models

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// query_value.go turns a literal token into a typed value the evaluator can
// compare against, coercing it to the kind of the field it was written next
// to. Coercion happens at PARSE time, once per query, so a query that runs
// over ten thousand notes does not re-parse '2026-01-01' ten thousand times —
// and so that a malformed literal is a syntax error with a position rather
// than a note that silently fails to match.

// timeGranularity is how precise a time literal was. It exists because of one
// specific expectation: a person who writes
//
//	created_at = '2026-03-04'
//
// means "on that day", not "at exactly midnight, to the nanosecond" — which is
// what a naive instant comparison gives, and which matches nothing. So a time
// value remembers how much of itself the user actually specified, and equality
// against it is a containment test over the window that precision names:
//
//	'2026'          |<------------- a year ------------->|
//	'2026-03'       |<-- a month -->|
//	'2026-03-04'    |< a day >|
//	'2026-03-04 09' |<hour>|
//	now             |. (an instant; equality is exact)
//
// Ordering comparisons (<, >) always use the window's START, which is the
// reading that makes `created_at >= '2026-03-04'` mean "from that day onward".
type timeGranularity int

const (
	granInstant timeGranularity = iota
	granMinute
	granHour
	granDay
	granMonth
	granYear
)

// window returns the half-open interval [start, end) a time literal covers at
// its granularity. AddDate is used for month and year so the window follows
// the calendar rather than a fixed number of days.
func (g timeGranularity) window(t time.Time) (time.Time, time.Time) {
	switch g {
	case granMinute:
		return t, t.Add(time.Minute)
	case granHour:
		return t, t.Add(time.Hour)
	case granDay:
		return t, t.AddDate(0, 0, 1)
	case granMonth:
		return t, t.AddDate(0, 1, 0)
	case granYear:
		return t, t.AddDate(1, 0, 0)
	}
	return t, t
}

// queryValue is one right-hand-side literal, already coerced to the kind of
// the field it will be compared against.
//
// It is a single struct with a Kind tag rather than an interface because the
// evaluator's hot loop compares a value against every note: an interface
// method call per comparison per note buys nothing over a switch, and a flat
// struct keeps the pre-computed halves (the folded string, the compiled
// pattern) next to the value they belong to.
type queryValue struct {
	Kind FieldKind

	Str  string // the literal as written, for rendering
	fold string // ASCII-folded, for case-insensitive string comparison
	Num  float64
	Bool bool

	// Time literals. abs is set for an absolute literal; rel is set for one
	// expressed against the clock (now, today, -7d) and resolved at evaluation
	// time so a stored query keeps meaning "the last seven days".
	abs  time.Time
	rel  *relativeTime
	gran timeGranularity

	// pattern is the compiled form of a LIKE/CONTAINS/MATCHES right-hand side.
	// Compiling at parse time turns a bad regex into a positioned syntax error
	// and keeps regexp.Compile out of the per-note loop.
	pattern *regexp.Regexp

	// isMe defers resolution to the querying user's GUID. The GUID is not
	// known when a query is parsed (the same text may be run by the TUI, the
	// API, or a test), so `created_by = me` carries the intent and the
	// evaluation environment supplies the identity.
	isMe bool
}

// relativeTime is a clock-relative literal: either a named point (now, today)
// or an offset from now (-7d).
type relativeTime struct {
	keyword string        // "", "now", "today", "yesterday", "tomorrow"
	offset  time.Duration // used when keyword is ""
}

// resolve turns a relative literal into an instant against a supplied clock.
// The clock is passed in rather than read from time.Now so a query's meaning
// is reproducible in tests and so every predicate in one query run sees the
// same "now" — otherwise `created_at > -1s AND updated_at > -1s` could be
// evaluated against two different moments.
func (r relativeTime) resolve(now time.Time) time.Time {
	switch r.keyword {
	case "now":
		return now
	case "today":
		return startOfDay(now)
	case "yesterday":
		return startOfDay(now).AddDate(0, 0, -1)
	case "tomorrow":
		return startOfDay(now).AddDate(0, 0, 1)
	}
	return now.Add(r.offset)
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// timeAt returns the value's instant, resolving a relative literal against
// now.
func (v queryValue) timeAt(now time.Time) time.Time {
	if v.rel != nil {
		return v.rel.resolve(now)
	}
	return v.abs
}

// timeKeywords are the named clock points the language accepts as time
// literals, in the order the completer offers them.
var timeKeywords = []string{"now", "today", "yesterday", "tomorrow"}

// timeLayouts are tried in order against a quoted time literal. They run from
// most to least specific; the granularity that comes back with each one is
// what makes `= '2026-03'` mean the whole month.
var timeLayouts = []struct {
	layout string
	gran   timeGranularity
}{
	{time.RFC3339Nano, granInstant},
	{time.RFC3339, granInstant},
	{"2006-01-02T15:04:05", granInstant},
	{"2006-01-02 15:04:05", granInstant},
	{"2006-01-02T15:04", granMinute},
	{"2006-01-02 15:04", granMinute},
	{"2006-01-02 15", granHour},
	{"2006-01-02", granDay},
	{"2006/01/02", granDay},
	{"01/02/2006", granDay},
	{"Jan 2, 2006", granDay},
	{"2 Jan 2006", granDay},
	{"2006-01", granMonth},
	{"2006", granYear},
}

// parseTimeLiteral parses a quoted absolute time. Local time is used for
// layouts that carry no zone, because a person typing a date in a search box
// means their own day, not UTC's.
func parseTimeLiteral(s string) (time.Time, timeGranularity, bool) {
	s = strings.TrimSpace(s)
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l.layout, s, time.Local); err == nil {
			return t, l.gran, true
		}
	}
	return time.Time{}, granInstant, false
}

// parseDurationLiteral reads a signed offset like -7d or +90min into a
// Duration. The scanner has already validated the suffix, so a failure here
// means the number itself was malformed.
func parseDurationLiteral(text string) (time.Duration, bool) {
	i := 0
	if i < len(text) && (text[i] == '-' || text[i] == '+') {
		i++
	}
	j := i
	for j < len(text) && (isDigit(text[j]) || text[j] == '.') {
		j++
	}
	n, err := strconv.ParseFloat(text[:j], 64)
	if err != nil {
		return 0, false
	}
	suffix := foldASCII(text[j:])
	for _, u := range durationUnits {
		if suffix == u.suffix {
			return time.Duration(n * u.seconds * float64(time.Second)), true
		}
	}
	return 0, false
}

// likeToRegexp compiles a SQL LIKE pattern.
//
// The translation is literal-by-literal rather than a blanket
// regexp.QuoteMeta so that % and _ can keep their meanings while every other
// regex metacharacter in the pattern loses its own: a note title containing
// "C++" must be findable with LIKE 'C++%' without the user escaping anything.
// The result is anchored, because LIKE matches the whole value — the
// unanchored "somewhere inside" reading is what CONTAINS is for.
func likeToRegexp(pattern string) (*regexp.Regexp, error) {
	var sb strings.Builder
	sb.WriteString("(?is)^") // i: case-insensitive, s: . matches newlines in bodies
	for _, r := range pattern {
		switch r {
		case '%':
			sb.WriteString(".*")
		case '_':
			sb.WriteString(".")
		default:
			sb.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}
