package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/acidsound/Motive/internal/config"
	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/session"
)

const (
	colorPrompt    = "#7aa2f7"
	colorUser      = "#7aa2f7"
	colorAssistant = "#c0caf5"
	colorReasoning = "#565f89"
	colorTool      = "#7dcfff"
	colorError     = "#f7768e"
	colorStatusBg  = "#1a1b26"
	colorStatusFg  = "#a9b1d6"
	colorIdle      = "#9ece6a"
	colorEffort    = "#e0af68"
	colorBookmark  = "#e0af68"
	colorScrollPos = "#7dcfff"
)

var (
	stylePrompt    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorPrompt))
	styleUser      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorUser))
	styleAssistant = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAssistant))
	styleReasoning = lipgloss.NewStyle().Faint(true).Italic(true).Foreground(lipgloss.Color(colorReasoning))
	styleTool      = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTool))
	styleToolSum   = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color(colorTool))
	styleError     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorError))
	styleStatus    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorStatusFg)).Background(lipgloss.Color(colorStatusBg))
	styleIdle      = lipgloss.NewStyle().Foreground(lipgloss.Color(colorIdle))
	styleEffort    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorEffort))
	styleBookmark  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorBookmark))
	styleScrollPos = lipgloss.NewStyle().Foreground(lipgloss.Color(colorScrollPos))
)

type streamMsg struct {
	event runtime.TraceEvent
}

type doneMsg struct {
	text string
	err  error
}

type openPickerMsg struct {
	kind overlayKind
}

type overlayKind int

const (
	overlayNone overlayKind = iota
	overlaySessionPicker
	overlayDiff
)

type message struct {
	role      string // user | assistant | error
	content   string
	reasoning string
	tools     []string
	ts        time.Time
	bookmark  bool
}

type model struct {
	rt    *runtime.Runtime
	cfg   *config.Config
	sess  *session.Store
	keys  Keymap
	prog  *tea.Program
	input textarea.Model
	spin  spinner.Model

	messages  []message
	history   []string
	histIdx   int
	busy      bool
	sessionID string

	width  int
	height int
	inputH int
	scroll int

	// Scroll position of the transcript viewport, refreshed on each render so
	// the input area can report where in the full transcript the user is
	// currently looking.
	transcriptTotal int // total rendered transcript lines
	transcriptTop   int // 0-based index of the first visible line
	transcriptAvail int // visible viewport height

	step      int
	maxSteps  int
	toolCalls int
	elapsed   time.Duration
	baseRev   string
	resultRev string

	overlay    overlayKind
	list       list.Model
	diffLines  []string
	diffScroll int

	helpOpen bool

	toolsCollapsed bool

	startPicker bool
}

const maxInputHeight = 10

func (m *model) syncInputHeight() {
	lines := m.input.LineCount()
	if lines < 1 {
		lines = 1
	}
	maxH := maxInputHeight
	if m.height > 0 {
		statusH := lineCount(m.statusLine())
		availForInput := m.height - statusH - 2
		if availForInput < 1 {
			availForInput = 1
		}
		if maxH > availForInput {
			maxH = availForInput
		}
	}
	h := min(lines, maxH)
	if h < 1 {
		h = 1
	}
	m.inputH = h
	m.input.SetHeight(h)

	// Ensure the viewport scroll position stays optimal:
	// When height expands, textarea's viewport offset may remain scrolled down
	// even though all lines fit, hiding earlier lines (like line 0 and '>').
	if m.input.ScrollYOffset() > 0 {
		if lines <= h || lines-m.input.ScrollYOffset() < h {
			savedLine := m.input.Line()
			savedCol := m.input.Column()
			m.input.MoveToBegin()
			for i := 0; i < savedLine; i++ {
				m.input.CursorDown()
			}
			m.input.SetCursorColumn(savedCol)
		}
	}
}

func newModel(rt *runtime.Runtime, cfg *config.Config, sess *session.Store, startPicker bool) model {
	input := textarea.New()
	input.SetPromptFunc(2, func(p textarea.PromptInfo) string {
		if p.LineNumber == 0 {
			return stylePrompt.Render("> ")
		}
		return "  "
	})
	input.ShowLineNumbers = false
	input.SetVirtualCursor(false)
	input.SetStyles(textarea.DefaultStyles(true))
	input.Focus()
	input.KeyMap.InsertNewline.SetEnabled(false)

	spin := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color(colorPrompt))),
	)

	keys := DefaultKeymap()
	keys.ApplyEnv()

	m := model{
		rt:          rt,
		cfg:         cfg,
		sess:        sess,
		keys:        keys,
		input:       input,
		spin:        spin,
		inputH:      1,
		startPicker: startPicker,
	}
	m.syncInputHeight()
	return m
}

// Run starts the terminal UI, forwarding every runtime trace event into the
// Bubble Tea update loop so the transcript and status bar update live.
func Run(rt *runtime.Runtime, cfg *config.Config, sess *session.Store, startPicker bool) error {
	m := newModel(rt, cfg, sess, startPicker)
	p := tea.NewProgram(&m)
	m.prog = p
	prevTrace := rt.Trace
	rt.Trace = func(event runtime.TraceEvent) {
		if prevTrace != nil {
			prevTrace(event)
		}
		p.Send(streamMsg{event: event})
	}
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, m.spin.Tick}
	if m.startPicker {
		cmds = append(cmds, func() tea.Msg { return openPickerMsg{kind: overlaySessionPicker} })
	}
	return tea.Batch(cmds...)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case doneMsg:
		return m.finishTurn(msg)

	case streamMsg:
		return m.handleTrace(msg.event)

	case openPickerMsg:
		m.openPicker(msg.kind)
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(max(1, msg.Width))
		m.syncInputHeight()
		if m.overlay == overlaySessionPicker {
			m.list.SetSize(max(40, m.width-6), max(10, m.height-8))
		}

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	if m.overlay == overlaySessionPicker {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncInputHeight()
	return m, cmd
}

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.overlay != overlayNone {
		return m.handleOverlayKey(msg)
	}
	switch msg.String() {
	case string(m.keys.Quit), "esc", "ctrl+c":
		if m.busy {
			return m, nil
		}
		if m.helpOpen {
			m.helpOpen = false
			return m, nil
		}
		return m, tea.Quit

	case string(m.keys.Run):
		if m.busy {
			return m, nil
		}
		return m.submit()

	case string(m.keys.Newline):
		m.input.InsertString("\n")
		m.syncInputHeight()
		return m, nil

	case string(m.keys.CycleEffort):
		if m.busy {
			return m, nil
		}
		m.cycleEffort()
		return m, nil

	case string(m.keys.SessionPicker):
		if m.busy {
			return m, nil
		}
		m.openPicker(overlaySessionPicker)
		return m, nil

	case string(m.keys.DiffToggle):
		m.openDiff()
		return m, nil

	case string(m.keys.ToolsToggle):
		m.toggleTools()
		return m, nil

	case string(m.keys.Help):
		m.toggleHelp()
		return m, nil

	case string(m.keys.ScrollUp):
		m.scroll += 2
		return m, nil

	case string(m.keys.ScrollDown):
		if m.scroll > 0 {
			m.scroll -= 2
		}
		return m, nil

	case string(m.keys.PageUp):
		m.scroll += m.height / 2
		return m, nil

	case string(m.keys.PageDown):
		if m.scroll > 0 {
			m.scroll = max(0, m.scroll-m.height/2)
		}
		return m, nil

	case string(m.keys.HistoryUp):
		if !m.busy && m.input.Value() == "" {
			m.historyPrev()
			return m, nil
		}

	case string(m.keys.HistoryDown):
		if !m.busy && m.input.Value() == "" {
			m.historyNext()
			return m, nil
		}

	case string(m.keys.Bookmark):
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			last.bookmark = !last.bookmark
		}
		return m, nil

	case string(m.keys.Clear):
		if m.busy {
			return m, nil
		}
		m.messages = nil
		m.scroll = 0
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncInputHeight()
	return m, cmd
}

func (m *model) handleOverlayKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.overlay = overlayNone
		return m, nil
	}
	switch m.overlay {
	case overlaySessionPicker:
		if key == string(m.keys.Run) {
			return m.applyPickerSelection()
		}
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd

	case overlayDiff:
		switch key {
		case string(m.keys.ScrollUp), "up":
			m.diffScroll = max(0, m.diffScroll-1)
		case string(m.keys.ScrollDown), "down":
			m.diffScroll++
		case string(m.keys.PageUp):
			m.diffScroll = max(0, m.diffScroll-m.height/2)
		case string(m.keys.PageDown):
			m.diffScroll += m.height / 2
		}
		return m, nil
	}
	return m, nil
}

// submit starts a new execution turn: records history, appends transcript
// entries, opens the assistant message the stream fills, and persists the user
// request to the current session.
func (m *model) submit() (tea.Model, tea.Cmd) {
	request := strings.TrimSpace(m.input.Value())
	if request == "" {
		return m, nil
	}
	m.input.Reset()
	m.syncInputHeight()
	m.histIdx = len(m.history)
	m.history = append(m.history, request)

	if m.rt != nil && m.rt.WS != nil {
		m.baseRev = m.rt.WS.GitHEAD()
	}
	m.resultRev = ""
	m.appendMessage(message{role: "user", content: request, ts: time.Now()})

	m.busy = true
	m.step = 0
	if m.rt != nil {
		m.maxSteps = m.rt.Budget.MaxSteps
		m.rt.Stream = true
	}
	m.toolCalls = 0
	m.elapsed = 0
	m.scroll = 0

	if m.sess != nil && m.sessionID == "" {
		if id, err := m.sess.New(); err == nil {
			m.sessionID = id
		}
	}
	if m.sess != nil && m.sessionID != "" {
		_ = m.sess.Append(m.sessionID, session.Entry{
			TS:           time.Now(),
			Role:         "user",
			Content:      request,
			BaseRevision: m.baseRev,
		})
	}

	m.appendMessage(message{role: "assistant", ts: time.Now()})
	return m, execute(m.rt, request)
}

func (m *model) finishTurn(done doneMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	if done.err != nil {
		// Drop the empty assistant placeholder that submit() appended so an
		// error turn does not leave a phantom blank message in the transcript.
		if n := len(m.messages); n > 0 && m.messages[n-1].role == "assistant" && !m.lastAssistantActive() {
			m.messages = m.messages[:n-1]
		}
		m.appendMessage(message{role: "error", content: done.err.Error(), ts: time.Now()})
		if m.sess != nil && m.sessionID != "" {
			_ = m.sess.Append(m.sessionID, session.Entry{
				TS:             time.Now(),
				Role:           "error",
				Content:        done.err.Error(),
				BaseRevision:   m.baseRev,
				ResultRevision: m.resultRev,
				ElapsedMS:      m.elapsed.Milliseconds(),
			})
		}
		return m, nil
	}
	if text := strings.TrimSpace(done.text); text != "" && !m.lastAssistantActive() {
		// Fill the assistant slot that submit() opened instead of appending
		// a duplicate message.
		slot := m.assistantSlot()
		slot.content = text
	}
	m.saveAssistantEntry()
	m.scroll = 0
	return m, nil
}

func (m *model) saveAssistantEntry() {
	if m.sess == nil || m.sessionID == "" || len(m.messages) == 0 {
		return
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "assistant" {
		return
	}
	_ = m.sess.Append(m.sessionID, session.Entry{
		TS:             last.ts,
		Role:           "assistant",
		Content:        last.content,
		Reasoning:      last.reasoning,
		Tools:          last.tools,
		BaseRevision:   m.baseRev,
		ResultRevision: m.resultRev,
		ElapsedMS:      m.elapsed.Milliseconds(),
	})
}

func (m *model) handleTrace(event runtime.TraceEvent) (tea.Model, tea.Cmd) {
	m.elapsed = event.TotalElapsed
	switch event.Kind {
	case "start":
		m.maxSteps = event.MaxSteps
		m.step = 0
		m.toolCalls = 0
		m.baseRev = event.BaseRevision

	case "model_start":
		m.step = event.Step
		m.maxSteps = event.MaxSteps

	case "delta":
		last := m.assistantSlot()
		if event.Text != "" {
			last.content += event.Text
		}
		if event.Reasoning != "" {
			last.reasoning += event.Reasoning
		}

	case "tool":
		m.toolCalls = event.TotalToolCalls
		last := m.assistantSlot()
		last.tools = append(last.tools, fmt.Sprintf("%s · %dB · %s", event.ToolName, event.ToolResultBytes, event.Latency.Round(time.Millisecond)))

	case "finish":
		m.busy = false
		m.resultRev = event.ResultRevision
		m.elapsed = event.TotalElapsed
	}
	return m, nil
}

// assistantSlot returns the live assistant message, creating one if needed.
func (m *model) assistantSlot() *message {
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "assistant" {
		m.appendMessage(message{role: "assistant", ts: time.Now()})
	}
	return &m.messages[len(m.messages)-1]
}

func (m *model) appendMessage(msg message) {
	m.messages = append(m.messages, msg)
}

// lastAssistantActive reports whether the most recent message is an
// assistant message that already carries content. It is a pure predicate:
// it never mutates state.
func (m *model) lastAssistantActive() bool {
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "assistant" {
		return false
	}
	last := m.messages[len(m.messages)-1]
	return strings.TrimSpace(last.content) != "" || strings.TrimSpace(last.reasoning) != "" || len(last.tools) > 0
}

func (m *model) cycleEffort() {
	current := m.rt.Model.GetReasoningEffort()
	order := []string{"low", "medium", "high", "xhigh", "max"}
	next := order[0]
	for i, lvl := range order {
		if lvl == current {
			next = order[(i+1)%len(order)]
			break
		}
	}
	m.rt.Model.SetReasoningEffort(next)
}

func (m *model) openPicker(kind overlayKind) {
	var title string
	var items []list.Item
	switch kind {
	case overlaySessionPicker:
		title = "Resume session"
		if m.sess != nil {
			if summaries, err := m.sess.List(); err == nil {
				items = buildSessionItems(summaries)
			}
		}
	default:
		return
	}
	l := list.New(items, list.NewDefaultDelegate(), max(40, m.width-6), max(10, m.height-8))
	l.Title = title
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(true)
	l.SetFilteringEnabled(false)
	m.list = l
	m.overlay = kind
}

func (m *model) applyPickerSelection() (tea.Model, tea.Cmd) {
	item := m.list.SelectedItem()
	m.overlay = overlayNone
	if item == nil {
		return m, nil
	}
	entry, ok := item.(pickerItem)
	if !ok {
		return m, nil
	}
	if sum, ok := entry.value.(session.Summary); ok {
		m.loadSession(sum.ID)
	}
	return m, nil
}

func (m *model) loadSession(id string) {
	entries, err := m.sess.Load(id)
	if err != nil {
		return
	}
	m.sessionID = id
	m.messages = nil
	m.history = nil
	m.histIdx = 0
	m.scroll = 0
	m.baseRev = ""
	m.resultRev = ""
	for _, e := range entries {
		switch e.Role {
		case "user":
			m.appendMessage(message{role: "user", content: e.Content, ts: e.TS})
		case "assistant":
			m.appendMessage(message{role: "assistant", content: e.Content, reasoning: e.Reasoning, tools: e.Tools, ts: e.TS})
		case "error":
			m.appendMessage(message{role: "error", content: e.Content, ts: e.TS})
		}
		if e.BaseRevision != "" {
			m.baseRev = e.BaseRevision
		}
		if e.ResultRevision != "" {
			m.resultRev = e.ResultRevision
		}
	}
}

func (m *model) historyPrev() {
	if len(m.history) == 0 {
		return
	}
	if m.histIdx > len(m.history)-1 {
		m.histIdx = len(m.history)
	}
	if m.histIdx > 0 {
		m.histIdx--
	}
	m.input.SetValue(m.history[m.histIdx])
	m.input.MoveToEnd()
	m.syncInputHeight()
}

func (m *model) historyNext() {
	if m.histIdx >= len(m.history)-1 {
		m.histIdx = len(m.history)
		m.input.SetValue("")
		m.syncInputHeight()
		return
	}
	m.histIdx++
	m.input.SetValue(m.history[m.histIdx])
	m.input.MoveToEnd()
	m.syncInputHeight()
}

func (m *model) toggleTools() {
	m.toolsCollapsed = !m.toolsCollapsed
}

func (m *model) toggleHelp() {
	m.helpOpen = !m.helpOpen
}

func (m *model) openDiff() {
	diff, err := m.rt.WS.GitDiff()
	if err != nil {
		diff = "git diff unavailable: " + err.Error()
	}
	m.diffLines = colorizeDiff(diff)
	m.diffScroll = 0
	m.overlay = overlayDiff
}

func execute(rt *runtime.Runtime, request string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), rt.Budget.MaxDuration)
		defer cancel()
		result, err := rt.Execute(ctx, request)
		return doneMsg{text: result, err: err}
	}
}

// renderTranscript lays out every message (newest at the bottom), applies the
// scroll offset, and returns the visible lines. It also records the viewport's
// position within the full transcript for the scroll position indicator.
func (m *model) renderTranscript(width, available int) []string {
	var all []string
	for _, msg := range m.messages {
		lines := m.renderMessage(msg, width)
		if msg.bookmark && len(lines) > 0 {
			lines[0] = styleBookmark.Render("📌 ") + lines[0]
		}
		all = append(all, lines...)
		all = append(all, "")
	}
	if m.busy {
		all = append(all, m.spin.View()+" "+styleDim.Render("working…"))
		all = append(all, "")
	}
	start := 0
	if available > 0 {
		// Clamp the scroll offset to the distance to the very top of the
		// transcript. Without this, repeated PageUp/ScrollUp presses inflate
		// the offset unboundedly, and PageDown then needs many presses before
		// the viewport visibly moves again (it appears stuck at the top).
		maxScroll := len(all) - available
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.scroll > maxScroll {
			m.scroll = maxScroll
		}
		start = len(all) - available - m.scroll
		if start < 0 {
			start = 0
		}
	}
	if start > len(all) {
		start = len(all)
	}
	out := all[start:]
	if available > 0 && len(out) > available {
		out = out[:available]
	}
	// Record where the current viewport sits within the full transcript so the
	// input area can report the scroll position.
	m.transcriptTotal = len(all)
	m.transcriptTop = start
	m.transcriptAvail = available
	return out
}

func (m model) renderMessage(msg message, width int) []string {
	switch msg.role {
	case "user":
		return wrapStyled(styleUser.Render("❯ "+msg.content), width)
	case "error":
		return wrapStyled(styleError.Render("✖ "+msg.content), width)
	case "assistant":
		var out []string
		if r := strings.TrimSpace(msg.reasoning); r != "" {
			for _, l := range renderMarkdown(r, width) {
				out = append(out, styleReasoning.Render(l))
			}
		}
		if c := strings.TrimSpace(msg.content); c != "" {
			out = append(out, renderMarkdown(c, width)...)
		}
		if len(msg.tools) > 0 {
			if m.toolsCollapsed {
				out = append(out, styleToolSum.Render(m.collapsedToolsSummary(msg.tools)))
			} else {
				for _, t := range msg.tools {
					out = append(out, styleTool.Render("→ "+t))
				}
			}
		}
		if len(out) == 0 && m.busy {
			out = append(out, styleReasoning.Render("…"))
		}
		return out
	}
	return wrapStyled(msg.content, width)
}

// collapsedToolsSummary builds the one-line summary shown for a message's
// tool calls while they are collapsed: the total count plus the most recent
// call's details, so the latest activity stays visible without expanding.
func (m model) collapsedToolsSummary(tools []string) string {
	summary := fmt.Sprintf("⏺ %d tool calls", len(tools))
	if last := tools[len(tools)-1]; last != "" {
		summary += " · last: " + last
	}
	return summary + " (" + string(m.keys.ToolsToggle) + " to expand)"
}

func (m model) statusLine() string {
	var b strings.Builder
	if m.busy {
		b.WriteString(m.spin.View() + " ")
	} else {
		b.WriteString(styleIdle.Render("●") + " ")
	}
	if m.rt != nil && m.rt.Model != nil {
		b.WriteString(m.rt.Model.Model)
		b.WriteString(" · " + styleEffort.Render("effort "+m.rt.Model.GetReasoningEffort()))
	}
	if m.busy {
		b.WriteString(fmt.Sprintf(" · step %d/%d · tools %d · %s", m.step, m.maxSteps, m.toolCalls, m.elapsed.Round(time.Second)))
	}
	if m.rt != nil {
		b.WriteString(fmt.Sprintf(" · budget %d steps / %d tools / %s", m.rt.Budget.MaxSteps, m.rt.Budget.MaxToolCalls, m.rt.Budget.MaxDuration.Round(time.Minute)))
	}
	if rev := m.revisionLabel(); rev != "" {
		b.WriteString(" · " + rev)
	}
	if m.sessionID != "" {
		b.WriteString(" · " + m.sessionID)
	}
	if m.helpOpen {
		b.WriteString(" · help")
	}
	if m.toolsCollapsed {
		b.WriteString(" · tools⏷")
	}
	return styleStatus.Render(b.String())
}

// scrollPosition reports the current viewport location within the full
// transcript, e.g. "↑ 12/456" where 12 is the first visible line of 456.
// It returns an empty string while the viewport is pinned to the newest
// content at the bottom, so the indicator only appears while scrolled up.
func (m model) scrollPosition() string {
	if m.transcriptTotal <= 0 || m.transcriptAvail <= 0 {
		return ""
	}
	atBottom := m.scroll == 0 && m.transcriptTop+m.transcriptAvail >= m.transcriptTotal
	if atBottom {
		return ""
	}
	top := m.transcriptTop + 1
	if top < 1 {
		top = 1
	}
	return fmt.Sprintf("↑ %d/%d", top, m.transcriptTotal)
}

// renderInputView overlays the transcript scroll position at the right edge of
// the input box's last line so it is visible exactly where the user types while
// paging or scrolling through older content. It leaves the input untouched when
// the viewport is pinned to the newest content at the bottom.
func (m model) renderInputView(inputView string) string {
	pos := m.scrollPosition()
	if pos == "" || inputView == "" || m.width <= 0 {
		return inputView
	}
	lines := strings.Split(inputView, "\n")
	if len(lines) == 0 {
		return inputView
	}
	marker := styleScrollPos.Render(pos)
	last := lines[len(lines)-1]
	lastW := lipgloss.Width(last)
	markerW := lipgloss.Width(marker)
	avail := m.width - lastW - markerW - 1
	if avail < 1 {
		return inputView
	}
	lines[len(lines)-1] = last + strings.Repeat(" ", avail) + marker
	return strings.Join(lines, "\n")
}

func (m model) revisionLabel() string {
	base := shortRev(m.baseRev)
	if base == "" {
		return ""
	}
	if m.resultRev != "" && m.resultRev != m.baseRev {
		return base + "→" + shortRev(m.resultRev)
	}
	return base
}

func (m *model) View() tea.View {
	width := max(20, m.width)
	height := max(6, m.height)
	if m.overlay == overlayDiff {
		return m.diffView(width, height)
	}
	if m.overlay == overlaySessionPicker {
		return m.pickerView(width, height)
	}

	status := m.statusLine()
	statusH := lineCount(status)
	panelW := 0
	if m.helpOpen {
		panelW = min(36, width/3)
		if panelW < 24 {
			panelW = 24
		}
		if width-panelW-1 < 20 {
			panelW = max(0, width-21)
		}
	}
	bodyW := width - panelW - 1
	if bodyW < 20 {
		bodyW = 20
	}
	avail := height - m.inputH - statusH
	transcript := m.renderTranscript(bodyW, avail)

	var body []string
	if panelW > 0 {
		panelH := max(avail, len(transcript))
		rightLines := m.renderRightColumn(panelH)
		body = zipColumns(transcript, rightLines, bodyW, panelW)
	} else {
		body = transcript
	}
	for len(body) < avail {
		body = append(body, "")
	}

	var b strings.Builder
	for _, l := range body {
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString(status)
	b.WriteString("\n")
	b.WriteString(m.renderInputView(m.input.View()))

	v := tea.NewView(b.String())
	v.AltScreen = true
	if cursor := m.input.Cursor(); cursor != nil {
		cursor.Y += len(body) + statusH
		v.Cursor = cursor
	}
	return v
}

func (m model) pickerView(width, height int) tea.View {
	var b strings.Builder
	b.WriteString(stylePanelHeading.Render(m.list.Title))
	b.WriteString("\n")
	b.WriteString(m.list.View())
	b.WriteString(styleDim.Render("  ↑/↓ select · enter apply · esc close"))
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m model) diffView(width, height int) tea.View {
	var b strings.Builder
	b.WriteString(styleDiffHeader.Render("Git diff"))
	b.WriteString("  " + styleDim.Render(fmt.Sprintf("(%d lines)", len(m.diffLines))))
	b.WriteString("\n")
	start := m.diffScroll
	if start > len(m.diffLines) {
		start = len(m.diffLines)
	}
	end := min(len(m.diffLines), start+max(1, height-3))
	for _, l := range m.diffLines[start:end] {
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString(styleDim.Render("  ↑/↓ or pgup/pgdn scroll · esc close"))
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m model) renderRightColumn(panelH int) []string {
	var lines []string
	if m.helpOpen {
		lines = append(lines, buildHelpLines(m.keys)...)
	}
	return panelWindow(lines, 0, panelH)
}

// buildHelpLines returns styled rows for the help box.
func buildHelpLines(k Keymap) []string {
	type row struct {
		keys string
		desc string
	}
	rows := []row{
		{string(k.Run), "Send message"},
		{string(k.Newline), "Insert newline"},
		{string(k.CycleEffort), "Cycle effort"},
		{string(k.SessionPicker), "Session picker"},
		{string(k.DiffToggle), "Git diff"},
		{string(k.ToolsToggle), "Toggle tools"},
		{string(k.Help), "Toggle help (this)"},
		{string(k.ScrollUp), "Scroll up"},
		{string(k.ScrollDown), "Scroll down"},
		{string(k.PageUp), "Page up"},
		{string(k.PageDown), "Page down"},
		{string(k.HistoryUp), "History up"},
		{string(k.HistoryDown), "History down"},
		{string(k.Bookmark), "Bookmark message"},
		{string(k.Clear), "Clear transcript"},
		{string(k.Quit), "Quit"},
	}
	keyW := 0
	for _, r := range rows {
		if len(r.keys) > keyW {
			keyW = len(r.keys)
		}
	}
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colorPrompt)).Width(keyW)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colorAssistant))
	out := []string{stylePanelHeading.Render("Keybindings")}
	for _, r := range rows {
		out = append(out, "  "+keyStyle.Render(r.keys)+"  "+descStyle.Render(r.desc))
	}
	return out
}

// panelWindow returns the visible slice of the panel content for the given
// scroll offset and height, appending a scroll hint when content is clipped.
func panelWindow(lines []string, scroll, height int) []string {
	if len(lines) <= height {
		return lines
	}
	start := min(scroll, len(lines)-height)
	out := append([]string(nil), lines[start:start+height]...)
	if start+height < len(lines) {
		out[height-1] = styleDim.Render("… (scroll)")
	}
	return out
}

// zipColumns lays the transcript and side panel out side by side with a
// separator column between them.
func zipColumns(left, right []string, leftW, rightW int) []string {
	rows := max(len(left), len(right))
	out := make([]string, rows)
	leftPad := lipgloss.NewStyle().Width(leftW)
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color(colorReasoning)).Render("│")
	rightPad := lipgloss.NewStyle().Width(rightW)
	for i := 0; i < rows; i++ {
		l := ""
		if i < len(left) {
			l = left[i]
		}
		r := ""
		if i < len(right) {
			r = right[i]
		}
		out[i] = leftPad.Render(l) + sep + rightPad.Render(r)
	}
	return out
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
