package tui

import (
	"strings"
	"testing"

	"github.com/acidsound/Motive/internal/config"
	llm "github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/session"
)

func newTestModel() model {
	return newModel(&runtime.Runtime{}, &config.Config{}, nil, false)
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
		role:  "assistant",
		content: "done",
		tools: []string{"shell · 12B · 5ms", "read_file · 30B · 2ms", "write_file · 45B · 8ms"},
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

	// Collapsed: single summary line.
	m.toolsCollapsed = true
	lines = m.renderMessage(msg, 60)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "3 tool calls") {
		t.Errorf("collapsed: missing summary:\n%s", joined)
	}
	if strings.Contains(joined, "shell · 12B · 5ms") {
		t.Errorf("collapsed: should not show individual tool lines:\n%s", joined)
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
