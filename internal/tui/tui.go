package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/acidsound/Motive/internal/runtime"
	tea "charm.land/bubbletea/v2"
)

type doneMsg struct { text string; err error }
type model struct { rt *runtime.Runtime; input string; output []string; busy bool; quitting bool }

func Run(rt *runtime.Runtime) error {
	_, err := tea.NewProgram(model{rt: rt}).Run()
	return err
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case doneMsg:
		m.busy = false
		if msg.err != nil { m.output = append(m.output, "ERROR: "+msg.err.Error()) } else { m.output = append(m.output, msg.text) }
		return m, nil
	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if m.busy || strings.TrimSpace(m.input) == "" { return m, nil }
			request := strings.TrimSpace(m.input)
			m.input = ""
			m.busy = true
			m.output = append(m.output, "You: "+request, "")
			return m, run(m.rt, request)
		case "backspace":
			if m.input != "" { r := []rune(m.input); m.input = string(r[:len(r)-1]) }
		default:
			if key != "" && !strings.Contains(key, "ctrl+") && key != "shift+enter" {
				m.input += key
			}
		}
	}
	return m, nil
}

func run(rt *runtime.Runtime, request string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		result, err := rt.Execute(ctx, request)
		return doneMsg{text: result, err: err}
	}
}

func (m model) View() tea.View {
	if m.quitting { return tea.NewView("Bye!\n") }
	var b strings.Builder
	b.WriteString("Motive — model-centric runtime\n\n")
	for _, line := range m.output {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if m.busy { b.WriteString("\n⠿ executing...\n") }
	b.WriteString("\n> ")
	b.WriteString(m.input)
	b.WriteString("\n\nEnter execute · Esc quit\n")
	v := tea.NewView(fmt.Sprintf("%s", b.String()))
	v.AltScreen = true
	return v
}
