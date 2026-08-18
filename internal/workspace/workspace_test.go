package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
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
