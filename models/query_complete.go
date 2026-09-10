package models

import (
	"sort"
	"strings"
)

// query_complete.go answers "what could come next here?" for a half-typed
// query, and is the reason the language is worth having: nobody remembers a
// schema, and a search box that finishes the field names, the operators and
// the actual category names in the database is a different tool from one that
// merely accepts SQL.
//
// It lives in models rather than in either UI on purpose. The web page and the
// terminal both need the same answer, and half of that answer is data — the
// categories, subcategories and tags this user actually has — which only the
// models layer can see. One implementation, two front ends:
//
//	web/static/js/advanced_search.js ─┐
//	                                  ├─▶ /api/v1/notes/query/complete ─▶ CompleteQuery
//	tui/query.go (via the Store) ─────┘                                        │
//	                                                    catalog + live data ◀──┘
//
// The completer works from a re-lex of the text to the LEFT of the cursor. It
// never parses: a query being typed is almost always invalid, and a parser
// that must succeed to say anything would go quiet exactly when help is
// wanted. A small state machine over the token stream is enough to know
// whether a field, an operator, a value or a conjunction belongs at the
// cursor, and it degrades gracefully — an unrecognised stretch of tokens
// leaves the machine in the "expecting a conjunction" state, which suggests
// AND/OR rather than nothing.

// QuerySuggestion is one completion candidate.
type QuerySuggestion struct {
	// Text is the literal replacement for [ReplaceStart, ReplaceEnd). It
	// includes quoting and any trailing space, so both front ends complete by
	// splicing a string and moving the cursor to the end of it — no
	// UI-specific rules about when to quote or where to put a space, which is
	// exactly the kind of thing that would drift between two clients.
	Text string `json:"text"`
	// Label is what the list shows; Detail is the dimmed right-hand column.
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	// Kind groups the list and picks its icon: field, operator, keyword,
	// value, logic, example.
	Kind string `json:"kind"`
	// Doc is the longer explanation, shown for the highlighted row.
	Doc string `json:"doc,omitempty"`
}

// QueryCompletion is a completion request's answer.
type QueryCompletion struct {
	Suggestions []QuerySuggestion `json:"suggestions"`
	// ReplaceStart and ReplaceEnd are byte offsets into the query text
	// delimiting what a chosen suggestion replaces — the partial word under
	// the cursor, or an empty span at the cursor when nothing is half-typed.
	ReplaceStart int `json:"replace_start"`
	ReplaceEnd   int `json:"replace_end"`
	// Context names what is being completed, for the UI's hint line
	// ("field", "operator", "value for category", …).
	Context string `json:"context"`
}

// completeState is the grammar position the state machine believes the cursor
// is in. It is a deliberately coarse model of query_parse.go's grammar: enough
// to choose a suggestion list, not enough to reject anything.
type completeState int

const (
	stField      completeState = iota // a field name, NOT, or '(' belongs here
	stOperator                        // a comparison operator for curField
	stValue                           // a literal of curField's kind
	stLogic                           // AND / OR / ORDER BY / LIMIT / ')'
	stIsWord                          // after IS [NOT]: NULL or EMPTY
	stBetweenAnd                      // after BETWEEN's first value: the AND
	stOrderBy                         // after ORDER: the BY
	stOrderField
	stOrderDir
	stNumberLiteral // after LIMIT / OFFSET
)

// CompleteQuery returns the completions valid at byte offset pos in src.
//
// userGUID scopes the live value suggestions; an empty GUID still yields the
// catalog-derived ones (fields, operators, keywords), which is what makes the
// completer usable in a test or before login.
func CompleteQuery(src string, pos int, userGUID string) *QueryCompletion {
	if pos < 0 {
		pos = 0
	}
	if pos > len(src) {
		pos = len(src)
	}

	prefixTok, committed := splitAtCursor(src, pos)
	state, field, inList := walkStates(committed)

	// The span a suggestion replaces: the partial token under the cursor,
	// extended rightwards through the rest of the word so completing in the
	// middle of a name does not leave its tail behind.
	start, end := pos, pos
	typed := ""
	if prefixTok != nil {
		start = prefixTok.Pos
		end = extendToWordEnd(src, pos, prefixTok)
		typed = prefixTok.Val
		if prefixTok.Kind == tokIdent || prefixTok.Kind == tokNumber {
			typed = prefixTok.Text
		}
	}

	c := &QueryCompletion{ReplaceStart: start, ReplaceEnd: end}
	// A trailing space is added to an insertion unless the text already
	// continues with one — so completing mid-query does not accumulate gaps.
	space := " "
	if end < len(src) && src[end] == ' ' {
		space = ""
	}

	quoteStyle := byte('\'')
	if prefixTok != nil && prefixTok.Kind == tokString && prefixTok.Quote != 0 {
		quoteStyle = prefixTok.Quote
	}

	switch state {
	case stField:
		c.Context = "field"
		c.Suggestions = fieldSuggestions(typed, space, len(committed) == 0 && typed == "")
	case stOperator:
		c.Context = "operator for " + field.Name
		c.Suggestions = operatorSuggestions(field, typed, space)
	case stValue:
		c.Context = "value for " + field.Name
		c.Suggestions = valueSuggestions(field, typed, quoteStyle, space, inList, userGUID)
	case stIsWord:
		c.Context = "NULL or EMPTY"
		c.Suggestions = keywordSuggestions([][2]string{
			{"NULL", "the attribute has no value"},
			{"NOT NULL", "the attribute has a value"},
			{"EMPTY", "no value, or a blank one"},
			{"NOT EMPTY", "has a non-blank value"},
		}, typed, space)
	case stBetweenAnd:
		c.Context = "AND (upper bound)"
		c.Suggestions = keywordSuggestions([][2]string{{"AND", "the upper bound of the range"}}, typed, space)
	case stOrderBy:
		c.Context = "BY"
		c.Suggestions = keywordSuggestions([][2]string{{"BY", "the field to order on"}}, typed, space)
	case stOrderField:
		c.Context = "order by field"
		c.Suggestions = sortFieldSuggestions(typed, space)
	case stOrderDir:
		c.Context = "sort direction"
		c.Suggestions = keywordSuggestions([][2]string{
			{"DESC", "highest or newest first"},
			{"ASC", "lowest or oldest first"},
			{"LIMIT", "cap the number of notes returned"},
		}, typed, space)
	case stNumberLiteral:
		c.Context = "count"
		c.Suggestions = keywordSuggestions([][2]string{
			{"10", ""}, {"25", ""}, {"50", ""}, {"100", ""},
		}, typed, space)
	default: // stLogic
		c.Context = "conjunction"
		rows := [][2]string{
			{"AND", "both sides must hold"},
			{"OR", "either side may hold"},
			{"ORDER BY", "choose the ordering"},
			{"LIMIT", "cap the number of notes returned"},
		}
		c.Suggestions = keywordSuggestions(rows, typed, space)
	}
	return c
}

// splitAtCursor lexes the text left of the cursor and separates the token
// being typed (if the cursor sits at its end with nothing between) from the
// tokens already committed.
//
// The distinction is the whole basis of the completer: "category" with the
// cursor right after the y is a partial field name to filter on, while
// "category " with a space is a committed field asking what operator comes
// next.
func splitAtCursor(src string, pos int) (*token, []token) {
	toks, err := lexQuery(src[:pos])
	if err != nil {
		// A hard lex error (an unknown duration unit, a stray character)
		// leaves whatever was scanned before it. Suggestions from a partial
		// stream are better than none while the user is still typing.
		toks = append(toks, token{Kind: tokEOF, Pos: pos, End: pos})
	}
	// Drop the EOF sentinel.
	if n := len(toks); n > 0 && toks[n-1].Kind == tokEOF {
		toks = toks[:n-1]
	}
	if n := len(toks); n > 0 {
		last := toks[n-1]
		// Only an ident, a number or a string can be "half typed"; a
		// punctuation token that ends at the cursor is complete.
		if last.End == pos && (last.Kind == tokIdent || last.Kind == tokNumber || last.Kind == tokString) {
			return &last, toks[:n-1]
		}
	}
	return nil, toks
}

// extendToWordEnd finds where the word under the cursor ends in the FULL
// text, so a completion accepted mid-word replaces the whole word rather than
// splicing a new head onto an old tail.
func extendToWordEnd(src string, pos int, tok *token) int {
	end := pos
	switch tok.Kind {
	case tokIdent, tokNumber:
		for end < len(src) && isIdentPart(src[end]) {
			end++
		}
	case tokString:
		// Consume through the closing quote if the literal is already closed.
		for end < len(src) && src[end] != tok.Quote {
			end++
		}
		if end < len(src) {
			end++
		}
	}
	return end
}

// betweenStage tracks how far through a BETWEEN clause the cursor is. The
// clause is the one place a keyword is part of an OPERATOR rather than a
// conjunction — `BETWEEN a AND b` — so without this the AND would be read as
// the start of a new condition and everything after it would complete in the
// wrong context.
type betweenStage int

const (
	betweenNone   betweenStage = iota
	betweenFirst               // after BETWEEN: the lower bound is due
	betweenAnd                 // after the lower bound: the AND is due
	betweenSecond              // after the AND: the upper bound is due
)

// walkStates runs the committed tokens through the state machine and reports
// where the cursor lands, which field is in scope, and whether an IN list is
// open (a list keeps expecting values until its ')').
func walkStates(toks []token) (completeState, *QueryField, bool) {
	state := stField
	var field *QueryField
	inList := false
	depth := 0
	between := betweenNone

	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.Kind {
		case tokLParen:
			depth++
			if state == stValue {
				inList = true
			} else {
				state = stField
			}
			continue
		case tokRParen:
			if depth > 0 {
				depth--
			}
			inList = false
			state = stLogic
			continue
		case tokComma:
			if inList {
				state = stValue
			} else if state == stOrderDir || state == stOrderField {
				state = stOrderField
			}
			continue
		case tokOp:
			state = stValue
			continue
		case tokString, tokNumber, tokDuration:
			switch state {
			case stValue:
				switch {
				case inList:
					// A list keeps taking values until its ')'.
				case between == betweenFirst:
					between = betweenAnd
					state = stBetweenAnd
				case between == betweenSecond:
					between = betweenNone
					state = stLogic
				default:
					state = stLogic
				}
			case stNumberLiteral:
				state = stLogic
			case stField:
				// A bare literal is a free-text term; it stands alone.
				state = stLogic
			}
			continue
		}

		// tokIdent: a keyword or a name, decided by the state it arrives in.
		switch state {
		case stField:
			switch t.Val {
			case "not", "where", "select", "from":
				// Modifiers that leave the expectation unchanged.
			default:
				if f, ok := LookupQueryField(t.Val); ok {
					field = f
					state = stOperator
				} else {
					state = stLogic // a bare word: free text
				}
			}

		case stOperator:
			switch t.Val {
			case "not":
				// Belongs to the operator that follows.
			case "is":
				state = stIsWord
			case "in":
				state = stValue
			case "between":
				between = betweenFirst
				state = stValue
			case "like", "ilike", "contains", "matches", "regexp", "rlike":
				state = stValue
			case "and", "or":
				// The field stood alone — a bare boolean predicate — and this
				// word starts the next condition. Without this arm the
				// conjunction would be eaten as if it were an operator and
				// everything after it would complete in the wrong context.
				state = stField
			case "order":
				state = stOrderBy
			case "limit", "offset":
				state = stNumberLiteral
			default:
				state = stLogic
			}

		case stValue:
			// An unquoted value, or `me`, or a time keyword. It advances a
			// BETWEEN clause the same way a quoted one does.
			switch {
			case inList:
			case between == betweenFirst:
				between = betweenAnd
				state = stBetweenAnd
			case between == betweenSecond:
				between = betweenNone
				state = stLogic
			default:
				state = stLogic
			}

		case stIsWord:
			if t.Val == "not" {
				break // still expecting NULL/EMPTY
			}
			state = stLogic

		case stBetweenAnd:
			if t.Val == "and" {
				between = betweenSecond
				state = stValue
			} else {
				between = betweenNone
				state = stLogic
			}

		case stLogic:
			switch t.Val {
			case "and", "or":
				state = stField
			case "order":
				state = stOrderBy
			case "limit", "offset":
				state = stNumberLiteral
			}

		case stOrderBy:
			if t.Val == "by" {
				state = stOrderField
			}

		case stOrderField:
			if f, ok := LookupQueryField(t.Val); ok {
				field = f
			}
			state = stOrderDir

		case stOrderDir:
			switch t.Val {
			case "asc", "desc":
				// stay
			case "limit", "offset":
				state = stNumberLiteral
			}

		case stNumberLiteral:
			if t.Val == "offset" {
				state = stNumberLiteral
			} else {
				state = stLogic
			}
		}
	}

	if field == nil {
		field, _ = LookupQueryField("title")
	}
	return state, field, inList
}

// ---------------------------------------------------------------------------
// Suggestion sources
// ---------------------------------------------------------------------------

// fieldSuggestions offers the catalog. Aliases are offered too, but only when
// the user has started typing something an alias matches better than a
// canonical name — otherwise the list would be three times as long and twice
// as confusing on the very first keystroke.
func fieldSuggestions(typed, space string, empty bool) []QuerySuggestion {
	var out []QuerySuggestion
	for i := range queryFields {
		f := &queryFields[i]
		name := f.Name
		if !matchesPrefix(name, typed) {
			// Try the aliases; complete to the CANONICAL name so the query
			// text converges on one spelling.
			hit := ""
			for _, a := range f.Aliases {
				if matchesPrefix(a, typed) {
					hit = a
					break
				}
			}
			if hit == "" {
				continue
			}
		}
		detail := string(f.Kind)
		if f.Multi {
			detail += " (multi)"
		}
		out = append(out, QuerySuggestion{
			Text: name + space, Label: name, Detail: detail,
			Kind: "field", Doc: f.Description,
		})
	}
	// Structural options belong in the same list: someone opening a
	// parenthesis or negating a clause is at a field position too.
	for _, kw := range [][2]string{
		{"NOT", "negate the next condition"},
		{"(", "group conditions"},
	} {
		if matchesPrefix(kw[0], typed) {
			text := kw[0] + space
			if kw[0] == "(" {
				text = "("
			}
			out = append(out, QuerySuggestion{Text: text, Label: kw[0], Kind: "logic", Doc: kw[1]})
		}
	}

	if empty {
		// An empty box is where a person most needs to see what the language
		// can do, so the worked examples lead — ahead of an alphabet of field
		// names that mean nothing on their own.
		ex := make([]QuerySuggestion, 0, len(QueryExamples))
		for _, e := range QueryExamples {
			ex = append(ex, QuerySuggestion{Text: e, Label: e, Kind: "example", Detail: "example"})
		}
		return append(ex, out...)
	}
	return rankSuggestions(out, typed)
}

func sortFieldSuggestions(typed, space string) []QuerySuggestion {
	var out []QuerySuggestion
	for i := range queryFields {
		f := &queryFields[i]
		if f.Multi || !matchesPrefix(f.Name, typed) {
			continue
		}
		out = append(out, QuerySuggestion{
			Text: f.Name + space, Label: f.Name, Detail: string(f.Kind),
			Kind: "field", Doc: f.Description,
		})
	}
	return rankSuggestions(out, typed)
}

func operatorSuggestions(field *QueryField, typed, space string) []QuerySuggestion {
	var out []QuerySuggestion
	for _, op := range operatorsForField(field) {
		if !matchesPrefix(op.Token, typed) {
			continue
		}
		out = append(out, QuerySuggestion{
			Text: op.Token + space, Label: op.Token, Kind: "operator", Doc: op.Description,
		})
	}
	return rankSuggestions(out, typed)
}

func keywordSuggestions(rows [][2]string, typed, space string) []QuerySuggestion {
	var out []QuerySuggestion
	for _, r := range rows {
		if !matchesPrefix(r[0], typed) {
			continue
		}
		out = append(out, QuerySuggestion{Text: r[0] + space, Label: r[0], Kind: "keyword", Doc: r[1]})
	}
	return rankSuggestions(out, typed)
}

// valueSuggestions is where the completer stops being a syntax helper and
// starts being useful: the categories, subcategories, tags and titles offered
// here are the ones this user actually has.
//
// inList suppresses the trailing space's companion — inside an IN list a
// comma usually follows, so the inserted text ends bare and the UI's next
// keystroke is the comma.
func valueSuggestions(field *QueryField, typed string, quote byte, space string, inList bool, userGUID string) []QuerySuggestion {
	trail := space
	if inList {
		trail = ""
	}

	quoted := func(v string) string {
		q := string(quote)
		return q + strings.ReplaceAll(v, q, q+q) + q + trail
	}
	bare := func(v string) string { return v + trail }

	var out []QuerySuggestion

	switch field.Kind {
	case KindBool:
		for _, v := range []string{"true", "false"} {
			if matchesPrefix(v, typed) {
				out = append(out, QuerySuggestion{Text: bare(v), Label: v, Kind: "value"})
			}
		}
		return out

	case KindTime:
		rows := [][2]string{
			{"today", "midnight this morning"},
			{"now", "this instant"},
			{"yesterday", "midnight yesterday"},
			{"-7d", "seven days ago"},
			{"-30d", "thirty days ago"},
			{"-24h", "twenty-four hours ago"},
			{"-1y", "a year ago"},
		}
		for _, r := range rows {
			if matchesPrefix(r[0], typed) {
				out = append(out, QuerySuggestion{Text: bare(r[0]), Label: r[0], Kind: "value", Doc: r[1]})
			}
		}
		return out

	case KindNumber:
		if field.Name == "category_id" {
			for _, c := range queryCategoryList(userGUID) {
				id := itoa(c.ID)
				if !matchesPrefix(id, typed) && !containsFold(c.Name, foldASCII(typed)) {
					continue
				}
				out = append(out, QuerySuggestion{Text: bare(id), Label: id, Detail: c.Name, Kind: "value"})
			}
		}
		return out
	}

	// String-valued fields.
	switch field.Name {
	case "category":
		for _, c := range queryCategoryList(userGUID) {
			if matchesPrefix(c.Name, typed) {
				out = append(out, QuerySuggestion{
					Text: quoted(c.Name), Label: c.Name, Kind: "value",
					Detail: subcategoryCountLabel(len(c.SubcategoryList())),
				})
			}
		}
	case "subcategory":
		for _, s := range distinctSubcategories(userGUID) {
			if matchesPrefix(s.name, typed) {
				out = append(out, QuerySuggestion{
					Text: quoted(s.name), Label: s.name, Detail: s.owner, Kind: "value",
				})
			}
		}
	case "tags":
		for _, t := range distinctTags(userGUID) {
			if matchesPrefix(t.name, typed) {
				out = append(out, QuerySuggestion{
					Text: quoted(t.name), Label: t.name, Detail: noteCountLabel(t.count), Kind: "value",
				})
			}
		}
	case "created_by", "updated_by":
		if matchesPrefix("me", typed) {
			out = append(out, QuerySuggestion{
				Text: bare("me"), Label: "me", Kind: "value",
				Doc: "the user running the query",
			})
		}
	case "title":
		for _, t := range distinctTitles(userGUID, typed) {
			out = append(out, QuerySuggestion{Text: quoted(t), Label: t, Kind: "value"})
		}
	}
	return rankSuggestions(out, typed)
}

// ---------------------------------------------------------------------------
// Live value sources
// ---------------------------------------------------------------------------

// completionValueLimit caps every data-derived suggestion list. A completion
// popup nobody can read past is the same as no completion, and the cap also
// bounds the work done on a keystroke.
const completionValueLimit = 40

func queryCategoryList(userGUID string) []Category {
	if userGUID == "" || pubDB == nil {
		return nil
	}
	cats, err := ListCategories(0, 0, userGUID)
	if err != nil {
		return nil
	}
	return cats
}

type namedValue struct {
	name  string
	owner string
	count int
}

// distinctSubcategories unions the subcategories DEFINED on the user's
// categories with the ones actually SELECTED on notes. Both matter: a
// definition is offerable before any note uses it, and a selection can outlive
// the definition it came from (renames, imports, a spec edited by hand).
func distinctSubcategories(userGUID string) []namedValue {
	if userGUID == "" {
		return nil
	}
	seen := map[string]*namedValue{}
	add := func(name, owner string) {
		if name == "" {
			return
		}
		key := foldASCII(name)
		if v, ok := seen[key]; ok {
			if v.owner != owner && v.owner != "" && owner != "" {
				v.owner = "several categories"
			}
			return
		}
		seen[key] = &namedValue{name: name, owner: owner}
	}

	for _, c := range queryCategoryList(userGUID) {
		for _, s := range c.SubcategoryList() {
			add(s, c.Name)
		}
	}
	if maps, err := GetAllNoteCategoryMappings(userGUID); err == nil {
		for _, m := range maps {
			for _, s := range m.SelectedSubcategories {
				add(s, m.CategoryName)
			}
		}
	}

	out := make([]namedValue, 0, len(seen))
	for _, v := range seen {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return foldASCII(out[i].name) < foldASCII(out[j].name) })
	return out
}

// distinctTags counts every tag across the user's notes, most used first —
// the order that puts a working vocabulary at the top of the list.
func distinctTags(userGUID string) []namedValue {
	notes, err := loadNotesForQuery(userGUID, false)
	if err != nil {
		return nil
	}
	counts := map[string]*namedValue{}
	for i := range notes {
		if !notes[i].Tags.Valid {
			continue
		}
		for _, t := range strings.Split(notes[i].Tags.String, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			key := foldASCII(t)
			if v, ok := counts[key]; ok {
				v.count++
				continue
			}
			counts[key] = &namedValue{name: t, count: 1}
		}
	}
	out := make([]namedValue, 0, len(counts))
	for _, v := range counts {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return foldASCII(out[i].name) < foldASCII(out[j].name)
	})
	return out
}

// distinctTitles offers existing note titles, which turns `title = ` into a
// picker. Filtering happens here rather than in the caller because the whole
// library is the candidate set and only a screenful can be shown.
func distinctTitles(userGUID, typed string) []string {
	notes, err := loadNotesForQuery(userGUID, false)
	if err != nil {
		return nil
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].UpdatedAt.After(notes[j].UpdatedAt) })
	fold := foldASCII(typed)
	var out []string
	seen := map[string]bool{}
	for i := range notes {
		t := notes[i].Title
		if t == "" || seen[t] {
			continue
		}
		if fold != "" && !containsFold(t, fold) {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= completionValueLimit {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Ranking
// ---------------------------------------------------------------------------

// matchesPrefix is the filter every suggestion list runs through. It accepts a
// prefix match or a substring match, so "cat" finds category and "flag" finds
// is_flagged — the underscore-prefixed column names would otherwise be
// unreachable by the word a person actually has in mind.
func matchesPrefix(candidate, typed string) bool {
	if typed == "" {
		return true
	}
	c, t := foldASCII(candidate), foldASCII(typed)
	return strings.HasPrefix(c, t) || strings.Contains(c, t)
}

// rankSuggestions puts true prefix matches above mere substring matches and
// otherwise preserves the source order, which for fields and operators is the
// order the catalog deliberately chose.
func rankSuggestions(in []QuerySuggestion, typed string) []QuerySuggestion {
	if typed == "" || len(in) < 2 {
		return capSuggestions(in)
	}
	t := foldASCII(typed)
	sort.SliceStable(in, func(i, j int) bool {
		pi := strings.HasPrefix(foldASCII(in[i].Label), t)
		pj := strings.HasPrefix(foldASCII(in[j].Label), t)
		return pi && !pj
	})
	return capSuggestions(in)
}

func capSuggestions(in []QuerySuggestion) []QuerySuggestion {
	if len(in) > completionValueLimit {
		return in[:completionValueLimit]
	}
	return in
}

// noteCountLabel and subcategoryCountLabel write the dimmed right-hand column
// of a value suggestion. They spell the singular out because "1 notes" in a
// popup is the kind of small wrongness that makes a tool feel unfinished.
func noteCountLabel(n int) string {
	if n == 1 {
		return "1 note"
	}
	return itoa(int64(n)) + " notes"
}

func subcategoryCountLabel(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 subcategory"
	}
	return itoa(int64(n)) + " subcategories"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
