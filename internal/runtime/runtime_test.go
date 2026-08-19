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
