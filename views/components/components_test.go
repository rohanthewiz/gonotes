package components

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/element"
)

// TestSidebarNoAlpine verifies Sidebar component doesn't use Alpine directives
func TestSidebarNoAlpine(t *testing.T) {
	b := element.NewBuilder()
	sidebar := Sidebar{
		UserGUID:   "test-user-guid-12345678",
		ActivePage: "dashboard",
		Tags:       []string{"tag1", "tag2"},
	}

	sidebar.Render(b)
	html := b.String()

	// Verify no Alpine-specific attributes
	alpineAttrs := []string{
		"x-data",
		"x-show",
		"x-if",
		"x-for",
		"x-init",
		"x-ref",
		":class",
		"@click",
		"@change",
	}

	for _, attr := range alpineAttrs {
		if strings.Contains(html, attr+"=") || strings.Contains(html, attr+`="`) {
			t.Errorf("Sidebar should not contain Alpine attribute: %s", attr)
		}
	}

	// Verify vanilla JS event handlers are used
	if !strings.Contains(html, "onclick") {
		t.Error("Sidebar should use onclick for event handlers")
	}

	// Verify toggleSidebar function is referenced
	if !strings.Contains(html, "toggleSidebar()") {
		t.Error("Sidebar should reference toggleSidebar() function")
	}

	// Verify import file functionality uses vanilla JS
	if !strings.Contains(html, "triggerImportFile()") {
		t.Error("Sidebar should reference triggerImportFile() function")
	}

	// Verify import file input has proper id
	if !strings.Contains(html, `id="import-file-input"`) {
		t.Error("Sidebar should have import-file-input id on file input")
	}
}

// TestSidebarStructure verifies Sidebar has correct structure
func TestSidebarStructure(t *testing.T) {
	b := element.NewBuilder()
	sidebar := Sidebar{
		UserGUID:   "test-user-guid-12345678",
		ActivePage: "dashboard",
	}

	sidebar.Render(b)
	html := b.String()

	// Verify sidebar has correct id
	if !strings.Contains(html, `id="sidebar"`) {
		t.Error("Sidebar should have id='sidebar'")
	}

	// Verify navigation links
	navLinks := []string{"/", "/notes", "/recent", "/search"}
	for _, link := range navLinks {
		if !strings.Contains(html, `href="`+link+`"`) {
			t.Errorf("Sidebar should contain navigation link to %s", link)
		}
	}

	// Verify HTMX attributes are preserved
	if !strings.Contains(html, "hx-get") {
		t.Error("Sidebar should contain HTMX hx-get attributes")
	}
	if !strings.Contains(html, "hx-target") {
		t.Error("Sidebar should contain HTMX hx-target attributes")
	}
}

// TestSidebarActiveClass verifies active class is applied correctly
func TestSidebarActiveClass(t *testing.T) {
	testCases := []struct {
		activePage string
		expected   string
	}{
		{"dashboard", "nav-link active"},
		{"notes", "nav-link active"},
		{"search", "nav-link active"},
	}

	for _, tc := range testCases {
		sidebar := Sidebar{
			UserGUID:   "test-user-guid-12345678",
			ActivePage: tc.activePage,
		}

		result := sidebar.activeClass(tc.activePage)
		if result != tc.expected {
			t.Errorf("activeClass(%s) = %s; want %s", tc.activePage, result, tc.expected)
		}

		// Also verify non-active returns regular class
		result = sidebar.activeClass("non-existent")
		if result != "nav-link" {
			t.Errorf("activeClass for non-active page should be 'nav-link', got %s", result)
		}
	}
}

// TestHeaderNoAlpine verifies Header component doesn't use Alpine directives
func TestHeaderNoAlpine(t *testing.T) {
	b := element.NewBuilder()
	header := Header{
		UserGUID: "test-user-guid-12345678",
	}

	header.Render(b)
	html := b.String()

	// Verify no Alpine-specific attributes
	alpineAttrs := []string{
		"x-data",
		"x-show",
		"x-if",
		"x-init",
		"@keydown",
		"@click",
		"$el",
	}

	for _, attr := range alpineAttrs {
		if strings.Contains(html, attr) {
			t.Errorf("Header should not contain Alpine attribute/syntax: %s", attr)
		}
	}

	// Verify vanilla JS is used for escape key handling
	if !strings.Contains(html, "onkeydown") {
		t.Error("Header should use onkeydown for keyboard events")
	}
}

// TestHeaderStructure verifies Header has correct structure
func TestHeaderStructure(t *testing.T) {
	b := element.NewBuilder()
	header := Header{
		UserGUID: "test-user-guid-12345678",
	}

	header.Render(b)
	html := b.String()

	// Verify header tag with correct id
	if !strings.Contains(html, `id="main-header"`) {
		t.Error("Header should have id='main-header'")
	}

	// Verify app title
	if !strings.Contains(html, "GoNotes") {
		t.Error("Header should contain app title 'GoNotes'")
	}

	// Verify search form with HTMX
	if !strings.Contains(html, "hx-get") && !strings.Contains(html, "/api/search") {
		t.Error("Header should contain search form with HTMX")
	}

	// Verify new note button
	if !strings.Contains(html, "New Note") {
		t.Error("Header should contain 'New Note' button")
	}

	// Verify user GUID is displayed (truncated)
	if !strings.Contains(html, "test-use...") {
		t.Error("Header should display truncated user GUID")
	}
}

// TestHeaderSearchEscapeHandler verifies escape key clears search
func TestHeaderSearchEscapeHandler(t *testing.T) {
	b := element.NewBuilder()
	header := Header{
		UserGUID: "test-user-guid-12345678",
	}

	header.Render(b)
	html := b.String()

	// Verify escape key handler uses vanilla JS
	if !strings.Contains(html, "Escape") {
		t.Error("Header should have Escape key handling")
	}

	// Should use this.value or similar vanilla JS, not $el
	if strings.Contains(html, "$el") {
		t.Error("Header should not use Alpine's $el syntax")
	}
}
