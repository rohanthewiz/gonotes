// Package pages contains the page components for the application.
// This file defines the Home page and its sub-components.
package pages

import (
	"gonotes/web/pages/comps"
	"gonotes/web/pages/shared" // Local package with shared components (Banner, Footer, Page)

	"github.com/rohanthewiz/element" // Third-party HTML builder library
)

// Home is defined in home_page_comps.go - this creates an instance of it
var HomePage = Home{
	// EMBEDDED FIELD: Page is embedded (no field name, just the type)
	// This gives Home access to all Page fields and methods
	// We initialize it with a nested struct literal
	Page: shared.Page{Title: "My Website"},

	// Regular field: Heading is a specific field of the Home struct
	// This is different from Page.Title - Heading is used for page content
	Heading: "Home Page",
}

// STRUCT DEFINITION with EMBEDDING
// Home represents the home page structure and data
type Home struct {
	// EMBEDDED STRUCT: shared.Page is embedded (no field name)
	// This is the MIXIN PATTERN - Home "inherits" all Page fields and methods
	// Home can now call h.Banner(), h.Footer(), and access h.Title
	shared.Page

	// Additional field specific to Home page
	// This demonstrates extending the base Page with page-specific data
	Heading string
}

// METHOD with VALUE RECEIVER and NAMED RETURN VALUE
// (h Home) - value receiver, method belongs to Home type
// (out string) - NAMED RETURN VALUE: the return variable is declared in the signature
//
//	This creates a variable 'out' that's automatically returned (though we don't use it here)
//	Named returns make code self-documenting and enable "naked returns"
func (h Home) Render() (out string) {
	// Create a new HTML builder instance
	// element.NewBuilder() returns a pointer to a Builder
	b := element.NewBuilder()

	// METHOD CHAINING: Build the HTML structure
	// b.Body() creates a <body> tag with inline CSS
	// .R() is a VARIADIC METHOD - accepts any number of arguments
	b.Body("style", "background-color:tan").R(
		// FUNCTION CALL: element.RenderComponents is a helper function
		// It takes a builder and multiple components, renders each component
		// This demonstrates the COMPOSITE PATTERN - combining multiple components
		element.RenderComponents(b,
			// METHOD CALL on EMBEDDED FIELD: h.Banner() works because Page is embedded
			// This is equivalent to h.Page.Banner(), but Go allows the shorthand
			h.Banner(), // Returns Banner struct from the embedded Page
			comps.Heading{Title: h.Heading},

			// Another method from the embedded Page
			h.Footer(), // Returns Footer struct
		),
	)

	// METHOD CALL: b.String() converts the builder to an HTML string
	// This returns the complete HTML document as a string
	return b.String()
}
