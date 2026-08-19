package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/tools"
	"github.com/acidsound/Motive/internal/workspace"
)

func TestContextBlockOutsideRepo(t *testing.T) {
	r := &Runtime{WS: workspace.New(t.TempDir())}
	block := r.ContextBlock()
	if strings.Contains(block, "fatal:") {
		t.Fatalf("context block contains git error output:\n%s", block)
	}
	if strings.Contains(block, "Git status:") {
		t.Fatalf("context block should not contain a git status section outside a repository:\n%s", block)
	}
}

func TestTruncateUTF8(t *testing.T) {
	s := strings.Repeat("한", 3000) // 9000 bytes
	got := truncateUTF8(s, 6000)
	if len(got) > 6000 {
		t.Fatalf("len = %d, want <= 6000", len(got))
	}
	if len(got)%3 != 0 {
		t.Fatalf("cut splits a rune: len = %d", len(got))
	}
	if got != s[:len(got)] {
		t.Fatal("truncated string is not a prefix of the input")
	}
}

func TestBoundedEnvInt(t *testing.T) {
	const key = "MOTIVE_TEST_BOUNDED"
	t.Setenv(key, "999")
	if got := boundedEnvInt(key, 32, 256); got != 256 {
		t.Fatalf("boundedEnvInt high = %d, want 256", got)
	}
	t.Setenv(key, "0")
	if got := boundedEnvInt(key, 32, 256); got != 32 {
		t.Fatalf("boundedEnvInt zero = %d, want 32", got)
	}
}

func TestEstimateContextTokens(t *testing.T) {
	small := []model.Message{{Role: "user", Content: "hi"}}
	big := []model.Message{{Role: "user", Content: strings.Repeat("x", 4096)}}
	s := estimateContextTokens(small)
	b := estimateContextTokens(big)
	if b <= s {
		t.Fatalf("estimate must grow with content: small=%d big=%d", s, b)
	}
	if got := estimateContextTokens(nil); got < 1 {
		t.Fatalf("estimate of empty context = %d, want >= 1", got)
	}
}

func TestContextAccountingRecord(t *testing.T) {
	// Unlimited accounting (MaxTokens = 0) must never report overflow.
	a := ContextAccounting{}
	a.Record([]model.Message{{Role: "user", Content: strings.Repeat("x", 4096)}})
	if a.Overflow {
		t.Fatal("unlimited accounting reported overflow")
	}
	if a.PeakRequest != a.LastRequest {
		t.Fatalf("peak=%d, last=%d; first record must set both", a.PeakRequest, a.LastRequest)
	}

	// Limited accounting: small context fits, grown context overflows.
	a = ContextAccounting{MaxTokens: 100}
	a.Record([]model.Message{{Role: "user", Content: "hello"}})
	if a.Overflow {
		t.Fatal("small context reported overflow under limit 100")
	}
	a.Record([]model.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: strings.Repeat("y", 1600)},
	})
	if !a.Overflow {
		t.Fatal("grown context did not report overflow")
	}
	if a.PeakRequest < a.LastRequest {
		t.Fatalf("peak=%d below last=%d", a.PeakRequest, a.LastRequest)
	}
}

func TestObservationFormat(t *testing.T) {
	// Basic: no limit, no overflow, no revision change.
	o := Observation{
		Step:              3,
		MaxSteps:          32,
		ToolCalls:         5,
		MaxToolCalls:      128,
		ToolFailures:      1,
		LastToolFailure:   true,
		ContextTokens:     1234,
		PeakContextTokens: 1234,
		Elapsed:           45 * time.Second,
		ReasoningEffort:   "xhigh",
		BaseRevision:      "abcdef1234567890",
		ResultRevision:    "abcdef1234567890",
	}
	s := o.Format()
	if !strings.HasPrefix(s, "[execution-state]\n") {
		t.Fatalf("format must start with [execution-state]:\n%s", s)
	}
	if !strings.Contains(s, "step=3/32") {
		t.Fatalf("missing step: %s", s)
	}
	if !strings.Contains(s, "tools=5/128") {
		t.Fatalf("missing tools: %s", s)
	}
	if !strings.Contains(s, "failures=1") {
		t.Fatalf("missing failures: %s", s)
	}
	if !strings.Contains(s, "last=FAIL") {
		t.Fatalf("missing last=FAIL: %s", s)
	}
	if !strings.Contains(s, "context=1234 peak=1234") {
		t.Fatalf("missing context: %s", s)
	}
	if !strings.Contains(s, "effort=xhigh") {
		t.Fatalf("missing effort: %s", s)
	}
	// No limit set: should not show limit or OVERFLOW.
	if strings.Contains(s, "/1234") && !strings.Contains(s, "step=3/32") {
		t.Fatalf("unexpected limit format: %s", s)
	}
	if strings.Contains(s, "OVERFLOW") {
		t.Fatalf("should not show OVERFLOW: %s", s)
	}
	// Same revision: should show single rev, not arrow.
	if strings.Contains(s, "→") {
		t.Fatalf("same revision should not use arrow: %s", s)
	}

	// With limit and overflow.
	o.MaxContextTokens = 1000
	o.ContextOverflow = true
	o.ResultRevision = "fedcba0987654321"
	s = o.Format()
	if !strings.Contains(s, "context=1234/1000") {
		t.Fatalf("missing context with limit: %s", s)
	}
	if !strings.Contains(s, "OVERFLOW") {
		t.Fatalf("missing OVERFLOW: %s", s)
	}
	if !strings.Contains(s, "→") {
		t.Fatalf("different revisions should use arrow: %s", s)
	}

	// Server prompt N.
	o.ServerPromptN = 42
	s = o.Format()
	if !strings.Contains(s, "server_prompt_n=42") {
		t.Fatalf("missing server_prompt_n: %s", s)
	}
}

func TestExecuteContextAccounting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"}}],"timings":{"prompt_n":42,"predicted_n":3}}`)
	}))
	defer server.Close()

	ws := workspace.New(t.TempDir())
	rt := &Runtime{
		Model: &model.Client{
			BaseURL:         server.URL,
			Model:           "test",
			ReasoningEffort: "low",
			HTTP:            server.Client(),
		},
		WS:               ws,
		Exec:             &tools.Executor{WS: ws},
		MaxSteps:         4,
		MaxContextTokens: 1, // deliberately tiny: overflow must be accounted, not enforced
		Budget:           ExecutionBudget{MaxSteps: 4, MaxDuration: time.Minute, MaxToolCalls: 8},
	}
	var events []TraceEvent
	rt.Trace = func(e TraceEvent) { events = append(events, e) }

	out, err := rt.Execute(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "done" {
		t.Fatalf("output = %q, want done", out)
	}

	var sawStart, sawModelEnd, sawFinish bool
	for _, e := range events {
		switch e.Kind {
		case "start":
			sawStart = true
			if e.ContextTokens < 1 {
				t.Fatalf("start ContextTokens = %d, want >= 1", e.ContextTokens)
			}
		case "model_end":
			sawModelEnd = true
			if e.ContextTokens < 1 {
				t.Fatalf("model_end ContextTokens = %d, want >= 1", e.ContextTokens)
			}
			if e.ServerPromptN != 42 {
				t.Fatalf("model_end ServerPromptN = %d, want 42", e.ServerPromptN)
			}
		case "finish":
			sawFinish = true
			if e.ContextTokens < 1 || e.PeakContextTokens < 1 {
				t.Fatalf("finish ContextTokens=%d PeakContextTokens=%d, want >= 1", e.ContextTokens, e.PeakContextTokens)
			}
		}
	}
	if !sawStart || !sawModelEnd || !sawFinish {
		t.Fatalf("missing trace events: start=%v model_end=%v finish=%v", sawStart, sawModelEnd, sawFinish)
	}
}

func TestExecuteObservationAppended(t *testing.T) {
	// Server that returns a tool call on the first request, then a final response.
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			// First call: return a tool call to read a file.
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]}}]}`)
		} else {
			// Second call: final response.
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"all done"}}]}`)
		}
	}))
	defer server.Close()

	// Create a workspace with a README.md so the tool call succeeds.
	dir := t.TempDir()
	ws := workspace.New(dir)
	if err := ws.Write("README.md", "test file\n"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rt := &Runtime{
		Model: &model.Client{
			BaseURL:         server.URL,
			Model:           "test",
			ReasoningEffort: "low",
			HTTP:            server.Client(),
		},
		WS:       ws,
		Exec:     &tools.Executor{WS: ws},
		MaxSteps: 4,
		Budget:   ExecutionBudget{MaxSteps: 4, MaxDuration: time.Minute, MaxToolCalls: 8},
	}
	var events []TraceEvent
	rt.Trace = func(e TraceEvent) { events = append(events, e) }

	out, err := rt.Execute(context.Background(), "read the readme")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "all done" {
		t.Fatalf("output = %q, want %q", out, "all done")
	}

	// Verify the second model request included the observation.
	// We can't directly inspect the request, but we can verify:
	// 1. The execution completed (2 model calls).
	// 2. The trace shows a tool event.
	var sawTool bool
	for _, e := range events {
		if e.Kind == "tool" {
			sawTool = true
		}
	}
	if !sawTool {
		t.Fatal("expected a tool trace event")
	}

	// The key assertion: the second model_start should have a higher
	// MessageCount than the first, reflecting the appended observation.
	var firstMsgCount, secondMsgCount int
	modelStarts := 0
	for _, e := range events {
		if e.Kind == "model_start" {
			modelStarts++
			if modelStarts == 1 {
				firstMsgCount = e.MessageCount
			} else if modelStarts == 2 {
				secondMsgCount = e.MessageCount
			}
		}
	}
	if modelStarts != 2 {
		t.Fatalf("expected 2 model_start events, got %d", modelStarts)
	}
	// After tool call: assistant msg + tool result + observation = +3 messages
	if secondMsgCount < firstMsgCount+3 {
		t.Fatalf("second model_start MessageCount=%d, want >= %d (first=%d + assistant + tool + observation)", secondMsgCount, firstMsgCount+3, firstMsgCount)
	}
}
