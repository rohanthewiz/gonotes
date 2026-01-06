package shared

import "github.com/rohanthewiz/element"

// EMPTY STRUCT: Footer is a component with no data fields
// Empty structs (struct{}) have zero size in memory - very efficient!
// They're useful for components that only provide behavior (methods), not data
// Even though Footer has no fields, it can still have methods
type Footer struct{}

// INTERFACE IMPLEMENTATION: Implements element.Component interface
// This is the same interface as Banner, but Footer has no data to store
//
// METHOD with VALUE RECEIVER
// (f Footer) - even though Footer is empty, we still use a receiver for consistency
// (b *element.Builder) - pointer to the builder (note: parameter name is 'b' not 'builder')
//   Different parameter names are fine - the type is what matters for the interface
func (f Footer) Render(b *element.Builder) any {
	// METHOD CHAINING to build HTML structure
	// b.Div() creates a <div> tag with inline CSS styling
	b.Div("style", "background-color:lightgray").R(
		// b.P() creates a <p> paragraph tag
		// &copy; is an HTML entity for the copyright symbol ©
		b.P("style", "color:gray").T("Copyright &copy; 2025"),
	)

	// Return nil - no error or special value to return
	return nil
}

// KEY CONCEPTS demonstrated in this file:
// 1. EMPTY STRUCT - struct{} has zero size, used for stateless components
// 2. METHODS ON EMPTY STRUCTS - Even empty structs can have methods
// 3. INTERFACE CONSISTENCY - Same Render signature as Banner (duck typing)
// 4. HTML ENTITIES - &copy; is rendered as © in HTML
// 5. SHORT PARAMETER NAMES - 'b' instead of 'builder' is idiomatic Go (short, clear)
// 6. STATELESS COMPONENTS - Footer doesn't need data, just behavior
