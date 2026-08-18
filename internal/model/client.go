package model

import (
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
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
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
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	ToolChoice  string    `json:"tool_choice,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type response struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Timings *ServerTimings `json:"timings,omitempty"`
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
	BaseURL     string
	APIKey      string
	Model       string
	Temperature float64
	MaxTokens   int
	HTTP        *http.Client
}

func NewFromEnv() *Client {
	return &Client{
		BaseURL:     strings.TrimRight(env("MOTIVE_BASE_URL", env("OPENAI_BASE_URL", "http://127.0.0.1:8080/v1")), "/"),
		APIKey:      env("MOTIVE_API_KEY", env("OPENAI_API_KEY", "")),
		Model:       env("MOTIVE_MODEL", env("OPENAI_MODEL", "Qwen3.8-27B")),
		Temperature: envFloat("MOTIVE_TEMPERATURE", envFloat("OPENAI_TEMPERATURE", 0.6)),
		MaxTokens:   envInt("MOTIVE_MAX_TOKENS", envInt("OPENAI_MAX_TOKENS", 0)),
		HTTP:        &http.Client{Timeout: 10 * time.Minute},
	}
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
	choice := ""
	if len(tools) > 0 {
		choice = "auto"
	}
	body, err := json.Marshal(request{
		Model:       c.Model,
		Messages:    messages,
		Tools:       tools,
		ToolChoice:  choice,
		Temperature: c.Temperature,
		MaxTokens:   c.MaxTokens,
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
	return out.Choices[0].Message, stats, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
