package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/acidsound/Motive/internal/runtime"
)

type doneMsg struct {
	text string
	err  error
}

type model struct {
	rt       *runtime.Runtime
	input    textarea.Model
	output   []string
	busy     bool
	quitting bool
	width    int
	height   int
}

func newModel(rt *runtime.Runtime) model {
	input := textarea.New()
	input.Placeholder = "Describe what you want Motive to do..."
	input.Prompt = "> "
	input.SetVirtualCursor(false)
	input.SetStyles(textarea.DefaultStyles(true))
	input.Focus()
	input.KeyMap.InsertNewline.SetEnabled(false)

	return model{rt: rt, input: input}
}

func Run(rt *runtime.Runtime) error {
	_, err := tea.NewProgram(newModel(rt)).Run()
	return err
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case doneMsg:
		m.busy = false
		if msg.err != nil {
			m.output = append(m.output, "ERROR: "+msg.err.Error())
		} else if strings.TrimSpace(msg.text) != "" {
			m.output = append(m.output, msg.text)
		}
		if m.height > 0 {
			m.trimOutput()
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(max(1, msg.Width-4))
		m.input.SetHeight(min(8, max(3, msg.Height/4)))

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			if m.busy {
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if m.busy {
				return m, nil
			}
			request := strings.TrimSpace(m.input.Value())
			if request == "" {
				return m, nil
			}
			m.input.Reset()
			m.busy = true
			m.output = append(m.output, userStyle.Render("You: ")+request, "")
			m.trimOutput()
			return m, run(m.rt, request)
		case "shift+enter", "ctrl+j":
			// Insert a newline explicitly. Enter submits a request.
			m.input.SetValue(m.input.Value() + "\n")
			m.input.MoveToEnd()
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func run(rt *runtime.Runtime, request string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		result, err := rt.Execute(ctx, request)
		return doneMsg{text: result, err: err}
	}
}

func (m *model) trimOutput() {
	available := m.height - m.input.Height() - 7
	if available < 4 {
		available = 4
	}
	if len(m.output) > available {
		m.output = m.output[len(m.output)-available:]
	}
}

func (m model) View() tea.View {
	if m.quitting {
		return tea.NewView("Bye!\n")
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("Motive"))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render("model-centric runtime"))
	b.WriteString("\n\n")

	for _, line := range m.output {
		b.WriteString(line)
		b.WriteString("\n")
	}

	if m.busy {
		b.WriteString("\n")
		b.WriteString(busyStyle.Render("● executing…"))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Enter execute · Shift+Enter newline · Esc quit"))

	v := tea.NewView(b.String())
	if cursor := m.input.Cursor(); cursor != nil {
		// Account for the header, output lines, and the input prompt itself.
		cursor.Y += 3 + len(m.output) + boolToInt(m.busy) + 2
		v.Cursor = cursor
	}
	return v
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
	busyStyle  = lipgloss.NewStyle().Bold(true)
	userStyle  = lipgloss.NewStyle().Bold(true)
	_          = fmt.Sprint
)
