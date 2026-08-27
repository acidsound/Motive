package model

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// TestAssistantToolCallMessageContentNull verifies the strict-provider
// compatibility rule: an assistant message that carries tool_calls but no
// text content must serialize content as null, not "" (MiniMax / Moonshot /
// DeepSeek reject "" and then report the follow-up tool_call_id as invalid).
func TestAssistantToolCallMessageContentNull(t *testing.T) {
	data, err := json.Marshal(Message{
		Role: "assistant",
		ToolCalls: []ToolCall{{ID: "call_abc", Type: "function", Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "read_file", Arguments: "{}"}}},
	})
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
	if content != nil {
		t.Fatalf("content = %v, want null for assistant tool-call message", content)
	}
}

// TestAssistantToolCallMessageWithTextKeepsContent: when the assistant says
// something AND calls tools, the text is preserved verbatim.
func TestAssistantToolCallMessageWithTextKeepsContent(t *testing.T) {
	data, err := json.Marshal(Message{
		Role:      "assistant",
		Content:   "Let me check.",
		ToolCalls: []ToolCall{{ID: "call_abc", Type: "function"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["content"] != "Let me check." {
		t.Fatalf("content = %v, want preserved text", got["content"])
	}
}

func TestAnnotateToolCallError(t *testing.T) {
	// MiniMax's exact error must get the diagnosis hint.
	in := `{"error":{"message":"invalid params, tool call id is invalid (2013)"}}`
	out := annotateToolCallError(in)
	if !strings.Contains(out, "content:null") {
		t.Fatalf("MiniMax error not annotated: %s", out)
	}
	// tool_call_id + invalid also triggers.
	out = annotateToolCallError(`{"error":{"message":"tool_call_id invalid"}}`)
	if !strings.Contains(out, "content:null") {
		t.Fatalf("tool_call_id error not annotated: %s", out)
	}
	// Unrelated errors pass through untouched.
	plain := `{"error":{"message":"model not found"}}`
	if annotateToolCallError(plain) != plain {
		t.Fatalf("unrelated error was annotated: %s", annotateToolCallError(plain))
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
		{"high", "high"},
		{"MAX", "max"},
		{"max", "max"},
		{"off", "off"},
		{"OFF", "off"},
		{"none", "off"},
		{"unknown", "low"},
	} {
		if got := normalizeEffort(tc.input); got != tc.want {
			t.Errorf("normalizeEffort(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestEffortOffOmitsReasoningFields verifies that effort "off" (the mode for
// endpoints that reject reasoning_effort) omits both the top-level
// reasoning_effort field and the chat_template_kwargs entry from the request
// body, in both the streaming and non-streaming paths. A non-off effort still
// carries both, so the on/off switch is the only thing that toggles them.
func TestEffortOffOmitsReasoningFields(t *testing.T) {
	type call struct {
		body map[string]any
	}
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		calls = append(calls, call{body: body})
		if v, _ := body["stream"].(bool); v {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()
	client := &Client{BaseURL: srv.URL, Model: "test", HTTP: http.DefaultClient}

	// Non-streaming, off: neither field present.
	if _, _, err := client.ChatWithEffort(context.Background(), nil, nil, "off"); err != nil {
		t.Fatalf("ChatWithEffort: %v", err)
	}
	if _, _, err := client.ChatStream(context.Background(), nil, nil, "off", nil); err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	// Non-streaming + streaming with a real level: both fields present.
	if _, _, err := client.ChatWithEffort(context.Background(), nil, nil, "high"); err != nil {
		t.Fatalf("ChatWithEffort high: %v", err)
	}
	if _, _, err := client.ChatStream(context.Background(), nil, nil, "high", nil); err != nil {
		t.Fatalf("ChatStream high: %v", err)
	}

	if len(calls) != 4 {
		t.Fatalf("calls = %d, want 4", len(calls))
	}
	for i, c := range calls[:2] {
		if _, ok := c.body["reasoning_effort"]; ok {
			t.Errorf("call %d: reasoning_effort present in body: %v", i, c.body["reasoning_effort"])
		}
		if _, ok := c.body["chat_template_kwargs"]; ok {
			t.Errorf("call %d: chat_template_kwargs present in body: %v", i, c.body["chat_template_kwargs"])
		}
	}
	for i, c := range calls[2:] {
		if got := c.body["reasoning_effort"]; got != "high" {
			t.Errorf("call %d: reasoning_effort = %v, want high", i+2, got)
		}
		kw, ok := c.body["chat_template_kwargs"].(map[string]any)
		if !ok || kw["reasoning_effort"] != "high" {
			t.Errorf("call %d: chat_template_kwargs = %v, want {reasoning_effort: high}", i+2, c.body["chat_template_kwargs"])
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
	if msg.Role != "assistant" {
		t.Errorf("role = %q, want %q", msg.Role, "assistant")
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

func TestChatStreamDefaultsRoleWhenOmittedInDeltas(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hi"}}]}]`,
		`data: [DONE]`,
		``,
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Model: "test", HTTP: http.DefaultClient}
	msg, _, err := client.ChatStream(context.Background(), nil, nil, "low", nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role != "assistant" {
		t.Fatalf("msg.Role = %q, want assistant", msg.Role)
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
	if msg.Role != "assistant" {
		t.Errorf("role = %q, want %q", msg.Role, "assistant")
	}
	if msg.Content != "plain reply" {
		t.Errorf("content = %q, want %q", msg.Content, "plain reply")
	}
}

func TestChatWithEffortDefaultsRole(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"no role in json"}}]}`)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Model: "test", HTTP: http.DefaultClient}
	msg, _, err := client.ChatWithEffort(context.Background(), nil, nil, "low")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role != "assistant" {
		t.Fatalf("msg.Role = %q, want assistant", msg.Role)
	}
	if msg.Content != "no role in json" {
		t.Fatalf("msg.Content = %q, want %q", msg.Content, "no role in json")
	}
}

func TestListModelsDataShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		fmt.Fprint(w, `{"object":"list","data":[
			{"id":"gpt-4o","object":"model","owned_by":"openai"},
			{"id":"gpt-4o-mini","object":"model","owned_by":"openai"}
		]}`)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, HTTP: http.DefaultClient}
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := ModelIds(models); len(got) != 2 || got[0] != "gpt-4o" || got[1] != "gpt-4o-mini" {
		t.Fatalf("ids = %v, want [gpt-4o gpt-4o-mini]", got)
	}
	if models[0].OwnedBy != "openai" {
		t.Errorf("owned_by = %q, want openai", models[0].OwnedBy)
	}
}

func TestListModelsBareIDArrayShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `["llama3","mistral"]`)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, HTTP: http.DefaultClient}
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := ModelIds(models); len(got) != 2 || got[0] != "llama3" || got[1] != "mistral" {
		t.Fatalf("ids = %v, want [llama3 mistral]", got)
	}
}

func TestListModelsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, HTTP: http.DefaultClient}
	if _, err := client.ListModels(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSetModel(t *testing.T) {
	c := &Client{Model: "old"}
	c.SetModel("  new-model  ")
	if c.Model != "new-model" {
		t.Errorf("Model = %q, want new-model", c.Model)
	}
	c.SetModel("   ")
	if c.Model != "new-model" {
		t.Errorf("Model = %q after blank, want unchanged new-model", c.Model)
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
