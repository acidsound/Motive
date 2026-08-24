package tui

import (
	"context"
	"encoding/json"
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
	"github.com/charmbracelet/x/ansi"
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
	colorStopped   = "#a9b1d6"
)

var (
	stylePrompt    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorPrompt))
	styleUser      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorUser))
	styleAssistant = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAssistant))
	styleReasoning = lipgloss.NewStyle().Faint(true).Italic(true).Foreground(lipgloss.Color(colorReasoning))
	styleTool      = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTool))
	styleToolSum   = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color(colorTool))
	styleError     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorError))
	styleStopped   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorStopped))
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
	overlayAttach
	overlayUnits
)

type message struct {
	role        string // user | assistant | error
	content     string
	reasoning   string
	tools       []string
	toolArgs    []string
	liveTool    string
	attachments []attachItem
	units       []unitChip
	ts          time.Time
	bookmark    bool
}

// unitChip records a sub-execution (`motive run`) launched via the shell tool,
// detected from the in-band `[motive] unit session: <id>` stderr marker.
type unitChip struct {
	id   string
	head string
}

// unitSessionID extracts a unit session id from a tool result head.
func unitSessionID(head string) (string, bool) {
	const marker = "[motive] unit session: "
	for _, l := range strings.Split(head, "\n") {
		if i := strings.Index(l, marker); i >= 0 {
			id := strings.TrimSpace(l[i+len(marker):])
			if id != "" && !strings.ContainsAny(id, " \t/") {
				return id, true
			}
		}
	}
	return "", false
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

	// stopCancel cancels the in-flight execution; nil when idle.
	stopCancel context.CancelFunc
	// stopping is set when the user stops a run so finishTurn records a
	// "stopped" entry instead of an error.
	stopping bool
	// stoppedPersisted guards persistStopped so the quit path and finishTurn
	// (which can both settle the same stopped turn) record exactly one entry.
	stoppedPersisted bool
	// queue holds user requests submitted while busy (queue mode); processed
	// FIFO after the current turn ends.
	queue []string
	// steerMode selects what enter does while busy: false queues the text for
	// the next turn, true steers the running execution.
	steerMode bool

	width  int
	height int
	inputH int
	scroll int

	units unitsPanel

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

	// attach is the file-attach overlay state; attachments holds the pending
	// attachments for the next turn.
	attach      dirPicker
	attachments []attachItem
	// notice is a transient one-line message (e.g. clipboard results) shown
	// above the input box; cleared on the next submit.
	notice string

	helpOpen bool

	toolsCollapsed bool

	startPicker bool
}

const maxInputHeight = 10

// visualLineCount returns the total number of terminal rows the current input
// occupies once each logical line is soft-wrapped at the textarea's width. The
// textarea reports logical lines via LineCount() — a single long line that
// wraps into several rows would otherwise keep the input box one row tall and
// hide the wrapped content and the cursor.
func (m model) visualLineCount() int {
	width := m.input.Width()
	if width < 1 {
		width = 1
	}
	total := 0
	for _, line := range strings.Split(m.input.Value(), "\n") {
		if line == "" {
			total++
			continue
		}
		// Match the textarea's wrapping: word-wrap, then hard-wrap, then
		// count the resulting lines.  This keeps the input box height
		// consistent with how the textarea actually renders.
		wrapped := ansi.Wordwrap(line, width, "")
		wrapped = ansi.Hardwrap(wrapped, width, true)
		total += strings.Count(wrapped, "\n") + 1
	}
	if total < 1 {
		total = 1
	}
	return total
}

func (m *model) syncInputHeight() {
	// Use the soft-wrapped (visual) line count so a single long line that
	// wraps across several terminal rows expands the input box accordingly.
	visLines := m.visualLineCount()
	if visLines < 1 {
		visLines = 1
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
	h := min(visLines, maxH)
	if h < 1 {
		h = 1
	}
	m.inputH = h
	m.input.SetHeight(h)

	// Keep the cursor visible: after a multiline paste the textarea's
	// internal viewport can stay scrolled to the top even though the cursor
	// sits on the last pasted line (the last line then renders off-screen).
	// Re-anchor by moving to the end and letting repositionView align the
	// viewport around the cursor.
	m.clampInputCursor()
}

// clampInputCursor re-anchors the textarea viewport so the cursor row is
// always inside the rendered view. It preserves the cursor position exactly
// (row and column) — only the scroll offset changes.
func (m *model) clampInputCursor() {
	in := &m.input
	lines := in.LineCount()
	h := m.inputH
	if h < 1 {
		h = 1
	}
	// All lines fit: pin the view to the top so line 0 and the prompt show.
	if lines <= h {
		if in.ScrollYOffset() != 0 {
			savedLine, savedCol := in.Line(), in.Column()
			in.MoveToBegin()
			for i := 0; i < savedLine; i++ {
				in.CursorDown()
			}
			in.SetCursorColumn(savedCol)
		}
		return
	}
	// Content overflows the box: make sure the cursor row is within
	// [YOffset, YOffset+h-1]; if not, scroll minimally via MoveToEnd-style
	// re-anchor. Moving to end guarantees the last line is reachable and the
	// viewport follows the cursor, which is what typing/pasting expects.
	// NOTE: the textarea's viewport content is only (re)populated inside
	// View()/Update(); until then maxYOffset() is stale, so a View() call is
	// required first or ScrollDown becomes a no-op and the last pasted lines
	// render off-screen.
	row := in.Line()
	off := in.ScrollYOffset()
	if row < off || row >= off+h {
		_ = in.View()
		in.MoveToEnd()
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
	rt.Steer = make(chan string, 16)
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

	case clipImageMsg:
		return m.handleClipImage(msg)

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
		if m.overlay == overlayAttach {
			m.attach.input.SetWidth(max(1, msg.Width-2))
			m.attach.list.SetSize(max(40, m.width-6), max(10, m.height-8))
		}

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	if m.overlay == overlayAttach {
		return m.updateAttach(msg)
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
	if m.overlay == overlayAttach {
		return m.handleAttachKey(msg)
	}
	if m.overlay != overlayNone {
		return m.handleOverlayKey(msg)
	}
	switch msg.String() {
	case string(m.keys.Quit), "ctrl+c":
		// Quit always exits. While busy, stop the run first so the partial
		// output and a "stopped" entry are persisted before the program ends.
		// The queue is dropped: queued turns must not start just to be
		// stopped by the exit.
		if m.busy {
			m.queue = nil
			m.stopExecution()
			m.persistStopped()
		}
		return m, tea.Quit

	case string(m.keys.Stop), "esc":
		// Stop cancels the in-flight run; finishTurn records the "stopped"
		// entry when the run's doneMsg arrives.
		if m.busy {
			m.stopExecution()
			return m, nil
		}
		if m.helpOpen {
			m.helpOpen = false
		}
		return m, nil

	case string(m.keys.Run):
		if m.busy {
			return m.submitBusy()
		}
		return m.submit()

	case string(m.keys.Newline), "alt+enter":
		// alt+enter is an alias for the newline binding: macOS Terminal.app
		// does not distinguish shift+enter from enter (both arrive as plain
		// enter, which submits), so alt+enter is the portable insert-newline
		// key without changing terminal settings.
		m.input.InsertString("\n")
		m.syncInputHeight()
		return m, nil

	case string(m.keys.CycleEffort):
		if m.busy {
			return m, nil
		}
		m.cycleEffort()
		return m, nil

	case string(m.keys.CycleQueueMode):
		// Only meaningful while a run is in progress.
		if m.busy {
			m.steerMode = !m.steerMode
		}
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

	case string(m.keys.UnitsPanel):
		m.openUnits()
		return m, nil

	case string(m.keys.ToolsToggle):
		m.toggleTools()
		return m, nil

	case string(m.keys.AttachFile):
		if m.busy {
			return m, nil
		}
		return m, m.openAttachPicker()

	case string(m.keys.PasteImage):
		if m.busy {
			return m, nil
		}
		return m, pasteImageCmd()

	case string(m.keys.Help), "alt+h":
		// alt+h is an alias for help: macOS Terminal.app's default keyboard
		// profiles often swallow ctrl+/ (it is not delivered to the app).
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
	// The units detail view consumes esc itself (back to the list) before the
	// generic overlay-close path can see it.
	if m.overlay == overlayUnits && m.units.detail >= 0 {
		return m.handleUnitsKey(key)
	}
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

	case overlayUnits:
		return m.handleUnitsKey(key)
	}
	return m, nil
}

// handleUnitsKey handles keys while the units overlay is open. The list is a
// cursor-driven picker (조회 → 선택 → 상세): up/down move the selection, enter
// opens the selected unit's transcript, esc backs out one level at a time.
func (m *model) handleUnitsKey(key string) (tea.Model, tea.Cmd) {
	half := m.height / 2
	if m.units.detail >= 0 {
		switch key {
		case string(m.keys.ScrollUp), "up":
			m.units.scroll = max(0, m.units.scroll-1)
		case string(m.keys.ScrollDown), "down":
			m.units.scroll++
		case string(m.keys.PageUp):
			m.units.scroll = max(0, m.units.scroll-half)
		case string(m.keys.PageDown):
			m.units.scroll += half
		case "esc", string(m.keys.Stop), string(m.keys.UnitsPanel):
			// Back out to the list, preserving the cursor position.
			m.units.detail = -1
			m.units.scroll = 0
		}
		return m, nil
	}
	switch key {
	case string(m.keys.ScrollUp), "up", "k":
		if len(m.units.units) > 0 {
			m.units.cursor--
			m.units.clampCursor()
		}
	case string(m.keys.ScrollDown), "down", "j":
		if len(m.units.units) > 0 {
			m.units.cursor++
			m.units.clampCursor()
		}
	case string(m.keys.PageUp):
		m.units.cursor -= half
		m.units.clampCursor()
	case string(m.keys.PageDown):
		m.units.cursor += half
		m.units.clampCursor()
	case "enter":
		if m.units.cursor >= 0 && m.units.cursor < len(m.units.units) {
			rec := m.units.units[m.units.cursor]
			if entries, err := m.sess.Load(rec.SessionID); err == nil {
				m.units.detailLn = strings.Split(formatUnitDetail(entries), "\n")
				m.units.detail = m.units.cursor
				m.units.scroll = 0
			}
		}
	}
	return m, nil
}

// submit starts a new execution turn from the input box, carrying any
// pending attachments. An empty prompt is allowed when attachments exist
// (e.g. "what is this?" is optional; a bare image alone is a valid turn).
func (m *model) submit() (tea.Model, tea.Cmd) {
	request := strings.TrimSpace(m.input.Value())
	if request == "" && len(m.attachments) == 0 {
		return m, nil
	}
	m.input.Reset()
	m.notice = ""
	m.syncInputHeight()
	return m.startTurn(request, m.attachments)
}

// startTurn starts a new execution turn for the given request: records
// history, appends transcript entries, opens the assistant message the stream
// fills, and persists the user request (with its attachments) to the current
// session.
func (m *model) startTurn(request string, attachments []attachItem) (tea.Model, tea.Cmd) {
	m.histIdx = len(m.history)
	m.history = append(m.history, request)
	m.attachments = nil

	if m.rt != nil && m.rt.WS != nil {
		m.baseRev = m.rt.WS.GitHEAD()
	}
	m.resultRev = ""
	m.appendMessage(message{role: "user", content: request, attachments: attachments, ts: time.Now()})

	m.busy = true
	m.step = 0
	m.stopping = false
	m.stoppedPersisted = false
	m.stopCancel = nil
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
	// Pass the active session to the runtime so the model sees its session id
	// and the session_log tool can read the transcript to recover.
	if m.rt != nil {
		m.rt.SessionID = m.sessionID
	}
	if m.sess != nil && m.sessionID != "" {
		_ = m.sess.Append(m.sessionID, session.Entry{
			TS:           time.Now(),
			Role:         "user",
			Content:      request,
			Attachments:  attachmentsModel(attachments),
			BaseRevision: m.baseRev,
		})
	}

	m.appendFull("user_input", nil, request, "", "")
	m.appendMessage(message{role: "assistant", ts: time.Now()})
	return m, m.startExecution(request, attachments)
}

// startExecution runs the turn in a cancellable context; the cancel func is
// kept on the model so esc (stop) can abort the in-flight request.
func (m *model) startExecution(request string, attachments []attachItem) tea.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), m.rt.Budget.MaxDuration)
	m.stopCancel = cancel
	return func() tea.Msg {
		result, err := m.rt.Execute(ctx, request, attachmentsModel(attachments)...)
		return doneMsg{text: result, err: err}
	}
}

// submitBusy handles enter while a run is in progress: in steer mode the text
// is injected into the running execution (falling back to the queue when the
// steer channel is full); in queue mode it is appended for the next turn.
func (m *model) submitBusy() (tea.Model, tea.Cmd) {
	request := strings.TrimSpace(m.input.Value())
	if request == "" {
		return m, nil
	}
	m.input.Reset()
	m.syncInputHeight()
	if m.steerMode && m.rt != nil && m.rt.Steer != nil {
		select {
		case m.rt.Steer <- request:
			// Persist the in-progress assistant output (if any) first so the
			// transcript keeps it before the steer message. If the slot is
			// still an empty placeholder, drop it so the transcript does not
			// keep a phantom blank assistant message.
			if m.lastAssistantActive() {
				m.saveAssistantEntry()
			} else if n := len(m.messages); n > 0 && m.messages[n-1].role == "assistant" {
				m.messages = m.messages[:n-1]
			}
			m.appendMessage(message{role: "user", content: request, ts: time.Now()})
			if m.sess != nil && m.sessionID != "" {
				_ = m.sess.Append(m.sessionID, session.Entry{
					TS:      time.Now(),
					Role:    "user",
					Content: request,
				})
			}
			m.appendFull("steer_input", nil, request, "", "")
			return m, nil
		default:
			// Steer channel full: queue the message instead.
		}
	}
	m.queue = append(m.queue, request)
	return m, nil
}

// stopExecution cancels the in-flight run and marks it as a user stop.
func (m *model) stopExecution() {
	m.appendFull("stop_requested", nil, "", "", "")
	if m.stopCancel != nil {
		m.stopCancel()
	}
	m.stopping = true
}

// persistStopped records the in-progress assistant output (if any) and a
// "stopped" entry so the transcript shows the user interrupted the run.
// Idempotent: the quit path and finishTurn can both settle the same stopped
// turn, and only the first call persists anything.
func (m *model) persistStopped() {
	if m.stoppedPersisted {
		return
	}
	m.stoppedPersisted = true
	m.appendFull("execution_stopped", nil, "stopped by user", "", "")
	m.saveAssistantEntry()
	m.appendMessage(message{role: "stopped", content: "stopped by user", ts: time.Now()})
	if m.sess != nil && m.sessionID != "" {
		_ = m.sess.Append(m.sessionID, session.Entry{
			TS:      time.Now(),
			Role:    "stopped",
			Content: "stopped by user",
		})
	}
}

// nextQueued starts the next queued request, if any. Queued requests are
// plain text (attachments are only submitted with the composing turn).
func (m *model) nextQueued() (tea.Model, tea.Cmd) {
	if len(m.queue) == 0 {
		return m, nil
	}
	next := m.queue[0]
	m.queue = m.queue[1:]
	return m.startTurn(next, nil)
}

func (m *model) finishTurn(done doneMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	// A user stop surfaces as a canceled context. If the turn nevertheless
	// finished cleanly (done.err == nil), treat it as a normal success.
	stopped := m.stopping && done.err != nil
	m.stopping = false
	m.stopCancel = nil
	if stopped {
		m.persistStopped()
		return m.nextQueued()
	}
	if done.err != nil {
		// Drop the empty assistant placeholder that submit() appended so an
		// error turn does not leave a phantom blank message in the transcript.
		if n := len(m.messages); n > 0 && m.messages[n-1].role == "assistant" && !m.lastAssistantActive() {
			m.messages = m.messages[:n-1]
		}
		// Persist whatever assistant output (content, reasoning, tool calls)
		// streamed in before the failure so a run interrupted by a network or
		// budget error still records its in-progress work. saveAssistantEntry
		// is a no-op when the assistant slot was dropped above.
		m.saveAssistantEntry()
		m.appendFull("execution_error", nil, "", "", done.err.Error())
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
		// A failed turn must not silently drop queued requests: continue
		// with the next queued turn, if any.
		return m.nextQueued()
	}
	if text := strings.TrimSpace(done.text); text != "" && !m.lastAssistantActive() {
		// Fill the assistant slot that submit() opened instead of appending
		// a duplicate message.
		slot := m.assistantSlot()
		slot.content = text
	}
	m.appendFull("execution_completed", nil, done.text, "", "")
	m.saveAssistantEntry()
	m.scroll = 0
	// Continue with queued requests, if any.
	return m.nextQueued()
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

// appendFull records the complete runtime observation stream separately from
// the compact session transcript. The normal transcript remains the only
// session data exposed to the model through session_log.
func (m *model) appendFull(kind string, event *runtime.TraceEvent, text, reasoning, errText string) {
	if m.sess == nil || m.sessionID == "" {
		return
	}
	rec := session.FullEvent{TS: time.Now(), Kind: kind, Text: text, Reasoning: reasoning, Error: errText}
	if event != nil {
		rec.Step = event.Step
		rec.MaxSteps = event.MaxSteps
		rec.ToolName = event.ToolName
		rec.ToolArgs = event.ToolArgs
		if rec.Text == "" {
			rec.Text = event.Text
		}
		if rec.Reasoning == "" {
			rec.Reasoning = event.Reasoning
		}
		if rec.Error == "" && event.Error != nil {
			rec.Error = event.Error.Error()
		}
	}
	_ = m.sess.AppendFull(m.sessionID, rec)
}

func (m *model) handleTrace(event runtime.TraceEvent) (tea.Model, tea.Cmd) {
	// While a user stop is in progress, ignore late stream events so they do
	// not touch the transcript after the "stopped" entry is recorded, and so
	// the busy state stays until finishTurn settles the turn.
	if m.stopping {
		return m, nil
	}
	m.appendFull("trace."+event.Kind, &event, "", "", "")
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

	case "tool_start":
		last := m.assistantSlot()
		last.liveTool = liveToolLine(event.ToolName, event.ToolArgs)

	case "tool":
		m.toolCalls = event.TotalToolCalls
		last := m.assistantSlot()
		if event.ToolName == "shell" {
			if id, ok := unitSessionID(event.ToolResultHead); ok {
				// A one-shot `motive run` sub-execution: render as a unit
				// chip instead of a plain shell line (option A).
				last.units = append(last.units, unitChip{id: id, head: event.ToolResultHead})
				last.tools = append(last.tools, "unit "+id)
				last.toolArgs = append(last.toolArgs, toolArgsPreview(event.ToolName, event.ToolArgs))
				last.liveTool = ""
				return m, nil
			}
		}
		last.tools = append(last.tools, toolSummary(event.ToolName, event.ToolArgs, event.ToolResultBytes, event.ToolResultLines, event.ToolResultHead))
		last.toolArgs = append(last.toolArgs, toolArgsPreview(event.ToolName, event.ToolArgs))
		last.liveTool = ""

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
	if m.rt != nil {
		m.rt.SessionID = id
	}
	m.messages = nil
	m.history = nil
	m.histIdx = 0
	m.scroll = 0
	m.baseRev = ""
	m.resultRev = ""
	for _, e := range entries {
		switch e.Role {
		case "user":
			atts := make([]attachItem, 0, len(e.Attachments))
			for _, a := range e.Attachments {
				atts = append(atts, attachItem{Attachment: a, thumb: thumbnail(a.Path)})
			}
			m.appendMessage(message{role: "user", content: e.Content, attachments: atts, ts: e.TS})
		case "assistant":
			m.appendMessage(message{role: "assistant", content: e.Content, reasoning: e.Reasoning, tools: e.Tools, ts: e.TS})
		case "error":
			m.appendMessage(message{role: "error", content: e.Content, ts: e.TS})
		case "stopped":
			m.appendMessage(message{role: "stopped", content: e.Content, ts: e.TS})
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

// openDiff opens the git diff overlay.
func (m *model) openDiff() {
	diff, err := m.rt.WS.GitDiff()
	if err != nil {
		diff = "git diff unavailable: " + err.Error()
	}
	m.diffLines = colorizeDiff(diff)
	m.diffScroll = 0
	m.overlay = overlayDiff
}

// openUnits refreshes and opens the units overlay, listing every recorded
// unit boundary so the user can inspect sub-execution outcomes.
func (m *model) openUnits() {
	m.units.reset()
	m.units.units = m.loadUnits()
	m.overlay = overlayUnits
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
		lines := wrapStyled(styleUser.Render("❯ "+msg.content), width)
		lines = append(lines, attachmentLines(msg.attachments, width)...)
		return lines
	case "error":
		return wrapStyled(styleError.Render("✖ "+msg.content), width)
	case "stopped":
		return wrapStyled(styleStopped.Render("⏹ "+msg.content), width)
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
		for _, u := range msg.units {
			out = append(out, styleBookmark.Render("⏵ unit "+u.id))
		}
		if len(msg.tools) > 0 || msg.liveTool != "" {
			if m.toolsCollapsed {
				// Completed calls fold into a one-line done summary; only the
				// most recent calls stay visible so live activity is readable.
				out = append(out, styleToolSum.Render(m.collapsedToolsSummary(msg.tools)))
				if msg.liveTool != "" {
					out = append(out, styleTool.Render("  ⟳ "+msg.liveTool))
				}
				for _, t := range recentTools(msg.tools) {
					out = append(out, styleToolSum.Render("  → "+t))
				}
			} else {
				for i, t := range msg.tools {
					out = append(out, styleTool.Render("→ "+t))
					if i < len(msg.toolArgs) && msg.toolArgs[i] != "" {
						out = append(out, styleReasoning.Render("    "+msg.toolArgs[i]))
					}
				}
				if msg.liveTool != "" {
					out = append(out, styleTool.Render("⟳ "+msg.liveTool))
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

// liveToolLine builds the one-line description shown while a tool call is
// still running. It surfaces the most useful identifying detail per tool:
// the command for shell, the target file for single-file tools (with an
// affected-line count when known), the query for web_search, and a summary
// of the fetched content for web_fetch.
func liveToolLine(name, args string) string {
	return toolDetail(name, args, 0, 0, "")
}

// toolSummary builds the completed one-line description for a finished tool
// call: the identifying detail from its arguments plus a size/line hint.
func toolSummary(name, args string, resultBytes, resultLines int, head string) string {
	return fmt.Sprintf("%s · %dB · %s", toolDetail(name, args, resultLines, resultBytes, head), resultBytes, name)
}

// toolDetail extracts the human-relevant summary of a tool invocation.
func toolDetail(name, args string, resultLines, resultBytes int, head string) string {
	get := func(key string) string {
		var m map[string]any
		if err := json.Unmarshal([]byte(args), &m); err != nil {
			return ""
		}
		s, _ := m[key].(string)
		return s
	}
	switch name {
	case "shell":
		if cmd := get("command"); cmd != "" {
			return truncateRunes(strings.ReplaceAll(cmd, "\n", " && "), 80)
		}
	case "read_file", "write_file", "edit_file", "delete_file":
		p := get("path")
		if p == "" {
			p = get("file")
		}
		detail := p
		if lines := affectedLines(name, args); lines > 0 {
			detail = fmt.Sprintf("%s (%d lines)", p, lines)
		}
		return detail
	case "web_search":
		return truncateRunes(get("query"), 80)
	case "web_fetch":
		url := get("url")
		if head != "" {
			return fmt.Sprintf("%s · %s", url, truncateRunes(head, 60))
		}
		return url
	}
	return ""
}

// affectedLines reports how many lines of a file a read/write/edit touches,
// or 0 when it cannot be determined.
func affectedLines(name, args string) int {
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return 0
	}
	num := func(key string) int {
		f, _ := m[key].(float64)
		return int(f)
	}
	str := func(key string) string {
		s, _ := m[key].(string)
		return s
	}
	switch name {
	case "read_file":
		if lim := num("limit"); lim > 0 {
			return lim
		}
	case "write_file":
		if c := str("content"); c != "" {
			return strings.Count(strings.TrimSuffix(c, "\n"), "\n") + 1
		}
	case "edit_file":
		oldS, newS := str("old_string"), str("new_string")
		n := strings.Count(str("content")+oldS+newS, "\n")
		if oldS != "" || newS != "" {
			return n + 1
		}
	}
	return 0
}

// maxRecentTools is how many of the most recent tool calls stay individually
// visible while the tools are collapsed; older calls fold into the summary.
const maxRecentTools = 3

// recentTools returns the trailing slice of tool calls that remain expanded
// while collapsed (at most maxRecentTools). For short lists it returns nil so
// the summary alone is shown without duplicating entries.
func recentTools(tools []string) []string {
	if len(tools) <= 1 {
		return nil
	}
	start := len(tools) - min(maxRecentTools, len(tools)-1)
	if start <= 0 {
		return nil
	}
	return tools[start:]
}

// toolArgsPreview builds a compact one-line preview of a tool call's arguments
// for the expanded tools view. It flattens JSON-ish args to "key=value" pairs
// and truncates long values.
func toolArgsPreview(name, args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "{}" {
		return ""
	}
	var pairs []string
	dec := json.NewDecoder(strings.NewReader(args))
	if tok, err := dec.Token(); err == nil {
		if m, ok := tok.(json.Delim); ok && m == '{' {
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					break
				}
				key, ok := keyTok.(string)
				if !ok {
					break
				}
				var val any
				if err := dec.Decode(&val); err != nil {
					break
				}
				s := fmt.Sprint(val)
				if str, ok := val.(string); ok {
					s = strings.ReplaceAll(str, "\n", "\\n")
				}
				pairs = append(pairs, fmt.Sprintf("%s=%s", key, truncateRunes(s, 80)))
			}
		}
	}
	if len(pairs) == 0 {
		return truncateRunes(strings.ReplaceAll(args, "\n", "\\n"), 120)
	}
	return strings.Join(pairs, " ")
}

// truncateRunes shortens s to at most max runes, appending an ellipsis.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// collapsedToolsSummary builds the one-line summary shown for a message's
// tool calls while they are collapsed: completed calls plus a live tail of
// the most recent calls, so the latest activity stays visible.
func (m model) collapsedToolsSummary(tools []string) string {
	summary := fmt.Sprintf("⏺ %d tool calls", len(tools))
	if done := len(tools) - len(recentTools(tools)); done > 0 {
		summary += fmt.Sprintf(" · ✓ %d done", done)
	}
	if len(tools) > 0 {
		if last := tools[len(tools)-1]; last != "" {
			summary += " · last: " + last
		}
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
		// Enter-mode while busy: steer (inject into the running execution) or
		// queue (FIFO for the next turn), cycled with ctrl+\.
		mode := "queue"
		if m.steerMode {
			mode = "steer"
		}
		b.WriteString(" · " + styleEffort.Render(mode))
		if len(m.queue) > 0 {
			b.WriteString(fmt.Sprintf(" · queue %d", len(m.queue)))
		}
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
	if len(m.attachments) > 0 {
		b.WriteString(fmt.Sprintf(" · 📎%d", len(m.attachments)))
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
	if m.overlay == overlayUnits {
		return m.unitsView(width, height)
	}
	if m.overlay == overlaySessionPicker {
		return m.pickerView(width, height)
	}
	if m.overlay == overlayAttach {
		return m.attachView(width, height)
	}

	status := m.statusLine()
	statusH := lineCount(status)
	panelW := 0
	if m.helpOpen {
		// Size the panel to its widest row so long key labels like
		// "shift+enter / alt+enter" are not clipped by a fixed cap.
		panelW = helpPanelWidth(width)
	}
	bodyW := width - panelW - 1
	if bodyW < 20 {
		bodyW = 20
	}
	// Lines rendered between the status bar and the input box: transient
	// notice plus the pending-attachment preview with inline thumbnails.
	// They must be subtracted from the transcript's available height,
	// otherwise the total content overflows the terminal and the input
	// box is pushed off-screen (or overlaps the notice).
	var pre []string
	if m.notice != "" {
		pre = append(pre, styleError.Render(m.notice))
	}
	pre = append(pre, attachmentLines(m.attachments, width)...)
	avail := height - m.inputH - statusH - len(pre)
	if avail < 1 {
		avail = 1
	}
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
	for _, l := range pre {
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString(m.renderInputView(m.input.View()))

	v := tea.NewView(b.String())
	v.AltScreen = true
	if cursor := m.input.Cursor(); cursor != nil {
		cursor.Y += len(body) + statusH + len(pre)
		v.Cursor = cursor
	}
	return v
}

func (m model) unitsView(width, height int) tea.View {
	v := tea.NewView(m.units.view(width, height))
	v.AltScreen = true
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

func (m model) attachView(width, height int) tea.View {
	var b strings.Builder
	b.WriteString(stylePanelHeading.Render("Attach file"))
	b.WriteString("  " + styleDim.Render(m.attach.dir))
	b.WriteString("\n")
	b.WriteString(stylePrompt.Render("path ") + m.attach.input.View())
	b.WriteString("\n")
	if m.attach.err != "" {
		b.WriteString(styleError.Render("✖ " + m.attach.err))
	} else {
		b.WriteString(styleDim.Render(m.attach.hint))
	}
	b.WriteString("\n")
	b.WriteString(m.attach.list.View())
	b.WriteString(styleDim.Render("  ↑/↓ browse · enter attach · type path or filter · esc close"))
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

// helpPanelWidth sizes the help side panel: wide enough for the widest
// keybinding label, capped so the transcript keeps at least 20 columns.
func helpPanelWidth(width int) int {
	keyW := 0
	for _, r := range buildHelpRows(DefaultKeymap()) {
		for _, key := range r.keys {
			if len(key) > keyW {
				keyW = len(key)
			}
		}
	}
	w := keyW + 2 + maxDescLen + 4 // indent + gap + desc + margin
	if w < 24 {
		w = 24
	}
	if w > width/2 {
		w = width / 2
	}
	if w < 24 {
		w = min(24, width)
	}
	return w
}

// helpRow is one keybindings panel row. An action bound to several keys
// lists them as separate rows (one key per line) instead of joining them
// with " / ", so every key stays scannable in its own column.
type helpRow struct {
	keys []string
	desc string
}

// buildHelpRows returns the keybinding rows shown in the help panel.
func buildHelpRows(k Keymap) []helpRow {
	return []helpRow{
		{[]string{string(k.Run)}, "Send"},
		{[]string{string(k.CycleQueueMode)}, "Cycle steer/queue"},
		{[]string{string(k.Stop)}, "Stop"},
		{[]string{string(k.Newline), "alt+enter"}, "Insert newline"},
		{[]string{string(k.CycleEffort)}, "Cycle effort"},
		{[]string{string(k.SessionPicker)}, "Sessions"},
		{[]string{string(k.DiffToggle)}, "Git diff"},
		{[]string{string(k.UnitsPanel)}, "Unit runs"},
		{[]string{string(k.ToolsToggle)}, "Toggle tools"},
		{[]string{string(k.AttachFile)}, "Attach file"},
		{[]string{string(k.PasteImage)}, "Paste image"},
		{[]string{string(k.Help), "alt+h"}, "Toggle help"},
		{[]string{string(k.ScrollUp), "/", string(k.ScrollDown)}, "Scroll up/down"},
		{[]string{string(k.PageUp), "/", string(k.PageDown)}, "Page up/down"},
		{[]string{string(k.HistoryUp), "/", string(k.HistoryDown)}, "History up/down"},
		{[]string{string(k.Bookmark)}, "Bookmark"},
		{[]string{string(k.Clear)}, "Clear transcript"},
		{[]string{string(k.Quit)}, "Quit"},
	}
}

// maxDescLen is the width of the widest description in the help rows.
var maxDescLen = func() int {
	m := 0
	for _, r := range buildHelpRows(DefaultKeymap()) {
		if len(r.desc) > m {
			m = len(r.desc)
		}
	}
	return m
}()

// buildHelpLines returns styled rows for the help box. Each row renders its
// bindings joined on a single line ("k1 / k2") so the whole panel fits a
// normal terminal height; the description column word-wraps within its fixed
// width so long descriptions wrap inside their own cell instead of pushing
// the layout wider.
func buildHelpLines(k Keymap) []string {
	rows := buildHelpRows(k)
	keyW := 0
	for _, r := range rows {
		if w := len(strings.Join(r.keys, " / ")); w > keyW {
			keyW = w
		}
	}
	descW := maxDescLen
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colorPrompt)).Width(keyW)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colorAssistant)).Width(descW)
	out := []string{stylePanelHeading.Render("Keybindings")}
	for _, r := range rows {
		descLines := wrapCell(r.desc, descW)
		for i, dl := range descLines {
			key := ""
			if i == 0 {
				key = strings.Join(r.keys, " / ")
			}
			out = append(out, "  "+keyStyle.Render(key)+"  "+descStyle.Render(dl))
		}
	}
	return out
}

// wrapCell word-wraps s to at most width display columns per line.
func wrapCell(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	wrapped := ansi.Wordwrap(s, width, "")
	return strings.Split(wrapped, "\n")
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
