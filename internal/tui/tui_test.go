package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/acidsound/Motive/internal/config"
	llm "github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/session"
	"github.com/acidsound/Motive/internal/workspace"
	"github.com/charmbracelet/x/ansi"
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

// TestToolsToggleHintBothStates verifies the toggle is discoverable from both
// directions: the expanded view advertises the collapse binding and the
// collapsed summary advertises the expand binding.
func TestToolsToggleHintBothStates(t *testing.T) {
	m := newTestModel()
	msg := message{
		role:  "assistant",
		tools: []string{"shell · 12B · 5ms", "read_file · 30B · 2ms"},
	}

	// Expanded (default): the collapse hint is shown.
	m.toolsCollapsed = false
	joined := strings.Join(m.renderMessage(msg, 60), "\n")
	if !strings.Contains(joined, "ctrl+t to collapse") {
		t.Errorf("expanded: missing collapse hint:\n%s", joined)
	}

	// Collapsed: the expand hint replaces it.
	m.toolsCollapsed = true
	joined = strings.Join(m.renderMessage(msg, 60), "\n")
	if !strings.Contains(joined, "ctrl+t to expand") {
		t.Errorf("collapsed: missing expand hint:\n%s", joined)
	}
	if strings.Contains(joined, "ctrl+t to collapse") {
		t.Errorf("collapsed: should not show the collapse hint:\n%s", joined)
	}
}

// TestCtrlTCollapsesExpandedTools verifies the full key path: with tool calls
// currently rendered expanded, ctrl+t collapses them and the view switches to
// the summary with the expand hint.
func TestCtrlTCollapsesExpandedTools(t *testing.T) {
	m := newTestModel()
	m.width = 60
	m.height = 20
	m.appendMessage(message{
		role:  "assistant",
		tools: []string{"shell · 12B · 5ms", "read_file · 30B · 2ms"},
	})

	if m.toolsCollapsed {
		t.Fatal("initial state should be expanded")
	}
	if !strings.Contains(m.View().Content, "ctrl+t to collapse") {
		t.Fatalf("expanded view missing collapse hint:\n%s", m.View().Content)
	}

	m2, _ := m.handleKey(teaKey("ctrl+t"))
	m = *m2.(*model)
	if !m.toolsCollapsed {
		t.Fatal("ctrl+t while expanded should collapse the tools")
	}
	if view := m.View().Content; !strings.Contains(view, "ctrl+t to expand") {
		t.Errorf("collapsed view missing expand hint:\n%s", view)
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

// TestHelpFloatingView verifies the keybindings panel is a floating overlay:
// it is drawn over the top-right of the transcript (which stays visible
// around it) rather than occupying a full-height side column.
func TestHelpFloatingView(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 24
	m.helpOpen = true
	m.appendMessage(message{role: "user", content: "transcript line"})

	view := m.View()
	if !strings.Contains(view.Content, "Keybindings") {
		t.Errorf("expected view to contain Keybindings heading, got:\n%s", view.Content)
	}
	if !strings.Contains(view.Content, "ctrl+/") {
		t.Errorf("expected view to contain help key ctrl+/, got:\n%s", view.Content)
	}
	// The transcript must remain visible beside the floating box (not hidden
	// behind a full-height panel column).
	if !strings.Contains(view.Content, "transcript line") {
		t.Errorf("transcript hidden behind the help panel, got:\n%s", view.Content)
	}
	if !strings.Contains(m.statusLine(), "help") {
		t.Errorf("status line should show help indicator: %s", m.statusLine())
	}
}

// TestHelpBoxHugsContentNoBottomBlank locks in the floating-panel rule: a
// floating box must hug its text. Even when handed far more vertical room than
// it needs, the rendered box is exactly content+2 rows (top + bottom border)
// and never pads blank rows below the content — otherwise it would cover the
// transcript underneath with whitespace.
func TestHelpBoxHugsContentNoBottomBlank(t *testing.T) {
	lines := buildHelpLines(DefaultKeymap())
	const bigH = 200
	box := renderHelpBox(lines, 40, bigH)
	if got, want := len(box), len(lines)+2; got != want {
		t.Fatalf("box height = %d, want %d (content %d + 2 border)", got, want, len(lines))
	}
	// The bottom row is the closing border, and the row above it still carries
	// a keybinding — the box does not cover its own bottom with whitespace.
	if !strings.Contains(ansi.Strip(box[len(box)-1]), "╰") {
		t.Errorf("box bottom row is not the closing border: %q", box[len(box)-1])
	}
	if s := strings.TrimSpace(ansi.Strip(box[len(box)-2])); s == "" {
		t.Errorf("row above the box bottom border is blank (box covers its bottom with whitespace): %q", box[len(box)-2])
	}
}

// TestOverlayLinesPreservesTranscriptBelowBox locks in the floating-overlay
// rule: a box at (x, y) covering w x h must only blank the cells it actually
// covers. Rows below the box's bottom edge (row >= y+h) must keep their full
// transcript content, including the right columns (col >= x) — they must not
// be erased to whitespace.
func TestOverlayLinesPreservesTranscriptBelowBox(t *testing.T) {
	// Each base row is exactly 8 cols: left half (col 0-3) and right half
	// (col 4-7) carry distinct content so a blanked right half is detectable.
	base := []string{"00001111", "22223333", "44445555", "66667777", "88889999"}
	top := []string{"XXXX", "YYYY"} // a 2-row box, 4 cols wide
	got := overlayLines(base, top, 4, 0, 8, 5)
	if len(got) != 5 {
		t.Fatalf("rows = %d, want 5", len(got))
	}
	// Box rows: left transcript kept, box drawn over the right portion.
	if got[0] != "0000XXXX" {
		t.Errorf("row0 = %q, want 0000XXXX", got[0])
	}
	if got[1] != "2222YYYY" {
		t.Errorf("row1 = %q, want 2222YYYY", got[1])
	}
	// Rows below the box: full transcript preserved (right half not blanked).
	for i, want := range []string{"44445555", "66667777", "88889999"} {
		if got[i+2] != want {
			t.Errorf("row%d = %q, want %q (right portion must not be blanked)", i+2, got[i+2], want)
		}
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

// TestWorkspaceLineHiddenWithoutWorkspace: with no runtime workspace the
// workspace line must be empty so the layout is unchanged (e.g. in tests).
func TestWorkspaceLineHiddenWithoutWorkspace(t *testing.T) {
	m := newTestModel()
	if got := m.workspaceLine(80); got != "" {
		t.Errorf("workspaceLine without rt.WS = %q, want empty", got)
	}
}

// TestWorkspaceLineShowsRoot: the workspace root appears on the line, with
// the home directory abbreviated to ~ when the root lives under HOME.
func TestWorkspaceLineShowsRoot(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	m := newTestModel()
	m.rt.WS = &workspace.Workspace{Root: "/home/tester/proj"}

	line := m.workspaceLine(80)
	if !strings.Contains(line, "~/proj") {
		t.Errorf("workspaceLine = %q, want it to contain ~/proj", line)
	}
	if strings.Contains(line, "/home/tester") {
		t.Errorf("workspaceLine = %q, home directory should be abbreviated", line)
	}

	// A root outside HOME is shown in full.
	m.rt.WS.Root = "/srv/other/workspace"
	if line := m.workspaceLine(80); !strings.Contains(line, "/srv/other/workspace") {
		t.Errorf("workspaceLine = %q, want full root outside home", line)
	}
}

// TestWorkspaceLineTruncatesToWidth: an overlong path is truncated from the
// left so the leaf directory stays visible and the line never exceeds the
// terminal width.
func TestWorkspaceLineTruncatesToWidth(t *testing.T) {
	t.Setenv("HOME", "/nonexistent")
	m := newTestModel()
	m.rt.WS = &workspace.Workspace{Root: "/very/long/path/with/many/segments/project"}

	const width = 20
	line := m.workspaceLine(width)
	plain := ansi.Strip(line)
	if ansi.StringWidth(plain) > width {
		t.Errorf("workspaceLine width = %d, want <= %d (%q)", ansi.StringWidth(plain), width, plain)
	}
	if !strings.HasSuffix(strings.TrimSpace(plain), "project") {
		t.Errorf("workspaceLine should keep the leaf directory visible: %q", plain)
	}
	if !strings.HasPrefix(plain, "…") {
		t.Errorf("workspaceLine should start with an ellipsis when truncated: %q", plain)
	}
}

// TestTruncateLeftKeepsTail locks the left-truncation rule: the rightmost
// content survives, the head is replaced by an ellipsis, and the result fits
// max display columns.
func TestTruncateLeftKeepsTail(t *testing.T) {
	if got := truncateLeft("abcdefghij", 6); got != "…fghij" {
		t.Errorf("truncateLeft = %q, want %q", got, "…fghij")
	}
	if got := truncateLeft("short", 80); got != "short" {
		t.Errorf("truncateLeft short = %q, want unchanged", got)
	}
	if got := truncateLeft("short", 0); got != "" {
		t.Errorf("truncateLeft max=0 = %q, want empty", got)
	}
}

// TestWorkspaceLineRenderedAboveStatusBar: in the full view the workspace
// line sits directly above the status bar (which shows the model name) and
// the rendered content stays within the terminal height.
func TestWorkspaceLineRenderedAboveStatusBar(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	m := newTestModel()
	m.rt.WS = &workspace.Workspace{Root: "/home/tester/proj"}
	m.width = 80
	m.height = 24
	m.appendMessage(message{role: "user", content: "hello"})

	view := m.View()
	lines := strings.Split(view.Content, "\n")
	wsIdx := -1
	statusIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "~/proj") {
			wsIdx = i
		}
		if strings.Contains(l, "test-model") {
			statusIdx = i
		}
	}
	if wsIdx < 0 {
		t.Fatalf("workspace line not rendered:\n%s", view.Content)
	}
	if statusIdx < 0 {
		t.Fatalf("status line not rendered:\n%s", view.Content)
	}
	if wsIdx != statusIdx-1 {
		t.Errorf("workspace line at %d, status bar at %d: want workspace directly above status bar", wsIdx, statusIdx)
	}
}

// TestStatusLineCleanLayout locks the status-bar layout: the same segments
// are shown while reasoning and while waiting for input (step capacity,
// tok/s, cache hit, last context, enter-mode steer/queue, revision, session)
// — no "budget" wording, no reshuffle. Unobserved metrics show "–"
// placeholders until the first model response.
// TestCycleQueueModeWhileIdle verifies that ctrl+\ cycles the enter-mode even
// while idle: the mode persists across turns, so the user may pre-select
// steer before submitting the next request.
func TestCycleQueueModeWhileIdle(t *testing.T) {
	m := newTestModel()
	if m.steerMode {
		t.Fatal("initial mode should be queue")
	}

	m2, _ := m.handleKey(teaKey("ctrl+\\"))
	m = *m2.(*model)
	if !m.steerMode {
		t.Fatal("ctrl+\\ while idle should switch to steer mode")
	}
	if !strings.Contains(m.statusLine(), "steer") {
		t.Errorf("idle status line should show steer mode: %s", m.statusLine())
	}

	m2, _ = m.handleKey(teaKey("ctrl+\\"))
	m = *m2.(*model)
	if m.steerMode {
		t.Fatal("second ctrl+\\ should cycle back to queue mode")
	}
}

// TestCycleQueueModeWhileBusy verifies that ctrl+\ still cycles the mode
// while a run is in progress.
func TestCycleQueueModeWhileBusy(t *testing.T) {
	m := newTestModel()
	m.busy = true

	m2, _ := m.handleKey(teaKey("ctrl+\\"))
	m = *m2.(*model)
	if !m.steerMode {
		t.Fatal("ctrl+\\ while busy should switch to steer mode")
	}
	m2, _ = m.handleKey(teaKey("ctrl+\\"))
	m = *m2.(*model)
	if m.steerMode {
		t.Fatal("second ctrl+\\ while busy should cycle back to queue mode")
	}
}

func TestStatusLineCleanLayout(t *testing.T) {
	m := newTestModel()
	m.baseRev = "abcdef1234567890"
	m.sessionID = "20260825-164451-103000"
	m.rt.Budget = runtime.ExecutionBudget{MaxSteps: 64, MaxToolCalls: 128, MaxDuration: 30 * time.Minute}

	// Idle: constant layout with placeholders; the enter-mode segment shows
	// the persisted default (queue) and no budget wording.
	idle := m.statusLine()
	if strings.Contains(idle, "budget") {
		t.Errorf("status line should not show budget wording: %s", idle)
	}
	for _, want := range []string{"64 steps", "tok/s", "cache", "ctx", "queue", "abcdef1", "20260825-164451"} {
		if !strings.Contains(idle, want) {
			t.Errorf("idle status line missing %q: %s", want, idle)
		}
	}
	if strings.Contains(idle, "steer") {
		t.Errorf("idle status line should not show steer mode: %s", idle)
	}
	if strings.Contains(idle, "20260825-164451-103000") {
		t.Errorf("status line should abbreviate the session id: %s", idle)
	}

	// Busy: the same segments stay, no reshuffle.
	m.busy = true
	m.maxSteps = 64
	m.elapsed = 45 * time.Second
	busy := m.statusLine()
	for _, want := range []string{"64 steps", "tok/s", "cache", "ctx", "queue", "abcdef1", "20260825-164451"} {
		if !strings.Contains(busy, want) {
			t.Errorf("busy status line missing %q: %s", want, busy)
		}
	}

	// Steer mode while busy: the mode flips to steer, and a non-empty queue
	// adds its count (steer falls back to the queue when the channel is full).
	m.steerMode = true
	m.queue = []string{"q1", "q2"}
	steer := m.statusLine()
	for _, want := range []string{"steer", "queue 2"} {
		if !strings.Contains(steer, want) {
			t.Errorf("steer status line missing %q: %s", want, steer)
		}
	}

	// After a model response: live metrics replace the placeholders.
	m.tokPerSec = 45.2
	m.cacheHit = 87.4
	m.lastCtx = 12345
	live := m.statusLine()
	for _, want := range []string{"45.2 tok/s", "cache 87%", "ctx 12.3k"} {
		if !strings.Contains(live, want) {
			t.Errorf("status line missing %q: %s", want, live)
		}
	}
}

// TestBuildSessionItemsPinsNewSessionFirst locks the session-picker contract:
// the "New session" entry is always the first row, even with no stored
// sessions, so a fresh session is reachable from the picker at all times.
func TestBuildSessionItemsPinsNewSessionFirst(t *testing.T) {
	items := buildSessionItems([]session.Summary{{ID: "abc", Preview: "p"}})
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	first, ok := items[0].(pickerItem)
	if !ok {
		t.Fatalf("item 0 = %T, want pickerItem", items[0])
	}
	if _, ok := first.value.(newSessionItem); !ok {
		t.Fatalf("item 0 value = %T, want newSessionItem", first.value)
	}
	if !strings.Contains(first.title, "New session") {
		t.Errorf("item 0 title = %q, want New session", first.title)
	}

	// No stored sessions: the New session entry is still present.
	empty := buildSessionItems(nil)
	if len(empty) != 1 {
		t.Fatalf("items without sessions = %d, want 1", len(empty))
	}
	if _, ok := empty[0].(pickerItem); !ok {
		t.Fatalf("empty item 0 = %T, want pickerItem", empty[0])
	}
}

// TestApplyPickerSelectionNewSession verifies the full picker flow: opening
// the session picker and selecting the pinned "New session" row resets the
// conversation to a fresh session and closes the overlay.
func TestApplyPickerSelectionNewSession(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(id, session.Entry{Role: "user", Content: "old", BaseRevision: "abcdef123456"}); err != nil {
		t.Fatal(err)
	}

	m := newTestModel()
	m.sess = s
	m.sessionID = id
	m.rt.SessionID = id
	m.messages = []message{{role: "user", content: "old"}}
	m.history = []string{"old"}
	m.baseRev = "abcdef123456"
	m.resultRev = "fedcba654321"
	m.queue = []string{"q"}
	m.width = 80
	m.height = 24

	m.openPicker(overlaySessionPicker)
	if m.overlay != overlaySessionPicker {
		t.Fatal("session picker did not open")
	}
	// The cursor starts on the pinned "New session" row.
	m2, _ := m.applyPickerSelection()
	m = *m2.(*model)

	if m.overlay != overlayNone {
		t.Fatal("picker should close after selection")
	}
	if m.sessionID != "" {
		t.Errorf("sessionID = %q, want empty", m.sessionID)
	}
	if m.rt.SessionID != "" {
		t.Errorf("rt.SessionID = %q, want empty", m.rt.SessionID)
	}
	if len(m.messages) != 0 {
		t.Errorf("messages = %d, want 0", len(m.messages))
	}
	if len(m.history) != 0 {
		t.Errorf("history = %d, want 0", len(m.history))
	}
	if m.baseRev != "" || m.resultRev != "" {
		t.Errorf("revisions not cleared: %q %q", m.baseRev, m.resultRev)
	}
	if len(m.queue) != 0 {
		t.Errorf("queue = %d, want 0", len(m.queue))
	}
}

// TestNewSessionKeybinding verifies the direct alt+n shortcut: it resets the
// conversation while idle and is a no-op while a run is in progress.
func TestNewSessionKeybinding(t *testing.T) {
	m := newTestModel()
	m.sessionID = "abc"
	m.rt.SessionID = "abc"
	m.messages = []message{{role: "user", content: "x"}}

	m2, _ := m.handleKey(teaKey("alt+n"))
	m = *m2.(*model)
	if m.sessionID != "" {
		t.Errorf("sessionID = %q, want empty", m.sessionID)
	}
	if m.rt.SessionID != "" {
		t.Errorf("rt.SessionID = %q, want empty", m.rt.SessionID)
	}
	if len(m.messages) != 0 {
		t.Errorf("messages = %d, want 0", len(m.messages))
	}
	if !strings.Contains(m.notice, "New session") {
		t.Errorf("notice = %q, want New session confirmation", m.notice)
	}

	// While busy the key must be ignored: the active session is preserved.
	m.busy = true
	m.sessionID = "def"
	m.rt.SessionID = "def"
	m2, _ = m.handleKey(teaKey("alt+n"))
	m = *m2.(*model)
	if m.sessionID != "def" {
		t.Errorf("sessionID = %q, want def (key ignored while busy)", m.sessionID)
	}
}
