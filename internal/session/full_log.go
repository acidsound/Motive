package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FullEvent is an append-only observational record of one event that occurred
// during a TUI session. It is intentionally separate from Entry: full events
// may contain reasoning and streaming detail, while Entry remains the compact
// transcript that Motive can inspect through session_log.
type FullEvent struct {
	TS        time.Time       `json:"ts"`
	Kind      string          `json:"kind"`
	Step      int             `json:"step,omitempty"`
	MaxSteps  int             `json:"max_steps,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolArgs  string          `json:"tool_args,omitempty"`
	Text      string          `json:"text,omitempty"`
	Reasoning string         `json:"reasoning,omitempty"`
	Error     string          `json:"error,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// FullLogPath returns the append-only observational log path for a session.
// Full logs live beside the model-visible transcript namespace but under their
// own directory so normal session enumeration and session_log never discover
// them.
func (s *Store) FullLogPath(id string) string {
	return filepath.Join(s.Dir, "full", id+".jsonl")
}

// AppendFull appends one observational event. Full logs are deliberately
// write-only from Motive's perspective: callers that build model context must
// use the normal transcript APIs instead.
func (s *Store) AppendFull(id string, e FullEvent) error {
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "..") {
		return fmt.Errorf("invalid session id %q", id)
	}
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	if err := os.MkdirAll(filepath.Dir(s.FullLogPath(id)), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.FullLogPath(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(e)
}

// LoadFull reads a full observational log in chronological order. It is kept
// separate from Load/Tail so full reasoning and streaming detail cannot enter
// model-visible session_log paths accidentally.
func (s *Store) LoadFull(id string) ([]FullEvent, error) {
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "..") {
		return nil, fmt.Errorf("invalid session id %q", id)
	}
	f, err := os.Open(s.FullLogPath(id))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []FullEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var e FullEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}
