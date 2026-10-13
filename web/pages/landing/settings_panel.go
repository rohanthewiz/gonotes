package landing

import "github.com/rohanthewiz/element"

// SettingsPanel is the dialog behind the toolbar's ⚙ button: one overlay with
// two tabs, Sync and Settings.
//
//	┌───────────────────────────────── × ┐
//	│ [ Sync ] [ Settings ]              │  ← .settings-tabs (role=tablist)
//	├────────────────────────────────────┤
//	│ Auto-sync / Interval / Peer URL    │  ← #settings-tab-sync   (static)
//	│ Sync Now, stats, conflicts         │
//	│              — or —                │
//	│ admin spoke-config export, …       │  ← #settings-tab-settings (built by
//	└────────────────────────────────────┘     app.showSettings on open)
//
// Design choices:
//
//   - It is its own overlay (#settings-overlay), not the shared #modal. The
//     shared modal's body is rebuilt with innerHTML by every dialog in the app,
//     but the sync controls must exist in the DOM from page load: sync.js reads
//     and writes them by id at init (auto-sync-toggle, sync-interval,
//     sync-peer-url, sync-last-time, …) and on every sync run. Keeping them as
//     static markup with their original ids means sync.js did not change at
//     all when they moved here from the old left sidebar.
//   - Because it is separate, it can stay open underneath when the shared modal
//     opens on top (e.g. Resolve → conflict list). The shared modal's z-index
//     is above this one; see .settings-overlay in app.css.
//   - The Settings tab's body is still generated in JS, because its content
//     depends on who is signed in (the spoke-config export is admin-only).
type SettingsPanel struct{}

// Render implements the element.Component interface
func (s SettingsPanel) Render(b *element.Builder) any {
	b.Div("class", "modal-overlay settings-overlay", "id", "settings-overlay",
		"onclick", "app.closeSettingsPanel()").R(
		b.Div("class", "modal settings-panel", "role", "dialog", "aria-modal", "true",
			"aria-label", "Sync and settings", "onclick", "event.stopPropagation()").R(
			b.DivClass("modal-header settings-panel-header").R(
				b.Div("class", "settings-tabs", "role", "tablist").R(
					b.Button("type", "button", "class", "settings-tab active", "id", "settings-tab-btn-sync",
						"role", "tab", "aria-selected", "true", "aria-controls", "settings-tab-sync",
						"onclick", "app.showSettingsTab('sync')").T("Sync"),
					b.Button("type", "button", "class", "settings-tab", "id", "settings-tab-btn-settings",
						"role", "tab", "aria-selected", "false", "aria-controls", "settings-tab-settings",
						"onclick", "app.showSettingsTab('settings')").T("Settings"),
				),
				b.Button("type", "button", "class", "modal-close", "onclick", "app.closeSettingsPanel()",
					"aria-label", "Close").T("×"),
			),
			b.DivClass("modal-body settings-panel-body").R(
				s.renderSyncTab(b),
				// Filled by app.showSettings each time the panel opens on this tab.
				b.Div("class", "settings-tab-pane", "id", "settings-tab-settings",
					"role", "tabpanel", "hidden", "hidden").R(),
			),
		),
	)
	return nil
}

// renderSyncTab is the sync control block that used to be the sidebar's
// collapsible "Sync" section. Ids are unchanged (sync.js depends on them).
// The "Unsynced only" filter that sat at its foot is a list filter, so it
// moved to the FilterBar instead.
func (s SettingsPanel) renderSyncTab(b *element.Builder) any {
	return b.Div("class", "settings-tab-pane", "id", "settings-tab-sync", "role", "tabpanel").R(
		// Auto-sync toggle row
		b.Div("class", "sync-control-row").R(
			b.SpanClass("sync-control-label").T("Auto-sync"),
			b.Label("class", "sync-toggle").R(
				b.Input("type", "checkbox", "id", "auto-sync-toggle",
					"onchange", "app.toggleAutoSync(this.checked)"),
				b.Span("class", "slider").R(),
			),
		),
		// Interval selector row
		b.Div("class", "sync-control-row").R(
			b.SpanClass("sync-control-label").T("Interval"),
			b.Select("class", "sync-select", "id", "sync-interval",
				"onchange", "app.setSyncInterval(this.value)").R(
				b.Option("value", "1").T("1 min"),
				b.Option("value", "5", "selected", "selected").T("5 min"),
				b.Option("value", "15").T("15 min"),
				b.Option("value", "30").T("30 min"),
			),
		),
		// Peer URL input row
		b.Div("class", "sync-control-row sync-peer-row").R(
			b.SpanClass("sync-control-label").T("Peer URL"),
			b.Input("type", "text", "class", "sync-peer-input", "id", "sync-peer-url",
				"placeholder", "https://peer:port",
				"onchange", "app.setPeerUrl(this.value)"),
			b.Button("class", "btn btn-secondary btn-sm", "id", "sync-test-btn",
				"onclick", "app.testPeerConnection()").T("Test"),
		),
		// Sync Now button. id=btn-sync lets sync.js mark it .syncing while a
		// run is in flight (it already looks that id up and tolerates absence).
		b.Div("class", "sync-control-row").R(
			b.Button("class", "btn btn-secondary btn-sm sync-now-btn", "id", "btn-sync",
				"onclick", "app.syncNotes()").T("↻ Sync Now"),
		),
		// Sync stats block
		b.Div("class", "sync-stats", "id", "sync-stats").R(
			b.Div("class", "sync-stat-row", "id", "sync-last-time").T("Last sync: Never"),
			b.Div("class", "sync-stat-row").R(
				b.Span("id", "sync-received").T("Received: 0"),
				b.Span("class", "sync-stat-sep").T(" "),
				b.Span("id", "sync-pushed").T("Pushed: 0"),
			),
			b.Div("class", "sync-stat-row", "id", "sync-conflict-row", "style", "display:none").R(
				b.Span("class", "text-warning", "id", "sync-conflict-count").T("Conflicts: 0"),
				b.Button("class", "btn-link text-warning", "onclick", "app.showConflicts()").T("Resolve"),
			),
		),
	)
}
