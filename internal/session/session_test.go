package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.New()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	entries := []Entry{
		{TS: now, Role: "user", Content: "fix the failing test", BaseRevision: "abc123"},
		{TS: now.Add(time.Second), Role: "assistant", Content: "done", Reasoning: "step one", Tools: []string{"shell · 12B · 5ms"}, BaseRevision: "abc123", ResultRevision: "def456", ElapsedMS: 1200},
	}
	for _, e := range entries {
		if err := s.Append(id, e); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d entries, want 2", len(loaded))
	}
	if loaded[0].Role != "user" || loaded[0].Content != "fix the failing test" {
		t.Errorf("entry 0 = %+v", loaded[0])
	}
	if loaded[1].Role != "assistant" || loaded[1].ResultRevision != "def456" || len(loaded[1].Tools) != 1 {
		t.Errorf("entry 1 = %+v", loaded[1])
	}

	summaries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries = %d, want 1", len(summaries))
	}
	sum := summaries[0]
	if sum.ID != id {
		t.Errorf("summary id = %q, want %q", sum.ID, id)
	}
	if sum.Preview != "fix the failing test" {
		t.Errorf("preview = %q", sum.Preview)
	}
	if sum.ToolCalls != 1 || sum.Lines != 2 {
		t.Errorf("tool calls / lines = %d / %d, want 1 / 2", sum.ToolCalls, sum.Lines)
	}
	if sum.ResultRevision != "def456" {
		t.Errorf("result revision = %q", sum.ResultRevision)
	}
}

func TestListOrderNewestFirst(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.New()
	newer, _ := s.New()
	// new is created after old; both have no entries yet, so ordering falls
	// back to file mod time. Force an explicit updated stamp on one entry.
	_ = s.Append(newer, Entry{TS: time.Now(), Role: "user", Content: "newer session"})
	_ = s.Append(old, Entry{TS: time.Now().Add(-time.Hour), Role: "user", Content: "older session"})

	summaries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %d, want 2", len(summaries))
	}
	if summaries[0].ID != newer || summaries[1].ID != old {
		t.Errorf("order = %q then %q, want newest first", summaries[0].ID, summaries[1].ID)
	}
}

func TestWorkspaceNamespace(t *testing.T) {
	a := WorkspaceNamespace("/Users/me/Projects/Motive")
	b := WorkspaceNamespace("/Users/me/Projects/Motive")
	if a != b {
		t.Errorf("same root must map to the same namespace: %q vs %q", a, b)
	}
	c := WorkspaceNamespace("/Users/me/Projects/Other")
	if a == c {
		t.Errorf("different roots must map to different namespaces: %q", a)
	}
	if !strings.Contains(a, "Motive") {
		t.Errorf("namespace should carry the readable basename: %q", a)
	}
	// A trailing slash must not change the namespace.
	d := WorkspaceNamespace("/Users/me/Projects/Motive/")
	if d != a {
		t.Errorf("trailing slash changed the namespace: %q vs %q", d, a)
	}
	// The namespace must be a single safe path segment.
	if strings.ContainsAny(a, "/\\") || a == "." || a == ".." {
		t.Errorf("namespace is not a safe path segment: %q", a)
	}
}

func TestNewStoreForWorkspace(t *testing.T) {
	base := t.TempDir()
	ws := "/Users/me/Projects/Motive"
	s, err := NewStoreForWorkspace(filepath.Join(base, "sessions"), ws)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "sessions", WorkspaceNamespace(ws))
	if s.Dir != want {
		t.Errorf("store dir = %q, want %q", s.Dir, want)
	}
	if _, err := os.Stat(s.Dir); err != nil {
		t.Errorf("namespaced dir was not created: %v", err)
	}

	// Two workspaces must not share a session store.
	other, err := NewStoreForWorkspace(filepath.Join(base, "sessions"), "/Users/me/Projects/Other")
	if err != nil {
		t.Fatal(err)
	}
	if other.Dir == s.Dir {
		t.Errorf("different workspaces share store dir %q", s.Dir)
	}

	// An empty workspace root falls back to the cwd, still namespaced.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := NewStoreForWorkspace(filepath.Join(base, "sessions"), "")
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Dir != filepath.Join(base, "sessions", WorkspaceNamespace(cwd)) {
		t.Errorf("fallback store dir = %q, want cwd-namespaced dir", fallback.Dir)
	}
}

func TestStoreRejectsUnsafeIDs(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if err := s.Append("../escape", Entry{Role: "user"}); err == nil {
		t.Fatal("expected error for path traversal id")
	}
	if _, err := s.Load("a/b"); err == nil {
		t.Fatal("expected error for id with slash")
	}
}

func TestPreviewTruncation(t *testing.T) {
	long := strings.Repeat("x", 200)
	if got := firstLine(long, 80); len([]rune(got)) > 81 {
		t.Errorf("firstLine runes = %d, want <= 81", len([]rune(got)))
	}
	multi := "first line\nsecond line"
	if got := firstLine(multi, 80); got != "first line" {
		t.Errorf("firstLine = %q, want first line only", got)
	}
}

func TestTailReturnsLastN(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	id, _ := s.New()
	for i := 0; i < 5; i++ {
		_ = s.Append(id, Entry{TS: time.Now(), Role: "user", Content: "msg " + string(rune('a'+i))})
	}

	tail, err := s.Tail(id, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 2 {
		t.Fatalf("tail len = %d, want 2", len(tail))
	}
	if tail[0].Content != "msg d" || tail[1].Content != "msg e" {
		t.Errorf("tail = %+v, want last two entries", tail)
	}

	// Tail more than available clamps to available.
	all, err := s.Tail(id, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("tail clamp len = %d, want 5", len(all))
	}
}

func TestFormatEntry(t *testing.T) {
	e := Entry{TS: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), Role: "user", Content: "hello world"}
	got := FormatEntry(e)
	if !strings.Contains(got, "user") || !strings.Contains(got, "hello world") {
		t.Errorf("FormatEntry = %q", got)
	}

	// Long content is truncated to a single line.
	ea := Entry{Role: "assistant", Content: strings.Repeat("x", 300), Tools: []string{"shell · 12B · 5ms"}}
	got = FormatEntry(ea)
	if !strings.Contains(got, "[shell · 12B · 5ms]") {
		t.Errorf("FormatEntry should include tool summary: %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("FormatEntry should be a single line: %q", got)
	}
}

func TestFormatEntryUnitBoundaryNotTruncated(t *testing.T) {
	// A unit boundary record is one compact JSON line; session_log must render
	// it whole so a parent execution can read status/revs/counts mechanically.
	long := `{"status":"budget-exceeded","steps":6,"max_steps":6,"tool_calls":5,"max_tool_calls":128,"tool_failures":0,"base_revision":"abc123","result_revision":"def456","elapsed_ms":1234,"text":"implementing git log; next: add tests","error":"execution budget exceeded: 6 steps"}`
	got := FormatEntry(Entry{Role: "unit", Content: long})
	if !strings.Contains(got, `"status":"budget-exceeded"`) || !strings.Contains(got, `"text":"implementing git log`) {
		t.Errorf("unit entry should carry the full boundary JSON: %q", got)
	}
	if strings.Contains(got, "…") {
		t.Errorf("unit boundary JSON was truncated: %q", got)
	}
}

func TestFullLogIsSeparateFromTranscript(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(id, Entry{Role: "user", Content: "request"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendFull(id, FullEvent{Kind: "trace.delta", Text: "visible", Reasoning: "private reasoning"}); err != nil {
		t.Fatal(err)
	}

	entries, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("transcript entries = %d, want 1", len(entries))
	}
	if strings.Contains(FormatEntry(entries[0]), "private reasoning") {
		t.Fatal("full-log reasoning leaked into transcript")
	}

	full, err := s.LoadFull(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 1 || full[0].Reasoning != "private reasoning" {
		t.Fatalf("full log = %+v", full)
	}
	if _, err := os.Stat(s.FullLogPath(id)); err != nil {
		t.Fatalf("full log path missing: %v", err)
	}
}
