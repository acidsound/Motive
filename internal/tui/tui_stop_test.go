package tui

import (
	"testing"

	"github.com/acidsound/Motive/internal/session"
)

// newStopTestModel builds a model with a session store and a steer channel,
// mirroring the wiring Run() performs for the TUI.
func newStopTestModel(t *testing.T) (model, *session.Store, string) {
	t.Helper()
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
	m.sess = s
	m.sessionID = id
	m.rt.Steer = make(chan string, 16)
	return m, s, id
}

// TestSubmitBusyQueueMode verifies that enter while busy in queue mode appends
// the request to the FIFO queue instead of starting a new turn.
func TestSubmitBusyQueueMode(t *testing.T) {
	m, _, _ := newStopTestModel(t)
	m.busy = true
	m.steerMode = false
	m.input.SetValue("queued request")

	m2, cmd := m.submitBusy()
	m = *m2.(*model)
	if cmd != nil {
		t.Fatal("queue mode must not start a turn, got a cmd")
	}
	if len(m.queue) != 1 || m.queue[0] != "queued request" {
		t.Fatalf("queue = %+v, want [queued request]", m.queue)
	}
	if m.input.Value() != "" {
		t.Error("input not cleared after submit")
	}
}

// TestSubmitBusySteerMode verifies that enter while busy in steer mode injects
// the request into the runtime's steer channel, appends a user message to the
// transcript, and persists both the partial assistant output and the steer to
// the session.
func TestSubmitBusySteerMode(t *testing.T) {
	m, s, id := newStopTestModel(t)
	m.busy = true
	m.steerMode = true
	// startTurn would have persisted the original user request.
	if err := s.Append(id, session.Entry{Role: "user", Content: "original"}); err != nil {
		t.Fatal(err)
	}
	m.appendMessage(message{role: "user", content: "original"})
	m.appendMessage(message{role: "assistant", content: "partial output"})
	m.input.SetValue("steer me")

	m2, _ := m.submitBusy()
	m = *m2.(*model)

	select {
	case got := <-m.rt.Steer:
		if got != "steer me" {
			t.Fatalf("steer = %q, want %q", got, "steer me")
		}
	default:
		t.Fatal("no steer message delivered to rt.Steer")
	}
	// Transcript: user, assistant (partial), user (steer).
	if len(m.messages) != 3 || m.messages[2].role != "user" || m.messages[2].content != "steer me" {
		t.Fatalf("messages = %+v, want 3 ending with the steer user message", m.messages)
	}
	entries, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("session entries = %d, want 3: %+v", len(entries), entries)
	}
	if entries[0].Role != "user" || entries[0].Content != "original" {
		t.Errorf("entry 0 = %+v, want original user", entries[0])
	}
	if entries[1].Role != "assistant" || entries[1].Content != "partial output" {
		t.Errorf("partial assistant not persisted before steer: %+v", entries[1])
	}
	if entries[2].Role != "user" || entries[2].Content != "steer me" {
		t.Errorf("steer not persisted: %+v", entries[2])
	}
}

// TestSubmitBusySteerDropsEmptyPlaceholder verifies that steering while the
// assistant slot is still an empty placeholder drops the placeholder instead
// of leaving a phantom blank assistant message in the transcript.
func TestSubmitBusySteerDropsEmptyPlaceholder(t *testing.T) {
	m, _, _ := newStopTestModel(t)
	m.busy = true
	m.steerMode = true
	m.appendMessage(message{role: "user", content: "original"})
	m.appendMessage(message{role: "assistant"}) // empty placeholder from startTurn
	m.input.SetValue("steer me")

	m2, _ := m.submitBusy()
	m = *m2.(*model)

	if len(m.messages) != 2 {
		t.Fatalf("messages = %d, want 2 (placeholder dropped): %+v", len(m.messages), m.messages)
	}
	if m.messages[1].role != "user" || m.messages[1].content != "steer me" {
		t.Fatalf("messages = %+v, want [user, steer]", m.messages)
	}
}

// TestSubmitBusySteerFullChannelFallsBackToQueue verifies that when the steer
// channel is full, the request is queued instead of blocking or being dropped.
func TestSubmitBusySteerFullChannelFallsBackToQueue(t *testing.T) {
	m, _, _ := newStopTestModel(t)
	m.rt.Steer = make(chan string, 1)
	m.rt.Steer <- "pending"
	m.busy = true
	m.steerMode = true
	m.input.SetValue("overflow steer")

	m2, _ := m.submitBusy()
	m = *m2.(*model)

	if len(m.queue) != 1 || m.queue[0] != "overflow steer" {
		t.Fatalf("queue = %+v, want [overflow steer]", m.queue)
	}
}

// TestFinishTurnStopPersistsStopped verifies that a user stop (canceled
// context) records a "stopped" entry in the transcript and the session,
// persists the partial assistant output, and continues with queued requests.
func TestFinishTurnStopPersistsStopped(t *testing.T) {
	m, s, id := newStopTestModel(t)
	m.busy = true
	m.stopping = true
	m.stopCancel = func() {}
	m.appendMessage(message{role: "user", content: "hello"})
	m.appendMessage(message{role: "assistant", content: "partial"})
	m.queue = []string{"next request"}

	m2, cmd := m.finishTurn(doneMsg{text: "", err: errNetwork})
	m = *m2.(*model)

	if m.stopping {
		t.Error("stopping flag not cleared after finishTurn")
	}
	// user, assistant (partial), stopped, then the queued turn: user +
	// assistant placeholder.
	if len(m.messages) != 5 {
		t.Fatalf("messages = %d, want 5: %+v", len(m.messages), m.messages)
	}
	if m.messages[2].role != "stopped" {
		t.Errorf("message 2 role = %q, want stopped", m.messages[2].role)
	}
	if m.messages[3].role != "user" || m.messages[3].content != "next request" {
		t.Errorf("queued turn not started: %+v", m.messages[3])
	}
	if !m.busy {
		t.Error("busy = false, want true (queued turn started)")
	}
	if cmd == nil {
		t.Error("queued turn must return an execution cmd")
	}

	entries, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	// assistant (partial) + stopped + user (queued request)
	if len(entries) != 3 {
		t.Fatalf("session entries = %d, want 3: %+v", len(entries), entries)
	}
	if entries[0].Role != "assistant" || entries[0].Content != "partial" {
		t.Errorf("partial assistant not persisted: %+v", entries[0])
	}
	if entries[1].Role != "stopped" {
		t.Errorf("entry 1 role = %q, want stopped", entries[1].Role)
	}
	if entries[2].Role != "user" || entries[2].Content != "next request" {
		t.Errorf("queued request not persisted: %+v", entries[2])
	}
}

// TestFinishTurnStopCleanDoneTreatedAsSuccess verifies that when a stop races
// a clean finish (done.err == nil), the turn is treated as a normal success
// and no "stopped" entry is recorded.
func TestFinishTurnStopCleanDoneTreatedAsSuccess(t *testing.T) {
	m, s, id := newStopTestModel(t)
	m.busy = true
	m.stopping = true
	m.stopCancel = func() {}
	m.appendMessage(message{role: "user", content: "hello"})
	m.appendMessage(message{role: "assistant", content: "final"})

	m2, _ := m.finishTurn(doneMsg{text: "final", err: nil})
	m = *m2.(*model)

	if m.stopping {
		t.Error("stopping flag not cleared")
	}
	for _, msg := range m.messages {
		if msg.role == "stopped" {
			t.Fatal("unexpected stopped message in transcript")
		}
	}
	entries, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Role == "stopped" {
			t.Fatal("unexpected stopped session entry")
		}
	}
}

// TestPersistStoppedIdempotent verifies that the quit path and finishTurn can
// both settle the same stopped turn without recording two "stopped" entries.
func TestPersistStoppedIdempotent(t *testing.T) {
	m, s, id := newStopTestModel(t)
	m.busy = true
	m.stopping = true
	m.appendMessage(message{role: "user", content: "hello"})
	m.appendMessage(message{role: "assistant", content: "partial"})

	m.persistStopped() // e.g. from the quit path
	m2, _ := m.finishTurn(doneMsg{text: "", err: errNetwork}) // racing settle
	m = *m2.(*model)

	var stopped int
	for _, msg := range m.messages {
		if msg.role == "stopped" {
			stopped++
		}
	}
	if stopped != 1 {
		t.Fatalf("stopped messages = %d, want 1: %+v", stopped, m.messages)
	}
	entries, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	var stoppedEntries int
	for _, e := range entries {
		if e.Role == "stopped" {
			stoppedEntries++
		}
	}
	if stoppedEntries != 1 {
		t.Fatalf("stopped session entries = %d, want 1: %+v", stoppedEntries, entries)
	}
}

// TestLoadSessionRestoresStopped verifies that a "stopped" transcript entry is
// restored when a session is loaded.
func TestLoadSessionRestoresStopped(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(id, session.Entry{Role: "user", Content: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(id, session.Entry{Role: "stopped", Content: "stopped by user"}); err != nil {
		t.Fatal(err)
	}

	m := newTestModel()
	m.sess = s
	m.loadSession(id)

	if len(m.messages) != 2 {
		t.Fatalf("messages = %d, want 2: %+v", len(m.messages), m.messages)
	}
	if m.messages[1].role != "stopped" || m.messages[1].content != "stopped by user" {
		t.Fatalf("message 1 = %+v, want stopped", m.messages[1])
	}
}
