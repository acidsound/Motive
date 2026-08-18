package tui

import (
	"os"
	"strings"
)

// Binding is a canonical key name as reported by bubbletea's String(), for
// example "ctrl+k", "alt+u", "enter", "pgup", or "up".
type Binding string

// Keymap holds every user-facing binding. Defaults follow jcode where a
// comparable action exists; each binding can be overridden with a
// MOTIVE_KEY_<NAME> environment variable (e.g. MOTIVE_KEY_SCROLL_UP=ctrl+k).
type Keymap struct {
	Quit            Binding
	Run             Binding
	Newline         Binding
	CycleEffort     Binding
	CycleModel      Binding
	ModelPicker     Binding
	SessionPicker   Binding
	DiffToggle      Binding
	PanelToggle     Binding
	ToolsToggle     Binding
	ReasoningToggle Binding
	ScrollUp        Binding
	ScrollDown      Binding
	PageUp          Binding
	PageDown        Binding
	HistoryUp       Binding
	HistoryDown     Binding
	Bookmark        Binding
	Clear           Binding
	Help            Binding
}

func DefaultKeymap() Keymap {
	return Keymap{
		Quit:            "ctrl+c",
		Run:             "enter",
		Newline:         "shift+enter",
		CycleEffort:     "ctrl+e",
		CycleModel:      "ctrl+tab",
		ModelPicker:     "ctrl+m",
		SessionPicker:   "ctrl+r",
		DiffToggle:      "ctrl+d",
		PanelToggle:     "ctrl+s",
		ToolsToggle:     "ctrl+t",
		ReasoningToggle: "ctrl+o",
		ScrollUp:        "ctrl+k",
		ScrollDown:      "ctrl+j",
		PageUp:          "alt+u",
		PageDown:        "alt+d",
		HistoryUp:       "up",
		HistoryDown:     "down",
		Bookmark:        "ctrl+g",
		Clear:           "ctrl+l",
		Help:            "ctrl+/",
	}
}

// ApplyEnv overlays MOTIVE_KEY_* environment variables on top of the defaults.
func (k *Keymap) ApplyEnv() {
	k.Quit = envBinding("MOTIVE_KEY_QUIT", k.Quit)
	k.Run = envBinding("MOTIVE_KEY_RUN", k.Run)
	k.Newline = envBinding("MOTIVE_KEY_NEWLINE", k.Newline)
	k.CycleEffort = envBinding("MOTIVE_KEY_CYCLE_EFFORT", k.CycleEffort)
	k.CycleModel = envBinding("MOTIVE_KEY_CYCLE_MODEL", k.CycleModel)
	k.ModelPicker = envBinding("MOTIVE_KEY_MODEL_PICKER", k.ModelPicker)
	k.SessionPicker = envBinding("MOTIVE_KEY_SESSION_PICKER", k.SessionPicker)
	k.DiffToggle = envBinding("MOTIVE_KEY_DIFF_TOGGLE", k.DiffToggle)
	k.PanelToggle = envBinding("MOTIVE_KEY_PANEL_TOGGLE", k.PanelToggle)
	k.ToolsToggle = envBinding("MOTIVE_KEY_TOOLS_TOGGLE", k.ToolsToggle)
	k.ReasoningToggle = envBinding("MOTIVE_KEY_REASONING_TOGGLE", k.ReasoningToggle)
	k.ScrollUp = envBinding("MOTIVE_KEY_SCROLL_UP", k.ScrollUp)
	k.ScrollDown = envBinding("MOTIVE_KEY_SCROLL_DOWN", k.ScrollDown)
	k.PageUp = envBinding("MOTIVE_KEY_PAGE_UP", k.PageUp)
	k.PageDown = envBinding("MOTIVE_KEY_PAGE_DOWN", k.PageDown)
	k.HistoryUp = envBinding("MOTIVE_KEY_HISTORY_UP", k.HistoryUp)
	k.HistoryDown = envBinding("MOTIVE_KEY_HISTORY_DOWN", k.HistoryDown)
	k.Bookmark = envBinding("MOTIVE_KEY_BOOKMARK", k.Bookmark)
	k.Clear = envBinding("MOTIVE_KEY_CLEAR", k.Clear)
	k.Help = envBinding("MOTIVE_KEY_HELP", k.Help)
}

func envBinding(name string, fallback Binding) Binding {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return Binding(v)
	}
	return fallback
}
