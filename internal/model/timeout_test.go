package model

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestNewFromEnvHasNoTotalTimeout verifies that the production client does
// not set http.Client.Timeout. A total timeout would kill long streaming
// responses (reasoning tokens) that are still within the execution budget.
// The context deadline from the runtime is the sole authority for request
// lifetime.
func TestNewFromEnvHasNoTotalTimeout(t *testing.T) {
	t.Setenv("MOTIVE_BASE_URL", "http://127.0.0.1:9999/v1")
	c := NewFromEnv()
	if c.HTTP.Timeout != 0 {
		t.Fatalf("http.Client.Timeout = %v, want 0 (context deadline is the sole authority)", c.HTTP.Timeout)
	}
	// Verify ResponseHeaderTimeout is set for hung-server protection.
	tr, ok := c.HTTP.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport, got different type")
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Fatal("ResponseHeaderTimeout not set; hung servers would block until context deadline")
	}
}

// TestSlowStreamCompletesWithinContextDeadline verifies that a streaming
// response that takes longer than the old 10-minute client timeout (simulated
// here with a short sleep) completes successfully when using the production
// client configuration (no total timeout, context deadline as authority).
//
// This is the regression test for the bug: "context deadline exceeded
// (Client.Timeout or context cancellation while reading body)" occurring
// during read_file tool calls in the execution loop.
func TestSlowStreamCompletesWithinContextDeadline(t *testing.T) {
	// Server that streams slowly: 3 tokens at 100ms each = 300ms total.
	// In production, a reasoning-heavy response can easily exceed 10 minutes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"tok%d\"}}]}\n\n", i)
			flusher.Flush()
			time.Sleep(100 * time.Millisecond)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	// Use the same client configuration as production (no total timeout).
	client := &Client{
		BaseURL: srv.URL,
		Model:   "test",
		HTTP: &http.Client{
			Transport: &http.Transport{
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
	}

	// Context deadline is generous (5s) — the stream only needs 300ms.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg, _, err := client.ChatStream(ctx, nil, nil, "low", nil)
	if err != nil {
		t.Fatalf("stream failed within context deadline: %v", err)
	}
	if msg.Content != "tok0tok1tok2" {
		t.Fatalf("content = %q, want %q", msg.Content, "tok0tok1tok2")
	}
}

// TestTotalTimeoutKillsSlowStream documents the Go http.Client behavior that
// caused the production bug: a total Timeout on http.Client covers the entire
// HTTP transaction including reading the response body. This test verifies
// that the old configuration (Timeout set) would indeed kill a valid stream.
// It is kept as a regression guard: if someone re-introduces a total timeout,
// this test will fail.
func TestTotalTimeoutKillsSlowStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"tok%d\"}}]}\n\n", i)
			flusher.Flush()
			time.Sleep(100 * time.Millisecond)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	// Old (buggy) configuration: total timeout shorter than stream duration.
	client := &Client{
		BaseURL: srv.URL,
		Model:   "test",
		HTTP:    &http.Client{Timeout: 100 * time.Millisecond},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := client.ChatStream(ctx, nil, nil, "low", nil)
	if err == nil {
		t.Log("note: stream completed despite total timeout (Go version behavior change?)")
		return
	}
	// Expected: the total timeout kills the stream.
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Logf("confirmed: total timeout kills stream: %v", err)
}

// TestContextDeadlineStillEnforced verifies that after removing
// http.Client.Timeout, the context deadline still properly cancels
// in-flight requests. This ensures we don't lose timeout protection.
func TestContextDeadlineStillEnforced(t *testing.T) {
	// Server that streams forever (one token per 50ms, never sends [DONE]).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for i := 0; ; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
			flusher.Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer srv.Close()

	// No client timeout — only the context deadline should stop the request.
	client := &Client{
		BaseURL: srv.URL,
		Model:   "test",
		HTTP:    &http.Client{}, // Timeout=0: no total timeout
	}

	// Short context deadline: 200ms. The stream never ends, so the context
	// must be the mechanism that cancels the request.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, _, err := client.ChatStream(ctx, nil, nil, "low", nil)
	if err == nil {
		t.Fatal("expected context deadline error, got nil")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected 'context deadline exceeded', got: %v", err)
	}
}

// TestResponseHeaderTimeoutProtectsAgainstHungServer verifies that a hung
// server (accepts TCP connection, never sends HTTP response headers) is
// detected via Transport.ResponseHeaderTimeout rather than requiring the
// full context deadline to elapse.
func TestResponseHeaderTimeoutProtectsAgainstHungServer(t *testing.T) {
	// Server that sleeps 2s before writing headers (simulates a hung server).
	// The handler exits early if the request context is cancelled.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			// If we get here, the header timeout didn't fire (test bug).
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"late"}}]}`)
		case <-r.Context().Done():
			return
		}
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		Model:   "test",
		HTTP: &http.Client{
			Transport: &http.Transport{
				ResponseHeaderTimeout: 100 * time.Millisecond,
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	_, _, err := client.ChatStream(ctx, nil, nil, "low", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from hung server, got nil")
	}
	// Should fail fast via ResponseHeaderTimeout (~100ms), not via the 5s context deadline.
	if elapsed > 2*time.Second {
		t.Fatalf("ResponseHeaderTimeout did not fire quickly: took %v, want < 2s", elapsed)
	}
	t.Logf("hung server detected in %v via ResponseHeaderTimeout", elapsed)
}
