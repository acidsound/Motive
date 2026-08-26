package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

func TestEditFileTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("foo bar baz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Executor{WS: workspace.New(dir)}
	out, err := e.Run(context.Background(), "edit_file", `{"path":"a.txt","old_string":"bar","new_string":"qux"}`)
	if err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	if !strings.Contains(out, "edited a.txt") {
		t.Fatalf("edit_file output = %q, want edit summary", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "foo qux baz\n" {
		t.Fatalf("file content = %q, want %q", string(data), "foo qux baz\n")
	}
}

func TestEditFileToolReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("x a x a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Executor{WS: workspace.New(dir)}
	if _, err := e.Run(context.Background(), "edit_file", `{"path":"a.txt","old_string":"a","new_string":"b","replace_all":true}`); err != nil {
		t.Fatalf("edit_file replace_all: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "x b x b\n" {
		t.Fatalf("file content = %q, want %q", string(data), "x b x b\n")
	}
}

func TestEditFileToolAmbiguousWithoutReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("x a x a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Executor{WS: workspace.New(dir)}
	_, err := e.Run(context.Background(), "edit_file", `{"path":"a.txt","old_string":"a","new_string":"b"}`)
	if err == nil {
		t.Fatal("expected error for ambiguous edit")
	}
	if !strings.Contains(err.Error(), "found 2 times") {
		t.Fatalf("error = %v, want occurrence count", err)
	}
}

func TestGlobTool(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "x.go"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Executor{WS: workspace.New(dir)}
	out, err := e.Run(context.Background(), "glob", `{"pattern":"**/*.go"}`)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if out != "internal/x.go" {
		t.Fatalf("glob output = %q, want internal/x.go", out)
	}
}

func TestGlobToolRejectsEmpty(t *testing.T) {
	e := newExecutor(t)
	if _, err := e.Run(context.Background(), "glob", `{"pattern":""}`); err == nil {
		t.Fatal("expected an error for an empty pattern")
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

// gitLogRepo init a git repo in a temp dir with an identity and makes the given
// commits (one per subject). Skips when git is unavailable.
func gitLogRepo(t *testing.T, subjects ...string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Skipf("git not available: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", dir, "config", "user.email", "test@example.com").CombinedOutput(); err != nil {
		t.Fatalf("set user.email: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", dir, "config", "user.name", "Test User").CombinedOutput(); err != nil {
		t.Fatalf("set user.name: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	for i, subject := range subjects {
		name := "f" + string(rune('a'+i)) + ".txt"
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(subject), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", dir, "add", name).CombinedOutput(); err != nil {
			t.Fatalf("git add %s: %v (%s)", name, err, strings.TrimSpace(string(out)))
		}
		if out, err := exec.Command("git", "-C", dir, "commit", "-q", "-m", subject).CombinedOutput(); err != nil {
			t.Fatalf("git commit %s: %v (%s)", subject, err, strings.TrimSpace(string(out)))
		}
	}
	return dir
}

func TestGitLogTool(t *testing.T) {
	dir := gitLogRepo(t, "first commit", "second commit")
	e := &Executor{WS: workspace.New(dir)}
	out, err := e.Run(context.Background(), "git_log", `{"n":2}`)
	if err != nil {
		t.Fatalf("git_log: %v", err)
	}
	for _, want := range []string{"first commit", "second commit"} {
		if !strings.Contains(out, want) {
			t.Errorf("git_log output missing %q; got:\n%s", want, out)
		}
	}
}

func TestGitLogToolDefault(t *testing.T) {
	dir := gitLogRepo(t, "first commit", "second commit")
	e := &Executor{WS: workspace.New(dir)}
	out, err := e.Run(context.Background(), "git_log", "")
	if err != nil {
		t.Fatalf("git_log with default args: %v", err)
	}
	for _, want := range []string{"first commit", "second commit"} {
		if !strings.Contains(out, want) {
			t.Errorf("git_log default output missing %q; got:\n%s", want, out)
		}
	}
}

func TestGitLogToolInvalidJSON(t *testing.T) {
	dir := gitLogRepo(t, "first commit")
	e := &Executor{WS: workspace.New(dir)}
	if _, err := e.Run(context.Background(), "git_log", "{bad"); err == nil {
		t.Fatal("expected an error for malformed git_log arguments")
	}
}

func TestSessionLogTool(t *testing.T) {
	e := newExecutor(t)
	e.SessionID = "sess-1"
	e.SessionLog = func(id string, lines int) (string, error) {
		if id != "sess-1" {
			t.Errorf("session log called with id %q, want sess-1", id)
		}
		return "tail content", nil
	}

	out, err := e.Run(context.Background(), "session_log", "")
	if err != nil {
		t.Fatalf("session_log: %v", err)
	}
	if out != "tail content" {
		t.Errorf("session_log = %q, want tail content", out)
	}
}

func TestSessionLogToolRespectsLines(t *testing.T) {
	e := newExecutor(t)
	e.SessionID = "sess-1"
	e.SessionLog = func(id string, lines int) (string, error) {
		if lines != 7 {
			t.Errorf("session log lines = %d, want 7", lines)
		}
		return "ok", nil
	}
	if _, err := e.Run(context.Background(), "session_log", `{"lines":7}`); err != nil {
		t.Fatalf("session_log: %v", err)
	}
}

func TestSessionLogToolExplicitID(t *testing.T) {
	// An execution can read another session's transcript by passing its id
	// explicitly.
	e := newExecutor(t)
	e.SessionID = "parent-session"
	e.SessionLog = func(id string, lines int) (string, error) {
		if id != "other-session-0002" {
			t.Errorf("session log id = %q, want other-session-0002", id)
		}
		return "transcript", nil
	}
	out, err := e.Run(context.Background(), "session_log", `{"session_id":"other-session-0002"}`)
	if err != nil {
		t.Fatalf("session_log with explicit id: %v", err)
	}
	if out != "transcript" {
		t.Errorf("session_log = %q, want transcript", out)
	}
}

func TestSessionLogToolNoLog(t *testing.T) {
	e := newExecutor(t)
	e.SessionID = "sess-1"
	if _, err := e.Run(context.Background(), "session_log", ""); err == nil {
		t.Fatal("expected an error when no session log is injected")
	}
}

func TestWebFetchToolRejectsEmptyURL(t *testing.T) {
	e := newExecutor(t)
	_, err := e.Run(context.Background(), "web_fetch", `{"url":""}`)
	if err == nil {
		t.Fatal("expected an error for an empty url")
	}
}

func TestWebFetchToolRejectsNonHTTPScheme(t *testing.T) {
	e := newExecutor(t)
	if _, err := e.Run(context.Background(), "web_fetch", `{"url":"file:///etc/passwd"}`); err == nil {
		t.Fatal("expected an error for a non-http scheme")
	}
}

func TestMotiveTool(t *testing.T) {
	e := newExecutor(t)
	out, err := e.Run(context.Background(), "motive", "")
	if err != nil {
		t.Fatalf("motive: %v", err)
	}
	if !strings.Contains(out, "Motive is a model-centric software execution runtime") {
		t.Errorf("motive tool output missing identity: %q", out)
	}
	if !strings.Contains(out, "session_log") {
		t.Errorf("motive tool output should mention session_log: %q", out)
	}
	if !strings.Contains(out, "edit_file") {
		t.Errorf("motive tool output should mention edit_file: %q", out)
	}
}
