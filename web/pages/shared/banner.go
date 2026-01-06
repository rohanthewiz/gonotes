package shared

// IMPORT STATEMENT for third-party package
// We need the element package to use its Builder type
import "github.com/rohanthewiz/element"

// COMPONENT PATTERN: Banner is a reusable UI component
// It's a simple struct with data (Title) and behavior (Render method)
// This follows the "Component" design pattern - self-contained, reusable UI elements
type Banner struct {
	// Title field stores the text to display in the banner
	Title string
}

// INTERFACE IMPLEMENTATION: This method implements the element.Component interface
// In Go, interfaces are implemented IMPLICITLY - no "implements" keyword needed
// If a type has all the methods of an interface, it automatically implements that interface
//
// METHOD with VALUE RECEIVER and POINTER PARAMETER
// (b Banner) - value receiver, method gets a copy of Banner
// (*element.Builder) - pointer parameter, we receive a pointer to avoid copying the builder
//
// RETURN TYPE 'any': This is an alias for interface{} - can return any type
// The element package uses 'any' for flexibility, though we return nil here
func (b Banner) Render(builder *element.Builder) any {
	// METHOD CHAINING: Each method returns the builder for chaining calls
	// builder.Header() creates a <header> tag with inline CSS styles
	// .R() is a variadic method that accepts child elements (components or tags)
	builder.Header("style", "background-color:#2c3e50; color:white; padding:20px").R(
		// builder.H1() creates an <h1> tag
		// .T() adds text content (T = Text)
		// b.Title accesses the Title field from the Banner struct
		builder.H1().T(b.Title),
	)

	// Returning nil satisfies the 'any' return type
	// Some components might return error information, but this component doesn't
	return nil
}

// KEY CONCEPTS demonstrated in this file:
// 1. INTERFACE IMPLEMENTATION - Implicit implementation of element.Component
// 2. METHOD RECEIVERS - (b Banner) attaches Render to the Banner type
// 3. POINTER PARAMETERS - *element.Builder avoids copying large structs
// 4. METHOD CHAINING - Builder pattern for fluent API
// 5. INLINE CSS - Styling HTML elements programmatically
// 6. 'any' TYPE - Go's alias for interface{} (accepts any type)
