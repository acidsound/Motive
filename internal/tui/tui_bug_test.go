package tui

import (
	"testing"
	"time"

	llm "github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/session"
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

// TestFinishTurnErrorPersistsPartialAssistant verifies that when a run is
// interrupted by an error (network/budget) after streaming partial output, the
// in-progress assistant content and tool calls are still written to the session
// transcript, not just the user request and the error message.
func TestFinishTurnErrorPersistsPartialAssistant(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.New()
	if err != nil {
		t.Fatal(err)
	}

	m := newTestModel()
	m.rt.Model = &llm.Client{}
	m.sess = s
	m.sessionID = id

	// Simulate submit(): user msg + assistant placeholder, then streamed deltas
	// and a tool call before the failure.
	if err := s.Append(id, session.Entry{TS: time.Now(), Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	m.appendMessage(message{role: "user", content: "hello"})
	m.appendMessage(message{role: "assistant", content: "partial reply", reasoning: "thinking…", tools: []string{"shell · 12B · 5ms"}})
	m.busy = true
	m.elapsed = 500 * 1e6 // 500ms in Duration nanoseconds

	m2, _ := m.finishTurn(doneMsg{text: "", err: errNetwork})
	m = *m2.(*model)

	// Transcript should keep the partial assistant plus an error message.
	if len(m.messages) != 3 {
		t.Fatalf("messages = %d, want 3 (got: %+v)", len(m.messages), m.messages)
	}
	if m.messages[1].role != "assistant" || m.messages[1].content != "partial reply" {
		t.Errorf("partial assistant content not preserved: %+v", m.messages[1])
	}
	if m.messages[2].role != "error" {
		t.Errorf("last message role = %q, want error", m.messages[2].role)
	}

	entries, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	// user + assistant + error
	if len(entries) != 3 {
		t.Fatalf("session entries = %d, want 3 (user+assistant+error): %+v", len(entries), entries)
	}
	as := entries[1]
	if as.Role != "assistant" {
		t.Fatalf("entry 1 role = %q, want assistant", as.Role)
	}
	if as.Content != "partial reply" || as.Reasoning != "thinking…" || len(as.Tools) != 1 {
		t.Errorf("assistant entry lost partial output: %+v", as)
	}
	if entries[2].Role != "error" {
		t.Errorf("entry 2 role = %q, want error", entries[2].Role)
	}
}

// TestFinishTurnErrorEmptyAssistantDoesNotPersist verifies that a failure
// before any output arrives still avoids a phantom blank assistant entry.
func TestFinishTurnErrorEmptyAssistantDoesNotPersist(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.New()
	if err != nil {
		t.Fatal(err)
	}

	m := newTestModel()
	m.rt.Model = &llm.Client{}
	m.sess = s
	m.sessionID = id

	if err := s.Append(id, session.Entry{TS: time.Now(), Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	m.appendMessage(message{role: "user", content: "hello"})
	m.appendMessage(message{role: "assistant"}) // empty placeholder from submit()
	m.busy = true

	m2, _ := m.finishTurn(doneMsg{text: "", err: errNetwork})
	m = *m2.(*model)

	// Empty placeholder dropped: only user + error remain in the transcript.
	if len(m.messages) != 2 {
		t.Fatalf("messages = %d, want 2 (got: %+v)", len(m.messages), m.messages)
	}
	if m.messages[1].role != "error" {
		t.Errorf("last message role = %q, want error", m.messages[1].role)
	}

	entries, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("session entries = %d, want 2 (user+error only): %+v", len(entries), entries)
	}
	if entries[0].Role != "user" || entries[1].Role != "error" {
		t.Errorf("unexpected entries: %+v", entries)
	}
}

var errNetwork = &testErr{"model request: connection refused"}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
