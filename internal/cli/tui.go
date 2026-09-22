package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stianfro/modelctl/internal/modelctl"
)

type screen int

const (
	menuScreen screen = iota
	pickerScreen
	modelScreen
	providerScreen
	tokenScreen
)

var menuItems = []string{"Switch default model", "Set model by ID", "Add or update custom provider", "Set API token", "Quit"}

type tokenSavedMsg struct {
	result modelctl.Result
	err    error
}

type modelsMsg struct {
	models []string
	err    error
}

var (
	accent     = lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Bold(true)
	muted      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	valueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("79"))
)

type ui struct {
	s          *modelctl.Service
	ctx        context.Context
	page       screen
	cursor     int
	width      int
	height     int
	current    string
	models     []string
	loading    bool
	search     textinput.Model
	fields     []textinput.Model
	labels     []string
	focus      int
	notice     string
	spinner    spinner.Model
	finished   bool
	saving     bool
	saveCancel context.CancelFunc
}

func newInput(secret bool) textinput.Model {
	f := textinput.New()
	f.Prompt = "› "
	f.CharLimit = 2048
	f.SetWidth(70)
	f.SetVirtualCursor(true)
	if secret {
		f.EchoMode = textinput.EchoPassword
		f.EchoCharacter = '*'
		// Validate the full value on save; never silently truncate a pasted token.
		f.CharLimit = 0
	}
	return f
}

func newUI(ctx context.Context, s *modelctl.Service) *ui {
	m := &ui{s: s, ctx: ctx, width: 80, height: 24, search: newInput(false)}
	m.spinner = spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(accent))
	m.search.Placeholder = "Search models"
	m.refreshCurrent()
	return m
}

func (m *ui) refreshCurrent() {
	c, err := m.s.Current()
	if err != nil {
		m.notice = err.Error()
		return
	}
	m.current = c.Model
}

func (m *ui) Init() tea.Cmd { return nil }

func (m *ui) form(page screen, labels []string, defaults []string) tea.Cmd {
	m.page, m.focus, m.notice = page, 0, ""
	m.labels = labels
	m.fields = make([]textinput.Model, len(labels))
	for i := range labels {
		m.fields[i] = newInput(page == tokenScreen && i == 1)
		m.fields[i].SetWidth(max(1, min(64, m.width-8)))
		if i < len(defaults) {
			m.fields[i].SetValue(defaults[i])
		}
	}
	return m.fields[0].Focus()
}

func (m *ui) back() {
	// Discard form contents, including the masked token, before returning to the menu.
	for i := range m.fields {
		m.fields[i].Reset()
	}
	m.fields, m.labels = nil, nil
	m.page, m.cursor, m.loading = menuScreen, 0, false
	m.search.Reset()
}

func (m *ui) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tokenSavedMsg:
		m.saving = false
		m.saveCancel = nil
		m.saved(msg.result, msg.err)
		return m, nil
	case spinner.TickMsg:
		if m.loading || m.saving {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.search.SetWidth(max(1, min(64, m.width-8)))
		for i := range m.fields {
			m.fields[i].SetWidth(max(1, min(64, m.width-8)))
		}
	case modelsMsg:
		if m.page != pickerScreen {
			return m, nil
		}
		m.loading = false
		m.models = msg.models
		if msg.err != nil {
			m.notice = msg.err.Error()
			if len(m.models) > 0 {
				m.notice += "; showing configured model IDs only."
			}
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, m.quit()
		}
		if m.saving {
			return m, nil
		}
		if key == "esc" {
			if m.page == menuScreen {
				return m, m.quit()
			}
			m.back()
			m.notice = "Canceled. Nothing saved."
			return m, nil
		}
		if m.page == menuScreen {
			switch key {
			case "q":
				return m, m.quit()
			case "up", "k":
				m.cursor = max(0, m.cursor-1)
			case "down", "j":
				m.cursor = min(len(menuItems)-1, m.cursor+1)
			case "enter":
				return m, m.selectMenu()
			}
			return m, nil
		}
		if m.page == pickerScreen {
			switch key {
			case "up":
				m.cursor = max(0, m.cursor-1)
				return m, nil
			case "down":
				m.cursor = min(max(0, len(m.filtered())-1), m.cursor+1)
				return m, nil
			case "enter":
				items := m.filtered()
				if !m.loading && len(items) > 0 && m.cursor < len(items) {
					r, err := m.s.Use(items[m.cursor])
					m.saved(r, err)
				}
				return m, nil
			}
		} else {
			switch key {
			case "tab", "shift+tab", "enter":
				if key == "enter" && m.focus == len(m.fields)-1 {
					return m, m.submit()
				}
				m.fields[m.focus].Blur()
				step := 1
				if key == "shift+tab" {
					step = -1
				}
				m.focus = (m.focus + step + len(m.fields)) % len(m.fields)
				return m, m.fields[m.focus].Focus()
			}
		}
	}
	var cmd tea.Cmd
	if m.page == pickerScreen {
		before := m.search.Value()
		m.search, cmd = m.search.Update(msg)
		if before != m.search.Value() {
			m.cursor = 0
		}
	} else if len(m.fields) > 0 {
		m.fields[m.focus], cmd = m.fields[m.focus].Update(msg)
	}
	return m, cmd
}

func (m *ui) selectMenu() tea.Cmd {
	switch m.cursor {
	case 0:
		m.page, m.cursor, m.loading, m.notice = pickerScreen, 0, true, ""
		m.models = nil
		return tea.Batch(m.search.Focus(), m.spinner.Tick, func() tea.Msg {
			models, err := m.s.List(m.ctx)
			return modelsMsg{models, err}
		})
	case 1:
		return m.form(modelScreen, []string{"Provider/model"}, []string{m.current})
	case 2:
		return m.form(providerScreen, []string{"Provider ID", "Base URL (blank: unchanged)", "Model IDs (comma-separated, blank: unchanged)", "Display name (optional)", "AI SDK package (optional)"}, nil)
	case 3:
		provider, _, _ := strings.Cut(m.current, "/")
		return m.form(tokenScreen, []string{"Provider ID", "API token (hidden)"}, []string{provider})
	default:
		return m.quit()
	}
}

func (m *ui) quit() tea.Cmd {
	if m.saveCancel != nil {
		m.saveCancel()
	}
	m.back()
	m.finished = true
	return tea.Quit
}

func (m *ui) filtered() []string {
	query := strings.ToLower(m.search.Value())
	var found []string
	for _, item := range m.models {
		if strings.Contains(strings.ToLower(item), query) {
			found = append(found, item)
		}
	}
	return found
}

func (m *ui) submit() tea.Cmd {
	if err := m.ctx.Err(); err != nil {
		m.notice = "Canceled. Nothing saved."
		return nil
	}
	var r modelctl.Result
	var err error
	switch m.page {
	case modelScreen:
		r, err = m.s.Use(m.fields[0].Value())
	case providerScreen:
		var models []string
		if value := strings.TrimSpace(m.fields[2].Value()); value != "" {
			for _, model := range strings.Split(value, ",") {
				models = append(models, strings.TrimSpace(model))
			}
		}
		r, err = m.s.SetProvider(modelctl.ProviderOptions{ID: m.fields[0].Value(), BaseURL: m.fields[1].Value(), Models: models, Name: m.fields[3].Value(), Package: m.fields[4].Value()})
	case tokenScreen:
		token := []byte(m.fields[1].Value())
		provider := m.fields[0].Value()
		m.fields[1].Reset()
		if m.s.Target == "opencode2" {
			ctx, cancel := context.WithCancel(m.ctx)
			m.saveCancel, m.saving, m.notice = cancel, true, ""
			return tea.Batch(m.spinner.Tick, func() tea.Msg {
				defer clear(token)
				defer cancel()
				r, err := m.s.SetTokenContext(ctx, provider, token)
				return tokenSavedMsg{r, err}
			})
		}
		r, err = m.s.SetTokenContext(m.ctx, provider, token)
		clear(token)
	}
	m.saved(r, err)
	return nil
}

func (m *ui) saved(r modelctl.Result, err error) {
	if err != nil {
		m.notice = err.Error()
		return
	}
	m.back()
	m.notice = "✓ Already up to date."
	if r.Changed {
		switch r.Action {
		case "use":
			m.notice = "✓ Default set to " + r.Model
		case "provider":
			m.notice = "✓ Provider saved: " + r.Provider
		case "token":
			m.notice = "✓ Token saved for " + r.Provider
		}
	}
	for _, warning := range r.Warnings {
		m.notice += "\nWarning: " + warning
	}
	m.refreshCurrent()
}

func (m *ui) View() tea.View {
	// Stay in the shell's normal buffer. On exit, replace the controls with a
	// short receipt rather than leaving an inactive menu in scrollback.
	width := max(1, min(76, m.width-2))
	clip := func(s string) string { return ansi.Truncate(s, width, "…") }
	current := m.current
	if current == "" {
		current = "no default set"
	}
	if m.finished {
		return tea.NewView(clip(accent.Render("modelctl")+"  "+current) + "\n")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n%s\n\n", accent.Render("modelctl"), muted.Render(m.targetLabel()), clip(valueStyle.Render(current)))
	row := func(selected bool, text string) {
		if selected {
			fmt.Fprintln(&b, clip(accent.Render("› "+text)))
		} else {
			fmt.Fprintln(&b, clip("  "+text))
		}
	}
	if m.saving {
		fmt.Fprintf(&b, "%s Saving API token…\n%s\n", m.spinner.View(), muted.Render("ctrl+c stop waiting"))
		return tea.NewView(b.String())
	}
	switch m.page {
	case menuScreen:
		for i, item := range menuItems {
			row(i == m.cursor, item)
		}
		b.WriteString("\n" + muted.Render("↑↓ choose · enter open · esc quit") + "\n")
	case pickerScreen:
		fmt.Fprintf(&b, "%s\n\n", m.search.View())
		items := m.filtered()
		if m.loading {
			fmt.Fprintf(&b, "%s Loading models\n", m.spinner.View())
		} else if m.notice != "" && len(items) == 0 && len(m.models) == 0 {
			b.WriteString("Model discovery failed. Use Set model by ID from the menu.\n")
		} else if len(items) == 0 {
			b.WriteString("No matches. Try a model ID from the menu.\n")
		} else {
			limit := max(1, min(6, m.height-10))
			start := max(0, m.cursor-limit+1)
			for i := start; i < min(len(items), start+limit); i++ {
				label := items[i]
				if label == m.current {
					label += " ✓"
				}
				row(i == m.cursor, label)
			}
			fmt.Fprintf(&b, "%s\n", muted.Render(fmt.Sprintf("  %d/%d", m.cursor+1, len(items))))
		}
		b.WriteString("\n" + muted.Render("type to filter · ↑↓ choose · enter save · esc back") + "\n")
	default:
		// One field at a time keeps even the provider form small and inline.
		fmt.Fprintf(&b, "%s\n%s\n\n%s\n", muted.Render(fmt.Sprintf("%d/%d", m.focus+1, len(m.fields))), accent.Render(m.labels[m.focus]), m.fields[m.focus].View())
		action := "enter next"
		if m.focus == len(m.fields)-1 {
			action = "enter save"
		}
		fmt.Fprintf(&b, "\n%s\n", muted.Render(action+" · tab/shift+tab move · esc back"))
	}
	if m.notice != "" {
		fmt.Fprintf(&b, "\n%s\n", m.notice)
	}
	// Wrap hints and errors without letting long model IDs grow the picker.
	return tea.NewView(lipgloss.NewStyle().Width(width).Render(b.String()))
}

func runUI(ctx context.Context, s *modelctl.Service, in io.Reader, out io.Writer) error {
	_, err := tea.NewProgram(newUI(ctx, s), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)).Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

type passwordUI struct {
	input    textinput.Model
	accepted bool
	finished bool
}

func (m passwordUI) Init() tea.Cmd { return m.input.Focus() }

func (m passwordUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "enter":
			m.accepted, m.finished = true, true
			return m, tea.Quit
		case "esc", "ctrl+c":
			m.input.Reset()
			m.finished = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m passwordUI) View() tea.View {
	if m.finished {
		return tea.NewView("")
	}
	return tea.NewView(accent.Render("API token") + muted.Render("  hidden") + "\n" + m.input.View() + "\n" + muted.Render("enter save · esc cancel") + "\n")
}

func readPassword(ctx context.Context, in io.Reader, out io.Writer) ([]byte, error) {
	m := passwordUI{input: newInput(true)}
	m.input.Focus()
	final, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)).Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	result := final.(passwordUI)
	if !result.accepted {
		return nil, context.Canceled
	}
	return []byte(result.input.Value()), nil
}

func (m *ui) targetLabel() string {
	if m.s.Target == "opencode2" {
		return "OpenCode 2"
	}
	return "OpenCode 1"
}
