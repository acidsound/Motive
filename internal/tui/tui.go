package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/acidsound/Motive/internal/runtime"
)

type doneMsg struct {
	text string
	err  error
}

type model struct {
	rt          *runtime.Runtime
	input       textarea.Model
	output      []string
	busy        bool
	width       int
	height      int
	inputHeight int
}

func newModel(rt *runtime.Runtime) model {
	input := textarea.New()
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.SetVirtualCursor(false)
	input.SetStyles(textarea.DefaultStyles(true))
	input.Focus()
	input.KeyMap.InsertNewline.SetEnabled(false)

	return model{rt: rt, input: input, inputHeight: 3}
}

func Run(rt *runtime.Runtime) error {
	_, err := tea.NewProgram(newModel(rt)).Run()
	return err
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case doneMsg:
		m.busy = false
		if msg.err != nil {
			m.output = append(m.output, "ERROR: "+msg.err.Error())
		} else if text := strings.TrimSpace(msg.text); text != "" {
			m.output = append(m.output, text)
		}
		m.trimOutput()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.inputHeight = max(3, min(8, msg.Height/4))
		m.input.SetWidth(max(1, msg.Width-2))
		m.input.SetHeight(m.inputHeight)

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			if m.busy {
				return m, nil
			}
			return m, tea.Quit

		case "ctrl+e":
			if m.busy {
				return m, nil
			}
			m.cycleEffort()
			return m, nil

		case "enter":
			if m.busy {
				return m, nil
			}
			request := strings.TrimSpace(m.input.Value())
			if request == "" {
				return m, nil
			}
			m.input.Reset()
			m.output = append(m.output, "> "+request, "")
			m.busy = true
			m.trimOutput()
			return m, execute(m.rt, request)

		case "shift+enter":
			m.input.SetValue(m.input.Value() + "\n")
			m.input.MoveToEnd()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *model) cycleEffort() {
	current := m.rt.Model.GetReasoningEffort()
	next := "low"
	switch current {
	case "low":
		next = "medium"
	case "medium":
		next = "xhigh"
	case "xhigh":
		next = "low"
	default:
		next = "low"
	}
	m.rt.Model.SetReasoningEffort(next)
}

func execute(rt *runtime.Runtime, request string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		result, err := rt.Execute(ctx, request)
		return doneMsg{text: result, err: err}
	}
}

func (m *model) trimOutput() {
	available := m.height - m.inputHeight - 3
	if available < 1 {
		return
	}
	used := 0
	for i := len(m.output) - 1; i >= 0; i-- {
		used += strings.Count(m.output[i], "\n") + 1
		if used > available {
			m.output = m.output[i+1:]
			return
		}
	}
}

func (m model) View() tea.View {
	var b strings.Builder
	lines := 0
	for _, line := range m.output {
		b.WriteString(line)
		b.WriteString("\n")
		lines += strings.Count(line, "\n") + 1
	}
	if m.busy {
		b.WriteString("working…\n")
	}
	b.WriteString("\n> ")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("reasoning: %s   [ctrl+e]   budget: %d steps / %s", m.rt.Model.GetReasoningEffort(), m.rt.Budget.MaxSteps, m.rt.Budget.MaxDuration.Round(time.Minute)))

	v := tea.NewView(b.String())
	v.AltScreen = true
	if cursor := m.input.Cursor(); cursor != nil {
		cursor.X += 2
		cursor.Y += lines + 1
		if m.busy {
			cursor.Y++
		}
		v.Cursor = cursor
	}
	return v
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
