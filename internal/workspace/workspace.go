package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// maxCommandOutputBytes caps how much command output is retained. Tool results
// are truncated downstream anyway, and an unbounded buffer would let a runaway
// command (e.g. "yes" or "cat /dev/zero") exhaust memory before the timeout.
const maxCommandOutputBytes = 1 << 20

type Workspace struct{ Root string }

func New(root string) *Workspace {
	if root == "" {
		root, _ = os.Getwd()
	}
	root, _ = filepath.Abs(root)
	return &Workspace{Root: root}
}

func (w *Workspace) path(name string) (string, error) {
	name = filepath.Clean(name)
	if name == "." || name == "" {
		return w.Root, nil
	}
	p := name
	if !filepath.IsAbs(p) {
		p = filepath.Join(w.Root, p)
	}
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(w.Root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", name)
	}
	return p, nil
}

func (w *Workspace) Read(name string) (string, error) {
	return w.ReadContext(context.Background(), name)
}

func (w *Workspace) ReadContext(ctx context.Context, name string) (string, error) {
	p, err := w.path(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	if len(data) > 8<<20 {
		return "", fmt.Errorf("file too large: %s", name)
	}
	return string(data), nil
}

func (w *Workspace) Write(name, content string) error {
	return w.WriteContext(context.Background(), name, content)
}

func (w *Workspace) WriteContext(ctx context.Context, name, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := w.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func (w *Workspace) Delete(name string) error {
	return w.DeleteContext(context.Background(), name)
}

func (w *Workspace) DeleteContext(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := w.path(name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// Edit replaces oldString with newString in the file at name. The match is a
// literal string, never a regex, so the model does not need to know sed/perl
// syntax and results are fully portable across platforms.
//
// oldString must be non-empty. When replaceAll is false the old string must
// occur exactly once, otherwise the edit is ambiguous and an error is returned
// so the caller is forced to supply a more specific context. The returned
// string summarises what changed.
func (w *Workspace) Edit(name, oldString, newString string, replaceAll bool) (string, error) {
	return w.EditContext(context.Background(), name, oldString, newString, replaceAll)
}

func (w *Workspace) EditContext(ctx context.Context, name, oldString, newString string, replaceAll bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if oldString == "" {
		return "", fmt.Errorf("old_string must not be empty")
	}
	p, err := w.path(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	if len(data) > 8<<20 {
		return "", fmt.Errorf("file too large: %s", name)
	}
	content := string(data)

	occurrences := strings.Count(content, oldString)
	if occurrences == 0 {
		return "", fmt.Errorf("old_string not found in %s", name)
	}
	if !replaceAll && occurrences > 1 {
		return "", fmt.Errorf("old_string found %d times in %s; use replace_all or supply a more specific old_string", occurrences, name)
	}

	var replaced string
	if replaceAll {
		replaced = strings.ReplaceAll(content, oldString, newString)
	} else {
		replaced = strings.Replace(content, oldString, newString, 1)
	}
	if replaced == content {
		return "", fmt.Errorf("no change: replacement is identical to original in %s", name)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(replaced), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s: replaced %d occurrence(s) of old_string", name, occurrences), nil
}

func (w *Workspace) List(name string) (string, error) {
	return w.ListContext(context.Background(), name)
}

func (w *Workspace) ListContext(ctx context.Context, name string) (string, error) {
	p, err := w.path(name)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == w.Root {
			return nil
		}
		rel, _ := filepath.Rel(w.Root, path)
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return fs.SkipDir
		}
		out.WriteString(rel)
		if d.IsDir() {
			out.WriteByte('/')
		}
		out.WriteByte('\n')
		return nil
	})
	return out.String(), err
}

func (w *Workspace) Glob(pattern string) (string, error) {
	return w.GlobContext(context.Background(), pattern)
}

// GlobContext returns the workspace-relative paths (one per line) matching a
// glob pattern. The pattern is relative to the workspace root and supports
// `*`, `?`, `[...]` per segment, plus `**` which matches zero or more path
// segments. Directories are suffixed with `/`. `.git` and `node_modules` are
// skipped, mirroring ListContext.
func (w *Workspace) GlobContext(ctx context.Context, pattern string) (string, error) {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" || pattern == "." {
		return "", fmt.Errorf("pattern is required")
	}
	if strings.HasPrefix(pattern, "/") {
		return "", fmt.Errorf("pattern must be relative to the workspace")
	}
	if pattern == ".." || strings.HasPrefix(pattern, "../") {
		return "", fmt.Errorf("pattern escapes workspace: %s", pattern)
	}
	pattern = strings.TrimPrefix(pattern, "./")
	var out strings.Builder
	err := filepath.WalkDir(w.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == w.Root {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return fs.SkipDir
		}
		rel, _ := filepath.Rel(w.Root, path)
		if globMatch(pattern, filepath.ToSlash(rel)) {
			out.WriteString(filepath.ToSlash(rel))
			if d.IsDir() {
				out.WriteByte('/')
			}
			out.WriteByte('\n')
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if out.Len() == 0 {
		return "No files matched.", nil
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// globMatch reports whether path (slash-separated, no leading/trailing slash)
// matches a slash-separated pattern. `**` matches zero or more path segments;
// every other segment is matched literally via filepath.Match.
func globMatch(pattern, path string) bool {
	return matchSegs(splitGlobSegs(pattern), splitGlobSegs(path))
}

func splitGlobSegs(s string) []string {
	s = strings.Trim(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func matchSegs(psegs, ssegs []string) bool {
	if len(psegs) == 0 {
		return len(ssegs) == 0
	}
	if psegs[0] == "**" {
		for i := 0; i <= len(ssegs); i++ {
			if matchSegs(psegs[1:], ssegs[i:]) {
				return true
			}
		}
		return false
	}
	if len(ssegs) == 0 {
		return false
	}
	ok, err := filepath.Match(psegs[0], ssegs[0])
	if err != nil || !ok {
		return false
	}
	return matchSegs(psegs[1:], ssegs[1:])
}

func (w *Workspace) Search(query string) (string, error) {
	return w.SearchContext(context.Background(), query)
}

func (w *Workspace) SearchContext(ctx context.Context, query string) (string, error) {
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	// -F searches for the literal query, matching the fixed-string behavior
	// of the fallback walker below.
	if _, err := exec.LookPath("rg"); err == nil {
		out, err := w.command(ctx, 30*time.Second, "rg", "-n", "-F", "--hidden", "--glob", "!.git", "--glob", "!node_modules", query, ".")
		if err != nil {
			// rg exits 1 when no files match; that is an empty result, not a failure.
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return "", nil
			}
			return out, err
		}
		return out, nil
	}
	var out strings.Builder
	err := filepath.WalkDir(w.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return fs.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		if strings.Contains(string(data), query) {
			rel, _ := filepath.Rel(w.Root, path)
			out.WriteString(rel)
			out.WriteByte('\n')
		}
		return nil
	})
	return out.String(), err
}

func (w *Workspace) Shell(command string) (string, error) {
	return w.ShellContext(context.Background(), command)
}

func (w *Workspace) ShellContext(ctx context.Context, command string) (string, error) {
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	if runtime.GOOS == "windows" {
		return w.command(ctx, 2*time.Minute, "powershell", "-NoProfile", "-Command", command)
	}
	return w.command(ctx, 2*time.Minute, "bash", "-lc", command)
}

func (w *Workspace) command(parent context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = w.Root
	// A long-running child that inherits our stdout/stderr (e.g.
	// "python3 -m http.server") would keep the pipes open forever after the
	// context deadline kills the shell, blocking cmd.Run() indefinitely.
	// WaitDelay bounds that wait: once the process exits (or is killed) and
	// WaitDelay elapses, Wait stops waiting on inherited pipes.
	cmd.WaitDelay = 5 * time.Second
	// Put the child in its own process group (where the platform supports it)
	// so the whole group dies with the shell; see setProcessGroup.
	setProcessGroup(cmd)
	out := cappedBuffer{limit: maxCommandOutputBytes}
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		killGroup(cmd)
		return out.String(), ctx.Err()
	}
	if err != nil {
		return out.String(), fmt.Errorf("%w\n%s", err, out.String())
	}
	return out.String(), nil
}

// cappedBuffer retains at most limit bytes of what is written to it. It always
// reports success and consumes every byte, so a child process writing to it
// never blocks or receives a broken pipe; bytes beyond the limit are dropped.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len() < c.limit {
		room := c.limit - c.buf.Len()
		if room < len(p) {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	return c.buf.String()
}

func (w *Workspace) GitStatus() (string, error) {
	return w.GitStatusContext(context.Background())
}

func (w *Workspace) GitStatusContext(ctx context.Context) (string, error) {
	return w.command(ctx, 10*time.Second, "git", "status", "--short", "--branch")
}

func (w *Workspace) GitDiff() (string, error) {
	return w.GitDiffContext(context.Background())
}

func (w *Workspace) GitDiffContext(ctx context.Context) (string, error) {
	return w.command(ctx, 20*time.Second, "git", "diff", "--no-ext-diff", "--")
}

// GitLog returns the last n commit summaries, newest first. The count is
// clamped to [1, 50] so the result is bounded.
func (w *Workspace) GitLog(n int) (string, error) {
	return w.GitLogContext(context.Background(), n)
}

func (w *Workspace) GitLogContext(ctx context.Context, n int) (string, error) {
	if n < 1 {
		n = 1
	}
	if n > 50 {
		n = 50
	}
	return w.command(ctx, 10*time.Second, "git", "log", "--oneline", "-n", strconv.Itoa(n))
}

func (w *Workspace) GitHEAD() string {
	out, err := w.command(context.Background(), 5*time.Second, "git", "rev-parse", "HEAD")
	if err != nil {
		// Outside a repository (or when git fails) there is no HEAD; the
		// captured output is git's error message, not a revision.
		return ""
	}
	return strings.TrimSpace(out)
}
