package partials

import (
	"strings"
	"testing"
	"time"
)

// TestRenderNotificationNoAlpine verifies notification doesn't use Alpine
func TestRenderNotificationNoAlpine(t *testing.T) {
	html := RenderNotification("success", "Test message")

	// Verify no Alpine-specific attributes
	alpineAttrs := []string{
		"x-data",
		"x-show",
		"x-init",
		"@click",
	}

	for _, attr := range alpineAttrs {
		if strings.Contains(html, attr) {
			t.Errorf("RenderNotification should not contain Alpine attribute: %s", attr)
		}
	}

	// Verify vanilla JS onclick is used
	if !strings.Contains(html, "onclick") {
		t.Error("RenderNotification should use onclick for close button")
	}

	// Verify auto-dismiss class for JS handling
	if !strings.Contains(html, "auto-dismiss") {
		t.Error("RenderNotification should include auto-dismiss class")
	}
}

// TestRenderNotificationStructure verifies notification structure
func TestRenderNotificationStructure(t *testing.T) {
	testCases := []struct {
		msgType  string
		message  string
		expected string
	}{
		{"success", "Success message", "notification-success"},
		{"error", "Error message", "notification-error"},
		{"info", "Info message", "notification-info"},
		{"warning", "Warning message", "notification-warning"},
	}

	for _, tc := range testCases {
		html := RenderNotification(tc.msgType, tc.message)

		if !strings.Contains(html, tc.expected) {
			t.Errorf("RenderNotification(%s) should contain class %s", tc.msgType, tc.expected)
		}

		if !strings.Contains(html, tc.message) {
			t.Errorf("RenderNotification should contain message: %s", tc.message)
		}

		// Verify close button exists
		if !strings.Contains(html, "notification-close") {
			t.Error("RenderNotification should contain close button")
		}
	}
}

// TestRenderTagsCloudNoAlpine verifies tags cloud doesn't use Alpine
func TestRenderTagsCloudNoAlpine(t *testing.T) {
	tagCounts := map[string]int{
		"go":     5,
		"web":    3,
		"coding": 1,
	}

	html := RenderTagsCloud(tagCounts)

	// Verify no Alpine attributes
	if strings.Contains(html, "x-") || strings.Contains(html, "@click") {
		t.Error("RenderTagsCloud should not contain Alpine attributes")
	}

	// Verify HTMX is used for tag navigation
	if !strings.Contains(html, "hx-get") {
		t.Error("RenderTagsCloud should use HTMX for tag navigation")
	}
}

// TestRenderNoteEditorNoAlpine verifies note editor doesn't use Alpine
func TestRenderNoteEditorNoAlpine(t *testing.T) {
	html := RenderNoteEditor(nil)

	// Verify no Alpine attributes
	alpineAttrs := []string{"x-data", "x-show", "x-model", "@submit"}
	for _, attr := range alpineAttrs {
		if strings.Contains(html, attr) {
			t.Errorf("RenderNoteEditor should not contain Alpine attribute: %s", attr)
		}
	}

	// Verify HTMX is used for form submission
	if !strings.Contains(html, "hx-post") {
		t.Error("RenderNoteEditor should use HTMX for form submission")
	}
}

// TestRenderNoteEditorNewNote verifies editor for new note
func TestRenderNoteEditorNewNote(t *testing.T) {
	html := RenderNoteEditor(nil)

	// Verify form action for new note
	if !strings.Contains(html, "hx-post") || !strings.Contains(html, "/api/notes") {
		t.Error("New note editor should have POST action to /api/notes")
	}

	// Verify create button text
	if !strings.Contains(html, "Create Note") {
		t.Error("New note editor should have 'Create Note' button")
	}
}

// TestHelperFunctions tests helper functions
func TestHelperFunctions(t *testing.T) {
	// Test formatDate
	testTime := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	formatted := formatDate(testTime)
	if formatted != "Jun 15, 2024" {
		t.Errorf("formatDate() = %s; want Jun 15, 2024", formatted)
	}

	// Test formatRelativeTime - just now
	recentTime := time.Now().Add(-30 * time.Second)
	relative := formatRelativeTime(recentTime)
	if relative != "just now" {
		t.Errorf("formatRelativeTime for 30s ago should be 'just now', got %s", relative)
	}

	// Test formatRelativeTime - minutes
	minutesAgo := time.Now().Add(-5 * time.Minute)
	relative = formatRelativeTime(minutesAgo)
	if !strings.Contains(relative, "minutes ago") {
		t.Errorf("formatRelativeTime for 5min ago should contain 'minutes ago', got %s", relative)
	}

	// Test calculateTagSize
	size := calculateTagSize(10, 10)
	if size != 5 {
		t.Errorf("calculateTagSize(10, 10) = %d; want 5", size)
	}

	size = calculateTagSize(1, 10)
	if size != 1 {
		t.Errorf("calculateTagSize(1, 10) = %d; want 1", size)
	}

	// Test joinTags
	tags := []string{"go", "web", "api"}
	joined := joinTags(tags)
	if joined != "go, web, api" {
		t.Errorf("joinTags() = %s; want 'go, web, api'", joined)
	}
}

// TestGetExcerpt tests the excerpt helper
func TestGetExcerpt(t *testing.T) {
	testCases := []struct {
		text      string
		query     string
		maxLength int
		expected  string
	}{
		{"short text", "test", 20, "short text"},
		{"this is a much longer text that needs truncation", "test", 20, "this is a much longe..."},
	}

	for _, tc := range testCases {
		result := getExcerpt(tc.text, tc.query, tc.maxLength)
		if result != tc.expected {
			t.Errorf("getExcerpt(%q, %q, %d) = %q; want %q", tc.text, tc.query, tc.maxLength, result, tc.expected)
		}
	}
}

// TestCalculateTagSize tests tag size calculation
func TestCalculateTagSize(t *testing.T) {
	testCases := []struct {
		count    int
		maxCount int
		expected int
	}{
		{10, 10, 5},  // 100% = size 5
		{9, 10, 5},   // 90% = size 5
		{7, 10, 4},   // 70% = size 4
		{5, 10, 3},   // 50% = size 3
		{3, 10, 2},   // 30% = size 2
		{1, 10, 1},   // 10% = size 1
		{0, 0, 1},    // edge case: zero max
	}

	for _, tc := range testCases {
		result := calculateTagSize(tc.count, tc.maxCount)
		if result != tc.expected {
			t.Errorf("calculateTagSize(%d, %d) = %d; want %d", tc.count, tc.maxCount, result, tc.expected)
		}
	}
}
