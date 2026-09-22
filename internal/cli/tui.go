package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
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

type modelsMsg struct {
	models []string
	err    error
}

type ui struct {
	s       *modelctl.Service
	ctx     context.Context
	page    screen
	cursor  int
	width   int
	height  int
	current string
	models  []string
	loading bool
	search  textinput.Model
	fields  []textinput.Model
	labels  []string
	focus   int
	notice  string
}

func newInput(secret bool) textinput.Model {
	f := textinput.New()
	f.Prompt = "> "
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
		m.fields[i].SetWidth(max(10, min(70, m.width-6)))
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
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.search.SetWidth(max(10, min(70, m.width-6)))
		for i := range m.fields {
			m.fields[i].SetWidth(max(10, min(70, m.width-6)))
		}
	case modelsMsg:
		if m.page != pickerScreen {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.notice = msg.err.Error()
		} else {
			m.models = msg.models
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			m.back()
			return m, tea.Quit
		}
		if key == "esc" {
			if m.page == menuScreen {
				return m, tea.Quit
			}
			m.back()
			m.notice = "Canceled. Nothing saved."
			return m, nil
		}
		if m.page == menuScreen {
			switch key {
			case "q":
				return m, tea.Quit
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
					m.submit()
					return m, nil
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
		return tea.Batch(m.search.Focus(), func() tea.Msg {
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
		return tea.Quit
	}
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

func (m *ui) submit() {
	if err := m.ctx.Err(); err != nil {
		m.notice = "Canceled. Nothing saved."
		return
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
		r, err = m.s.SetToken(m.fields[0].Value(), token)
		clear(token)
		m.fields[1].Reset()
	}
	m.saved(r, err)
}

func (m *ui) saved(r modelctl.Result, err error) {
	if err != nil {
		m.notice = err.Error()
		return
	}
	m.back()
	m.notice = resultText(r)
	for _, warning := range r.Warnings {
		m.notice += "\nWarning: " + warning
	}
	m.refreshCurrent()
}

func (m *ui) View() tea.View {
	var b strings.Builder
	current := m.current
	if current == "" {
		current = "(not set in this file)"
	}
	fmt.Fprintf(&b, "modelctl\n\nConfig: %s\nDefault: %s\n\n", m.s.ConfigPath, current)
	switch m.page {
	case menuScreen:
		for i, item := range menuItems {
			marker := "  "
			if i == m.cursor {
				marker = "> "
			}
			fmt.Fprintf(&b, "%s%s\n", marker, item)
		}
		b.WriteString("\nUp/Down: select  Enter: open  q: quit\n")
	case pickerScreen:
		fmt.Fprintf(&b, "Filter: %s\n\n", m.search.View())
		items := m.filtered()
		if m.loading {
			b.WriteString("Loading OpenCode models...\n")
		} else if len(items) == 0 {
			b.WriteString("No matching models. Use 'Set model by ID' or add a provider and token.\n")
		}
		limit := max(3, m.height-12)
		start := max(0, m.cursor-limit+1)
		for i := start; i < min(len(items), start+limit); i++ {
			marker := "  "
			if i == m.cursor {
				marker = "> "
			}
			fmt.Fprintf(&b, "%s%s\n", marker, items[i])
		}
		b.WriteString("\nType to filter  Up/Down: select  Enter: save  Esc: cancel\n")
	default:
		for i, field := range m.fields {
			fmt.Fprintf(&b, "%s\n%s\n\n", m.labels[i], field.View())
		}
		b.WriteString("Tab: next field  Enter: next field / save on last field  Esc: cancel\n")
	}
	if m.notice != "" {
		fmt.Fprintf(&b, "\n%s\n", m.notice)
	}
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
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
	return tea.NewView("API token (hidden): " + m.input.View() + "\nEnter: save  Esc: cancel\n")
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
