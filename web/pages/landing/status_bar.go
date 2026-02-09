package landing

import "github.com/rohanthewiz/element"

// StatusBar represents the bottom status bar
type StatusBar struct{}

// Render implements the element.Component interface
func (s StatusBar) Render(b *element.Builder) any {
	b.FooterClass("status-bar").R(
		// Left section - Sync status with stats
		b.DivClass("status-left").R(
			b.Div("class", "sync-status synced", "id", "sync-status").R(
				b.Span("id", "sync-status-icon").T("✓"),
				b.Span("id", "sync-status-text").T("Ready"),
			),
			b.Span("class", "sync-stat", "id", "sync-stat-pulled", "title", "Notes received").T(""),
			b.Span("class", "sync-stat", "id", "sync-stat-pushed", "title", "Notes pushed").T(""),
			b.Span("class", "sync-stat sync-conflicts", "id", "sync-stat-conflicts",
				"onclick", "app.showConflicts()", "title", "Unresolved conflicts").T(""),
		),

		// Center section - Active filters
		b.DivClass("status-center").R(
			b.Div("class", "active-filters", "id", "active-filters").R(
				// Active filters will be populated by JavaScript
			),
		),

		// Right section - Result count
		b.DivClass("status-right").R(
			b.Span("class", "result-count", "id", "result-count").T(""),
		),
	)
	return nil
}
