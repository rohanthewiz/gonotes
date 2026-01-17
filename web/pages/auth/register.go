package auth

import "github.com/rohanthewiz/element"

// RegisterPage represents the registration page
type RegisterPage struct {
	Title string
}

// NewRegisterPage creates a new register page
func NewRegisterPage() RegisterPage {
	return RegisterPage{
		Title: "Register - GoNotes",
	}
}

// Render generates the HTML for the registration page
func (p RegisterPage) Render() string {
	b := element.NewBuilder()

	b.Html("lang", "en").R(
		p.renderHead(b),
		p.renderBody(b),
	)

	return b.String()
}

func (p RegisterPage) renderHead(b *element.Builder) any {
	return b.Head().R(
		b.Meta("charset", "UTF-8"),
		b.Meta("name", "viewport", "content", "width=device-width, initial-scale=1.0"),
		b.Title().T(p.Title),
		b.Link("rel", "stylesheet", "href", "/static/css/app.css"),
	)
}

func (p RegisterPage) renderBody(b *element.Builder) any {
	return b.Body().R(
		b.DivClass("auth-container").R(
			b.DivClass("auth-card").R(
				// Logo
				b.DivClass("auth-logo").R(
					b.H1().T("GoNotes"),
				),

				// Title
				b.H2Class("auth-title").T("Create your account"),

				// Error message container - empty div needs R() termination
				b.Div("class", "auth-error hidden", "id", "error-message").R(),

				// Register form
				b.Form("class", "auth-form", "id", "register-form", "onsubmit", "return handleRegister(event)").R(
					// Username field
					b.DivClass("form-group").R(
						b.LabelClass("form-label", "for", "username").T("Username"),
						b.Input("type", "text", "class", "form-input", "id", "username",
							"name", "username", "required", "required", "autocomplete", "username",
							"placeholder", "Choose a username", "minlength", "3", "maxlength", "50",
							"pattern", "[a-zA-Z0-9_]+"),
						b.SmallClass("text-muted").T("3-50 characters, letters, numbers, and underscores only"),
					),

					// Email field (optional)
					b.DivClass("form-group").R(
						b.LabelClass("form-label", "for", "email").T("Email (optional)"),
						b.Input("type", "email", "class", "form-input", "id", "email",
							"name", "email", "autocomplete", "email",
							"placeholder", "your@email.com"),
					),

					// Password field
					b.DivClass("form-group").R(
						b.LabelClass("form-label", "for", "password").T("Password"),
						b.Input("type", "password", "class", "form-input", "id", "password",
							"name", "password", "required", "required", "autocomplete", "new-password",
							"placeholder", "Create a password", "minlength", "8"),
						b.SmallClass("text-muted").T("At least 8 characters"),
					),

					// Confirm password field
					b.DivClass("form-group").R(
						b.LabelClass("form-label", "for", "confirm-password").T("Confirm Password"),
						b.Input("type", "password", "class", "form-input", "id", "confirm-password",
							"name", "confirm_password", "required", "required", "autocomplete", "new-password",
							"placeholder", "Confirm your password"),
					),

					// Submit button
					b.Button("type", "submit", "class", "auth-submit", "id", "submit-btn").T("Create Account"),
				),

				// Footer with login link
				b.DivClass("auth-footer").R(
					b.Span().T("Already have an account? "),
					b.A("href", "/login").T("Sign in"),
				),
			),
		),

		// Auth JavaScript
		b.Script("src", "/static/js/auth.js").R(),
	)
}
