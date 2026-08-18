package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/acidsound/Motive/internal/workspace"
)

func newExecutor(t *testing.T) *Executor {
	t.Helper()
	return &Executor{WS: workspace.New(t.TempDir())}
}

func TestRunEmptyArguments(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Skipf("git not available: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	e := &Executor{WS: workspace.New(dir)}
	if _, err := e.Run(context.Background(), "git_status", ""); err != nil {
		t.Fatalf("git_status with empty arguments failed: %v", err)
	}
}

func TestRunInvalidArguments(t *testing.T) {
	e := newExecutor(t)
	if _, err := e.Run(context.Background(), "read_file", "{not json"); err == nil {
		t.Fatal("expected an error for malformed arguments")
	}
}

func TestRunUnknownTool(t *testing.T) {
	e := newExecutor(t)
	if _, err := e.Run(context.Background(), "no_such_tool", "{}"); err == nil {
		t.Fatal("expected an error for an unknown tool")
	}
}

func TestTruncateKeepsValidUTF8(t *testing.T) {
	s := strings.Repeat("한", MaxToolResultBytes) // 3 bytes per rune
	out := Truncate(s)
	if !utf8.ValidString(out) {
		t.Fatal("truncated result is not valid UTF-8")
	}
	if !strings.Contains(out, "tool result truncated") {
		t.Fatalf("truncated result missing notice: %q", out[len(out)-128:])
	}
	if len(s[:strings.Index(out, "\n\n[tool result")]) > MaxToolResultBytes {
		t.Fatal("retained prefix exceeds MaxToolResultBytes")
	}
}
