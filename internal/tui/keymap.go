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
	Stop            Binding
	Run             Binding
	Newline         Binding
	CycleEffort     Binding
	ModelPicker     Binding
	CycleQueueMode  Binding
	SessionPicker   Binding
	DiffToggle      Binding
	UnitsPanel      Binding
	ToolsToggle     Binding
	ReasoningToggle Binding
	AttachFile      Binding
	PasteImage      Binding
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
		Quit:    "ctrl+c",
		Stop:    "esc",
		Run:     "enter",
		Newline: "shift+enter",
		// Readline-collision policy: never bind plain ctrl+<letter> keys that
		// macOS terminal "natural text editing" profiles send for cmd+backspace
		// (ctrl+u), cmd+left (ctrl+a), and cmd+right (ctrl+e) — hijacking
		// those breaks line editing and fires overlays accidentally. Such
		// actions use alt+<letter> instead (requires Option-as-Meta;
		// rebindable via MOTIVE_KEY_<NAME>).
		CycleEffort:     "alt+e",
		ModelPicker:     "alt+m",
		CycleQueueMode:  "ctrl+\\",
		SessionPicker:   "ctrl+r",
		DiffToggle:      "ctrl+d",
		UnitsPanel:      "alt+u",
		ToolsToggle:     "ctrl+t",
		ReasoningToggle: "ctrl+o",
		AttachFile:      "alt+a",
		PasteImage:      "ctrl+y",
		ScrollUp:        "ctrl+k",
		ScrollDown:      "ctrl+j",
		PageUp:          "ctrl+shift+k",
		PageDown:        "ctrl+shift+j",
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
	k.Stop = envBinding("MOTIVE_KEY_STOP", k.Stop)
	k.Run = envBinding("MOTIVE_KEY_RUN", k.Run)
	k.Newline = envBinding("MOTIVE_KEY_NEWLINE", k.Newline)
	k.CycleEffort = envBinding("MOTIVE_KEY_CYCLE_EFFORT", k.CycleEffort)
	k.CycleQueueMode = envBinding("MOTIVE_KEY_CYCLE_QUEUE_MODE", k.CycleQueueMode)
	k.SessionPicker = envBinding("MOTIVE_KEY_SESSION_PICKER", k.SessionPicker)
	k.DiffToggle = envBinding("MOTIVE_KEY_DIFF_TOGGLE", k.DiffToggle)
	k.UnitsPanel = envBinding("MOTIVE_KEY_UNITS_PANEL", k.UnitsPanel)
	k.ToolsToggle = envBinding("MOTIVE_KEY_TOOLS_TOGGLE", k.ToolsToggle)
	k.ReasoningToggle = envBinding("MOTIVE_KEY_REASONING_TOGGLE", k.ReasoningToggle)
	k.AttachFile = envBinding("MOTIVE_KEY_ATTACH_FILE", k.AttachFile)
	k.PasteImage = envBinding("MOTIVE_KEY_PASTE_IMAGE", k.PasteImage)
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
