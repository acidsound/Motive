package tui

import (
	"testing"
)

func TestDefaultKeymap(t *testing.T) {
	k := DefaultKeymap()
	if k.ScrollUp != "ctrl+k" || k.Run != "enter" {
		t.Fatalf("unexpected defaults: %+v", k)
	}
	if k.PageUp != "ctrl+shift+k" || k.PageDown != "ctrl+shift+j" {
		t.Errorf("page bindings = %q/%q, want ctrl+shift+k/j", k.PageUp, k.PageDown)
	}
	if k.ToolsToggle != "ctrl+t" {
		t.Errorf("ToolsToggle = %q, want ctrl+t", k.ToolsToggle)
	}
	if k.Help != "ctrl+/" {
		t.Errorf("Help = %q, want ctrl+/", k.Help)
	}
	if k.Stop != "esc" {
		t.Errorf("Stop = %q, want esc", k.Stop)
	}
	if k.CycleQueueMode != "ctrl+\\" {
		t.Errorf("CycleQueueMode = %q, want ctrl+\\", k.CycleQueueMode)
	}
	// Readline-collision policy: cmd+backspace (ctrl+u), cmd+left (ctrl+a)
	// and cmd+right (ctrl+e) must not be shadowed by overlay bindings.
	if k.UnitsPanel != "alt+u" {
		t.Errorf("UnitsPanel = %q, want alt+u", k.UnitsPanel)
	}
	if k.AttachFile != "alt+a" {
		t.Errorf("AttachFile = %q, want alt+a", k.AttachFile)
	}
	if k.CycleEffort != "alt+e" {
		t.Errorf("CycleEffort = %q, want alt+e", k.CycleEffort)
	}
}

func TestKeymapApplyEnv(t *testing.T) {
	t.Setenv("MOTIVE_KEY_SCROLL_UP", "ctrl+u")
	k := DefaultKeymap()
	k.ApplyEnv()
	if k.ScrollUp != "ctrl+u" {
		t.Errorf("scroll up = %q, want ctrl+u override", k.ScrollUp)
	}
}

func TestKeymapToolsToggleEnv(t *testing.T) {
	t.Setenv("MOTIVE_KEY_TOOLS_TOGGLE", "ctrl+x")
	k := DefaultKeymap()
	k.ApplyEnv()
	if k.ToolsToggle != "ctrl+x" {
		t.Errorf("ToolsToggle = %q, want ctrl+x override", k.ToolsToggle)
	}
}

func TestKeymapHelpEnv(t *testing.T) {
	t.Setenv("MOTIVE_KEY_HELP", "f1")
	k := DefaultKeymap()
	k.ApplyEnv()
	if k.Help != "f1" {
		t.Errorf("Help = %q, want f1 override", k.Help)
	}
}

func TestKeymapStopAndQueueModeEnv(t *testing.T) {
	t.Setenv("MOTIVE_KEY_STOP", "ctrl+s")
	t.Setenv("MOTIVE_KEY_CYCLE_QUEUE_MODE", "ctrl+q")
	k := DefaultKeymap()
	k.ApplyEnv()
	if k.Stop != "ctrl+s" {
		t.Errorf("Stop = %q, want ctrl+s override", k.Stop)
	}
	if k.CycleQueueMode != "ctrl+q" {
		t.Errorf("CycleQueueMode = %q, want ctrl+q override", k.CycleQueueMode)
	}
}
