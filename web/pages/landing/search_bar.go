package landing

import "github.com/rohanthewiz/element"

// SearchBar is the full-width search/filter bar positioned between the toolbar
// and the main content area. It replaces the old narrow search input that was
// squeezed into the 220px filter panel. The bar supports three combinable
// search dimensions: text/ID search, category filter, and subcategory chips.
//
// Layout: [🔍 input] [Category ▾] [subcat chips...] [Clear]
//
// JavaScript populates the category <select> and subcategory chips dynamically
// from state.categories and state.noteCategoryMap.
type SearchBar struct{}

// Render implements element.Component — builds the search bar HTML.
func (s SearchBar) Render(b *element.Builder) (x any) {
	b.DivClass("search-bar", "id", "search-bar").R(
		// Text/ID search input — reuses id="search-input" so the "/" keyboard
		// shortcut keeps working without any JS changes to the key handler.
		b.DivClass("search-bar-input-wrapper").R(
			b.SpanClass("search-bar-icon").T("🔍"),
			b.Input("type", "text", "class", "search-bar-input", "id", "search-input",
				"placeholder", "Search by text or ID...",
				"oninput", "app.handleSearch(this.value)",
				"autocomplete", "off"),
		),

		// Regex toggle — switches search between substring and regular expression mode
		b.Button("class", "btn btn-secondary search-bar-regex-toggle", "id", "regex-toggle",
			"onclick", "app.toggleRegex()",
			"title", "Toggle regular expression search").T(".*"),

		// Category dropdown — populated from state.categories by JS on init
		b.Select("class", "search-bar-select", "id", "search-category-select",
			"onchange", "app.handleCategoryFilter(this.value)").R(
			b.Option("value", "").T("All Categories"),
		),

		// Subcategory chips container — hidden until a category with subcats is chosen
		b.Div("class", "search-bar-subcats", "id", "search-subcats-container").R(),

		// Clear button — resets all search bar state (text, category, subcats)
		b.ButtonClass("btn btn-secondary search-bar-clear", "onclick", "app.clearSearchBar()",
			"title", "Clear search and filters").T("Clear"),
	)
	return
}
