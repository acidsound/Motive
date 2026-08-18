package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

type model struct{}

func (model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (model) View() tea.View {
	view := tea.NewView(fmt.Sprintf("Motive\n\nMinimal model runtime.\n\nq / Ctrl-C  quit\n"))
	view.AltScreen = true
	return view
}

func Run() error {
	_, err := tea.NewProgram(model{}).Run()
	return err
}
