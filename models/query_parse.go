package models

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// query_parse.go is the grammar: a recursive-descent parser over the tokens
// from query_lex.go, producing a Query the evaluator walks per note.
//
// The language is the WHERE clause of SQL, narrowed to what a note store can
// answer and widened where SQL would only get in the way:
//
//	query      := [ SELECT * FROM notes ] [ WHERE ] [ expr ]
//	              [ ORDER BY order (',' order)* ] [ LIMIT n [ OFFSET m ] ]
//	expr       := orExpr
//	orExpr     := andExpr ( OR andExpr )*
//	andExpr    := notExpr ( AND notExpr )*
//	notExpr    := NOT notExpr | primary
//	primary    := '(' expr ')' | predicate
//	predicate  := field [NOT] IN '(' value (',' value)* ')'
//	            | field [NOT] BETWEEN value AND value
//	            | field IS [NOT] (NULL | EMPTY)
//	            | field [NOT] (LIKE | CONTAINS | MATCHES) value
//	            | field ('='|'!='|'<>'|'<'|'<='|'>'|'>='|'~'|'!~') value
//	            | field                       -- boolean field, tested for true
//	            | string                      -- free text across every text field
//	order      := field [ ASC | DESC ]
//
// Three deliberate departures from SQL, each earning its keep:
//
//   - The projection is not part of the language. A query selects notes; there
//     is nothing else it could return, and a half-honored column list would be
//     worse than none. A pasted `SELECT * FROM notes` prefix is accepted and
//     dropped so SQL from elsewhere runs, but a real column list is refused
//     with a message saying so rather than silently ignored.
//   - A bare string is a free-text term over every text field, so the fast
//     thing a search box is for stays one word long and still composes:
//     'conversion' AND category = 'airflow'.
//   - NULL is absence, not SQL's third truth value. See the comment on
//     predicate.eval for why three-valued logic is the wrong trade here.
//
// Validation is complete at parse time: every field resolves against the
// catalog, every literal is coerced to that field's kind, and every pattern is
// compiled. What comes out cannot fail at evaluation, which is what lets the
// evaluator be a plain bool walk with no error return.

// Query is a parsed, validated advanced search, ready to run.
type Query struct {
	// Source is the text as typed, kept for round-tripping into a UI.
	Source string
	// Where is the filter; nil matches every note (an empty query).
	Where queryNode
	// Order is the ORDER BY list, empty when the query did not say. The
	// runner falls back to its own default ordering then.
	Order []QueryOrder
	// Limit and Offset come from the LIMIT/OFFSET clause; 0 means unset for
	// Limit and "from the start" for Offset. Options passed to RunQuery
	// override them, so an API caller's paging wins over a literal in the
	// text and the two cannot fight.
	Limit, Offset int

	// used is the set of canonical field names the query names anywhere,
	// including in ORDER BY. It drives two decisions in the runner: whether
	// the category link table has to be loaded at all, and whether the query
	// opted into seeing soft-deleted notes.
	used map[string]bool
}

// QueryOrder is one ORDER BY term.
type QueryOrder struct {
	Field *QueryField
	Desc  bool
}

// Uses reports whether the query names a field (by canonical name).
func (q *Query) Uses(name string) bool { return q.used[name] }

// UsedFields lists the canonical names of every field the query names,
// sorted. Handed back over the API so a UI can show what a query actually
// touched — which is also the quickest way to notice that an alias resolved
// somewhere unexpected.
func (q *Query) UsedFields() []string {
	out := make([]string, 0, len(q.used))
	for n := range q.used {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// NeedsCategories reports whether evaluating this query requires the
// note→category link table. Loading those links is a second pass over both
// databases, so a query that never mentions a category skips it entirely.
func (q *Query) NeedsCategories() bool {
	return q.used["category"] || q.used["subcategory"] || q.used["category_id"] || q.used["text"]
}

// IncludesDeleted reports whether the query mentions deleted_at. Naming the
// field is the opt-in: a query that does not mention it is filtered to live
// notes, and one that does is trusted to mean what it says (deleted_at IS NOT
// NULL would otherwise be a query that can never match anything).
func (q *Query) IncludesDeleted() bool { return q.used["deleted_at"] }

// String renders the query back in canonical form: uppercase keywords,
// canonical field names in place of aliases, single-quoted strings. The web UI
// shows it under the input as confirmation of how the text was understood,
// which is the cheapest possible answer to "why did that match?".
func (q *Query) String() string {
	var parts []string
	if q.Where != nil {
		parts = append(parts, q.Where.String())
	}
	if len(q.Order) > 0 {
		terms := make([]string, len(q.Order))
		for i, o := range q.Order {
			dir := "ASC"
			if o.Desc {
				dir = "DESC"
			}
			terms[i] = o.Field.Name + " " + dir
		}
		parts = append(parts, "ORDER BY "+strings.Join(terms, ", "))
	}
	if q.Limit > 0 {
		parts = append(parts, "LIMIT "+strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		parts = append(parts, "OFFSET "+strconv.Itoa(q.Offset))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// AST
// ---------------------------------------------------------------------------

// queryNode is one node of the parsed filter. eval takes the environment
// (the clock and the querying user) and the facts of one note; it cannot
// fail, because everything that could fail was settled during parsing.
type queryNode interface {
	eval(env *evalEnv, f *noteFacts) bool
	String() string
}

type andNode struct{ left, right queryNode }

func (n *andNode) eval(env *evalEnv, f *noteFacts) bool {
	// Short-circuit: the left side is often the cheap one (a flag or a
	// category) and the right the expensive one (a regex over a body).
	return n.left.eval(env, f) && n.right.eval(env, f)
}
func (n *andNode) String() string { return n.left.String() + " AND " + n.right.String() }

type orNode struct{ left, right queryNode }

func (n *orNode) eval(env *evalEnv, f *noteFacts) bool {
	return n.left.eval(env, f) || n.right.eval(env, f)
}

// OR renders parenthesised so that re-parsing the canonical form yields the
// same tree. Without the parentheses `a OR b AND c` would come back regrouped,
// since AND binds tighter.
func (n *orNode) String() string { return "(" + n.left.String() + " OR " + n.right.String() + ")" }

type notNode struct{ inner queryNode }

func (n *notNode) eval(env *evalEnv, f *noteFacts) bool { return !n.inner.eval(env, f) }
func (n *notNode) String() string                       { return "NOT " + n.inner.String() }

// predicate is a leaf: one field tested against zero or more literals.
type predicate struct {
	Field  *QueryField
	Op     CompareOp
	Negate bool
	Values []queryValue

	// Pos/End locate the predicate in the source, so a UI can point at the
	// one that produced a warning.
	Pos, End int
}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

type queryParser struct {
	src  string
	toks []token
	i    int
	used map[string]bool
}

// ParseQuery parses and validates advanced-search text. The returned Query is
// ready to run; the returned error is always a *QueryError carrying the byte
// offset of the problem.
//
// Empty (or whitespace-only) input is not an error: it parses to a query with
// no filter, which matches everything. A search box that is momentarily empty
// should show the whole library, not a complaint.
func ParseQuery(src string) (*Query, error) {
	toks, err := lexQuery(src)
	if err != nil {
		return nil, err
	}
	p := &queryParser{src: src, toks: toks, used: map[string]bool{}}

	p.skipSelectPrefix()
	p.skipKeyword("where")

	q := &Query{Source: src, used: p.used}
	if !p.atClauseEnd() {
		node, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		q.Where = node
	}
	if err := p.parseOrderBy(q); err != nil {
		return nil, err
	}
	if err := p.parseLimit(q); err != nil {
		return nil, err
	}
	if t := p.peek(); t.Kind != tokEOF {
		return nil, queryErrorf(t.Pos, t.End-t.Pos, "unexpected %s %q", t.Kind, t.Text)
	}
	return q, nil
}

// ---- token helpers --------------------------------------------------------

func (p *queryParser) peek() token { return p.toks[p.i] }
func (p *queryParser) next() token { t := p.toks[p.i]; p.i++; return t }
func (p *queryParser) atEOF() bool { return p.toks[p.i].Kind == tokEOF }
func (p *queryParser) peekN(n int) token {
	if p.i+n >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.i+n]
}

// isKeyword reports whether the current token is the given bare word.
// Comparison is against the token's folded value, so keywords are
// case-insensitive without the scanner having to know them.
func (p *queryParser) isKeyword(kw string) bool {
	t := p.peek()
	return t.Kind == tokIdent && t.Val == kw
}

// skipKeyword consumes the keyword if present and reports whether it did.
func (p *queryParser) skipKeyword(kw string) bool {
	if p.isKeyword(kw) {
		p.i++
		return true
	}
	return false
}

// atClauseEnd reports that the filter expression is over: end of input, or the
// start of a trailing clause. The trailing clauses are the reason this is not
// simply atEOF — parseOr must stop before ORDER BY rather than trying to read
// "order" as a field.
func (p *queryParser) atClauseEnd() bool {
	if p.atEOF() {
		return true
	}
	return p.isKeyword("order") || p.isKeyword("limit") || p.isKeyword("offset")
}

// skipSelectPrefix tolerates SQL pasted from a database client. Only `SELECT
// *` is accepted: a real column list is refused, because honoring it is
// impossible (a note is returned whole or not at all) and ignoring it would
// answer a different question than the one asked.
func (p *queryParser) skipSelectPrefix() {
	if !p.isKeyword("select") {
		return
	}
	star := p.peekN(1)
	if star.Kind != tokStar {
		// Leave it alone; the expression parser will produce a positioned
		// error naming the real problem.
		return
	}
	p.i += 2
	if p.skipKeyword("from") {
		// The table name, if any, is dropped. There is only one table.
		if p.peek().Kind == tokIdent {
			p.i++
		}
	}
}

// ---- expression -----------------------------------------------------------

func (p *queryParser) parseOr() (queryNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.skipKeyword("or") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &orNode{left: left, right: right}
	}
	return left, nil
}

func (p *queryParser) parseAnd() (queryNode, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.skipKeyword("and") {
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &andNode{left: left, right: right}
	}
	return left, nil
}

func (p *queryParser) parseNot() (queryNode, error) {
	if p.isKeyword("not") || (p.peek().Kind == tokOp && p.peek().Val == "!") {
		p.i++
		inner, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &notNode{inner: inner}, nil
	}
	return p.parsePrimary()
}

func (p *queryParser) parsePrimary() (queryNode, error) {
	t := p.peek()
	if t.Kind == tokLParen {
		p.i++
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().Kind != tokRParen {
			return nil, queryErrorf(p.peek().Pos, p.peek().End-p.peek().Pos, "expected ')'")
		}
		p.i++
		return inner, nil
	}
	return p.parsePredicate()
}

// textField is the pseudo-field a bare string searches. Resolved once here
// rather than looked up per free-text term.
var textField, _ = LookupQueryField("text")

// parsePredicate reads one leaf. It is the only place field names are
// resolved, which is why every "unknown field" message and its suggestion come
// from here.
func (p *queryParser) parsePredicate() (queryNode, error) {
	t := p.next()

	switch t.Kind {
	case tokString:
		// A bare quoted string: free text over every text field.
		if t.Unterminated {
			return nil, queryErrorf(t.Pos, t.End-t.Pos, "unterminated quoted value")
		}
		p.used["text"] = true
		return &predicate{
			Field: textField, Op: OpContains,
			Values: []queryValue{{Kind: KindString, Str: t.Val, fold: foldASCII(t.Val)}},
			Pos:    t.Pos, End: t.End,
		}, nil

	case tokIdent:
		field, ok := LookupQueryField(t.Val)
		if !ok {
			// A bare word followed by an operator was meant to be a field, and
			// getting the name wrong is the most likely mistake in a language
			// whose vocabulary is a fixed list. Anything else is free text.
			if p.startsOperator() {
				return nil, &QueryError{
					Msg:  fmt.Sprintf("unknown field %q", t.Text),
					Pos:  t.Pos,
					Len:  t.End - t.Pos,
					Hint: suggestField(t.Val),
				}
			}
			p.used["text"] = true
			return &predicate{
				Field: textField, Op: OpContains,
				Values: []queryValue{{Kind: KindString, Str: t.Text, fold: foldASCII(t.Text)}},
				Pos:    t.Pos, End: t.End,
			}, nil
		}
		p.used[field.Name] = true
		return p.parseComparison(field, t)

	case tokNumber:
		// A bare number is the note id. Typing a number into a search box and
		// getting that note is the behavior the simple search bar already has;
		// the advanced language keeps it rather than erroring.
		idField, _ := LookupQueryField("id")
		n, err := strconv.ParseFloat(t.Val, 64)
		if err != nil {
			return nil, queryErrorf(t.Pos, t.End-t.Pos, "malformed number %q", t.Text)
		}
		p.used["id"] = true
		return &predicate{
			Field: idField, Op: OpEq,
			Values: []queryValue{{Kind: KindNumber, Num: n, Str: t.Val}},
			Pos:    t.Pos, End: t.End,
		}, nil
	}

	return nil, queryErrorf(t.Pos, max(t.End-t.Pos, 1), "expected a field name, got %s", t.Kind)
}

// startsOperator reports whether the next token could begin a comparison —
// used only to decide whether an unresolvable bare word was a typo'd field or
// a free-text term.
func (p *queryParser) startsOperator() bool {
	t := p.peek()
	if t.Kind == tokOp {
		return true
	}
	if t.Kind != tokIdent {
		return false
	}
	switch t.Val {
	case "is", "in", "like", "contains", "matches", "between", "not":
		return true
	}
	return false
}

// parseComparison reads everything after a resolved field name.
func (p *queryParser) parseComparison(field *QueryField, fieldTok token) (queryNode, error) {
	start := fieldTok.Pos

	// `IS [NOT] NULL | EMPTY`
	if p.skipKeyword("is") {
		negate := p.skipKeyword("not")
		switch {
		case p.skipKeyword("null"):
			return &predicate{Field: field, Op: OpNull, Negate: negate, Pos: start, End: p.prevEnd()}, nil
		case p.skipKeyword("empty"):
			return &predicate{Field: field, Op: OpEmpty, Negate: negate, Pos: start, End: p.prevEnd()}, nil
		}
		t := p.peek()
		return nil, queryErrorf(t.Pos, max(t.End-t.Pos, 1), "expected NULL or EMPTY after IS")
	}

	// A leading NOT belongs to the operator that follows it: `NOT IN`,
	// `NOT LIKE`, `NOT BETWEEN`, `NOT CONTAINS`, `NOT MATCHES`.
	negate := p.skipKeyword("not")

	switch {
	case p.skipKeyword("in"):
		return p.parseIn(field, start, negate)
	case p.skipKeyword("between"):
		return p.parseBetween(field, start, negate)
	case p.skipKeyword("like"):
		return p.parsePattern(field, start, negate, OpLike)
	case p.skipKeyword("ilike"): // accepted as a synonym; LIKE is already case-insensitive here
		return p.parsePattern(field, start, negate, OpLike)
	case p.skipKeyword("contains"):
		return p.parsePattern(field, start, negate, OpContains)
	case p.skipKeyword("matches"), p.skipKeyword("regexp"), p.skipKeyword("rlike"):
		return p.parsePattern(field, start, negate, OpMatches)
	}

	// A symbolic operator.
	if t := p.peek(); t.Kind == tokOp {
		p.i++
		op, opNegate, err := symbolicOp(t, field)
		if err != nil {
			return nil, err
		}
		if op == OpMatches || op == OpLike || op == OpContains {
			return p.parsePattern(field, start, negate != opNegate, op)
		}
		val, err := p.parseValue(field)
		if err != nil {
			return nil, err
		}
		return &predicate{
			Field: field, Op: op, Negate: negate != opNegate,
			Values: []queryValue{val}, Pos: start, End: p.prevEnd(),
		}, nil
	}

	if negate {
		t := p.peek()
		return nil, queryErrorf(t.Pos, max(t.End-t.Pos, 1), "expected IN, LIKE, MATCHES or BETWEEN after NOT")
	}

	// A bare field with no operator. Only booleans can stand alone, where the
	// bare name reads as "is true" — `is_flagged AND category = 'work'`.
	if field.Kind == KindBool {
		return &predicate{Field: field, Op: OpTruthy, Pos: start, End: fieldTok.End}, nil
	}
	t := p.peek()
	return nil, queryErrorf(t.Pos, max(t.End-t.Pos, 1),
		"expected an operator after %s", field.Name)
}

// symbolicOp maps a punctuation operator onto a CompareOp, rejecting the ones
// that make no sense for the field's kind. `~` and `!~` are the regex
// shorthands; `==` is accepted as a synonym for `=` because it is what a
// programmer's fingers type.
func symbolicOp(t token, field *QueryField) (CompareOp, bool, error) {
	switch t.Val {
	case "=", "==":
		return OpEq, false, nil
	case "!=", "<>":
		return OpEq, true, nil
	case "~":
		return OpMatches, false, nil
	case "!~":
		return OpMatches, true, nil
	case "<", "<=", ">", ">=":
		if field.Kind == KindBool {
			return "", false, queryErrorf(t.Pos, t.End-t.Pos,
				"%s cannot be ordered; use = with true or false", field.Name)
		}
		return CompareOp(t.Val), false, nil
	}
	return "", false, queryErrorf(t.Pos, t.End-t.Pos, "unsupported operator %q", t.Text)
}

func (p *queryParser) parseIn(field *QueryField, start int, negate bool) (queryNode, error) {
	if p.peek().Kind != tokLParen {
		t := p.peek()
		return nil, queryErrorf(t.Pos, max(t.End-t.Pos, 1), "expected '(' after IN")
	}
	p.i++
	var vals []queryValue
	for {
		v, err := p.parseValue(field)
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
		if p.peek().Kind == tokComma {
			p.i++
			continue
		}
		break
	}
	if p.peek().Kind != tokRParen {
		t := p.peek()
		return nil, queryErrorf(t.Pos, max(t.End-t.Pos, 1), "expected ',' or ')' in IN list")
	}
	p.i++
	return &predicate{Field: field, Op: OpIn, Negate: negate, Values: vals, Pos: start, End: p.prevEnd()}, nil
}

// parseBetween reads `BETWEEN a AND b`. The AND here is part of the operator,
// not a conjunction — reading it as one is the classic recursive-descent bug
// in this grammar, so the keyword is consumed explicitly and its absence is a
// named error rather than a confusing one further along.
func (p *queryParser) parseBetween(field *QueryField, start int, negate bool) (queryNode, error) {
	lo, err := p.parseValue(field)
	if err != nil {
		return nil, err
	}
	if !p.skipKeyword("and") {
		t := p.peek()
		return nil, queryErrorf(t.Pos, max(t.End-t.Pos, 1), "expected AND in BETWEEN")
	}
	hi, err := p.parseValue(field)
	if err != nil {
		return nil, err
	}
	return &predicate{
		Field: field, Op: OpBetween, Negate: negate,
		Values: []queryValue{lo, hi}, Pos: start, End: p.prevEnd(),
	}, nil
}

// parsePattern reads the right-hand side of LIKE / CONTAINS / MATCHES and
// compiles it. Pattern operators only make sense against text, so a
// non-string field is refused here with a message that names the field rather
// than failing to match at run time.
func (p *queryParser) parsePattern(field *QueryField, start int, negate bool, op CompareOp) (queryNode, error) {
	if field.Kind != KindString {
		t := p.peek()
		return nil, queryErrorf(start, max(t.End-start, 1),
			"%s is a %s; %s applies to text fields", field.Name, field.Kind, op)
	}
	t := p.peek()
	val, err := p.parseValue(field)
	if err != nil {
		return nil, err
	}

	switch op {
	case OpLike:
		re, err := likeToRegexp(val.Str)
		if err != nil {
			return nil, queryErrorf(t.Pos, t.End-t.Pos, "invalid LIKE pattern: %v", err)
		}
		val.pattern = re
	case OpMatches:
		re, err := regexp.Compile(val.Str)
		if err != nil {
			// The regexp package's message already names the offending
			// construct; prefixing it keeps the position and the cause together.
			return nil, queryErrorf(t.Pos, t.End-t.Pos, "invalid regular expression: %v", err)
		}
		val.pattern = re
	}
	return &predicate{
		Field: field, Op: op, Negate: negate,
		Values: []queryValue{val}, Pos: start, End: p.prevEnd(),
	}, nil
}

// parseValue reads one literal and coerces it to the field's kind. Every
// "that is not a number" style error in the language originates here, with the
// literal's own position.
func (p *queryParser) parseValue(field *QueryField) (queryValue, error) {
	t := p.next()
	span := max(t.End-t.Pos, 1)

	if t.Kind == tokString && t.Unterminated {
		return queryValue{}, queryErrorf(t.Pos, span, "unterminated quoted value")
	}

	switch field.Kind {
	case KindString:
		switch t.Kind {
		case tokString:
			return queryValue{Kind: KindString, Str: t.Val, fold: foldASCII(t.Val)}, nil
		case tokIdent:
			// `me` is the one identifier with a meaning of its own; everything
			// else is an unquoted value, which is tolerated so a one-word
			// category need not be quoted.
			if t.Val == "me" {
				return queryValue{Kind: KindString, Str: "me", isMe: true}, nil
			}
			if t.Val == "null" {
				return queryValue{}, queryErrorf(t.Pos, span, "use IS NULL rather than = NULL")
			}
			return queryValue{Kind: KindString, Str: t.Text, fold: foldASCII(t.Text)}, nil
		case tokNumber, tokDuration:
			return queryValue{Kind: KindString, Str: t.Val, fold: foldASCII(t.Val)}, nil
		}

	case KindNumber:
		switch t.Kind {
		case tokNumber:
			n, err := strconv.ParseFloat(t.Val, 64)
			if err != nil {
				return queryValue{}, queryErrorf(t.Pos, span, "malformed number %q", t.Text)
			}
			return queryValue{Kind: KindNumber, Num: n, Str: t.Val}, nil
		case tokString:
			n, err := strconv.ParseFloat(strings.TrimSpace(t.Val), 64)
			if err != nil {
				return queryValue{}, queryErrorf(t.Pos, span,
					"%s is a number; %q is not one", field.Name, t.Val)
			}
			return queryValue{Kind: KindNumber, Num: n, Str: t.Val}, nil
		}

	case KindBool:
		if b, ok := boolLiteral(t); ok {
			return queryValue{Kind: KindBool, Bool: b, Str: strconv.FormatBool(b)}, nil
		}
		return queryValue{}, queryErrorf(t.Pos, span,
			"%s is a true/false field; %q is neither", field.Name, t.Text)

	case KindTime:
		switch t.Kind {
		case tokDuration:
			d, ok := parseDurationLiteral(t.Val)
			if !ok {
				return queryValue{}, queryErrorf(t.Pos, span, "malformed time offset %q", t.Text)
			}
			return queryValue{Kind: KindTime, Str: t.Val, rel: &relativeTime{offset: d}}, nil
		case tokIdent:
			for _, kw := range timeKeywords {
				if t.Val == kw {
					gran := granDay
					if kw == "now" {
						gran = granInstant
					}
					return queryValue{Kind: KindTime, Str: kw, rel: &relativeTime{keyword: kw}, gran: gran}, nil
				}
			}
			return queryValue{}, queryErrorf(t.Pos, span,
				"%s is a timestamp; %q is not a date, an offset like -7d, or one of now/today/yesterday/tomorrow",
				field.Name, t.Text)
		case tokString:
			// A quoted offset is accepted too: a UI that quotes everything it
			// inserts should not produce a query that fails to parse.
			if d, ok := parseDurationLiteral(t.Val); ok && len(t.Val) > 0 && (t.Val[0] == '-' || t.Val[0] == '+') {
				return queryValue{Kind: KindTime, Str: t.Val, rel: &relativeTime{offset: d}}, nil
			}
			if tv, gran, ok := parseTimeLiteral(t.Val); ok {
				return queryValue{Kind: KindTime, Str: t.Val, abs: tv, gran: gran}, nil
			}
			return queryValue{}, queryErrorf(t.Pos, span,
				"%q is not a date this language recognises (try 2026-03-04, or -7d)", t.Val)
		}
	}

	return queryValue{}, queryErrorf(t.Pos, span, "expected a value for %s, got %s", field.Name, t.Kind)
}

// boolLiteral accepts the spellings people actually type for a flag.
func boolLiteral(t token) (bool, bool) {
	switch t.Kind {
	case tokIdent, tokString:
		switch foldASCII(t.Val) {
		case "true", "t", "yes", "y", "on", "1":
			return true, true
		case "false", "f", "no", "n", "off", "0":
			return false, true
		}
	case tokNumber:
		switch t.Val {
		case "1":
			return true, true
		case "0":
			return false, true
		}
	}
	return false, false
}

// prevEnd is the end offset of the token just consumed, used to close a
// predicate's source span.
func (p *queryParser) prevEnd() int {
	if p.i == 0 {
		return 0
	}
	return p.toks[p.i-1].End
}

// ---- trailing clauses -----------------------------------------------------

func (p *queryParser) parseOrderBy(q *Query) error {
	if !p.isKeyword("order") {
		return nil
	}
	p.i++
	if !p.skipKeyword("by") {
		t := p.peek()
		return queryErrorf(t.Pos, max(t.End-t.Pos, 1), "expected BY after ORDER")
	}
	for {
		t := p.next()
		if t.Kind != tokIdent {
			return queryErrorf(t.Pos, max(t.End-t.Pos, 1), "expected a field name in ORDER BY")
		}
		field, ok := LookupQueryField(t.Val)
		if !ok {
			return &QueryError{
				Msg: fmt.Sprintf("unknown field %q in ORDER BY", t.Text),
				Pos: t.Pos, Len: t.End - t.Pos, Hint: suggestField(t.Val),
			}
		}
		if field.Multi {
			// Sorting by a set has no single answer, and picking one silently
			// (first? lowest?) would be a rule nobody could predict.
			return queryErrorf(t.Pos, t.End-t.Pos,
				"%s holds several values per note and cannot be sorted on", field.Name)
		}
		p.used[field.Name] = true

		desc := false
		if p.skipKeyword("desc") {
			desc = true
		} else {
			p.skipKeyword("asc")
		}
		q.Order = append(q.Order, QueryOrder{Field: field, Desc: desc})

		if p.peek().Kind == tokComma {
			p.i++
			continue
		}
		return nil
	}
}

func (p *queryParser) parseLimit(q *Query) error {
	if p.skipKeyword("limit") {
		t := p.next()
		n, err := strconv.Atoi(t.Val)
		if t.Kind != tokNumber || err != nil || n < 0 {
			return queryErrorf(t.Pos, max(t.End-t.Pos, 1), "LIMIT expects a non-negative whole number")
		}
		q.Limit = n
	}
	if p.skipKeyword("offset") {
		t := p.next()
		n, err := strconv.Atoi(t.Val)
		if t.Kind != tokNumber || err != nil || n < 0 {
			return queryErrorf(t.Pos, max(t.End-t.Pos, 1), "OFFSET expects a non-negative whole number")
		}
		q.Offset = n
	}
	return nil
}

// ---- diagnostics ----------------------------------------------------------

// suggestField finds the closest accepted field spelling to what was typed,
// so "catgory" comes back as "did you mean category?". A cutoff keeps it from
// suggesting something unrelated: past a third of the word's length the guess
// is noise, and a wrong suggestion is worse than none.
func suggestField(typed string) string {
	best, bestDist := "", 1<<30
	limit := len(typed)/3 + 1
	for _, name := range QueryFieldNames() {
		d := editDistance(typed, name)
		if d < bestDist {
			best, bestDist = name, d
		}
	}
	if best == "" || bestDist > limit {
		return ""
	}
	return "did you mean " + best + "?"
}

// editDistance is Levenshtein with a single rolling row. Field names are short
// and the list is fixed, so this runs in microseconds and only ever runs on
// the error path.
func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(min(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
