package pages

import (
	"github.com/rohanthewiz/element"
	"go_notes_web/models"
	"go_notes_web/views"
)

// RenderSearchPage renders the search page
func RenderSearchPage(query string, results []models.Note, userGUID string) string {
	return views.BaseLayout("", "", views.PageWithHeader{
		UserGUID: userGUID,
		ActivePage: "search",
		Content: SearchContent{
			Query: query,
			Results: results,
		},
	})
}

// SearchContent component for search page
type SearchContent struct {
	Query   string
	Results []models.Note
}

func (sc SearchContent) Render(b *element.Builder) (x any) {
	b.DivClass("search-page").R(
		// Search header
		b.DivClass("search-header").R(
			b.H2().T("Search Notes"),
		),
		
		// Advanced search form
		b.Form("id", "advanced-search",
			"hx-get", "/api/search",
			"hx-target", "#search-results",
			"hx-trigger", "submit",
			"class", "search-form-advanced").R(
			
			// Search input
			b.DivClass("form-group").R(
				b.Label("for", "search-query").T("Search Query"),
				b.Input("type", "text",
					"id", "search-query",
					"name", "q",
					"class", "form-control",
					"placeholder", "Enter search terms...",
					"value", sc.Query,
					"autofocus", "autofocus"),
			),
			
			// Search type selector
			b.DivClass("form-group").R(
				b.Label("for", "search-type").T("Search In"),
				b.Select("id", "search-type",
					"name", "type",
					"class", "form-control").R(
					b.Option("value", "all").T("All Fields"),
					b.Option("value", "title").T("Title Only"),
					b.Option("value", "body").T("Body Only"),
					b.Option("value", "tags").T("Tags Only"),
				),
			),
			
			// Date range filters
			b.DivClass("form-row").R(
				b.DivClass("form-group col").R(
					b.Label("for", "date-from").T("From Date"),
					b.Input("type", "date",
						"id", "date-from",
						"name", "from",
						"class", "form-control"),
				),
				b.DivClass("form-group col").R(
					b.Label("for", "date-to").T("To Date"),
					b.Input("type", "date",
						"id", "date-to",
						"name", "to",
						"class", "form-control"),
				),
			),
			
			// Search button
			b.DivClass("form-actions").R(
				b.Button("type", "submit",
					"class", "btn btn-primary").T("Search"),
				b.Button("type", "reset",
					"class", "btn btn-secondary").T("Clear"),
			),
		),
		
		// Search results
		b.Div("id", "search-results", "class", "search-results").R(
			sc.renderResults(b),
		),
	)
	return
}

func (sc SearchContent) renderResults(b *element.Builder) (x any) {
	if sc.Query == "" {
		b.DivClass("search-info").R(
			b.P().T("Enter a search query to find notes"),
		)
		return
	}
	
	if len(sc.Results) == 0 {
		b.DivClass("no-results").R(
			b.H3().T("No results found"),
			b.P().F("No notes matching \"%s\" were found.", sc.Query),
			b.P().T("Try different search terms or check your filters."),
		)
		return
	}
	
	// Results header
	b.DivClass("results-header").R(
		b.H3().F("Found %d results for \"%s\"", len(sc.Results), sc.Query),
	)
	
	// Results list
	b.DivClass("results-list").R(
		element.ForEach(sc.Results, func(note models.Note) {
			b.Wrap(func() {
				element.RenderComponents(b, RenderNoteCard(note))
			})
		}),
	)
	
	return
}