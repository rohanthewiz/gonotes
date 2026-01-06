// Package shared contains reusable components that are shared across multiple pages.
// Package names are typically short, lowercase, and describe their purpose.
package shared

// STRUCT DEFINITION: Page is a struct type that can be embedded in other structs
// This implements the "MIXIN PATTERN" - providing shared functionality through composition
// Any struct that embeds Page will inherit its fields and methods
//
// Example usage:
//   type Home struct {
//       shared.Page  // Embedded field - Home now has Title and all Page methods
//       Heading string
//   }
type Page struct {
	// Title is an exported field (starts with capital letter)
	// Exported fields are accessible from other packages
	Title string
}

// METHOD with VALUE RECEIVER
// (p Page) is the receiver - this method "belongs to" the Page type
// VALUE RECEIVER means the method receives a copy of the Page struct
// Changes to p inside this method won't affect the original Page
//
// METHOD SIGNATURE: func (receiver Type) MethodName(params) ReturnType
// This method can be called like: myPage.Banner()
func (p Page) Banner() Banner {
	// STRUCT LITERAL: Creating a new Banner instance
	// We pass p.Title to initialize the Banner's Title field
	// This shows how mixins can share data with components they create
	return Banner{Title: p.Title}
}

// Another method with value receiver
// This demonstrates that structs can have multiple methods
// EMPTY STRUCT LITERAL: Footer{} creates a Footer with all zero values
// Since Footer has no fields, the empty literal is sufficient
func (p Page) Footer() Footer {
	// Returning an empty Footer struct
	// Even though Footer is empty, it has a Render() method defined elsewhere
	return Footer{}
}

// KEY CONCEPTS demonstrated in this file:
// 1. STRUCT DEFINITION - Page is a custom type
// 2. EXPORTED vs UNEXPORTED - Title (exported) vs title (would be unexported)
// 3. METHOD RECEIVERS - Functions attached to a type
// 4. VALUE RECEIVER - Method receives a copy, doesn't modify original
// 5. MIXIN PATTERN - Embedding this struct provides shared functionality
// 6. STRUCT LITERALS - Creating instances with {Field: Value} syntax
