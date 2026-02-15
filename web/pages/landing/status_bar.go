package landing

import "github.com/rohanthewiz/element"

// StatusBar represents the bottom status bar
type StatusBar struct{}

// Render implements the element.Component interface
func (s StatusBar) Render(b *element.Builder) any {
	b.FooterClass("status-bar").R(
		// Left section - Sync status
		b.DivClass("status-left").R(
			b.Div("class", "sync-status synced", "id", "sync-status").R(
				b.Span("id", "sync-status-icon").T("✓"),
				b.Span("id", "sync-status-text").T("Ready"),
			),
		),

		// Center section - Query condition display
		// Shows a truncated, monospace query string representing active filters.
		// On hover, a popup reveals the full query with a copy-to-clipboard button.
		b.DivClass("status-center").R(
			b.Div("class", "query-display-wrapper", "id", "query-display-wrapper").R(
				b.Span("class", "query-display", "id", "query-display").T(""),
				b.Div("class", "query-popup", "id", "query-popup").R(
					b.Text(`<code class="query-popup-text" id="query-popup-text"></code>`),
					b.Button("class", "query-copy-btn", "onclick", "app.copyQuery()", "title", "Copy query").R(
						// Double-rectangle (copy) SVG icon — raw HTML since element doesn't have SVG helpers
						b.Text(`<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`),
					),
				),
			),
		),

		// Right section - Result count
		b.DivClass("status-right").R(
			b.Span("class", "result-count", "id", "result-count").T(""),
		),
	)
	return nil
}
