package landing

import "github.com/rohanthewiz/element"

// StatusBar represents the bottom status bar
type StatusBar struct{}

// Render implements the element.Component interface
func (s StatusBar) Render(b *element.Builder) any {
	b.Footer("class", "status-bar").R(
		// Left section - Sync status
		b.Div("class", "status-left").R(
			b.Div("class", "sync-status synced", "id", "sync-status").R(
				b.Span("id", "sync-status-icon").T("✓"),
				b.Span("id", "sync-status-text").T("Ready"),
			),
		),

		// Center section - Active filters
		b.Div("class", "status-center").R(
			b.Div("class", "active-filters", "id", "active-filters").R(
				// Active filters will be populated by JavaScript
			),
		),

		// Right section - Result count
		b.Div("class", "status-right").R(
			b.Span("class", "result-count", "id", "result-count").T(""),
		),
	)
	return nil
}
