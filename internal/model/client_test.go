package model

import (
	"encoding/json"
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
