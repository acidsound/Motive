package workspace

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

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
	if err != nil { return "", err }
	rel, err := filepath.Rel(w.Root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", name)
	}
	return p, nil
}

func (w *Workspace) Read(name string) (string, error) {
	p, err := w.path(name); if err != nil { return "", err }
	data, err := os.ReadFile(p); if err != nil { return "", fmt.Errorf("read %s: %w", name, err) }
	if len(data) > 8<<20 { return "", fmt.Errorf("file too large: %s", name) }
	return string(data), nil
}

func (w *Workspace) Write(name, content string) error {
	p, err := w.path(name); if err != nil { return err }
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { return err }
	return os.WriteFile(p, []byte(content), 0o644)
}

func (w *Workspace) Delete(name string) error {
	p, err := w.path(name); if err != nil { return err }
	return os.Remove(p)
}

func (w *Workspace) List(name string) (string, error) {
	p, err := w.path(name); if err != nil { return "", err }
	var out strings.Builder
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil { return err }
		if path == w.Root { return nil }
		rel, _ := filepath.Rel(w.Root, path)
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") { return fs.SkipDir }
		out.WriteString(rel)
		if d.IsDir() { out.WriteByte('/') }
		out.WriteByte('\n')
		return nil
	})
	return out.String(), err
}

func (w *Workspace) Search(query string) (string, error) {
	if query == "" { return "", fmt.Errorf("query is required") }
	if _, err := exec.LookPath("rg"); err == nil {
		return w.command(30*time.Second, "rg", "-n", "--hidden", "--glob", "!.git", "--glob", "!node_modules", query, ".")
	}
	var out strings.Builder
	err := filepath.WalkDir(w.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil { return err }
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") { return fs.SkipDir }
		if d.IsDir() { return nil }
		data, err := os.ReadFile(path); if err != nil || bytes.IndexByte(data, 0) >= 0 { return nil }
		if strings.Contains(string(data), query) {
			rel, _ := filepath.Rel(w.Root, path)
			out.WriteString(rel); out.WriteByte('\n')
		}
		return nil
	})
	return out.String(), err
}

func (w *Workspace) Shell(command string) (string, error) {
	if command == "" { return "", fmt.Errorf("command is required") }
	if runtime.GOOS == "windows" {
		return w.command(2*time.Minute, "powershell", "-NoProfile", "-Command", command)
	}
	return w.command(2*time.Minute, "bash", "-lc", command)
}

func (w *Workspace) command(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout); defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = w.Root
	var out bytes.Buffer
	cmd.Stdout = &out; cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() != nil { return out.String(), ctx.Err() }
	if err != nil { return out.String(), fmt.Errorf("%w\n%s", err, out.String()) }
	return out.String(), nil
}

func (w *Workspace) GitStatus() (string, error) {
	return w.command(10*time.Second, "git", "status", "--short", "--branch")
}

func (w *Workspace) GitDiff() (string, error) {
	return w.command(20*time.Second, "git", "diff", "--no-ext-diff", "--")
}

func (w *Workspace) GitHEAD() string {
	out, _ := w.command(5*time.Second, "git", "rev-parse", "HEAD")
	return strings.TrimSpace(out)
}
