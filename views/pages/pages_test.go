package pages

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/element"
)

// TestTagsContentNoAlpine verifies tags page doesn't use Alpine
func TestTagsContentNoAlpine(t *testing.T) {
	b := element.NewBuilder()
	tagsContent := TagsContent{
		Tags: []TagInfo{
			{Name: "go", Count: 5},
			{Name: "web", Count: 3},
		},
	}

	tagsContent.Render(b)
	html := b.String()

	// Verify no Alpine-specific attributes
	if strings.Contains(html, "@click") {
		t.Error("TagsContent should not use @click Alpine syntax")
	}

	// Verify vanilla JS onclick is used
	if !strings.Contains(html, "onclick") {
		t.Error("TagsContent should use onclick for event handlers")
	}

	// Verify renameTag function is referenced with proper onclick
	if !strings.Contains(html, `onclick="renameTag(`) {
		t.Error("TagsContent should reference renameTag function via onclick")
	}
}

// TestTagsContentStructure verifies tags page structure
func TestTagsContentStructure(t *testing.T) {
	b := element.NewBuilder()
	tagsContent := TagsContent{
		Tags: []TagInfo{
			{Name: "go", Count: 5},
			{Name: "web", Count: 3},
		},
	}

	tagsContent.Render(b)
	html := b.String()

	// Verify tag count display
	if !strings.Contains(html, "2 tags") {
		t.Error("TagsContent should display total tag count")
	}

	// Verify tag names are displayed
	if !strings.Contains(html, "go") || !strings.Contains(html, "web") {
		t.Error("TagsContent should display tag names")
	}

	// Verify HTMX navigation
	if !strings.Contains(html, "hx-get") {
		t.Error("TagsContent should use HTMX for navigation")
	}

	// Verify delete functionality preserved
	if !strings.Contains(html, "hx-delete") {
		t.Error("TagsContent should have HTMX delete functionality")
	}
}

// TestTagsContentEmpty verifies empty tags state
func TestTagsContentEmpty(t *testing.T) {
	b := element.NewBuilder()
	tagsContent := TagsContent{
		Tags: []TagInfo{},
	}

	tagsContent.Render(b)
	html := b.String()

	if !strings.Contains(html, "No tags yet") {
		t.Error("TagsContent should show 'No tags yet' for empty state")
	}
}

// TestTagsRenameScript verifies inline JavaScript is included
func TestTagsRenameScript(t *testing.T) {
	b := element.NewBuilder()
	tagsContent := TagsContent{
		Tags: []TagInfo{
			{Name: "test", Count: 1},
		},
	}

	tagsContent.Render(b)
	html := b.String()

	// Verify script tag is present for renameTag function
	if !strings.Contains(html, "<script>") {
		t.Error("TagsContent should include script for renameTag function")
	}

	// Verify renameTag function is defined
	if !strings.Contains(html, "function renameTag") {
		t.Error("TagsContent should define renameTag function")
	}
}

// TestTagInfoStructure verifies TagInfo struct usage
func TestTagInfoStructure(t *testing.T) {
	tag := TagInfo{
		Name:  "golang",
		Count: 42,
	}

	if tag.Name != "golang" {
		t.Error("TagInfo.Name should be 'golang'")
	}

	if tag.Count != 42 {
		t.Error("TagInfo.Count should be 42")
	}
}
