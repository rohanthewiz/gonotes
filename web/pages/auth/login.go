package auth

import "github.com/rohanthewiz/element"

// LoginPage represents the login page
type LoginPage struct {
	Title string
}

// NewLoginPage creates a new login page
func NewLoginPage() LoginPage {
	return LoginPage{
		Title: "Login - GoNotes",
	}
}

// Render generates the HTML for the login page
func (p LoginPage) Render() string {
	b := element.NewBuilder()

	b.Html("lang", "en").R(
		p.renderHead(b),
		p.renderBody(b),
	)

	return "<!DOCTYPE html>" + b.String()
}

func (p LoginPage) renderHead(b *element.Builder) any {
	return b.Head().R(
		b.Meta("charset", "UTF-8"),
		b.Meta("name", "viewport", "content", "width=device-width, initial-scale=1.0"),
		b.Title().T(p.Title),
		b.Link("rel", "stylesheet", "href", "/static/css/app.css"),
	)
}

func (p LoginPage) renderBody(b *element.Builder) any {
	return b.Body().R(
		b.Div("class", "auth-container").R(
			b.Div("class", "auth-card").R(
				// Logo
				b.Div("class", "auth-logo").R(
					b.H1().T("GoNotes"),
				),

				// Title
				b.H2("class", "auth-title").T("Sign in to your account"),

				// Error message container
				b.Div("class", "auth-error hidden", "id", "error-message"),

				// Login form
				b.Form("class", "auth-form", "id", "login-form", "onsubmit", "return handleLogin(event)").R(
					// Username field
					b.Div("class", "form-group").R(
						b.Label("class", "form-label", "for", "username").T("Username"),
						b.Input("type", "text", "class", "form-input", "id", "username",
							"name", "username", "required", "required", "autocomplete", "username",
							"placeholder", "Enter your username"),
					),

					// Password field
					b.Div("class", "form-group").R(
						b.Label("class", "form-label", "for", "password").T("Password"),
						b.Input("type", "password", "class", "form-input", "id", "password",
							"name", "password", "required", "required", "autocomplete", "current-password",
							"placeholder", "Enter your password"),
					),

					// Submit button
					b.Button("type", "submit", "class", "auth-submit", "id", "submit-btn").T("Sign In"),
				),

				// Footer with register link
				b.Div("class", "auth-footer").R(
					b.Span().T("Don't have an account? "),
					b.A("href", "/register").T("Create one"),
				),
			),
		),

		// Auth JavaScript
		b.Script("src", "/static/js/auth.js"),
	)
}
