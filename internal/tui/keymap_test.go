package tui

import (
	"strings"
	"testing"

	"github.com/acidsound/Motive/internal/config"
)

func TestDefaultKeymap(t *testing.T) {
	k := DefaultKeymap()
	if k.ScrollUp != "ctrl+k" || k.ModelPicker != "ctrl+m" || k.Run != "enter" {
		t.Fatalf("unexpected defaults: %+v", k)
	}
	if k.ToolsToggle != "ctrl+t" {
		t.Errorf("ToolsToggle = %q, want ctrl+t", k.ToolsToggle)
	}
}

func TestKeymapApplyEnv(t *testing.T) {
	t.Setenv("MOTIVE_KEY_SCROLL_UP", "ctrl+u")
	t.Setenv("MOTIVE_KEY_MODEL_PICKER", "")
	k := DefaultKeymap()
	k.ApplyEnv()
	if k.ScrollUp != "ctrl+u" {
		t.Errorf("scroll up = %q, want ctrl+u override", k.ScrollUp)
	}
	if k.ModelPicker != "ctrl+m" {
		t.Errorf("model picker = %q, want default when env empty", k.ModelPicker)
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

func TestBuildModelItemsMarksActive(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.Provider{
			{Name: "a", BaseURL: "http://a/v1", Model: "m1", Models: []string{"m2"}},
			{Name: "b", BaseURL: "http://b/v1", Model: "m3"},
		},
		Default: &config.Provider{Name: "a"},
	}
	items := buildModelItems(cfg, "m2", "a")
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	active := 0
	for _, it := range items {
		pi := it.(pickerItem)
		if pi.title == "m2" && strings.HasPrefix(pi.desc, "●") {
			active++
		}
	}
	if active != 1 {
		t.Errorf("active markers = %d, want 1", active)
	}
}
