package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	llm "github.com/acidsound/Motive/internal/model"
	"github.com/charmbracelet/x/ansi"
)

// Slash commands are the keybindings' text-mode twin: they must act on the
// spot, never reach the model as a prompt, and stay discoverable through
// live completion and tab-completion.

func TestSlashSteerQueueSwitchMode(t *testing.T) {
	m := newTestModel()

	m.input.SetValue("/steer")
	m2, _ := m.submit()
	m = *m2.(*model)
	if !m.steerMode {
		t.Fatal("/steer should enable steer mode")
	}
	if m.busy || len(m.messages) != 0 {
		t.Fatal("/steer must not start a turn")
	}
	if m.notice == "" {
		t.Fatal("/steer should confirm the mode change")
	}

	m.input.SetValue("/queue")
	m3, _ := m.submit()
	m = *m3.(*model)
	if m.steerMode {
		t.Fatal("/queue should disable steer mode")
	}
	if len(m.history) != 0 {
		t.Fatalf("commands must not enter prompt history: %v", m.history)
	}
}

func TestSlashEffortSetAndCycle(t *testing.T) {
	m := newTestModel()
	m.rt.Model = &llm.Client{ReasoningEffort: "low"}

	m.input.SetValue("/effort high")
	m2, _ := m.submit()
	m = *m2.(*model)
	if got := m.rt.Model.GetReasoningEffort(); got != "high" {
		t.Fatalf("effort = %q, want high", got)
	}

	m.input.SetValue("/effort")
	m3, _ := m.submit()
	m = *m3.(*model)
	if got := m.rt.Model.GetReasoningEffort(); got != "xhigh" {
		t.Fatalf("cycled effort = %q, want xhigh", got)
	}

	m.input.SetValue("/effort bogus")
	m4, _ := m.submit()
	m = *m4.(*model)
	if got := m.rt.Model.GetReasoningEffort(); got != "xhigh" {
		t.Fatalf("invalid argument changed effort to %q", got)
	}
	if !strings.Contains(m.notice, "usage:") {
		t.Fatalf("notice = %q, want usage hint", m.notice)
	}
}

func TestSlashUnknownDoesNotReachModel(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/xyz")
	m2, _ := m.submit()
	m = *m2.(*model)
	if m.busy || len(m.messages) != 0 {
		t.Fatal("unknown command must not start a turn")
	}
	if !strings.Contains(m.notice, "unknown command") {
		t.Fatalf("notice = %q, want unknown-command warning", m.notice)
	}
}

func TestSlashDoubleSlashSendsLiteralPrompt(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("//path/to/file explain")
	m2, _ := m.submit()
	m = *m2.(*model)
	if !m.busy {
		t.Fatal("// should start a turn")
	}
	if got := m.messages[0].content; got != "/path/to/file explain" {
		t.Fatalf("prompt = %q, want leading-slash text", got)
	}
}

func TestSlashNewResetsSession(t *testing.T) {
	m := newTestModel()
	m.sessionID = "20260101-000000-000001"
	m.messages = []message{{role: "user", content: "old"}}
	m.queue = []string{"pending"}

	m.input.SetValue("/new")
	m2, _ := m.submit()
	m = *m2.(*model)
	if m.sessionID != "" || m.messages != nil || m.queue != nil {
		t.Fatal("/new should reset the session like the keybinding")
	}
}

func TestSlashBusyGuard(t *testing.T) {
	m := newTestModel()
	m.busy = true

	m.input.SetValue("/new")
	m2, _ := m.runSlash("/new")
	m = *m2.(*model)
	if !strings.Contains(m.notice, "not available while running") {
		t.Fatalf("notice = %q, want busy guard", m.notice)
	}
	if m.messages != nil {
		t.Fatal("/new must not reset while busy")
	}

	// Mode switches stay available while busy (they steer the run).
	m2, _ = m.runSlash("/steer")
	m = *m2.(*model)
	if !m.steerMode {
		t.Fatal("/steer must work while busy")
	}
}

func TestSlashTabCompletion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/se", "/sessions"},  // unique match
		{"/effo", "/effort "}, // arg-taking command gets a trailing space
		{"/qu", "/qu"},        // ambiguous: already at the common prefix
		{"/q", "/qu"},         // /queue and /quit share the "qu" prefix
	}
	for _, tc := range cases {
		m := newTestModel()
		m.input.SetValue(tc.in)
		m2, _ := m.completeSlash(tea.KeyPressMsg{})
		m = *m2.(*model)
		if got := m.input.Value(); got != tc.want {
			t.Errorf("completeSlash(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSlashTabOutsideContextPassesThrough(t *testing.T) {
	// Outside a slash word, tab must behave exactly as it did before the
	// command system: the key goes to the textarea, no completion happens.
	m := newTestModel()
	m.input.SetValue("hello ")
	m2, _ := m.completeSlash(tea.KeyPressMsg{})
	m = *m2.(*model)
	if m.input.Value() != "hello " {
		t.Fatalf("value changed to %q, want unchanged pass-through", m.input.Value())
	}
}

func TestSlashHintListing(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/se")
	lines := m.slashHintLines()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "/sessions") {
		t.Fatalf("hint listing missing /sessions:\n%s", joined)
	}

	// A fully typed command leaves nothing to discover.
	m.input.SetValue("/sessions")
	if lines := m.slashHintLines(); lines != nil {
		t.Fatalf("fully typed command should hide the listing: %v", lines)
	}

	// A bare "/" lists commands but never floods the screen.
	m.input.SetValue("/")
	if lines := m.slashHintLines(); len(lines) > 9 {
		t.Fatalf("bare / listing has %d lines, want capped", len(lines))
	}
}

func TestHelpPanelListsCommands(t *testing.T) {
	lines := buildHelpLines(DefaultKeymap())
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Commands") {
		t.Fatal("help panel should have a Commands section")
	}
	for _, want := range []string{"/effort", "/steer", "/queue", "/sessions", "/diff", "/system", "/attach", "/help"} {
		if !strings.Contains(joined, want) {
			t.Errorf("help panel missing command %s", want)
		}
	}
	// The panel width must accommodate the widest row set it renders.
	w := helpPanelWidth(DefaultKeymap(), 200)
	for _, l := range lines {
		if ansi.Strip(l) != "" && len([]rune(ansi.Strip(l))) > w-2 {
			t.Errorf("help row wider than the panel: %q (w=%d)", l, w)
		}
	}
}
