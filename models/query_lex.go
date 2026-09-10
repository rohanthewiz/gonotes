package models

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// query_lex.go turns advanced-search source text into tokens.
//
// It is a hand-written scanner rather than a generated one for two reasons
// that both come from the autocompleter rather than the parser. First, the
// completer re-lexes the text to the left of the cursor on every keystroke and
// needs each token's byte offsets to know what range a suggestion replaces —
// so positions are carried on every token, not just on errors. Second, the
// completer must be able to lex a half-typed query: an unterminated string at
// the very end of the input is a normal state there ("category = 'air"), so
// the scanner reports it as a token flagged Unterminated instead of refusing
// to produce anything. The parser rejects that flag; the completer completes
// it.

// tokenKind classifies a token. Keywords are not separated out here — they
// arrive as tokIdent and the parser decides whether a bare word is a keyword,
// a field name or an unquoted value from its position in the grammar. That
// keeps a note tagged "and" or a category named "in" from being unusable: it
// is only a keyword where a keyword can appear.
type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString   // quoted literal
	tokNumber   // 42, 3.5
	tokDuration // -7d, +3h — a time offset from now
	tokOp       // = != <> < <= > >= ~ !~
	tokLParen
	tokRParen
	tokComma
	tokStar // * , accepted only inside a tolerated SELECT * prefix
)

func (k tokenKind) String() string {
	switch k {
	case tokEOF:
		return "end of query"
	case tokIdent:
		return "name"
	case tokString:
		return "quoted value"
	case tokNumber:
		return "number"
	case tokDuration:
		return "time offset"
	case tokOp:
		return "operator"
	case tokLParen:
		return "'('"
	case tokRParen:
		return "')'"
	case tokComma:
		return "','"
	case tokStar:
		return "'*'"
	}
	return "token"
}

// token is one lexeme plus everything a caller needs to point at it.
type token struct {
	Kind tokenKind
	// Text is the raw source slice, quotes included. Error messages and the
	// completer's "what am I replacing" both work from this.
	Text string
	// Val is the decoded value: a string literal with its quotes removed and
	// doubled quotes collapsed, an identifier lowercased for matching, or the
	// raw text for everything else.
	Val string
	// Pos and End are byte offsets into the source, half-open [Pos, End).
	Pos, End int
	// Quote is the quote character of a string token, 0 otherwise. The
	// completer needs it to insert a replacement in the same style the user
	// started typing.
	Quote byte
	// Unterminated marks a string literal that ran to the end of the input.
	// Only the completer tolerates it.
	Unterminated bool
}

// QueryError is a syntax or validation failure with a position, so a UI can
// underline the offending run rather than just print a sentence.
//
// Pos/Len are byte offsets into the query text. Hint carries the "did you
// mean" for an unknown field, which is the single most common mistake in a
// language whose whole vocabulary is a fixed list of column names.
type QueryError struct {
	Msg  string `json:"message"`
	Pos  int    `json:"position"`
	Len  int    `json:"length"`
	Hint string `json:"hint,omitempty"`
}

func (e *QueryError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("%s at position %d (%s)", e.Msg, e.Pos, e.Hint)
	}
	return fmt.Sprintf("%s at position %d", e.Msg, e.Pos)
}

// queryErrorf builds a positioned error.
func queryErrorf(pos, length int, format string, args ...any) *QueryError {
	return &QueryError{Msg: fmt.Sprintf(format, args...), Pos: pos, Len: length}
}

// durationUnits maps a duration suffix to its length in seconds. Months and
// years are the calendar approximations people mean when they type "-6mo" in
// a search box; nothing here is doing accounting.
//
// Order matters at the scanner: the longest matching suffix wins, so "mo" is
// tried before "m" and "min" before "m". Keeping that ordering in one sorted
// slice rather than in the scanner's control flow is what stops "-6mo" from
// lexing as six minutes followed by a stray "o".
var durationUnits = []struct {
	suffix  string
	seconds float64
}{
	{"mo", 30 * 24 * 3600},
	{"min", 60},
	{"ms", 0.001},
	{"s", 1},
	{"m", 60},
	{"h", 3600},
	{"d", 24 * 3600},
	{"w", 7 * 24 * 3600},
	{"y", 365 * 24 * 3600},
}

// lexQuery scans the whole input. It returns tokens up to the first hard
// error; an unterminated trailing string is not a hard error (see the file
// comment) and comes back flagged.
func lexQuery(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]

		// Whitespace.
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}

		// Quoted string. Both quote styles are accepted: SQL reserves double
		// quotes for identifiers, but this language has no quoted identifiers
		// and a user reaching for "airflow" means the value.
		if c == '\'' || c == '"' {
			tok, next, err := lexString(src, i)
			if err != nil {
				return toks, err
			}
			toks = append(toks, tok)
			i = next
			continue
		}

		// Punctuation.
		switch c {
		case '(':
			toks = append(toks, token{Kind: tokLParen, Text: "(", Val: "(", Pos: i, End: i + 1})
			i++
			continue
		case ')':
			toks = append(toks, token{Kind: tokRParen, Text: ")", Val: ")", Pos: i, End: i + 1})
			i++
			continue
		case ',':
			toks = append(toks, token{Kind: tokComma, Text: ",", Val: ",", Pos: i, End: i + 1})
			i++
			continue
		case '*':
			toks = append(toks, token{Kind: tokStar, Text: "*", Val: "*", Pos: i, End: i + 1})
			i++
			continue
		}

		// Comparison operators, longest match first so "<=" never lexes as
		// "<" followed by an unexpected "=".
		if op, n := lexOperator(src, i); n > 0 {
			toks = append(toks, token{Kind: tokOp, Text: op, Val: op, Pos: i, End: i + n})
			i += n
			continue
		}

		// A number or a signed duration. Both start with a digit or a sign,
		// and only the suffix tells them apart, so one scanner handles both.
		if isDigit(c) || ((c == '-' || c == '+') && i+1 < len(src) && isDigit(src[i+1])) {
			tok, next, err := lexNumberOrDuration(src, i)
			if err != nil {
				return toks, err
			}
			toks = append(toks, tok)
			i = next
			continue
		}

		// Bare word: a field name, a keyword, or an unquoted value.
		if isIdentStart(c) {
			j := i + 1
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			text := src[i:j]
			// A table qualifier is tolerated and dropped so SQL pasted from
			// elsewhere ("notes.title") resolves to the field it names. Only
			// "notes." qualifies — anything else stays part of the name and
			// fails field lookup with a position, which is the honest answer.
			val := foldASCII(text)
			if rest, ok := strings.CutPrefix(val, "notes."); ok {
				val = rest
			}
			toks = append(toks, token{Kind: tokIdent, Text: text, Val: val, Pos: i, End: j})
			i = j
			continue
		}

		r, size := utf8.DecodeRuneInString(src[i:])
		return toks, queryErrorf(i, size, "unexpected character %q", r)
	}

	toks = append(toks, token{Kind: tokEOF, Pos: len(src), End: len(src)})
	return toks, nil
}

// lexString scans a quoted literal starting at the quote character. A doubled
// quote inside the literal is an escaped quote, SQL-style: two apostrophes
// in a row stand for one.
// Backslashes are deliberately NOT escapes: regular expressions are a
// first-class value here (MATCHES '\d+') and doubling is the only escape a
// pattern never needs.
func lexString(src string, start int) (token, int, error) {
	quote := src[start]
	var sb strings.Builder
	i := start + 1
	for i < len(src) {
		if src[i] == quote {
			if i+1 < len(src) && src[i+1] == quote { // '' -> literal quote
				sb.WriteByte(quote)
				i += 2
				continue
			}
			return token{
				Kind: tokString, Text: src[start : i+1], Val: sb.String(),
				Pos: start, End: i + 1, Quote: quote,
			}, i + 1, nil
		}
		sb.WriteByte(src[i])
		i++
	}
	// Ran off the end. Reported as a token rather than an error so the
	// completer can still offer values for a half-typed literal; the parser
	// turns the flag into a syntax error.
	return token{
		Kind: tokString, Text: src[start:], Val: sb.String(),
		Pos: start, End: len(src), Quote: quote, Unterminated: true,
	}, len(src), nil
}

// lexOperator matches a comparison operator at i, returning it and its
// length. Two-character forms are tested first.
func lexOperator(src string, i int) (string, int) {
	if i+1 < len(src) {
		switch src[i : i+2] {
		case "!=", "<>", "<=", ">=", "==", "!~":
			return src[i : i+2], 2
		}
	}
	switch src[i] {
	case '=', '<', '>', '~':
		return src[i : i+1], 1
	}
	return "", 0
}

// lexNumberOrDuration scans a numeric literal and decides, from the suffix,
// whether it is a plain number or a relative time offset.
//
//	-7          number
//	-7d         duration: seven days before now
//	7dx         error, positioned on the suffix
//
// A bare number and a duration cannot be told apart until the suffix is read,
// which is why this is one function: backtracking a token the parser already
// received is worse than a few extra bytes of lookahead here.
func lexNumberOrDuration(src string, start int) (token, int, error) {
	i := start
	if src[i] == '-' || src[i] == '+' {
		i++
	}
	for i < len(src) && isDigit(src[i]) {
		i++
	}
	if i < len(src) && src[i] == '.' && i+1 < len(src) && isDigit(src[i+1]) {
		i++
		for i < len(src) && isDigit(src[i]) {
			i++
		}
	}
	numEnd := i

	// Suffix letters, if any, decide the kind.
	for i < len(src) && isIdentPart(src[i]) {
		i++
	}
	if i == numEnd {
		return token{Kind: tokNumber, Text: src[start:i], Val: src[start:i], Pos: start, End: i}, i, nil
	}

	suffix := foldASCII(src[numEnd:i])
	for _, u := range durationUnits {
		if suffix == u.suffix {
			return token{Kind: tokDuration, Text: src[start:i], Val: src[start:i], Pos: start, End: i}, i, nil
		}
	}
	return token{}, i, queryErrorf(numEnd, i-numEnd,
		"unknown time unit %q — use s, min, h, d, w, mo or y", src[numEnd:i])
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= utf8.RuneSelf
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || isDigit(c) || c == '.'
}

// foldASCII lowercases ASCII letters and leaves everything else alone. It is
// the case folding used for keywords, field names and string equality.
//
// strings.ToLower would do the same for ASCII but also applies Unicode
// special-casing, which for a note titled with a Turkish dotless ı or a
// Greek final sigma changes which strings compare equal depending on the
// user's data rather than on the query. Query matching should be predictable,
// so the fold stops at ASCII — the alphabet every field name and keyword in
// this language is written in.
func foldASCII(s string) string {
	hasUpper := false
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return s
	}
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
