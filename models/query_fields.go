package models

import "sort"

// query_fields.go is the catalog of the advanced-search language: the one
// list of note attributes a query may name, what type each one carries, and
// which operators are legal against it.
//
// Everything else in the query stack reads from here rather than from its own
// copy of the field names:
//
//	                    ┌──────────────────┐
//	                    │  query_fields.go │  ← the catalog (this file)
//	                    └────────┬─────────┘
//	          ┌──────────────────┼──────────────────┐
//	   parser (validate)   evaluator (read)   completer (suggest)
//	   query_parse.go      query_eval.go      query_complete.go
//	                              │
//	                       /notes/query/schema  → the web help panel
//
// The point of the single table is that "ensure all attributes are queryable"
// stays true as the note model grows: a column added to models.Note is a row
// added here, and the parser, the evaluator, the autocompleter and the
// documented schema all pick it up together. A field that exists here but has
// no arm in fieldValues fails loudly in the evaluator's default case rather
// than silently matching nothing.

// FieldKind is the value type of a queryable attribute. It decides which
// operators the parser accepts, how a literal on the right-hand side is
// coerced, and what the completer offers as values.
type FieldKind string

const (
	KindString FieldKind = "string"
	KindNumber FieldKind = "number"
	KindBool   FieldKind = "bool"
	KindTime   FieldKind = "time"
)

// QueryField describes one queryable note attribute.
type QueryField struct {
	Name string    `json:"name"`
	Kind FieldKind `json:"kind"`

	// Multi marks an attribute a note carries zero or more of — tags,
	// categories, subcategories. It changes the meaning of a comparison, not
	// its syntax:
	//
	//	category = 'airflow'       ANY of the note's categories is airflow
	//	category != 'airflow'      NONE of them is airflow
	//
	// That is, a positive operator is existential and a negated one is
	// universal, which is exactly "NOT (existential)" — so negation stays a
	// single rule applied on top of the positive test (see predicate.eval).
	// SQL would give the second form the existential reading too ("has some
	// category that is not airflow"), which is never what someone filtering a
	// note list means.
	Multi bool `json:"multi"`

	// Nullable marks an attribute that can be absent, and therefore answers
	// IS NULL / IS NOT NULL meaningfully. Absent values are modeled as an
	// empty value list, so a NULL never satisfies a comparison — see the
	// three-valued-logic note in query_eval.go.
	Nullable bool `json:"nullable"`

	// Aliases are alternate spellings accepted by the parser. They exist
	// because the storage name and the word a person reaches for differ
	// ("is_private" vs "private", "category" vs "cat"), and guessing wrong
	// should not be an error the user has to look up.
	Aliases []string `json:"aliases,omitempty"`

	// Derived marks a field that is not a notes column: it is assembled from
	// the note_categories link table (category, subcategory) or split out of
	// a column (tags), or spans several columns (text). Derived fields are
	// what make the loader decide whether it needs the category mappings at
	// all — see Query.NeedsCategories.
	Derived bool `json:"derived"`

	Description string `json:"description"`
	Example     string `json:"example"`
}

// queryFields is the catalog. Order is the order the schema endpoint and the
// completer present them in: identity, the text a person writes, the flags,
// the classification, then the bookkeeping columns nobody types first.
var queryFields = []QueryField{
	{
		Name: "id", Kind: KindNumber,
		Description: "Numeric primary key, unique across both databases.",
		Example:     "id > 100",
	},
	{
		Name: "guid", Kind: KindString,
		Description: "Stable external identifier, the anchor for sync and Markdown round-trips.",
		Example:     "guid = '9f3c…'",
	},
	{
		Name: "title", Kind: KindString,
		Description: "Note title.",
		Example:     "title LIKE 'Deploy%'",
	},
	{
		Name: "description", Kind: KindString, Nullable: true,
		Aliases:     []string{"desc"},
		Description: "Optional one-line summary.",
		Example:     "description IS NOT NULL",
	},
	{
		Name: "body", Kind: KindString, Nullable: true,
		Aliases:     []string{"content"},
		Description: "The note's Markdown content.",
		Example:     "body CONTAINS 'airflow'",
	},
	{
		Name: "tags", Kind: KindString, Multi: true, Derived: true,
		Aliases:     []string{"tag"},
		Description: "Comma-separated tags, compared one tag at a time.",
		Example:     "tags = 'capture'",
	},
	{
		Name: "is_private", Kind: KindBool,
		Aliases:     []string{"private"},
		Description: "True for notes held in the encrypted database.",
		Example:     "is_private = false",
	},
	{
		Name: "is_flagged", Kind: KindBool,
		Aliases:     []string{"flagged", "flag"},
		Description: "The follow-up flag.",
		Example:     "is_flagged",
	},
	{
		Name: "category", Kind: KindString, Multi: true, Derived: true,
		Aliases:     []string{"categories", "cat"},
		Description: "Names of the categories the note is filed under.",
		Example:     "category = 'airflow'",
	},
	{
		Name: "subcategory", Kind: KindString, Multi: true, Derived: true,
		Aliases:     []string{"subcategories", "subcat", "sub"},
		Description: "Subcategories selected on this note's category links.",
		Example:     "subcategory = 'conversion'",
	},
	{
		Name: "category_id", Kind: KindNumber, Multi: true, Derived: true,
		Aliases:     []string{"cat_id"},
		Description: "Numeric ids of the linked categories.",
		Example:     "category_id IN (3, 7)",
	},
	{
		Name: "created_at", Kind: KindTime,
		Aliases:     []string{"created"},
		Description: "When the note row was first written.",
		Example:     "created_at > -30d",
	},
	{
		Name: "updated_at", Kind: KindTime,
		Aliases:     []string{"updated", "modified"},
		Description: "When the note was last written, by anyone including sync.",
		Example:     "updated_at >= today",
	},
	{
		Name: "authored_at", Kind: KindTime, Nullable: true,
		Aliases:     []string{"authored"},
		Description: "Last human edit — the timestamp sync resolves conflicts on.",
		Example:     "authored_at < '2026-01-01'",
	},
	{
		Name: "synced_at", Kind: KindTime, Nullable: true,
		Aliases:     []string{"synced"},
		Description: "Last time the note reached a peer. NULL means never synced.",
		Example:     "synced_at IS NULL",
	},
	{
		Name: "deleted_at", Kind: KindTime, Nullable: true,
		Aliases: []string{"deleted"},
		Description: "Soft-delete timestamp. Naming this field is what opts a query " +
			"into seeing deleted notes at all.",
		Example: "deleted_at IS NOT NULL",
	},
	{
		Name: "version", Kind: KindNumber,
		Description: "Optimistic-concurrency counter, bumped by every write.",
		Example:     "version > 1",
	},
	{
		Name: "created_by", Kind: KindString,
		Aliases:     []string{"creator", "owner"},
		Description: "GUID of the owning user. 'me' resolves to the current user.",
		Example:     "created_by = me",
	},
	{
		Name: "updated_by", Kind: KindString, Nullable: true,
		Aliases:     []string{"editor"},
		Description: "GUID of whoever wrote the note last.",
		Example:     "updated_by = me",
	},
	{
		Name: "text", Kind: KindString, Multi: true, Derived: true,
		Aliases: []string{"any", "anything"},
		Description: "Pseudo-field spanning title, description, body, tags, categories " +
			"and subcategories. A bare quoted string is shorthand for text CONTAINS it.",
		Example: "'conversion' AND category = 'airflow'",
	},
}

// fieldIndex resolves a name or alias to its catalog entry. Built once at
// package init because it is read on every token of every query and every
// keystroke of the completer.
var fieldIndex = func() map[string]*QueryField {
	ix := make(map[string]*QueryField, len(queryFields)*2)
	for i := range queryFields {
		f := &queryFields[i]
		ix[f.Name] = f
		for _, a := range f.Aliases {
			ix[a] = f
		}
	}
	return ix
}()

// LookupQueryField resolves a field name or alias, case-insensitively.
// The returned pointer is into the package-level catalog and must not be
// mutated.
func LookupQueryField(name string) (*QueryField, bool) {
	f, ok := fieldIndex[foldASCII(name)]
	return f, ok
}

// QueryFields returns the catalog, for the schema endpoint and the TUI's help
// panel. The slice is a copy so a caller cannot reorder the shared catalog,
// but the entries are values and safe to hand out.
func QueryFields() []QueryField {
	out := make([]QueryField, len(queryFields))
	copy(out, queryFields)
	return out
}

// QueryFieldNames lists every accepted spelling — canonical names and aliases
// together, sorted. The completer ranks over this; error messages suggest from
// it.
func QueryFieldNames() []string {
	names := make([]string, 0, len(fieldIndex))
	for n := range fieldIndex {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ---------------------------------------------------------------------------
// Operators
// ---------------------------------------------------------------------------

// CompareOp is the comparison a predicate performs, after the parser has
// desugared the surface syntax. The negated spellings do not appear here:
// `!=`, `NOT LIKE`, `IS NOT NULL` and friends all parse to their positive
// operator with predicate.Negate set, so the evaluator has one rule for
// negation instead of one per operator.
type CompareOp string

const (
	OpEq       CompareOp = "="
	OpLt       CompareOp = "<"
	OpLte      CompareOp = "<="
	OpGt       CompareOp = ">"
	OpGte      CompareOp = ">="
	OpLike     CompareOp = "LIKE"     // SQL wildcards: % any run, _ one character
	OpContains CompareOp = "CONTAINS" // substring, i.e. LIKE '%x%' without the punctuation
	OpMatches  CompareOp = "MATCHES"  // Go regular expression (RE2)
	OpIn       CompareOp = "IN"
	OpBetween  CompareOp = "BETWEEN"
	OpNull     CompareOp = "IS NULL"
	OpEmpty    CompareOp = "IS EMPTY" // NULL or the empty string / no elements
	OpTruthy   CompareOp = "TRUTHY"   // a bare boolean field used as a predicate
)

// QueryOperator documents one surface operator for the schema endpoint and
// gives the completer the set to offer after a field.
type QueryOperator struct {
	// Token is what the user types. Multi-word operators are spelled with
	// single spaces, which is how the completer inserts them.
	Token string `json:"token"`
	// Kinds is the field kinds the operator applies to; empty means all.
	Kinds       []FieldKind `json:"kinds,omitempty"`
	Description string      `json:"description"`
}

// queryOperators is the surface syntax, in the order the completer offers it:
// equality first because it is what nearly every query starts with.
var queryOperators = []QueryOperator{
	{Token: "=", Description: "Equal. Strings compare case-insensitively."},
	{Token: "!=", Description: "Not equal. On a multi-valued field: none of the values match."},
	{Token: "<", Kinds: []FieldKind{KindNumber, KindTime, KindString}, Description: "Less than."},
	{Token: "<=", Kinds: []FieldKind{KindNumber, KindTime, KindString}, Description: "Less than or equal."},
	{Token: ">", Kinds: []FieldKind{KindNumber, KindTime, KindString}, Description: "Greater than."},
	{Token: ">=", Kinds: []FieldKind{KindNumber, KindTime, KindString}, Description: "Greater than or equal."},
	{Token: "CONTAINS", Kinds: []FieldKind{KindString}, Description: "Case-insensitive substring."},
	{Token: "LIKE", Kinds: []FieldKind{KindString}, Description: "SQL pattern: % matches any run, _ one character."},
	{Token: "NOT LIKE", Kinds: []FieldKind{KindString}, Description: "Negated LIKE."},
	{Token: "MATCHES", Kinds: []FieldKind{KindString}, Description: "Regular expression (RE2). Case-sensitive unless the pattern says otherwise."},
	{Token: "IN", Description: "Membership in a parenthesised list."},
	{Token: "NOT IN", Description: "Absence from a parenthesised list."},
	{Token: "BETWEEN", Kinds: []FieldKind{KindNumber, KindTime, KindString}, Description: "Inclusive range: BETWEEN a AND b."},
	{Token: "IS NULL", Description: "The attribute has no value."},
	{Token: "IS NOT NULL", Description: "The attribute has a value."},
	{Token: "IS EMPTY", Description: "No value, or an empty one."},
	{Token: "IS NOT EMPTY", Description: "Has a non-empty value."},
}

// QueryOperators returns the operator table for the schema endpoint.
func QueryOperators() []QueryOperator {
	out := make([]QueryOperator, len(queryOperators))
	copy(out, queryOperators)
	return out
}

// operatorsForField narrows the table to what makes sense after a given
// field, so the completer never offers `title BETWEEN` a boolean or `LIKE`
// against a timestamp.
//
// A nullable-or-multi test gates the NULL/EMPTY family: `IS NULL` against a
// NOT NULL column is not an error worth failing a query over, but it is
// noise in a completion list, so it is filtered here and still accepted by
// the parser.
func operatorsForField(f *QueryField) []QueryOperator {
	var out []QueryOperator
	for _, op := range queryOperators {
		switch op.Token {
		case "IS NULL", "IS NOT NULL":
			if !f.Nullable && !f.Multi {
				continue
			}
		case "IS EMPTY", "IS NOT EMPTY":
			if f.Kind != KindString {
				continue
			}
		}
		if len(op.Kinds) > 0 && !kindIn(f.Kind, op.Kinds) {
			continue
		}
		out = append(out, op)
	}
	return out
}

func kindIn(k FieldKind, set []FieldKind) bool {
	for _, s := range set {
		if s == k {
			return true
		}
	}
	return false
}

// QueryExamples are the worked examples shown in the UI's help panel. They
// double as a smoke test: query_test.go parses every one of them, so an
// example that stops being valid syntax breaks the build rather than the
// user's first impression.
var QueryExamples = []string{
	"category = 'airflow' AND subcategory = 'conversion'",
	"title CONTAINS 'deploy' AND is_flagged",
	"tags IN ('capture', 'summary') AND updated_at > -7d",
	"is_private = true AND synced_at IS NULL",
	"body MATCHES '(?i)panic|fatal' ORDER BY updated_at DESC LIMIT 20",
	"'conversion' AND NOT category = 'archive'",
	"created_at BETWEEN '2026-01-01' AND '2026-06-30'",
	"description IS EMPTY AND body IS NOT EMPTY",
}

// ---------------------------------------------------------------------------
// Schema document
// ---------------------------------------------------------------------------

// QueryKeyword documents a non-operator word of the language for the help
// panel: the conjunctions, the trailing clauses, and the literals that are
// spelled as words.
type QueryKeyword struct {
	Word        string `json:"word"`
	Description string `json:"description"`
}

// QuerySchemaDoc is everything a UI needs to explain the language without
// hard-coding any of it. Served by GET /api/v1/notes/query/schema and rendered
// by the web help panel and the TUI's field list, so the two can never fall
// out of step with what the parser accepts.
type QuerySchemaDoc struct {
	Fields    []QueryField    `json:"fields"`
	Operators []QueryOperator `json:"operators"`
	Keywords  []QueryKeyword  `json:"keywords"`
	Examples  []string        `json:"examples"`
	// Semantics are the deliberate departures from SQL. They are shipped
	// with the schema rather than written into a UI because they are the
	// answers to "why did that match?" and belong wherever the language is
	// being explained.
	Semantics []string `json:"semantics"`
}

// QuerySchema builds the schema document.
func QuerySchema() QuerySchemaDoc {
	return QuerySchemaDoc{
		Fields:    QueryFields(),
		Operators: QueryOperators(),
		Keywords: []QueryKeyword{
			{Word: "AND", Description: "Both conditions must hold."},
			{Word: "OR", Description: "Either condition may hold."},
			{Word: "NOT", Description: "Negate the condition that follows."},
			{Word: "WHERE", Description: "Optional; a query may start straight with a condition."},
			{Word: "ORDER BY", Description: "Order the results: ORDER BY updated_at DESC."},
			{Word: "LIMIT / OFFSET", Description: "Cap and page the results."},
			{Word: "me", Description: "The user running the query, for created_by / updated_by."},
			{Word: "now / today / yesterday / tomorrow", Description: "Named points in time."},
			{Word: "-7d, -24h, -6mo", Description: "An offset from now. Units: s, min, h, d, w, mo, y."},
			{Word: "true / false", Description: "Boolean literals; yes/no and on/off are accepted too."},
		},
		Examples: QueryExamples,
		Semantics: []string{
			"String comparison ignores case: category = 'Airflow' matches airflow.",
			"A bare quoted string searches title, description, body, tags, categories and subcategories.",
			"On a multi-valued field (tags, category, subcategory) an operator asks whether ANY value matches, and its negation asks whether NONE does — so category != 'archive' means \"not filed under archive\".",
			"A missing value is absence, not SQL's unknown: description != 'x' is true for a note with no description. Ask about absence itself with IS NULL.",
			"A date literal covers the precision it was written at: created_at = '2026-03-04' matches the whole day, and '2026-03' the whole month.",
			"Soft-deleted notes are excluded unless the query mentions deleted_at.",
			"MATCHES takes a Go regular expression and is case-sensitive unless the pattern says otherwise, e.g. '(?i)panic'.",
		},
	}
}
