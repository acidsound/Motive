package runtime

import (
	"strings"
	"testing"

	"github.com/acidsound/Motive/internal/workspace"
)

func TestContextBlockOutsideRepo(t *testing.T) {
	r := &Runtime{WS: workspace.New(t.TempDir())}
	block := r.ContextBlock()
	if strings.Contains(block, "fatal:") {
		t.Fatalf("context block contains git error output:\n%s", block)
	}
	if strings.Contains(block, "Git status:") {
		t.Fatalf("context block should not contain a git status section outside a repository:\n%s", block)
	}
}

func TestTruncateUTF8(t *testing.T) {
	s := strings.Repeat("한", 3000) // 9000 bytes
	got := truncateUTF8(s, 6000)
	if len(got) > 6000 {
		t.Fatalf("len = %d, want <= 6000", len(got))
	}
	if len(got)%3 != 0 {
		t.Fatalf("cut splits a rune: len = %d", len(got))
	}
	if got != s[:len(got)] {
		t.Fatal("truncated string is not a prefix of the input")
	}
}

func TestBoundedEnvInt(t *testing.T) {
	const key = "MOTIVE_TEST_BOUNDED"
	t.Setenv(key, "999")
	if got := boundedEnvInt(key, 32, 256); got != 256 {
		t.Fatalf("boundedEnvInt high = %d, want 256", got)
	}
	t.Setenv(key, "0")
	if got := boundedEnvInt(key, 32, 256); got != 32 {
		t.Fatalf("boundedEnvInt zero = %d, want 32", got)
	}
}
