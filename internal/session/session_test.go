package session

import (
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
	if got := firstLine(long); len([]rune(got)) > 81 {
		t.Errorf("firstLine runes = %d, want <= 81", len([]rune(got)))
	}
	multi := "first line\nsecond line"
	if got := firstLine(multi); got != "first line" {
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
