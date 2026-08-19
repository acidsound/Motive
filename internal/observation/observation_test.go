package observation

import "testing"

func TestObserveFileTracksHashAndSummary(t *testing.T) {
	s := New()
	first, unchanged := s.ObserveFile("internal/runtime/runtime.go", "package runtime\n\nfunc Execute() {}\nfunc helper() {}\n", 1)
	if unchanged {
		t.Fatal("first observation must not be unchanged")
	}
	if first.Lines != 4 || first.Functions != 2 || first.Exports != 1 {
		t.Fatalf("unexpected summary: %+v", first)
	}
	second, unchanged := s.ObserveFile("internal/runtime/runtime.go", "package runtime\n\nfunc Execute() {}\nfunc helper() {}\n", 2)
	if !unchanged || second.Hash != first.Hash || second.ObservedAt != first.ObservedAt {
		t.Fatalf("expected stable observation: first=%+v second=%+v unchanged=%v", first, second, unchanged)
	}
}

func TestObserveDiagnostic(t *testing.T) {
	s := New()
	got := s.ObserveDiagnostic("internal/runtime/runtime.go:42:7: undefined: missing")
	if len(got) != 1 {
		t.Fatalf("diagnostics=%v", got)
	}
	if got[0].Path != "internal/runtime/runtime.go" || got[0].Line != 42 || got[0].Column != 7 {
		t.Fatalf("unexpected diagnostic: %+v", got[0])
	}
	if len(s.ObserveDiagnostic("internal/runtime/runtime.go:42:7: undefined: missing")) != 1 {
		t.Fatal("parser should return the current diagnostic")
	}
	if len(s.Diagnostics) != 1 {
		t.Fatalf("expected deduplicated state, got %d", len(s.Diagnostics))
	}
}
