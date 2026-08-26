package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/tui"
)

// The CLI progress display mirrors the TUI's busy line so a one-shot
// `motive "<request>"` run shows the same working animation as the terminal
// UI: a spinner plus a phase label (waiting for model response, reasoning,
// running tool, answering) with the elapsed wait live for the no-output
// phases. The animation renders on stderr with carriage-return redraws, so
// stdout stays clean for the final result. --silent (or a non-terminal
// stderr) disables it entirely.

// Spinner/phase colors match the TUI (colorPrompt = "#7aa2f7"). The model
// label on the left of the busy line uses the TUI's accent cyan.
var (
	cliStyleDim    = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("#a9b1d6"))
	cliStyleEffort = lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68"))
	cliStyleModel  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff"))
)

// cliModelLabelMax caps the "provider * model" label shown on the left of
// the CLI busy line. Provider/model names can be long (e.g. a full model id
// like "anthropic/claude-3-opus-20240229"), so the combined label is
// truncated to at most this many characters instead of stretching the line.
const cliModelLabelMax = 20

// cliPhase mirrors the TUI's busyPhase: what the runtime is doing while busy.
type cliPhase int

const (
	cliWorking cliPhase = iota
	cliPrefill
	cliReasoning
	cliTooling
	cliAnswering
)

// String renders the phase name, matching the TUI's labels.
func (p cliPhase) String() string {
	switch p {
	case cliPrefill:
		return "prefill"
	case cliReasoning:
		return "reasoning"
	case cliTooling:
		return "tooling"
	case cliAnswering:
		return "answering"
	}
	return "working"
}

// cliProgress renders the busy line on out (usually stderr) while a one-shot
// execution runs. A ticker goroutine (started by start) redraws the line; the
// runtime's trace handler calls trace to update the phase, and stop clears
// the line and terminates the ticker.
type cliProgress struct {
	mu           sync.Mutex
	spin         spinner.Model
	phase        cliPhase
	phaseStart   time.Time
	phaseElapsed time.Duration
	// modelLabel is the "provider * model" tag shown on the left of the busy
	// line, truncated to cliModelLabelMax characters. Empty when the CLI is
	// built without provider/model info (tests).
	modelLabel string
	// toolDetail is the human-readable summary of the tool call currently
	// running — the shell command, the file a read/write/edit touches, the
	// web_search query, or the fetched URL — shown on the tooling busy line
	// so the CLI reveals which command is executing instead of a generic
	// "running tool". Falls back to the tool name when the args carry no
	// extractable detail (e.g. git_status). Cleared when the call completes.
	toolDetail string
	out        io.Writer
	stopCh     chan struct{}
	stoppedCh  chan struct{}
}

// newCLIProgress builds a progress display writing to out. provider/model
// form the label on the left of the busy line ("provider * model", truncated
// to cliModelLabelMax characters). When out is nil the display is inert (used
// by tests that only inspect state).
func newCLIProgress(out io.Writer, provider, model string) *cliProgress {
	return &cliProgress{
		spin: spinner.New(
			spinner.WithSpinner(spinner.Dot),
			spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7"))),
		),
		modelLabel: formatModelLabel(provider, model),
		out:        out,
		stopCh:     make(chan struct{}),
		stoppedCh:  make(chan struct{}),
	}
}

// formatModelLabel renders the "provider * model" tag for the left of the
// busy line. The combined label never exceeds cliModelLabelMax characters:
// when it would, the model id is truncated (the part that varies most and
// tends to be long) and a "…" marks the cut. If even the provider alone is
// too long, the whole label is truncated. Truncation counts Unicode
// characters, not bytes, so multibyte names are handled correctly. With both
// names empty the label is empty (no tag); a missing provider name falls back
// to "default", the built-in env-only provider name.
func formatModelLabel(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" && model == "" {
		return ""
	}
	if provider == "" {
		provider = "default"
	}
	label := provider + " * " + model
	if utf8.RuneCountInString(label) <= cliModelLabelMax {
		return label
	}
	prefix := provider + " * "
	// Reserve one character for the ellipsis when the model is cut.
	modelMax := cliModelLabelMax - utf8.RuneCountInString(prefix) - 1
	if modelMax < 1 {
		// Provider itself is too long; truncate the whole label.
		return truncateRunes(label, cliModelLabelMax-1) + "…"
	}
	return prefix + truncateRunes(model, modelMax) + "…"
}

// truncateRunes returns the first n characters of s (counting runes), or s
// unchanged when it is already within n characters.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// start launches the redraw ticker goroutine. It must be called once, after
// the trace handler is wired, and stopped with stop before the process exits.
func (p *cliProgress) start() {
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.mu.Lock()
				p.spin, _ = p.spin.Update(spinner.TickMsg{Time: time.Now(), ID: p.spin.ID()})
				// Keep the elapsed wait fresh for the no-output phases, exactly
				// like the TUI's spinner tick handler.
				if (p.phase == cliPrefill || p.phase == cliReasoning) && !p.phaseStart.IsZero() {
					p.phaseElapsed = time.Since(p.phaseStart)
				}
				line := p.busyLine()
				p.mu.Unlock()
				if p.out != nil {
					fmt.Fprintf(p.out, "\x1b[2K\r%s", line)
				}
			case <-p.stopCh:
				if p.out != nil {
					fmt.Fprint(p.out, "\x1b[2K\r")
				}
				close(p.stoppedCh)
				return
			}
		}
	}()
}

// stop clears the progress line and waits for the ticker goroutine to exit.
// Safe to call exactly once after start.
func (p *cliProgress) stop() {
	close(p.stopCh)
	<-p.stoppedCh
}

// trace feeds a runtime trace event into the phase state, mirroring the TUI's
// handleTrace phase transitions. It is called from the executing goroutine.
func (p *cliProgress) trace(event runtime.TraceEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch event.Kind {
	case "model_start":
		// A fresh model request: waiting for the first response bytes.
		p.phase = cliPrefill
		p.phaseStart = time.Now()
		p.phaseElapsed = 0
	case "model_end":
		// The server responded; the next event is tooling or the end of the
		// run, so fall back to the generic working state.
		p.phase = cliWorking
	case "delta":
		if event.Text != "" {
			p.phase = cliAnswering
		} else if event.Reasoning != "" {
			// Entering the reasoning phase: start its elapsed timer once.
			if p.phase != cliReasoning {
				p.phase = cliReasoning
				p.phaseStart = time.Now()
				p.phaseElapsed = 0
			}
		}
	case "tool_start":
		p.phase = cliTooling
		p.toolDetail = tui.ToolDetail(event.ToolName, event.ToolArgs, 0, 0, "")
		// Tools whose args carry no extractable detail (git_status, git_log…)
		// still reveal themselves by name so the line never says just
		// "running tool".
		if p.toolDetail == "" {
			p.toolDetail = event.ToolName
		}
	case "tool":
		// The call completed: clear its detail so the next busy line does not
		// keep describing a finished command.
		p.toolDetail = ""
	case "finish":
		p.phase = cliWorking
		p.toolDetail = ""
	}
}

// busyLine renders the current phase line. It is a pure function of state, so
// tests can call it directly without starting the ticker. The left side shows
// which provider * model the run is using (truncated to cliModelLabelMax
// characters); the cancel binding advertises ctrl+c (the CLI's native
// interrupt), mirroring the TUI's esc.
func (p *cliProgress) busyLine() string {
	prefix := ""
	if p.modelLabel != "" {
		prefix = cliStyleModel.Render(p.modelLabel) + "  "
	}
	elapsed := p.phaseElapsed.Round(time.Second)
	switch p.phase {
	case cliPrefill:
		return prefix + p.spin.View() + " " +
			cliStyleDim.Render("waiting for model response ") +
			cliStyleEffort.Render("("+elapsed.String()+")") +
			cliStyleDim.Render(" · ctrl+c to cancel")
	case cliReasoning:
		return prefix + p.spin.View() + " " +
			cliStyleDim.Render("reasoning ") +
			cliStyleEffort.Render("("+elapsed.String()+")") +
			cliStyleDim.Render(" · ctrl+c to cancel")
	case cliTooling:
		label := "running tool"
		if p.toolDetail != "" {
			// The command (shell), the file (read/write/edit), the query
			// (web_search), or the URL (web_fetch) — whatever identifies the
			// call — so the user sees what is executing, not just a phase.
			label += ": " + truncateRunes(p.toolDetail, 60)
		}
		return prefix + p.spin.View() + " " +
			cliStyleDim.Render(label) +
			cliStyleDim.Render(" · ctrl+c to cancel")
	case cliAnswering:
		return prefix + p.spin.View() + " " +
			cliStyleDim.Render("answering") +
			cliStyleDim.Render(" · ctrl+c to cancel")
	}
	return prefix + p.spin.View() + " " + cliStyleDim.Render("working…")
}

// stderrIsTerminal reports whether stderr is attached to a terminal (a
// char device). The animation is only rendered then, so piped stderr never
// collects spinner redraw garbage in logs or parent-execution captures.
func stderrIsTerminal() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
