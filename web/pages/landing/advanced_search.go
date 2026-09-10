package landing

import "github.com/rohanthewiz/element"

// AdvancedSearchBar is the SQL query bar: a second, expert-mode search row
// that sits between the toolbar and the panes and is hidden until the toolbar's
// { } button opens it.
//
//	┌───────────────────────────────────────────────────────────────────┐
//	│ SQL │ category = 'airflow' AND subcategory = 'conversion' │ Run ? ×│
//	│     └─ suggestion popup, absolutely positioned under the input ─┘  │
//	│ 12 notes · of 412 scanned · 2.1 ms                                │
//	└───────────────────────────────────────────────────────────────────┘
//
// Everything inside is driven by advanced_search.js, which in turn is driven
// by the server: the field list, the operators, the examples and the
// completions all arrive from /api/v1/notes/query{,/complete,/schema}. Nothing
// about the note schema is written into this markup, so a column added to the
// note model becomes queryable — and completable — without touching the page.
//
// It is a separate row rather than a mode of the existing search input on
// purpose. The two languages are different (a substring versus an expression
// over every attribute), and a single box that silently changed meaning is the
// kind of thing that makes a user distrust both. Keeping them apart also lets
// the plain search keep working exactly as it always has.
type AdvancedSearchBar struct{}

// Render implements the element.Component interface.
func (a AdvancedSearchBar) Render(b *element.Builder) any {
	// hidden is set on the server so the row never flashes on a page load
	// before the script decides whether it was left open.
	b.Div("class", "advanced-search-bar", "id", "advanced-search-bar", "hidden", "hidden").R(
		b.DivClass("aq-row").R(
			b.SpanClass("aq-badge", "title", "Advanced query").T("SQL"),

			// The input and its popup share a positioned wrapper so the popup
			// can be absolutely placed under the input without depending on
			// the width of the buttons beside it.
			b.DivClass("aq-input-wrap").R(
				b.Input("type", "text", "class", "aq-input", "id", "advanced-query-input",
					"placeholder", "category = 'airflow' AND subcategory = 'conversion'",
					"autocomplete", "off", "autocapitalize", "off", "spellcheck", "false",
					"aria-label", "Advanced note query",
					"aria-autocomplete", "list", "aria-expanded", "false"),
				// The completion popup. Populated and shown by the script;
				// empty and hidden in the served HTML.
				b.Div("class", "aq-popup", "id", "advanced-query-popup", "hidden", "hidden").R(),
			),

			b.Button("class", "btn btn-primary btn-sm", "id", "advanced-query-run",
				"onclick", "app.runAdvancedQuery()", "title", "Run the query (Enter)").T("Run"),
			b.Button("class", "btn btn-secondary btn-sm", "id", "advanced-query-help-toggle",
				"onclick", "app.toggleAdvancedHelp()",
				"title", "Show every queryable attribute", "aria-label", "Query help").T("?"),
			b.Button("class", "btn btn-secondary btn-sm aq-close", "id", "advanced-query-close",
				"onclick", "app.closeAdvancedSearch()",
				"title", "Close the query bar and clear the query (Esc)",
				"aria-label", "Close advanced query").T("×"),
		),

		// The status line carries the result count, the timing and the
		// server's normalized reading of the query; the error line replaces it
		// when a query will not parse.
		b.Div("class", "aq-status", "id", "advanced-query-status").R(),
		b.Div("class", "aq-error", "id", "advanced-query-error", "hidden", "hidden", "role", "alert").R(),

		// The help panel: the whole field catalog, rendered from the schema
		// endpoint. Collapsed until asked for — it is a reference, not a
		// permanent fixture.
		b.Div("class", "aq-help", "id", "advanced-query-help", "hidden", "hidden").R(),
	)
	return nil
}
