package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/acidsound/Motive/internal/runtime"
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
)

var (
	stylePrompt    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorPrompt))
	styleUser      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorUser))
	styleAssistant = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAssistant))
	styleReasoning = lipgloss.NewStyle().Faint(true).Italic(true).Foreground(lipgloss.Color(colorReasoning))
	styleTool      = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTool))
	styleError     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorError))
	styleStatus    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorStatusFg)).Background(lipgloss.Color(colorStatusBg))
	styleIdle      = lipgloss.NewStyle().Foreground(lipgloss.Color(colorIdle))
	styleEffort    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorEffort))
)

type streamMsg struct {
	event runtime.TraceEvent
}

type doneMsg struct {
	text string
	err  error
}

type chatMessage struct {
	role      string // user | assistant | error
	text      string
	reasoning string
}

type model struct {
	rt        *runtime.Runtime
	program   *tea.Program
	input     textarea.Model
	spin      spinner.Model
	messages  []chatMessage
	busy      bool
	width     int
	height    int
	inputH    int
	step      int
	maxSteps  int
	toolCalls int
	elapsed   time.Duration
}

func newModel(rt *runtime.Runtime) model {
	input := textarea.New()
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.SetVirtualCursor(false)
	input.SetStyles(textarea.DefaultStyles(true))
	input.Focus()
	input.KeyMap.InsertNewline.SetEnabled(false)

	spin := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color(colorPrompt))),
	)

	return model{rt: rt, input: input, spin: spin, inputH: 3}
}

// Run starts the terminal UI and forwards every runtime trace event into the
// Bubble Tea update loop so the transcript and status bar update live.
func Run(rt *runtime.Runtime) error {
	m := newModel(rt)
	p := tea.NewProgram(&m)
	m.program = p
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
	return tea.Batch(textarea.Blink, m.spin.Tick)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case doneMsg:
		m.busy = false
		if msg.err != nil {
			m.appendMessage("error", msg.err.Error())
			return m, nil
		}
		// In streaming mode the assistant text already rendered live; only
		// append the final text when nothing was streamed (blocking mode).
		if text := strings.TrimSpace(msg.text); text != "" && !m.lastAssistantActive() {
			m.appendMessage("assistant", text)
		}
		return m, nil

	case streamMsg:
		return m.handleTrace(msg.event)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.inputH = max(3, min(8, msg.Height/4))
		m.input.SetWidth(max(1, msg.Width-2))
		m.input.SetHeight(m.inputH)

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			if m.busy {
				return m, nil
			}
			return m, tea.Quit

		case "ctrl+e":
			if m.busy {
				return m, nil
			}
			m.cycleEffort()
			return m, nil

		case "enter":
			if m.busy {
				return m, nil
			}
			request := strings.TrimSpace(m.input.Value())
			if request == "" {
				return m, nil
			}
			m.input.Reset()
			m.appendMessage("user", request)
			m.prepareExecution()
			return m, execute(m.rt, request)

		case "shift+enter":
			m.input.SetValue(m.input.Value() + "\n")
			m.input.MoveToEnd()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// prepareExecution resets live status and opens an assistant message that the
// streamed trace events will fill in. The returned tea.Cmd that runs the
// request is returned by the caller's Update.
func (m *model) prepareExecution() {
	m.busy = true
	m.step = 0
	m.maxSteps = m.rt.Budget.MaxSteps
	m.toolCalls = 0
	m.elapsed = 0
	m.appendMessage("assistant", "")
	m.rt.Stream = true
}

func (m *model) handleTrace(event runtime.TraceEvent) (tea.Model, tea.Cmd) {
	m.elapsed = event.TotalElapsed
	switch event.Kind {
	case "start":
		m.maxSteps = event.MaxSteps
		m.step = 0
		m.toolCalls = 0

	case "model_start":
		m.step = event.Step
		m.maxSteps = event.MaxSteps

	case "delta":
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "assistant" {
			m.appendMessage("assistant", "")
		}
		last := &m.messages[len(m.messages)-1]
		if event.Text != "" {
			last.text += event.Text
		}
		if event.Reasoning != "" {
			last.reasoning += event.Reasoning
		}

	case "tool":
		m.toolCalls = event.TotalToolCalls
		detail := fmt.Sprintf("→ %s · %dB · %s", event.ToolName, event.ToolResultBytes, event.Latency.Round(time.Millisecond))
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "assistant" {
			m.appendMessage("assistant", "")
		}
		m.messages[len(m.messages)-1].text += "\n" + styleTool.Render(detail)

	case "model_end":
		// Errors are reported once via the finish event that always follows.

	case "finish":
		m.busy = false
		if event.Error != nil && m.lastMessageRole() != "error" {
			m.appendMessage("error", event.Error.Error())
		}
	}
	return m, nil
}

func (m *model) appendMessage(role, text string) {
	m.messages = append(m.messages, chatMessage{role: role, text: text})
}

// lastAssistantActive reports whether the most recent assistant message already
// carries streamed content or tool lines, in which case the final joined text
// from doneMsg would be a duplicate.
func (m *model) lastAssistantActive() bool {
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "assistant" {
		return false
	}
	last := m.messages[len(m.messages)-1]
	return strings.TrimSpace(last.text) != "" || strings.TrimSpace(last.reasoning) != ""
}

func (m *model) lastMessageRole() string {
	if len(m.messages) == 0 {
		return ""
	}
	return m.messages[len(m.messages)-1].role
}

func (m *model) cycleEffort() {
	current := m.rt.Model.GetReasoningEffort()
	next := "low"
	switch current {
	case "low":
		next = "medium"
	case "medium":
		next = "xhigh"
	default:
		next = "low"
	}
	m.rt.Model.SetReasoningEffort(next)
}

func execute(rt *runtime.Runtime, request string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), rt.Budget.MaxDuration)
		defer cancel()
		result, err := rt.Execute(ctx, request)
		return doneMsg{text: result, err: err}
	}
}

// renderMessages lays out every message (newest at the bottom) and returns only
// the tail that fits the transcript area.
func (m model) renderMessages(width int, available int) string {
	if available < 1 {
		return ""
	}
	lines := make([]string, 0, len(m.messages)*2)
	for _, msg := range m.messages {
		lines = append(lines, m.renderMessage(msg, width)...)
	}
	if m.busy {
		lines = append(lines, m.spin.View()+" working…")
	}
	total := 0
	start := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		total += strings.Count(lines[i], "\n") + 1
		if total > available {
			start = i + 1
			break
		}
	}
	return strings.Join(lines[start:], "\n")
}

func (m model) renderMessage(msg chatMessage, width int) []string {
	w := max(10, width-2)
	switch msg.role {
	case "user":
		return wrapLines(styleUser.Render("> "+msg.text), w)
	case "error":
		return wrapLines(styleError.Render("✖ "+msg.text), w)
	case "assistant":
		var out []string
		if reasoning := strings.TrimSpace(msg.reasoning); reasoning != "" {
			for _, line := range wrapLines(styleReasoning.Render("· "+reasoning), w) {
				out = append(out, line)
			}
		}
		if text := strings.TrimRight(msg.text, "\n"); text != "" {
			out = append(out, wrapLines(styleAssistant.Render(text), w)...)
		} else if len(out) == 0 && m.busy {
			out = append(out, styleReasoning.Render("…"))
		}
		return out
	}
	return wrapLines(msg.text, w)
}

func wrapLines(text string, width int) []string {
	return strings.Split(lipgloss.Wrap(text, width, ""), "\n")
}

func (m model) statusLine() string {
	modelID := m.rt.Model.Model
	effort := m.rt.Model.GetReasoningEffort()
	var b strings.Builder
	if m.busy {
		b.WriteString(m.spin.View() + " ")
	} else {
		b.WriteString(styleIdle.Render("●") + " ")
	}
	b.WriteString(modelID)
	b.WriteString(" · " + styleEffort.Render("effort "+effort))
	if m.busy {
		b.WriteString(fmt.Sprintf(" · step %d/%d · tools %d · %s", m.step, m.maxSteps, m.toolCalls, m.elapsed.Round(time.Second)))
	}
	b.WriteString(fmt.Sprintf(" · budget %d steps / %d tools / %s", m.rt.Budget.MaxSteps, m.rt.Budget.MaxToolCalls, m.rt.Budget.MaxDuration.Round(time.Minute)))
	b.WriteString("  [ctrl+e] effort  [enter] run  [esc] quit")
	return styleStatus.Render(b.String())
}

func (m model) View() tea.View {
	width := max(20, m.width)
	height := max(6, m.height)
	statusLines := strings.Count(m.statusLine(), "\n") + 1
	transcriptHeight := height - m.inputH - statusLines - 2
	transcript := m.renderMessages(width, transcriptHeight)
	linesAbove := strings.Count(transcript, "\n") + 1

	var b strings.Builder
	b.WriteString(transcript)
	if transcript != "" {
		b.WriteString("\n")
	}
	b.WriteString(m.statusLine())
	b.WriteString("\n")
	b.WriteString(stylePrompt.Render("> "))
	b.WriteString(m.input.View())

	v := tea.NewView(b.String())
	v.AltScreen = true
	if cursor := m.input.Cursor(); cursor != nil {
		cursor.X += 2
		cursor.Y += linesAbove + statusLines
		v.Cursor = cursor
	}
	return v
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
