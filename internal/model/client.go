package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
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
	Model      string    `json:"model"`
	Messages   []Message `json:"messages"`
	Tools      []Tool    `json:"tools,omitempty"`
	ToolChoice string    `json:"tool_choice,omitempty"`
}

type response struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

func NewFromEnv() *Client {
	return &Client{
		BaseURL: strings.TrimRight(env("MOTIVE_BASE_URL", env("OPENAI_BASE_URL", "http://127.0.0.1:8080/v1")), "/"),
		APIKey:  env("MOTIVE_API_KEY", env("OPENAI_API_KEY", "")),
		Model:   env("MOTIVE_MODEL", env("OPENAI_MODEL", "Qwen3.8-27B")),
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
	}
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func (c *Client) Chat(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	choice := ""
	if len(tools) > 0 {
		choice = "auto"
	}
	body, err := json.Marshal(request{Model: c.Model, Messages: messages, Tools: tools, ToolChoice: choice})
	if err != nil {
		return Message{}, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("model request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return Message{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Message{}, fmt.Errorf("model returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var out response
	if err := json.Unmarshal(data, &out); err != nil {
		return Message{}, fmt.Errorf("decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return Message{}, fmt.Errorf("model returned no choices")
	}
	return out.Choices[0].Message, nil
}
