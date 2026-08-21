package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchNoMatchReturnsEmpty(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := New(dir)
	out, err := w.Search("definitely-not-present-xyz")
	if err != nil {
		t.Fatalf("Search returned error for no-match: %v", err)
	}
	if out != "" {
		t.Fatalf("Search output = %q, want empty", out)
	}
}

func TestSearchMatch(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := New(dir)
	out, err := w.Search("hello")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if out == "" {
		t.Fatal("Search output is empty, want match")
	}
}

// gitTestRepo initialises a temp git repo, sets an identity, writes the given
// files (one commit per file) and returns the repo directory. It skips the test
// when git is unavailable.
func gitTestRepo(t *testing.T, files map[string]string) string {
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
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", dir, "add", name).CombinedOutput(); err != nil {
			t.Fatalf("git add %s: %v (%s)", name, err, strings.TrimSpace(string(out)))
		}
		if out, err := exec.Command("git", "-C", dir, "commit", "-q", "-m", "commit "+name).CombinedOutput(); err != nil {
			t.Fatalf("git commit %s: %v (%s)", name, err, strings.TrimSpace(string(out)))
		}
	}
	return dir
}

func TestGitLogBounded(t *testing.T) {
	dir := gitTestRepo(t, map[string]string{
		"a.txt": "a",
		"b.txt": "b",
		"c.txt": "c",
	})
	w := New(dir)
	out, err := w.GitLog(50)
	if err != nil {
		t.Fatalf("GitLog: %v", err)
	}
	for _, want := range []string{"commit c.txt", "commit b.txt", "commit a.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("GitLog missing %q; got:\n%s", want, out)
		}
	}
	// newest first: c before a
	if strings.Index(out, "commit c.txt") > strings.Index(out, "commit a.txt") {
		t.Errorf("GitLog not newest-first; got:\n%s", out)
	}
}

func TestGitLogClampsN(t *testing.T) {
	dir := gitTestRepo(t, map[string]string{
		"a.txt": "a",
		"b.txt": "b",
		"c.txt": "c",
	})
	w := New(dir)

	one, err := w.GitLog(1)
	if err != nil {
		t.Fatalf("GitLog(1): %v", err)
	}
	zero, err := w.GitLog(0)
	if err != nil {
		t.Fatalf("GitLog(0): %v", err)
	}
	neg, err := w.GitLog(-5)
	if err != nil {
		t.Fatalf("GitLog(-5): %v", err)
	}
	if zero != one {
		t.Errorf("GitLog(0) = %q, want equal to GitLog(1) = %q", zero, one)
	}
	if neg != one {
		t.Errorf("GitLog(-5) = %q, want equal to GitLog(1) = %q", neg, one)
	}
	if strings.Count(one, "\n") > 1 {
		t.Errorf("GitLog(1) output has >1 line; got:\n%s", one)
	}

	fifty, err := w.GitLog(50)
	if err != nil {
		t.Fatalf("GitLog(50): %v", err)
	}
	huge, err := w.GitLog(9999)
	if err != nil {
		t.Fatalf("GitLog(9999): %v", err)
	}
	if huge != fifty {
		t.Errorf("GitLog(9999) = %q, want equal to GitLog(50) = %q", huge, fifty)
	}
}

func TestGitHEADOutsideRepo(t *testing.T) {
	w := New(t.TempDir())
	if head := w.GitHEAD(); head != "" {
		t.Fatalf("GitHEAD outside a repository = %q, want empty", head)
	}
}

func TestSearchLiteralRegexMetacharacters(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("value is a+b (x)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := New(dir)
	out, err := w.Search("a+b (x)")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if !strings.Contains(out, "a.txt") {
		t.Fatalf("Search output = %q, want a match in a.txt", out)
	}
}

func TestEditSingleOccurrence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "greeting.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := New(dir)
	summary, err := w.Edit("greeting.txt", "hello", "goodbye", false)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if !strings.Contains(summary, "edited greeting.txt") {
		t.Fatalf("summary = %q, want edit summary", summary)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "goodbye world\n" {
		t.Fatalf("file content = %q, want %q", string(data), "goodbye world\n")
	}
}

func TestEditMultipleOccurrencesWithoutReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.txt")
	if err := os.WriteFile(path, []byte("a b a c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := New(dir)
	_, err := w.Edit("dup.txt", "a", "x", false)
	if err == nil {
		t.Fatal("expected error for ambiguous edit with multiple occurrences")
	}
	if !strings.Contains(err.Error(), "found 2 times") {
		t.Fatalf("error message = %q, want mention of occurrence count", err.Error())
	}
	// file unchanged
	data, _ := os.ReadFile(path)
	if string(data) != "a b a c\n" {
		t.Fatalf("file should be unchanged, got %q", string(data))
	}
}

func TestEditMultipleOccurrencesWithReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.txt")
	if err := os.WriteFile(path, []byte("a b a c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := New(dir)
	summary, err := w.Edit("dup.txt", "a", "x", true)
	if err != nil {
		t.Fatalf("Edit with replace_all: %v", err)
	}
	if !strings.Contains(summary, "replaced 2 occurrence(s)") {
		t.Fatalf("summary = %q, want 2 occurrences replaced", summary)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "x b x c\n" {
		t.Fatalf("file content = %q, want %q", string(data), "x b x c\n")
	}
}

func TestEditNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "miss.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := New(dir)
	_, err := w.Edit("miss.txt", "zzz", "yyy", false)
	if err == nil {
		t.Fatal("expected error when old_string is not found")
	}
}

func TestEditEmptyOldString(t *testing.T) {
	w := New(t.TempDir())
	_, err := w.Edit("doesnt-matter.txt", "", "yyy", false)
	if err == nil {
		t.Fatal("expected error for empty old_string")
	}
}

func TestEditNoChangeIdentical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "same.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := New(dir)
	_, err := w.Edit("same.txt", "hello", "hello", false)
	if err == nil {
		t.Fatal("expected error when old and new are identical")
	}
}

func TestEditEscapesWorkspace(t *testing.T) {
	w := New(t.TempDir())
	_, err := w.Edit("../etc/passwd", "x", "y", false)
	if err == nil {
		t.Fatal("expected error for path escaping workspace")
	}
}

func TestGlobRecursiveDoubleStar(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"root.go":            "a",
		"internal/a/b.go":    "b",
		"internal/a/c.txt":   "c",
		"internal/d/x.go":    "d",
		"cmd/motive/main.go": "e",
	}
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := New(dir)

	out, err := w.Glob("**/*.go")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	for _, want := range []string{"root.go", "internal/a/b.go", "internal/d/x.go", "cmd/motive/main.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("Glob(**/*.go) missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "internal/a/c.txt") {
		t.Errorf("Glob(**/*.go) should not match .txt; got:\n%s", out)
	}
}

func TestGlobSingleStarDoesNotCrossDirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.go", "sub/b.go"} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := New(dir)

	out, err := w.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if out != "a.go" {
		t.Fatalf("Glob(*.go) = %q, want just a.go", out)
	}
}

func TestGlobNoMatch(t *testing.T) {
	w := New(t.TempDir())
	out, err := w.Glob("*.zzz")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if out != "No files matched." {
		t.Fatalf("Glob no-match = %q, want notice", out)
	}
}

func TestGlobRejectsEmptyAndEscapes(t *testing.T) {
	w := New(t.TempDir())
	if _, err := w.Glob(""); err == nil {
		t.Fatal("expected error for empty pattern")
	}
	if _, err := w.Glob("../x"); err == nil {
		t.Fatal("expected error for escaping pattern")
	}
	if _, err := w.Glob("/abs/path"); err == nil {
		t.Fatal("expected error for absolute pattern")
	}
}
