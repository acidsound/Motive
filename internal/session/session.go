// Package session persists conversation transcripts as JSONL files so the TUI
// can resume earlier work and record git revisions per turn.
package session

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	model "github.com/acidsound/Motive/internal/model"
)

// Entry is one persisted transcript line: a user request, an assistant reply,
// a tool line, an error, or a user stop.
//
// "stopped" is a Motive transcript role, not a chat API role: it must never be
// sent to a model as a message role. The transcript is model-visible only
// through the session_log tool, which renders entries as plain text.
type Entry struct {
	TS             time.Time          `json:"ts"`
	Role           string             `json:"role"` // user | assistant | error | tool | stopped
	Content        string             `json:"content,omitempty"`
	Reasoning      string             `json:"reasoning,omitempty"`
	Tools          []string           `json:"tools,omitempty"`
	Attachments    []model.Attachment `json:"attachments,omitempty"`
	BaseRevision   string             `json:"base_revision,omitempty"`
	ResultRevision string             `json:"result_revision,omitempty"`
	ElapsedMS      int64              `json:"elapsed_ms,omitempty"`
}

// Summary describes a session for the picker without loading its transcript.
type Summary struct {
	ID             string
	Path           string
	Created        time.Time
	Updated        time.Time
	Preview        string
	BaseRevision   string
	ResultRevision string
	ToolCalls      int
	Lines          int
}

// Store writes and reads session transcripts under a single directory.
type Store struct{ Dir string }

// NewStore creates a store rooted at dir. Production callers should use
// NewStoreForWorkspace instead: session state must never live inside a
// workspace, and a global flat directory would mix the transcripts of every
// workspace the user has ever worked on.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

// WorkspaceNamespace derives a stable, filesystem-safe directory name for a
// workspace root. The hash is over the canonical absolute root path, so the
// same workspace always maps to the same namespace; the trailing directory
// name is included as a readable prefix so the namespace is identifiable by
// eye.
func WorkspaceNamespace(workspaceRoot string) string {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		root = "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	sum := sha1.Sum([]byte(root))
	return strings.TrimSuffix(filepath.Base(root), "/") + "-" + hex.EncodeToString(sum[:])[:12]
}

// NewStoreForWorkspace creates the session store for the given workspace under
// dir, namespaced per workspace: dir/<namespace>. An empty workspace root
// falls back to the current working directory, so a store is always scoped to
// a concrete workspace and the workspace itself never holds Motive state.
func NewStoreForWorkspace(dir, workspaceRoot string) (*Store, error) {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		root, _ = os.Getwd()
	}
	return NewStore(filepath.Join(dir, WorkspaceNamespace(root)))
}

func (s *Store) path(id string) string {
	return filepath.Join(s.Dir, id+".jsonl")
}

// New creates an empty session and returns its id.
func (s *Store) New() (string, error) {
	id := time.Now().UTC().Format("20060102-150405") + "-" + shortID()
	f, err := os.OpenFile(s.path(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return id, nil
}

// Append adds one entry to a session file. Sessions are append-only; a resumed
// session continues with the same file.
func (s *Store) Append(id string, e Entry) error {
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "..") {
		return fmt.Errorf("invalid session id %q", id)
	}
	f, err := os.OpenFile(s.path(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(e)
}

// Tail returns the last n entries of a session transcript, in chronological
// order. n is clamped to the number of available entries.
func (s *Store) Tail(id string, n int) ([]Entry, error) {
	entries, err := s.Load(id)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		n = 1
	}
	if n > len(entries) {
		n = len(entries)
	}
	return entries[len(entries)-n:], nil
}

// List returns sessions newest first.
func (s *Store) List() ([]Summary, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var out []Summary
	for _, de := range entries {
		if de.IsDir() || filepath.Ext(de.Name()) != ".jsonl" {
			continue
		}
		id := strings.TrimSuffix(de.Name(), ".jsonl")
		sum, err := s.summarize(id, de)
		if err != nil {
			continue
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

func (s *Store) summarize(id string, de os.DirEntry) (Summary, error) {
	f, err := os.Open(filepath.Join(s.Dir, de.Name()))
	if err != nil {
		return Summary{}, err
	}
	defer f.Close()
	sum := Summary{ID: id, Path: filepath.Join(s.Dir, de.Name())}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		sum.Lines++
		if e.Role == "tool" {
			sum.ToolCalls++
		}
		sum.ToolCalls += len(e.Tools)
		if e.BaseRevision != "" && sum.BaseRevision == "" {
			sum.BaseRevision = e.BaseRevision
		}
		if e.ResultRevision != "" {
			sum.ResultRevision = e.ResultRevision
		}
		if sum.Created.IsZero() {
			sum.Created = e.TS
		}
		if !e.TS.IsZero() {
			sum.Updated = e.TS
		}
		if sum.Preview == "" && e.Role == "user" {
			sum.Preview = firstLine(e.Content, 80)
		}
	}
	if err := sc.Err(); err != nil {
		return Summary{}, err
	}
	if info, err := de.Info(); err == nil {
		if sum.Updated.IsZero() {
			sum.Updated = info.ModTime()
		}
		if sum.Created.IsZero() {
			sum.Created = info.ModTime()
		}
	}
	if sum.Preview == "" {
		sum.Preview = "(no user message)"
	}
	return sum, nil
}

// Load reads a session transcript in order.
func (s *Store) Load(id string) ([]Entry, error) {
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "..") {
		return nil, fmt.Errorf("invalid session id %q", id)
	}
	f, err := os.Open(s.path(id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// FormatEntry renders a single transcript entry as a compact, model-visible
// line. Content is truncated so a tail of many entries stays small.
func FormatEntry(e Entry) string {
	content := firstLine(e.Content, 80)
	if e.Role == "assistant" && len(e.Tools) > 0 {
		tools := strings.Join(e.Tools, ", ")
		if content == "" {
			content = "[" + tools + "]"
		} else {
			content += " [" + tools + "]"
		}
	}
	if len(e.Attachments) > 0 {
		names := make([]string, 0, len(e.Attachments))
		for _, a := range e.Attachments {
			names = append(names, a.Name)
		}
		content += " [attached: " + strings.Join(names, ", ") + "]"
	}
	ts := ""
	if !e.TS.IsZero() {
		ts = e.TS.UTC().Format("2006-01-02 15:04:05")
	}
	return fmt.Sprintf("%s %s: %s", ts, e.Role, content)
}

func firstLine(s string, limit int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

func shortID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
}
