package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/acidsound/Motive/internal/config"
	llm "github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/session"
)

func newTestModel() model {
	return newModel(&runtime.Runtime{Model: &llm.Client{Model: "test-model"}}, &config.Config{}, nil, false)
}

func TestLoadSessionRestoresMessages(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(id, session.Entry{Role: "user", Content: "u1", BaseRevision: "abcdef123456"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(id, session.Entry{Role: "assistant", Content: "a1", Reasoning: "r1", Tools: []string{"shell · 12B · 5ms"}, ResultRevision: "fedcba654321"}); err != nil {
		t.Fatal(err)
	}

	m := newTestModel()
	m.sess = s
	m.loadSession(id)

	if m.sessionID != id {
		t.Errorf("sessionID = %q, want %q", m.sessionID, id)
	}
	if len(m.messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(m.messages))
	}
	if m.messages[0].role != "user" || m.messages[0].content != "u1" {
		t.Errorf("message 0 = %+v", m.messages[0])
	}
	as := m.messages[1]
	if as.role != "assistant" || as.content != "a1" || as.reasoning != "r1" || len(as.tools) != 1 {
		t.Errorf("message 1 = %+v", as)
	}
	if m.baseRev != "abcdef123456" || m.resultRev != "fedcba654321" {
		t.Errorf("revisions = %q -> %q", m.baseRev, m.resultRev)
	}
}

func TestHistoryNavigation(t *testing.T) {
	m := newTestModel()
	m.history = []string{"first", "second"}
	m.histIdx = len(m.history)

	m.historyPrev()
	if got := m.input.Value(); got != "second" {
		t.Errorf("historyPrev 1 = %q, want second", got)
	}
	m.historyPrev()
	if got := m.input.Value(); got != "first" {
		t.Errorf("historyPrev 2 = %q, want first", got)
	}
	m.historyNext()
	if got := m.input.Value(); got != "second" {
		t.Errorf("historyNext 1 = %q, want second", got)
	}
	m.historyNext()
	if got := m.input.Value(); got != "" {
		t.Errorf("historyNext 2 = %q, want empty", got)
	}
	// Wrapping up again from the newest edge should show the last entry.
	m.historyPrev()
	if got := m.input.Value(); got != "second" {
		t.Errorf("historyPrev after end = %q, want second", got)
	}
}

func TestRenderTranscriptKeepsNewestAtBottom(t *testing.T) {
	m := newTestModel()
	m.width = 60
	m.appendMessage(message{role: "user", content: "first"})
	m.appendMessage(message{role: "assistant", content: "second"})
	lines := m.renderTranscript(60, 4)
	if len(lines) == 0 {
		t.Fatal("no lines rendered")
	}
	// The transcript is bottom-anchored: the last line is the separator after
	// the assistant message, and the assistant content appears before it.
	joined := ""
	for _, l := range lines {
		joined += l + "\n"
	}
	if !strings.Contains(joined, "second") {
		t.Errorf("newest message missing:\n%s", joined)
	}
	if !strings.Contains(joined, "first") {
		t.Errorf("older message missing:\n%s", joined)
	}
}

func TestCycleEffort(t *testing.T) {
	m := newTestModel()
	m.rt.Model = &llm.Client{ReasoningEffort: "low"}
	steps := []string{"medium", "xhigh", "low"}
	for _, want := range steps {
		m.cycleEffort()
		if got := m.rt.Model.GetReasoningEffort(); got != want {
			t.Errorf("cycle = %q, want %q", got, want)
		}
	}
}

func TestToolsCollapsedRendering(t *testing.T) {
	m := newTestModel()
	m.width = 60
	msg := message{
		role:    "assistant",
		content: "done",
		tools:   []string{"shell · 12B · 5ms", "read_file · 30B · 2ms", "write_file · 45B · 8ms"},
	}

	// Expanded: each tool gets its own line.
	m.toolsCollapsed = false
	lines := m.renderMessage(msg, 60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "shell · 12B · 5ms") {
		t.Errorf("expanded: missing individual tool line:\n%s", joined)
	}
	if !strings.Contains(joined, "read_file · 30B · 2ms") {
		t.Errorf("expanded: missing individual tool line:\n%s", joined)
	}
	if !strings.Contains(joined, "write_file · 45B · 8ms") {
		t.Errorf("expanded: missing individual tool line:\n%s", joined)
	}

	// Collapsed: single summary line with the last tool call's details.
	m.toolsCollapsed = true
	lines = m.renderMessage(msg, 60)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "3 tool calls") {
		t.Errorf("collapsed: missing summary:\n%s", joined)
	}
	if !strings.Contains(joined, "last: write_file · 45B · 8ms") {
		t.Errorf("collapsed: missing last tool call details:\n%s", joined)
	}
	if strings.Contains(joined, "shell · 12B · 5ms") {
		t.Errorf("collapsed: should not show individual tool lines:\n%s", joined)
	}
	if strings.Contains(joined, "read_file · 30B · 2ms") {
		t.Errorf("collapsed: should not show non-last tool lines:\n%s", joined)
	}
}

func TestToggleTools(t *testing.T) {
	m := newTestModel()
	if m.toolsCollapsed {
		t.Fatal("initial state should be expanded")
	}
	m.toggleTools()
	if !m.toolsCollapsed {
		t.Fatal("after toggle should be collapsed")
	}
	m.toggleTools()
	if m.toolsCollapsed {
		t.Fatal("after second toggle should be expanded")
	}
}

func TestToggleHelp(t *testing.T) {
	m := newTestModel()
	if m.helpOpen {
		t.Fatal("initial state should be closed")
	}
	m.toggleHelp()
	if !m.helpOpen {
		t.Fatal("after toggle should be open")
	}
	m.toggleHelp()
	if m.helpOpen {
		t.Fatal("after second toggle should be closed")
	}
}

func TestHelpInRightColumnView(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 24
	m.helpOpen = true

	view := m.View()
	if !strings.Contains(view.Content, "Keybindings") {
		t.Errorf("expected view to contain Keybindings heading, got:\n%s", view.Content)
	}
	if !strings.Contains(view.Content, "ctrl+/") {
		t.Errorf("expected view to contain help key ctrl+/, got:\n%s", view.Content)
	}
	if !strings.Contains(m.statusLine(), "help") {
		t.Errorf("status line should show help indicator: %s", m.statusLine())
	}
}

func TestEscClosesHelp(t *testing.T) {
	m := newTestModel()
	m.helpOpen = true
	m2, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected no quit cmd when closing help, got: %v", cmd)
	}
	m = m2.(model)
	if m.helpOpen {
		t.Fatal("esc should close helpOpen")
	}
}

func TestDynamicInputHeight(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 30
	if m.inputH != 1 {
		t.Fatalf("initial inputH = %d, want 1", m.inputH)
	}

	m.input.SetValue("line 1\nline 2")
	m.syncInputHeight()
	if m.inputH != 2 {
		t.Errorf("inputH for 2 lines = %d, want 2", m.inputH)
	}

	m.input.SetValue("line 1\nline 2\nline 3\nline 4\nline 5")
	m.syncInputHeight()
	if m.inputH != 5 {
		t.Errorf("inputH for 5 lines = %d, want 5", m.inputH)
	}
}

func TestDynamicInputHeightMaxClamped(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 30

	lines15 := strings.Repeat("line\n", 14) + "last"
	m.input.SetValue(lines15)
	m.syncInputHeight()
	if m.inputH != maxInputHeight {
		t.Errorf("inputH for 15 lines = %d, want %d (maxInputHeight)", m.inputH, maxInputHeight)
	}
}

func TestDynamicInputHeightShrinkOnSubmit(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 30

	m.input.SetValue("line 1\nline 2\nline 3")
	m.syncInputHeight()
	if m.inputH != 3 {
		t.Fatalf("inputH before submit = %d, want 3", m.inputH)
	}

	m2, _ := m.submit()
	m = m2.(model)
	if m.inputH != 1 {
		t.Errorf("inputH after submit = %d, want 1", m.inputH)
	}
}

func TestShiftEnterRendering(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 20

	m.input.SetValue("hello")
	m.syncInputHeight()

	// Trigger shift+enter via handleKey
	m2, _ := m.handleKey(tea.KeyPressMsg{Text: "\n", Code: tea.KeyEnter, Mod: tea.ModShift})
	m = m2.(model)

	if m.inputH != 2 {
		t.Errorf("inputH = %d, want 2", m.inputH)
	}
	if offset := m.input.ScrollYOffset(); offset != 0 {
		t.Errorf("ScrollYOffset = %d, want 0", offset)
	}

	view := m.View()
	if !strings.Contains(view.Content, "hello") {
		t.Errorf("view missing first line content 'hello':\n%s", view.Content)
	}
	if !strings.Contains(view.Content, "> ") {
		t.Errorf("view missing prompt '> ':\n%s", view.Content)
	}
}
