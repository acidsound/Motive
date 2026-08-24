package tui

import (
	"strconv"
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
	steps := []string{"medium", "high", "xhigh", "max", "low"}
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

	// Collapsed: summary line plus the most recent calls; older ones fold
	// into the "done" count.
	m.toolsCollapsed = true
	lines = m.renderMessage(msg, 60)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "3 tool calls") {
		t.Errorf("collapsed: missing summary:\n%s", joined)
	}
	if !strings.Contains(joined, "✓ 1 done") {
		t.Errorf("collapsed: missing done count:\n%s", joined)
	}
	if !strings.Contains(joined, "read_file · 30B · 2ms") || !strings.Contains(joined, "write_file · 45B · 8ms") {
		t.Errorf("collapsed: recent tool lines should stay visible:\n%s", joined)
	}
	if strings.Contains(joined, "shell · 12B · 5ms") {
		t.Errorf("collapsed: oldest tool should fold into summary:\n%s", joined)
	}
}

func TestExpandedToolArgsPreview(t *testing.T) {
	m := newTestModel()
	m.toolsCollapsed = false
	msg := message{
		role:     "assistant",
		tools:    []string{"read_file · 30B · 2ms"},
		toolArgs: []string{`path=internal/tui/tui.go`},
	}
	joined := strings.Join(m.renderMessage(msg, 80), "\n")
	if !strings.Contains(joined, "path=internal/tui/tui.go") {
		t.Errorf("expanded: missing args preview:\n%s", joined)
	}

	// Collapsed view must not show the args preview.
	m.toolsCollapsed = true
	joined = strings.Join(m.renderMessage(msg, 80), "\n")
	if strings.Contains(joined, "path=internal/tui/tui.go") {
		t.Errorf("collapsed: args preview should be hidden:\n%s", joined)
	}
}

func TestToolArgsPreviewFormat(t *testing.T) {
	got := toolArgsPreview("shell", `{"command":"go test ./...","timeout":30}`)
	if got != `command=go test ./... timeout=30` {
		t.Errorf("unexpected preview: %q", got)
	}
	if got := toolArgsPreview("x", `{}`); got != "" {
		t.Errorf("empty args should yield empty preview, got %q", got)
	}
	if got := toolArgsPreview("x", strings.Repeat("a", 200)); len(got) > 130 {
		t.Errorf("long args should be truncated, got %d chars", len(got))
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
	m = *m2.(*model)
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

func TestVisualLineCountWrapsLongLine(t *testing.T) {
	m := newTestModel()
	m.width = 30
	m.height = 30
	m.input.SetWidth(m.width)

	// A short line stays one visual row.
	m.input.SetValue("hi")
	if got := m.visualLineCount(); got != 1 {
		t.Errorf("visualLineCount for short line = %d, want 1", got)
	}

	// A line longer than the input width wraps into multiple rows. With the
	// 2-column prompt the editable width is 28, so 80 characters fill 3 rows.
	m.input.SetValue(strings.Repeat("a", 80))
	if got := m.visualLineCount(); got != 3 {
		t.Errorf("visualLineCount for 80 chars at width 30 = %d, want 3", got)
	}

	// Empty input counts as a single row so the prompt line stays visible.
	m.input.SetValue("")
	if got := m.visualLineCount(); got != 1 {
		t.Errorf("visualLineCount for empty input = %d, want 1", got)
	}
}

func TestLongLineExpandsInputHeight(t *testing.T) {
	m := newTestModel()
	m.width = 30
	m.height = 30
	m.input.SetWidth(m.width)
	m.syncInputHeight()
	if m.inputH != 1 {
		t.Fatalf("initial inputH = %d, want 1", m.inputH)
	}

	// Typing past the terminal width must soft-wrap and grow the input box so
	// the wrapped content (and the cursor) stay visible.
	for _, ch := range strings.Repeat("a", 80) {
		m.input, _ = m.input.Update(tea.KeyPressMsg{Text: string(ch)})
	}
	m.syncInputHeight()
	if m.inputH != 3 {
		t.Errorf("inputH after 80 chars at width 30 = %d, want 3", m.inputH)
	}

	// The textarea view must actually render the wrapped rows (more than the
	// single-row box we had before the fix).
	if view := m.input.View(); strings.Count(view, "\n") < 2 {
		t.Errorf("input view should span multiple wrapped rows, got %q", view)
	}

	// Clearing the input must shrink the box back to a single row.
	m.input.SetValue("")
	m.syncInputHeight()
	if m.inputH != 1 {
		t.Errorf("inputH after clearing = %d, want 1", m.inputH)
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
	m = *m2.(*model)
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
	m = *m2.(*model)

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

func TestPageDownRecoversAfterExcessivePageUp(t *testing.T) {
	m := newTestModel()
	m.width = 60
	m.height = 20
	// Transcript much taller than the viewport so it is scrollable. Each
	// message is distinct so a moving viewport is detectable.
	for i := 0; i < 40; i++ {
		m.appendMessage(message{role: "user", content: "message " + strconv.Itoa(i)})
	}

	// Simulate hammering PageUp far beyond the actual transcript top: the
	// scroll offset becomes huge and would take many PageDown presses to
	// drain if it were not clamped during render.
	m.scroll = 10000

	// Rendering must clamp the offset to the true top of the transcript.
	topView := m.View().Content
	if m.scroll >= 10000 {
		t.Fatalf("scroll not clamped by render: scroll = %d", m.scroll)
	}

	// A single PageDown must immediately move the viewport off the top.
	m.scroll = max(0, m.scroll-m.height/2)
	downView := m.View().Content

	if topView == downView {
		t.Fatalf("viewport did not move after a single PageDown following excessive PageUp")
	}
}

func TestScrollPositionIndicator(t *testing.T) {
	m := newTestModel()
	m.width = 60
	m.height = 20
	for i := 0; i < 40; i++ {
		m.appendMessage(message{role: "user", content: "message " + strconv.Itoa(i)})
	}

	// Pinned to the bottom: no indicator.
	m.scroll = 0
	m.View()
	if pos := m.scrollPosition(); pos != "" {
		t.Errorf("scrollPosition at bottom = %q, want empty", pos)
	}

	// Scrolled up partway: indicator shows the first visible line over total.
	m.scroll = 30
	m.View()
	pos := m.scrollPosition()
	if pos == "" {
		t.Fatal("scrollPosition while scrolled up should not be empty")
	}
	if !strings.Contains(pos, "/"+strconv.Itoa(m.transcriptTotal)) {
		t.Errorf("scrollPosition %q should reference the total %d", pos, m.transcriptTotal)
	}
	if !strings.HasPrefix(pos, "↑ ") {
		t.Errorf("scrollPosition %q should start with the ↑ marker", pos)
	}

	// Scrolled to the very top.
	m.scroll = 10000
	m.View()
	if m.transcriptTop != 0 {
		t.Fatalf("transcriptTop = %d, want 0 at top", m.transcriptTop)
	}
	if got := m.scrollPosition(); got != "↑ 1/"+strconv.Itoa(m.transcriptTotal) {
		t.Errorf("scrollPosition at top = %q, want ↑ 1/%d", got, m.transcriptTotal)
	}
}

func TestScrollPositionInInputArea(t *testing.T) {
	m := newTestModel()
	m.width = 60
	m.height = 20
	for i := 0; i < 40; i++ {
		m.appendMessage(message{role: "user", content: "message " + strconv.Itoa(i)})
	}

	// While pinned to the newest content at the bottom the indicator must not
	// appear anywhere in the rendered view.
	m.scroll = 0
	view := m.View()
	if strings.Contains(view.Content, "↑ ") {
		t.Errorf("view shows scroll position while at bottom:\n%s", view.Content)
	}

	// While scrolled up, the indicator should be rendered into the input box's
	// bottom line so the current line / total lines are visible next to where
	// the user types.
	m.scroll = 30
	view = m.View()
	want := "↑ " + strconv.Itoa(m.transcriptTop+1) + "/" + strconv.Itoa(m.transcriptTotal)
	if !strings.Contains(view.Content, want) {
		t.Errorf("view missing scroll position %q:\n%s", want, view.Content)
	}
}

func TestFullLogCapturesReasoningWithoutTranscriptLeak(t *testing.T) {
	s, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.New()
	if err != nil {
		t.Fatal(err)
	}
	m := newTestModel()
	m.sess = s
	m.sessionID = id
	m.messages = []message{{role: "assistant"}}

	event := runtime.TraceEvent{
		Kind:      "delta",
		Step:      1,
		MaxSteps:  4,
		Text:      "visible",
		Reasoning: "hidden reasoning",
	}
	m.handleTrace(event)

	full, err := s.LoadFull(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 1 {
		t.Fatalf("full events = %d, want 1", len(full))
	}
	if full[0].Kind != "trace.delta" || full[0].Reasoning != "hidden reasoning" {
		t.Fatalf("full event = %+v", full[0])
	}

	if err := s.Append(id, session.Entry{Role: "assistant", Content: "visible"}); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Reasoning, "hidden reasoning") || strings.Contains(entry.Content, "hidden reasoning") {
			t.Fatal("full-log reasoning leaked into model-visible transcript")
		}
	}
}


func TestRecoveryCommandStartsEvidenceBackedExecution(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 24
	m.input.SetValue("/recovery")

	m2, _ := m.submit()
	m = *m2.(*model)

	if len(m.messages) < 2 {
		t.Fatalf("messages = %d, want user + assistant", len(m.messages))
	}
	if got := m.messages[len(m.messages)-2].content; got != recoveryBootstrap {
		t.Fatalf("recovery request = %q, want recovery bootstrap", got)
	}
	if m.messages[len(m.messages)-2].content == recoveryRequest {
		t.Fatal("literal /recovery leaked into model request")
	}
}

func TestFailedTurnSuggestsRecovery(t *testing.T) {
	m := newTestModel()
	m.messages = []message{{role: "assistant"}}

	m2, _ := m.finishTurn(doneMsg{err: fmt.Errorf("model unavailable")})
	m = *m2.(*model)

	if !strings.Contains(m.notice, "/recovery") {
		t.Fatalf("notice = %q, want /recovery suggestion", m.notice)
	}
}
