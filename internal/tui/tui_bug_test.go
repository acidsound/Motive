package tui

import (
	"testing"

	llm "github.com/acidsound/Motive/internal/model"
)

// TestFinishTurnNoStreaming verifies that when no delta events were received
// (assistant message is still empty from submit), finishTurn fills the
// existing assistant message instead of appending a duplicate.
func TestFinishTurnNoStreaming(t *testing.T) {
	m := newTestModel()
	m.rt.Model = &llm.Client{}

	// Simulate what submit() does: user msg + empty assistant msg
	m.appendMessage(message{role: "user", content: "hello"})
	m.appendMessage(message{role: "assistant"})
	m.busy = true

	// Simulate doneMsg arriving with the full text (no deltas came through)
	m2, _ := m.finishTurn(doneMsg{text: "world", err: nil})
	m = *m2.(*model)

	if len(m.messages) != 2 {
		t.Fatalf("messages = %d, want 2 (got: %+v)", len(m.messages), m.messages)
	}
	if m.messages[1].role != "assistant" {
		t.Fatalf("last message role = %q, want assistant", m.messages[1].role)
	}
	if m.messages[1].content != "world" {
		t.Errorf("assistant content = %q, want %q", m.messages[1].content, "world")
	}
}

// TestFinishTurnWithStreaming verifies that when deltas already filled the
// assistant message, finishTurn does NOT append a duplicate.
func TestFinishTurnWithStreaming(t *testing.T) {
	m := newTestModel()
	m.rt.Model = &llm.Client{}

	m.appendMessage(message{role: "user", content: "hello"})
	m.appendMessage(message{role: "assistant", content: "world"})
	m.busy = true

	m2, _ := m.finishTurn(doneMsg{text: "world", err: nil})
	m = *m2.(*model)

	if len(m.messages) != 2 {
		t.Fatalf("messages = %d, want 2 (got: %+v)", len(m.messages), m.messages)
	}
	if m.messages[1].content != "world" {
		t.Errorf("assistant content = %q, want %q", m.messages[1].content, "world")
	}
}
