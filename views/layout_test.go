package views

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/element"
)

// TestBaseLayoutNoAlpine verifies that Alpine.js is not included in the layout
func TestBaseLayoutNoAlpine(t *testing.T) {
	// Create a simple test component
	testComponent := testContent{}

	html := BaseLayout("", "", testComponent)

	// Verify Alpine.js script is not included
	if strings.Contains(html, "alpine.min.js") {
		t.Error("Layout should not include Alpine.js script")
	}
	if strings.Contains(html, "alpine:init") {
		t.Error("Layout should not contain alpine:init references")
	}

	// Verify no x-data attributes
	if strings.Contains(html, "x-data") {
		t.Error("Layout should not contain x-data attributes")
	}

	// Verify HTMX is still included
	if !strings.Contains(html, "htmx.min.js") {
		t.Error("Layout should include HTMX script")
	}

	// Verify body tag doesn't have Alpine attributes
	if strings.Contains(html, `<body x-data`) {
		t.Error("Body tag should not have x-data attribute")
	}

	// Verify app.js is included
	if !strings.Contains(html, "app.js") {
		t.Error("Layout should include app.js script")
	}
}

// TestBaseLayoutStructure verifies the basic HTML structure
func TestBaseLayoutStructure(t *testing.T) {
	testComponent := testContent{}

	html := BaseLayout("", "", testComponent)

	// Check for basic HTML structure
	if !strings.Contains(html, "<!DOCTYPE html>") && !strings.Contains(html, "<html>") {
		// The element library might not add doctype, just check for html tag
		if !strings.Contains(html, "<html") {
			t.Error("Layout should contain html tag")
		}
	}

	if !strings.Contains(html, "<head>") {
		t.Error("Layout should contain head tag")
	}

	if !strings.Contains(html, "<body>") {
		t.Error("Layout should contain body tag")
	}

	if !strings.Contains(html, "<title>GoNotes Web</title>") {
		t.Error("Layout should contain correct title")
	}
}

// TestBaseLayoutWithStyles verifies custom styles are included
func TestBaseLayoutWithStyles(t *testing.T) {
	customStyles := ".custom { color: red; }"
	testComponent := testContent{}

	html := BaseLayout(customStyles, "", testComponent)

	if !strings.Contains(html, customStyles) {
		t.Error("Layout should include custom styles")
	}

	if !strings.Contains(html, "<style>") {
		t.Error("Layout should contain style tag for custom styles")
	}
}

// TestBaseLayoutWithHeadContent verifies custom head content is included
func TestBaseLayoutWithHeadContent(t *testing.T) {
	customHead := `<meta name="custom" content="test">`
	testComponent := testContent{}

	html := BaseLayout("", customHead, testComponent)

	if !strings.Contains(html, customHead) {
		t.Error("Layout should include custom head content")
	}
}

// TestSimpleLayoutNoAlpine verifies SimpleLayout doesn't use Alpine
func TestSimpleLayoutNoAlpine(t *testing.T) {
	testComponent := testContent{}

	html := SimpleLayout("Test Title", testComponent)

	// Verify no Alpine attributes
	if strings.Contains(html, "x-data") {
		t.Error("SimpleLayout should not contain x-data attributes")
	}
	if strings.Contains(html, "x-") {
		t.Error("SimpleLayout should not contain any x-* Alpine attributes")
	}
	if strings.Contains(html, "@click") || strings.Contains(html, "@change") {
		t.Error("SimpleLayout should not contain Alpine event handlers")
	}
}

// TestPageWithHeaderNoAlpine verifies PageWithHeader doesn't use Alpine attributes
func TestPageWithHeaderNoAlpine(t *testing.T) {
	b := element.NewBuilder()
	page := PageWithHeader{
		UserGUID:   "test-user-guid-12345678",
		ActivePage: "dashboard",
		Content:    testContent{},
	}

	page.Render(b)
	html := b.String()

	// Verify no Alpine attributes
	alpineAttrs := []string{"x-data", "x-show", "x-if", "x-for", "x-init", "x-ref", ":class"}
	for _, attr := range alpineAttrs {
		if strings.Contains(html, attr) {
			t.Errorf("PageWithHeader should not contain %s attribute", attr)
		}
	}
}

// testContent is a simple test component
type testContent struct{}

func (tc testContent) Render(b *element.Builder) (x any) {
	b.Div("class", "test-content").T("Test content")
	return
}
