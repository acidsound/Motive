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
	if _, err := e.Run(context.Background(), "git_status", ""); err != nil { t.Fatalf("git_status with empty arguments failed: %v", err) }
}

func TestRunInvalidArguments(t *testing.T) {
	e := newExecutor(t)
	if _, err := e.Run(context.Background(), "read_file", "{not json"); err == nil { t.Fatal("expected an error for malformed arguments") }
}

func TestRunUnknownTool(t *testing.T) {
	e := newExecutor(t)
	if _, err := e.Run(context.Background(), "no_such_tool", "{}"); err == nil { t.Fatal("expected an error for an unknown tool") }
}

func TestReadFileIncludesStableObservation(t *testing.T) {
	e := newExecutor(t)
	if err := e.WS.Write("main.go", "package main\n\nfunc main() {}\n"); err != nil { t.Fatal(err) }
	first, err := e.Run(context.Background(), "read_file", `{"path":"main.go"}`)
	if err != nil { t.Fatal(err) }
	second, err := e.Run(context.Background(), "read_file", `{"path":"main.go"}`)
	if err != nil { t.Fatal(err) }
	if !strings.Contains(first, "[observation]") { t.Fatalf("missing observation: %s", first) }
	if !strings.Contains(second, "already_read=true") { t.Fatalf("missing incremental context signal: %s", second) }
}

func TestDefinitionsIncludeSymbolLookup(t *testing.T) {
	found := false
	for _, tool := range Definitions() { if tool.Function.Name == "symbol_lookup" { found = true; break } }
	if !found { t.Fatal("symbol_lookup tool is not exposed") }
}

func TestAppendDiagnostics(t *testing.T) {
	out := appendDiagnostics("build failed\ninternal/foo.go:12:7: undefined: Thing")
	if !strings.Contains(out, "[diagnostics]") || !strings.Contains(out, "internal/foo.go:12:7") { t.Fatalf("diagnostics not extracted: %s", out) }
}

func TestTruncateKeepsValidUTF8(t *testing.T) {
	s := strings.Repeat("한", MaxToolResultBytes)
	out := Truncate(s)
	if !utf8.ValidString(out) { t.Fatal("truncated result is not valid UTF-8") }
	if !strings.Contains(out, "tool result truncated") { t.Fatalf("truncated result missing notice: %q", out[len(out)-128:]) }
	if len(s[:strings.Index(out, "\n\n[tool result")]) > MaxToolResultBytes { t.Fatal("retained prefix exceeds MaxToolResultBytes") }
}
