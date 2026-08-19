package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/acidsound/Motive/internal/workspace"
)

func TestReadFileObservationTracksChanges(t *testing.T) {
	e := &Executor{WS: workspace.New(t.TempDir())}
	if err := e.WS.Write("a.txt", "one\ntwo\n"); err != nil {
		t.Fatal(err)
	}

	first, err := e.Run(context.Background(), "read_file", `{"path":"a.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "status=first_read") || !strings.Contains(first, "bytes=8") || !strings.Contains(first, "lines=2") {
		t.Fatalf("first observation = %q", first)
	}

	second, err := e.Run(context.Background(), "read_file", `{"path":"a.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second, "status=unchanged") {
		t.Fatalf("second observation = %q", second)
	}

	if err := e.WS.Write("a.txt", "changed\n"); err != nil {
		t.Fatal(err)
	}
	third, err := e.Run(context.Background(), "read_file", `{"path":"a.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(third, "status=changed") {
		t.Fatalf("third observation = %q", third)
	}
}

func TestAddDiagnostics(t *testing.T) {
	got := addDiagnostics("go: internal/runtime/runtime.go:184:17: undefined: budget")
	for _, want := range []string{"[diagnostics]", "file=internal/runtime/runtime.go", "line=184", "column=17", "message=undefined: budget"} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostics = %q, missing %q", got, want)
		}
	}
}

func TestAddDiagnosticsNoMatch(t *testing.T) {
	input := "command completed successfully"
	if got := addDiagnostics(input); got != input {
		t.Fatalf("got %q, want unchanged result", got)
	}
}
