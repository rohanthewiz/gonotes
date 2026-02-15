package setup

import "github.com/rohanthewiz/element"

// Page renders the spoke setup screen — a standalone page (like login) where
// the user uploads a JSON config file exported from the hub admin. The config
// is previewed, then applied by writing it to the .env file.
//
// This page is only useful on a fresh spoke where sync is not yet configured.
type Page struct {
	Title string
}

// NewPage creates a new setup page with sensible defaults.
func NewPage() Page {
	return Page{Title: "GoNotes - Setup Sync"}
}

// Render generates the full HTML document for the setup page.
func (p Page) Render() string {
	b := element.NewBuilder()
	b.Html("lang", "en").R(
		p.renderHead(b),
		p.renderBody(b),
	)
	return b.String()
}

func (p Page) renderHead(b *element.Builder) any {
	return b.Head().R(
		b.Meta("charset", "UTF-8"),
		b.Meta("name", "viewport", "content", "width=device-width, initial-scale=1.0"),
		b.Title().T(p.Title),
		// Inline theme init — runs before CSS to prevent flash of wrong theme
		b.Script().T(`(function(){var t=localStorage.getItem('gonotes-theme')||'dark-green';document.documentElement.setAttribute('data-theme',t);})()`),
		b.Link("rel", "stylesheet", "href", "/static/css/app.css?v=4"),
	)
}

func (p Page) renderBody(b *element.Builder) any {
	return b.Body().R(
		b.DivClass("auth-container").R(
			b.DivClass("auth-card").R(
				// Logo
				b.DivClass("auth-logo").R(
					b.H1().T("GoNotes"),
				),

				// Title and description
				b.H2Class("auth-title").T("Setup Sync"),
				b.P("class", "settings-description").T(
					"Upload a spoke config file exported from the hub admin to configure sync automatically."),

				// Status message area — hidden by default
				b.Div("class", "auth-error hidden", "id", "setup-error").R(),
				b.Div("class", "setup-success hidden", "id", "setup-success").R(),

				// File upload
				b.DivClass("form-group").R(
					b.LabelClass("form-label", "for", "config-file").T("Config File (.json)"),
					b.Input("type", "file", "class", "form-input", "id", "config-file",
						"accept", ".json", "onchange", "handleFileSelect(this)"),
				),

				// Config preview — hidden until a file is loaded
				b.Div("class", "config-preview hidden", "id", "config-preview").R(),

				// Apply button — hidden until preview is shown
				b.Button("type", "button", "class", "auth-submit hidden", "id", "btn-apply",
					"onclick", "applyConfig()").T("Apply Configuration"),

				// Link back to login
				b.DivClass("auth-footer").R(
					b.A("href", "/login").T("Back to Login"),
				),
			),
		),

		// Toast container for notifications
		b.Div("class", "toast-container", "id", "toast-container").R(),

		// Setup JavaScript
		b.Script("src", "/static/js/setup.js").R(),
	)
}
