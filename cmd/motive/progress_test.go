package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/acidsound/Motive/internal/runtime"
)

// TestCLIProgressPhaseTransitions verifies that the CLI progress display
// follows the same phase lifecycle as the TUI: model_start → prefill,
// reasoning delta → reasoning, content delta → answering, tool_start →
// tooling, model_end → working, finish → working.
func TestCLIProgressPhaseTransitions(t *testing.T) {
	p := newCLIProgress(nil, "", "")

	p.trace(runtime.TraceEvent{Kind: "model_start"})
	if p.phase != cliPrefill {
		t.Fatalf("model_start: phase = %v, want prefill", p.phase)
	}
	if p.phaseStart.IsZero() {
		t.Fatal("model_start should set phaseStart")
	}

	p.trace(runtime.TraceEvent{Kind: "delta", Reasoning: "thinking…"})
	if p.phase != cliReasoning {
		t.Fatalf("reasoning delta: phase = %v, want reasoning", p.phase)
	}

	p.trace(runtime.TraceEvent{Kind: "delta", Text: "hello"})
	if p.phase != cliAnswering {
		t.Fatalf("content delta: phase = %v, want answering", p.phase)
	}

	p.trace(runtime.TraceEvent{Kind: "tool_start", ToolName: "shell"})
	if p.phase != cliTooling {
		t.Fatalf("tool_start: phase = %v, want tooling", p.phase)
	}

	p.trace(runtime.TraceEvent{Kind: "model_end"})
	if p.phase != cliWorking {
		t.Fatalf("model_end: phase = %v, want working", p.phase)
	}

	p.trace(runtime.TraceEvent{Kind: "finish"})
	if p.phase != cliWorking {
		t.Fatalf("finish: phase = %v, want working", p.phase)
	}
}

// TestCLIProgressToolingShowsDetail verifies the tooling busy line reveals
// which call is executing: the shell command for shell, and the tool name as
// a fallback when the args carry no extractable detail (git_status). The
// detail must clear once the call completes, so the line never keeps
// describing a finished command.
func TestCLIProgressToolingShowsDetail(t *testing.T) {
	p := newCLIProgress(nil, "", "")
	p.trace(runtime.TraceEvent{Kind: "tool_start", ToolName: "shell", ToolArgs: `{"command":"go test ./..."}`})
	line := p.busyLine()
	if !strings.Contains(line, "go test ./...") {
		t.Errorf("tooling line missing shell command: %q", line)
	}
	if !strings.Contains(line, "running tool") {
		t.Errorf("tooling line missing phase label: %q", line)
	}

	// The completed call clears the detail: the next tooling line (a
	// different tool) must not echo the previous command.
	p.trace(runtime.TraceEvent{Kind: "tool", ToolName: "shell", ToolArgs: `{"command":"go test ./..."}`})
	if strings.Contains(p.busyLine(), "go test") {
		t.Errorf("tooling line still shows finished command: %q", p.busyLine())
	}

	// Tools without extractable args fall back to their name so the line
	// never says just "running tool".
	q := newCLIProgress(nil, "", "")
	q.trace(runtime.TraceEvent{Kind: "tool_start", ToolName: "git_status"})
	if !strings.Contains(q.busyLine(), "git_status") {
		t.Errorf("tooling line missing fallback tool name: %q", q.busyLine())
	}
}

// TestCLIProgressBusyLine verifies the busy line labels each phase distinctly
// with the same wording as the TUI, shows the live elapsed for the no-output
// phases (prefill, reasoning), advertises the ctrl+c cancel binding, and
// never falls back to the generic "working…" for a distinct phase.
func TestCLIProgressBusyLine(t *testing.T) {
	cases := []struct {
		phase    cliPhase
		label    string
		withTime bool
	}{
		{cliPrefill, "waiting for model response", true},
		{cliReasoning, "reasoning", true},
		{cliTooling, "running tool", false},
		{cliAnswering, "answering", false},
	}
	for _, tc := range cases {
		p := newCLIProgress(nil, "", "")
		p.phase = tc.phase
		p.phaseElapsed = 90 * time.Second

		line := p.busyLine()
		if !strings.Contains(line, tc.label) {
			t.Errorf("phase %v: busy line missing %q: %q", tc.phase, tc.label, line)
		}
		if !strings.Contains(line, "ctrl+c to cancel") {
			t.Errorf("phase %v: busy line missing cancel hint: %q", tc.phase, line)
		}
		if got := strings.Contains(line, "1m30s"); got != tc.withTime {
			t.Errorf("phase %v: elapsed shown = %v, want %v: %q", tc.phase, got, tc.withTime, line)
		}
		if strings.Contains(line, "working…") {
			t.Errorf("phase %v: busy line should not show 'working…': %q", tc.phase, line)
		}
	}
}

// TestCLIProgressWorkingFallback verifies the generic fallback line is used
// before any phase event arrives (startup gap), matching the TUI.
func TestCLIProgressWorkingFallback(t *testing.T) {
	p := newCLIProgress(nil, "", "")
	line := p.busyLine()
	if !strings.Contains(line, "working…") {
		t.Errorf("default busy line missing 'working…': %q", line)
	}
}

// TestFormatModelLabel verifies the "provider * model" tag on the left of the
// busy line: short labels pass through unchanged, long labels are truncated
// to at most cliModelLabelMax characters (model id cut with a "…"), an
// over-long provider truncates the whole label, and empty names yield no tag.
func TestFormatModelLabel(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{
			name:     "short_label_unchanged",
			provider: "default",
			model:    "qwen",
			want:     "default * qwen",
		},
		{
			name:     "fits_exactly_at_limit",
			provider: "a",
			model:    "1234567890123456", // 1 + len(" * ") + 16 = 20 → unchanged
			want:     "a * 1234567890123456",
		},
		{
			name:     "long_model_truncated",
			provider: "anthropic",
			model:    "claude-3-opus-20240229",
			want:     "anthropic * claude-…", // 9 + " * " + 7 + "…" = 20
		},
		{
			name:     "long_provider_truncates_all",
			provider: "super-long-provider-name-here",
			model:    "x",
			want:     "super-long-provider…", // 19 + "…" = 20
		},
		{
			name:     "both_empty_no_label",
			provider: "",
			model:    "",
			want:     "",
		},
		{
			name:     "empty_provider_falls_back_default",
			provider: "",
			model:    "qwen",
			want:     "default * qwen",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatModelLabel(tc.provider, tc.model)
			if utf8.RuneCountInString(got) > cliModelLabelMax {
				t.Errorf("label %q exceeds %d characters (rune count %d)", got, cliModelLabelMax, utf8.RuneCountInString(got))
			}
			if tc.want != "" && got != tc.want {
				t.Errorf("formatModelLabel(%q, %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
			}
		})
	}
}

// TestCLIProgressBusyLineShowsModel verifies the provider * model tag appears
// on the left of the busy line when configured, and is absent when no
// provider/model was given (mirroring the TUI, which never shows the tag).
func TestCLIProgressBusyLineShowsModel(t *testing.T) {
	p := newCLIProgress(nil, "openai", "gpt-4o")
	p.phase = cliPrefill
	p.phaseElapsed = 90 * time.Second

	line := p.busyLine()
	if !strings.Contains(line, "openai * gpt-4o") {
		t.Errorf("busy line missing model tag: %q", line)
	}
	// The tag sits on the left, before the spinner and phase label.
	if strings.Index(line, "openai * gpt-4o") > strings.Index(line, "waiting for model response") {
		t.Errorf("model tag must be on the left of the phase label: %q", line)
	}

	// Without provider/model the tag must not appear at all.
	plain := newCLIProgress(nil, "", "")
	plain.phase = cliPrefill
	if strings.Contains(plain.busyLine(), " * ") {
		t.Errorf("busy line must not show a model tag when none configured: %q", plain.busyLine())
	}
}

// TestCLIProgressModelLabelTruncatedInLine verifies a long model id is
// truncated in the rendered busy line so the tag never exceeds
// cliModelLabelMax characters.
func TestCLIProgressModelLabelTruncatedInLine(t *testing.T) {
	p := newCLIProgress(nil, "openai", "gpt-4o-2024-08-06-very-long-suffix")
	p.phase = cliPrefill

	line := p.busyLine()
	if !strings.Contains(line, "…") {
		t.Errorf("truncated model tag missing ellipsis: %q", line)
	}
	if p.modelLabel == "" {
		t.Fatal("expected a truncated model label")
	}
	if utf8.RuneCountInString(p.modelLabel) > cliModelLabelMax {
		t.Errorf("model label %q exceeds %d characters", p.modelLabel, cliModelLabelMax)
	}
}

// TestCLIProgressStartStop verifies the ticker goroutine renders frames while
// running and that stop clears the line and terminates cleanly.
func TestCLIProgressStartStop(t *testing.T) {
	var buf bytes.Buffer
	p := newCLIProgress(&buf, "", "")
	p.start()
	// Let the ticker render a frame or two.
	time.Sleep(250 * time.Millisecond)
	p.stop()

	out := buf.String()
	if out == "" {
		t.Fatal("expected progress output on start, got none")
	}
	// The final write must clear the line so no spinner frame survives next
	// to the result (which is printed on stdout).
	if !strings.HasSuffix(out, "\x1b[2K\r") {
		t.Errorf("expected final clear sequence, got: %q", out)
	}
	// The animation renders on stderr with carriage returns, never newlines:
	// a piped/`2>`-redirected stderr must not collect progress line garbage.
	if strings.Contains(out, "\n") {
		t.Errorf("progress output must not contain newlines: %q", out)
	}
}

// TestCLIProgressTraceFromTicker verifies the trace handler is safe to call
// from another goroutine while the ticker renders (the production wiring:
// runtime goroutine traces, ticker goroutine renders).
func TestCLIProgressTraceFromTicker(t *testing.T) {
	p := newCLIProgress(nil, "", "")
	p.start()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			p.trace(runtime.TraceEvent{Kind: "delta", Text: "x"})
			time.Sleep(5 * time.Millisecond)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("trace handler blocked while ticker ran")
	}
	// Stop the ticker before reading phase so no goroutine touches it.
	p.stop()
	if p.phase != cliAnswering {
		t.Fatalf("phase = %v, want answering", p.phase)
	}
}
