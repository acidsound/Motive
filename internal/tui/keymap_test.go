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
