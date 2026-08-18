package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
)

func newInput() textarea.Model {
	m := textarea.New()
	m.Prompt = "> "
	m.Placeholder = ""
	m.SetVirtualCursor(false)
	m.SetStyles(textarea.DefaultStyles(true))
	m.Focus()
	m.KeyMap.InsertNewline.SetEnabled(false)
	return m
}

func submitValue(m *textarea.Model) string {
	return strings.TrimSpace(m.Value())
}
