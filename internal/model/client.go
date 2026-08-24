package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Role string `json:"role"`
	// Content is the plain-text body. When ContentParts is non-empty it is
	// ignored during marshaling and "content" is emitted as the multimodal
	// content array instead (OpenAI-compatible). Responses from the server
	// always decode into Content.
	Content string `json:"-"`
	// ContentParts carries a multimodal user message (text + image/video
	// parts). See MarshalJSON.
	ContentParts     []ContentPart `json:"-"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"`
	FinishReason     string        `json:"-"`
}

// MarshalJSON serializes the message. The "content" field is always present:
// the multimodal content array when ContentParts is set (attachments), the
// plain string otherwise (including the empty string, matching the previous
// behavior that tool messages always carry an explicit content key).
func (m Message) MarshalJSON() ([]byte, error) {
	type alias Message
	aux := struct {
		Role             string          `json:"role"`
		Content          json.RawMessage `json:"content"`
		ReasoningContent string          `json:"reasoning_content,omitempty"`
		ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
		ToolCallID       string          `json:"tool_call_id,omitempty"`
	}{Role: m.Role, ReasoningContent: m.ReasoningContent, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID}
	if len(m.ContentParts) > 0 {
		parts, err := json.Marshal(m.ContentParts)
		if err != nil {
			return nil, err
		}
		aux.Content = parts
	} else {
		content, err := json.Marshal(m.Content)
		if err != nil {
			return nil, err
		}
		aux.Content = content
	}
	return json.Marshal(aux)
}

// UnmarshalJSON decodes a server response message. Assistant and tool
// messages always carry a string content, so "content" is read as plain text;
// a multimodal array is not expected in responses and is ignored.
func (m *Message) UnmarshalJSON(data []byte) error {
	var aux struct {
		Role             string          `json:"role"`
		Content          json.RawMessage `json:"content"`
		ReasoningContent string          `json:"reasoning_content"`
		ToolCalls        []ToolCall      `json:"tool_calls"`
		ToolCallID       string          `json:"tool_call_id"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	m.Role = aux.Role
	m.ReasoningContent = aux.ReasoningContent
	m.ToolCalls = aux.ToolCalls
	m.ToolCallID = aux.ToolCallID
	if len(aux.Content) > 0 && string(aux.Content) != "null" {
		var s string
		if err := json.Unmarshal(aux.Content, &s); err == nil {
			m.Content = s
		}
	}
	return nil
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  Parameters `json:"parameters"`
}

type Parameters struct {
	Type       string                  `json:"type"`
	Properties map[string]ToolProperty `json:"properties,omitempty"`
	Required   []string                `json:"required,omitempty"`
}

type ToolProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type request struct {
	Model              string            `json:"model"`
	Messages           []Message         `json:"messages"`
	Tools              []Tool            `json:"tools,omitempty"`
	ToolChoice         string            `json:"tool_choice,omitempty"`
	Temperature        float64           `json:"temperature"`
	MaxTokens          int               `json:"max_tokens,omitempty"`
	ReasoningEffort    string            `json:"reasoning_effort,omitempty"`
	ChatTemplateKwargs map[string]string `json:"chat_template_kwargs,omitempty"`
	Stream             bool              `json:"stream,omitempty"`
}

type response struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Timings *ServerTimings `json:"timings,omitempty"`
}

// StreamDelta carries one incremental chunk of a streamed chat completion.
// Exactly one of Content, Reasoning, or ToolCall is set per delta in practice,
// but callers should treat them as independent accumulators.
type StreamDelta struct {
	Content   string
	Reasoning string
	ToolCall  *StreamToolCallDelta
}

type StreamToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Role             string                `json:"role"`
			Content          string                `json:"content"`
			ReasoningContent string                `json:"reasoning_content"`
			ToolCalls        []streamToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Timings *ServerTimings `json:"timings,omitempty"`
}

type streamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ServerTimings struct {
	CacheN              int     `json:"cache_n"`
	PromptN             int     `json:"prompt_n"`
	PromptMS            float64 `json:"prompt_ms"`
	PromptPerTokenMS    float64 `json:"prompt_per_token_ms"`
	PromptPerSecond     float64 `json:"prompt_per_second"`
	PredictedN          int     `json:"predicted_n"`
	PredictedMS         float64 `json:"predicted_ms"`
	PredictedPerTokenMS float64 `json:"predicted_per_token_ms"`
	PredictedPerSecond  float64 `json:"predicted_per_second"`
	DraftN              int     `json:"draft_n"`
	DraftNAccepted      int     `json:"draft_n_accepted"`
}

type ChatStats struct {
	RequestBytes         int
	EstimatedInputTokens int
	ResponseBytes        int
	Latency              time.Duration
	ServerTimings        *ServerTimings
}

type Client struct {
	BaseURL         string
	APIKey          string
	Model           string
	Temperature     float64
	MaxTokens       int
	ReasoningEffort string
	HTTP            *http.Client
}

// NewHTTPClient returns the shared default HTTP client for model requests.
//
// It deliberately sets no total http.Client.Timeout. The runtime/request
// context deadline is the sole authority for the full lifetime of a request,
// including long streaming (reasoning) responses. A total client timeout would
// kill a valid stream that is still within the execution budget. For
// hung-server protection, ResponseHeaderTimeout is kept: a server that accepts
// the connection but never sends response headers fails fast instead of
// waiting for the context deadline to elapse.
//
// Both NewFromEnv and the production entrypoint must use this factory so the
// timeout authority never diverges between the test-protected policy and the
// actual production execution path.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

func NewFromEnv() *Client {
	return &Client{
		BaseURL:         strings.TrimRight(env("MOTIVE_BASE_URL", env("OPENAI_BASE_URL", "http://127.0.0.1:8080/v1")), "/"),
		APIKey:          env("MOTIVE_API_KEY", env("OPENAI_API_KEY", "")),
		Model:           env("MOTIVE_MODEL", env("OPENAI_MODEL", "Qwen3.8-27B")),
		Temperature:     envFloat("MOTIVE_TEMPERATURE", envFloat("OPENAI_TEMPERATURE", 0.6)),
		MaxTokens:       envInt("MOTIVE_MAX_TOKENS", envInt("OPENAI_MAX_TOKENS", 0)),
		ReasoningEffort: normalizeEffort(env("MOTIVE_REASONING_EFFORT", "low")),
		HTTP:            NewHTTPClient(),
	}
}

// normalizeEffort accepts the full standard reasoning-effort vocabulary and
// passes recognized values straight through, letting the provider reject any
// level it does not actually support. Only unknown or empty values fall back
// to the Motive default of "low".
func normalizeEffort(v string) string {
	n := strings.ToLower(strings.TrimSpace(v))
	switch n {
	case "low", "medium", "high", "xhigh", "max":
		return n
	default:
		return "low"
	}
}

func (c *Client) SetReasoningEffort(effort string) {
	c.ReasoningEffort = normalizeEffort(effort)
}

// SetEndpoint switches the active endpoint. Empty fields keep their current
// value so callers can change just one of base URL, model, or API key.
func (c *Client) SetEndpoint(baseURL, model, apiKey string) {
	if strings.TrimSpace(baseURL) != "" {
		c.BaseURL = strings.TrimRight(baseURL, "/")
	}
	if strings.TrimSpace(model) != "" {
		c.Model = model
	}
	if apiKey != "" {
		c.APIKey = apiKey
	}
}

func (c *Client) GetReasoningEffort() string {
	return c.ReasoningEffort
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func (c *Client) Chat(ctx context.Context, messages []Message, tools []Tool) (Message, ChatStats, error) {
	return c.ChatWithEffort(ctx, messages, tools, c.ReasoningEffort)
}

func (c *Client) ChatWithEffort(ctx context.Context, messages []Message, tools []Tool, effort string) (Message, ChatStats, error) {
	choice := ""
	if len(tools) > 0 {
		choice = "auto"
	}
	effort = normalizeEffort(effort)
	body, err := json.Marshal(request{
		Model:              c.Model,
		Messages:           messages,
		Tools:              tools,
		ToolChoice:         choice,
		Temperature:        c.Temperature,
		MaxTokens:          c.MaxTokens,
		ReasoningEffort:    effort,
		ChatTemplateKwargs: map[string]string{"reasoning_effort": effort},
	})
	if err != nil {
		return Message{}, ChatStats{}, fmt.Errorf("marshal request: %w", err)
	}
	stats := ChatStats{
		RequestBytes:         len(body),
		EstimatedInputTokens: max(1, len(body)/4),
	}
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, stats, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	stats.Latency = time.Since(started)
	if err != nil {
		return Message{}, stats, fmt.Errorf("model request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return Message{}, stats, fmt.Errorf("read response: %w", err)
	}
	stats.ResponseBytes = len(data)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Message{}, stats, fmt.Errorf("model returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var out response
	if err := json.Unmarshal(data, &out); err != nil {
		return Message{}, stats, fmt.Errorf("decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return Message{}, stats, fmt.Errorf("model returned no choices")
	}
	stats.ServerTimings = out.Timings
	msg := out.Choices[0].Message
	if msg.Role == "" {
		msg.Role = "assistant"
	}
	return msg, stats, nil
}

// ChatStream is ChatWithEffort with incremental delivery. Each streamed chunk
// is passed to onDelta (if non-nil) as it arrives; the returned Message is the
// fully accumulated assistant message, including tool calls. Servers that
// ignore the stream flag and reply with a plain JSON body are handled by
// falling back to a single non-streaming parse.
func (c *Client) ChatStream(ctx context.Context, messages []Message, tools []Tool, effort string, onDelta func(StreamDelta)) (Message, ChatStats, error) {
	choice := ""
	if len(tools) > 0 {
		choice = "auto"
	}
	effort = normalizeEffort(effort)
	body, err := json.Marshal(request{
		Model:              c.Model,
		Messages:           messages,
		Tools:              tools,
		ToolChoice:         choice,
		Temperature:        c.Temperature,
		MaxTokens:          c.MaxTokens,
		ReasoningEffort:    effort,
		ChatTemplateKwargs: map[string]string{"reasoning_effort": effort},
		Stream:             true,
	})
	if err != nil {
		return Message{}, ChatStats{}, fmt.Errorf("marshal request: %w", err)
	}
	stats := ChatStats{
		RequestBytes:         len(body),
		EstimatedInputTokens: max(1, len(body)/4),
	}
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, stats, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	stats.Latency = time.Since(started)
	if err != nil {
		return Message{}, stats, fmt.Errorf("model request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		stats.ResponseBytes = len(data)
		return Message{}, stats, fmt.Errorf("model returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	msg, stats, err := c.consumeStream(resp.Body, stats, onDelta)
	if err != nil {
		return Message{}, stats, err
	}
	return msg, stats, nil
}

func (c *Client) consumeStream(body io.Reader, stats ChatStats, onDelta func(StreamDelta)) (Message, ChatStats, error) {
	msg := Message{Role: "assistant"}
	reader := bufio.NewReader(body)
	first := true
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			stats.ResponseBytes += len(line)
		}
		trimmed := strings.TrimSpace(line)
		if first && trimmed != "" && !strings.HasPrefix(trimmed, "data:") && strings.HasPrefix(trimmed, "{") {
			// Non-streaming fallback: the server ignored stream=true and sent a
			// single JSON document. Read the remainder and parse it normally.
			rest, rerr := io.ReadAll(reader)
			stats.ResponseBytes += len(rest)
			payload := append([]byte(trimmed), rest...)
			var out response
			if uerr := json.Unmarshal(payload, &out); uerr != nil {
				if rerr == nil && err == io.EOF {
					rerr = nil
				}
				if rerr != nil && rerr != io.EOF {
					return Message{}, stats, fmt.Errorf("read response: %w", rerr)
				}
				return Message{}, stats, fmt.Errorf("decode response: %w", uerr)
			}
			stats.ServerTimings = out.Timings
			if len(out.Choices) == 0 {
				return Message{}, stats, fmt.Errorf("model returned no choices")
			}
			out.Choices[0].Message.FinishReason = "stop"
			if out.Choices[0].Message.Role == "" {
				out.Choices[0].Message.Role = "assistant"
			}
			return out.Choices[0].Message, stats, nil
		}
		first = false
		if err == io.EOF {
			break
		}
		if err != nil {
			return Message{}, stats, fmt.Errorf("read stream: %w", err)
		}
		if trimmed == "" || !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk streamChunk
		if uerr := json.Unmarshal([]byte(payload), &chunk); uerr != nil {
			// Skip keep-alive comments or partial lines rather than failing the
			// whole request; a conforming server only sends valid JSON chunks.
			continue
		}
		if chunk.Timings != nil {
			stats.ServerTimings = chunk.Timings
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if ch.Delta.Role != "" {
			msg.Role = ch.Delta.Role
		}
		var delta StreamDelta
		if ch.Delta.ReasoningContent != "" {
			msg.ReasoningContent += ch.Delta.ReasoningContent
			delta.Reasoning = ch.Delta.ReasoningContent
		}
		if ch.Delta.Content != "" {
			msg.Content += ch.Delta.Content
			delta.Content = ch.Delta.Content
		}
		for _, tc := range ch.Delta.ToolCalls {
			if tc.Index < 0 {
				tc.Index = 0
			}
			for len(msg.ToolCalls) <= tc.Index {
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{Type: "function"})
			}
			dst := &msg.ToolCalls[tc.Index]
			if tc.ID != "" {
				dst.ID = tc.ID
			}
			if tc.Type != "" {
				dst.Type = tc.Type
			}
			if tc.Function.Name != "" {
				dst.Function.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				dst.Function.Arguments += tc.Function.Arguments
			}
			if delta.ToolCall == nil {
				delta.ToolCall = &StreamToolCallDelta{Index: tc.Index, ID: dst.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments}
			}
		}
		if ch.FinishReason != "" {
			msg.FinishReason = ch.FinishReason
		}
		if onDelta != nil && (delta.Content != "" || delta.Reasoning != "" || delta.ToolCall != nil) {
			onDelta(delta)
		}
	}
	return msg, stats, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
