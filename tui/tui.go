package tui

import (
	"fmt"
	"gonotes/models"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// screenID identifies the currently active screen in the TUI.
// The root appModel delegates Update/View to the active screen's model.
type screenID int

const (
	screenLogin screenID = iota
	screenNoteList
	screenNoteDetail
	screenNoteForm
	screenCategoryList
	screenSearch
)

// Run is the entry point called from main.go when the "tui" subcommand is used.
// It initializes the bubbletea program and blocks until the user quits.
func Run() error {
	p := tea.NewProgram(newAppModel(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// appModel is the root bubbletea model. It holds shared state (authenticated user,
// terminal dimensions) and delegates to the active screen's sub-model.
type appModel struct {
	currentScreen screenID
	user          *models.User // populated after login
	width, height int          // terminal dimensions for responsive layout

	// Screen sub-models — each manages its own state
	loginModel    loginModel
	listModel     listModel
	detailModel   detailModel
	formModel     formModel
	categoryModel categoryModel
	searchModel   searchModel

	// Transient UI state
	statusMsg string // one-line feedback shown at bottom
}

func newAppModel() appModel {
	return appModel{
		currentScreen: screenLogin,
		loginModel:    newLoginModel(),
	}
}

func (m appModel) Init() tea.Cmd {
	return m.loginModel.Init()
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle terminal resize globally so every screen has current dimensions.
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = ws.Width
		m.height = ws.Height
	}

	switch m.currentScreen {
	case screenLogin:
		return m.updateLogin(msg)
	case screenNoteList:
		return m.updateNoteList(msg)
	case screenNoteDetail:
		return m.updateNoteDetail(msg)
	case screenNoteForm:
		return m.updateNoteForm(msg)
	case screenCategoryList:
		return m.updateCategoryList(msg)
	case screenSearch:
		return m.updateSearch(msg)
	}
	return m, nil
}

func (m appModel) View() string {
	var content string
	switch m.currentScreen {
	case screenLogin:
		content = m.loginModel.View()
	case screenNoteList:
		content = m.listModel.View(m.width, m.height)
	case screenNoteDetail:
		content = m.detailModel.View(m.width, m.height)
	case screenNoteForm:
		content = m.formModel.View(m.width, m.height)
	case screenCategoryList:
		content = m.categoryModel.View(m.width, m.height)
	case screenSearch:
		content = m.searchModel.View(m.width, m.height)
	}

	// Append status message if present
	if m.statusMsg != "" {
		content += "\n" + statusBarStyle.Render(m.statusMsg)
	}
	return content
}

// --- Screen transition helpers ---

// goToNoteList refreshes the notes list and switches to it.
func (m *appModel) goToNoteList() tea.Cmd {
	m.currentScreen = screenNoteList
	m.statusMsg = ""
	return m.listModel.loadNotes(m.user.GUID)
}

// goToNoteDetail loads a note by ID and switches to the detail screen.
func (m *appModel) goToNoteDetail(noteID int64) tea.Cmd {
	m.currentScreen = screenNoteDetail
	m.statusMsg = ""
	return m.detailModel.loadNote(noteID, m.user.GUID)
}

// goToNoteForm opens the form for create (noteID=0) or edit (noteID>0).
func (m *appModel) goToNoteForm(noteID int64) tea.Cmd {
	m.currentScreen = screenNoteForm
	m.statusMsg = ""
	return m.formModel.initForm(noteID, m.user.GUID)
}

// goToCategoryList loads categories and switches to the category manager.
func (m *appModel) goToCategoryList() tea.Cmd {
	m.currentScreen = screenCategoryList
	m.statusMsg = ""
	return m.categoryModel.loadCategories(m.user.GUID)
}

// goToSearch opens the search/filter screen.
func (m *appModel) goToSearch() tea.Cmd {
	m.currentScreen = screenSearch
	m.statusMsg = ""
	return m.searchModel.initSearch(m.user.GUID)
}

// --- Login screen ---

// loginModel handles username/password authentication at TUI startup.
type loginModel struct {
	usernameInput textinput.Model
	passwordInput textinput.Model
	focusIndex    int    // 0=username, 1=password
	errMsg        string // inline error from failed auth
}

func newLoginModel() loginModel {
	ui := textinput.New()
	ui.Placeholder = "username"
	ui.Focus()
	ui.CharLimit = 64

	pi := textinput.New()
	pi.Placeholder = "password"
	pi.EchoMode = textinput.EchoPassword
	pi.CharLimit = 128

	return loginModel{
		usernameInput: ui,
		passwordInput: pi,
	}
}

func (l loginModel) Init() tea.Cmd {
	return textinput.Blink
}

// loginSuccessMsg carries the authenticated user back to appModel.
type loginSuccessMsg struct {
	user *models.User
}

// loginErrMsg carries an auth failure message.
type loginErrMsg struct {
	err string
}

func (m appModel) updateLogin(msg tea.Msg) (tea.Model, tea.Cmd) {
	l := &m.loginModel

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, globalKeys.Quit):
			return m, tea.Quit

		case msg.Type == tea.KeyTab || msg.Type == tea.KeyShiftTab:
			// Toggle focus between username and password fields
			if l.focusIndex == 0 {
				l.focusIndex = 1
				l.usernameInput.Blur()
				l.passwordInput.Focus()
			} else {
				l.focusIndex = 0
				l.passwordInput.Blur()
				l.usernameInput.Focus()
			}
			return m, nil

		case msg.Type == tea.KeyEnter:
			// Attempt authentication
			l.errMsg = ""
			username := l.usernameInput.Value()
			password := l.passwordInput.Value()
			if username == "" || password == "" {
				l.errMsg = "Username and password are required"
				return m, nil
			}
			return m, func() tea.Msg {
				user, err := models.AuthenticateUser(models.UserLoginInput{
					Username: username,
					Password: password,
				})
				if err != nil {
					return loginErrMsg{err: "Invalid username or password"}
				}
				return loginSuccessMsg{user: user}
			}
		}

	case loginSuccessMsg:
		// Auth succeeded — initialize sub-models and go to notes list
		m.user = msg.user
		m.listModel = newListModel()
		m.detailModel = newDetailModel()
		m.formModel = newFormModel()
		m.categoryModel = newCategoryModel()
		m.searchModel = newSearchModel()
		return m, m.goToNoteList()

	case loginErrMsg:
		l.errMsg = msg.err
		return m, nil
	}

	// Forward to focused text input
	var cmd tea.Cmd
	if l.focusIndex == 0 {
		l.usernameInput, cmd = l.usernameInput.Update(msg)
	} else {
		l.passwordInput, cmd = l.passwordInput.Update(msg)
	}
	return m, cmd
}

func (l loginModel) View() string {
	banner := lipgloss.NewStyle().
		Bold(true).
		Foreground(colorPrimary).
		MarginBottom(2).
		Render("  GoNotes TUI")

	form := fmt.Sprintf(
		"%s\n%s\n\n%s\n%s",
		labelStyle.Render("Username"),
		l.usernameInput.View(),
		labelStyle.Render("Password"),
		l.passwordInput.View(),
	)

	errLine := ""
	if l.errMsg != "" {
		errLine = "\n" + errorStyle.Render(l.errMsg)
	}

	help := helpStyle.Render("tab: switch field • enter: login • q/ctrl+c: quit")

	return lipgloss.JoinVertical(lipgloss.Left,
		banner,
		boxStyle.Render(form+errLine),
		help,
	)
}

// --- Delegation stubs for other screens (filled in by their respective files) ---

func (m appModel) updateNoteList(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.listModel, cmd = m.listModel.Update(msg, &m)
	return m, cmd
}

func (m appModel) updateNoteDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.detailModel, cmd = m.detailModel.Update(msg, &m)
	return m, cmd
}

func (m appModel) updateNoteForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.formModel, cmd = m.formModel.Update(msg, &m)
	return m, cmd
}

func (m appModel) updateCategoryList(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.categoryModel, cmd = m.categoryModel.Update(msg, &m)
	return m, cmd
}

func (m appModel) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.searchModel, cmd = m.searchModel.Update(msg, &m)
	return m, cmd
}
