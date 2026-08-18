package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessageAlwaysSerializesContent(t *testing.T) {
	data, err := json.Marshal(Message{Role: "tool", ToolCallID: "call-1"})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	content, ok := got["content"]
	if !ok {
		t.Fatalf("content field missing from %s", data)
	}
	if content != "" {
		t.Fatalf("content = %v, want empty string", content)
	}
}

func TestNormalizeEffort(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"low", "low"},
		{"MEDIUM", "medium"},
		{" xhigh ", "xhigh"},
		{"high", "low"},
		{"max", "low"},
		{"none", "low"},
		{"unknown", "low"},
	} {
		if got := normalizeEffort(tc.input); got != tc.want {
			t.Errorf("normalizeEffort(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestChatStreamAccumulatesDeltas(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","reasoning_content":"think step"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"Hel"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"lo"}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read_file","arguments":"{\"pa"}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a\"}"}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Model: "test", HTTP: http.DefaultClient}

	var deltas []StreamDelta
	msg, _, err := client.ChatStream(context.Background(), nil, nil, "low", func(d StreamDelta) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "Hello" {
		t.Errorf("content = %q, want %q", msg.Content, "Hello")
	}
	if msg.ReasoningContent != "think step" {
		t.Errorf("reasoning = %q, want %q", msg.ReasoningContent, "think step")
	}
	if msg.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want %q", msg.FinishReason, "tool_calls")
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call-1" || tc.Function.Name != "read_file" || tc.Function.Arguments != `{"path":"a"}` {
		t.Errorf("tool call = %+v", tc)
	}

	var reasoning, content, toolCalls int
	for _, d := range deltas {
		if d.Reasoning != "" {
			reasoning++
		}
		if d.Content != "" {
			content++
		}
		if d.ToolCall != nil {
			toolCalls++
		}
	}
	if reasoning != 1 || content != 2 || toolCalls != 2 {
		t.Errorf("delta counts = reasoning %d, content %d, tool_calls %d; want 1, 2, 2", reasoning, content, toolCalls)
	}
}

func TestChatStreamFallsBackToPlainJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"plain reply"}}]}`)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Model: "test", HTTP: http.DefaultClient}
	msg, _, err := client.ChatStream(context.Background(), nil, nil, "low", nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "plain reply" {
		t.Errorf("content = %q, want %q", msg.Content, "plain reply")
	}
}

func TestChatStreamPropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Model: "test", HTTP: http.DefaultClient}
	_, _, err := client.ChatStream(context.Background(), nil, nil, "low", nil)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want 404", err)
	}
}
