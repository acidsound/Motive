package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestRenderMarkdownCodeFence(t *testing.T) {
	src := "before\n```go\nfunc main() {}\n```\nafter"
	lines := renderMarkdown(src, 60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "func main() {}") {
		t.Fatalf("code body missing:\n%s", joined)
	}
	if !strings.Contains(joined, "before") || !strings.Contains(joined, "after") {
		t.Fatalf("paragraphs missing:\n%s", joined)
	}
	// Fence lines themselves should not leak.
	if strings.Contains(joined, "```") {
		t.Fatalf("fence markers leaked:\n%s", joined)
	}
}

func TestRenderMarkdownHeadingAndInline(t *testing.T) {
	src := "# Title\n\nbody with **bold** and `code`"
	lines := renderMarkdown(src, 60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Title") {
		t.Fatalf("heading text missing:\n%s", joined)
	}
	if !strings.Contains(joined, "bold") {
		t.Fatalf("bold text missing:\n%s", joined)
	}
	if strings.Contains(joined, "**bold**") {
		t.Fatalf("raw bold markers leaked:\n%s", joined)
	}
	if !strings.Contains(joined, "code") {
		t.Fatalf("inline code text missing:\n%s", joined)
	}
	if strings.Contains(joined, "`code`") {
		t.Fatalf("raw backticks leaked:\n%s", joined)
	}
}

func TestRenderMarkdownWrapsLongParagraphs(t *testing.T) {
	src := strings.Repeat("word ", 60)
	lines := renderMarkdown(src, 30)
	total := strings.Join(lines, "\n")
	if lipgloss.Width(total) > 32 {
		t.Fatalf("wrap exceeded width: %d", lipgloss.Width(total))
	}
}

func TestRenderMarkdownCodeWrapsInsteadOfTruncating(t *testing.T) {
	// A code line wider than the view must hard-wrap across rows, not end in
	// an ellipsis: the TUI offers no way to recover or copy a hidden tail, so
	// truncation would destroy the content.
	long := strings.Repeat("x", 100)
	src := "```\n" + long + "\n```"
	lines := renderMarkdown(src, 30)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "…") {
		t.Fatalf("code line was truncated with an ellipsis:\n%s", joined)
	}
	// The wrapped tail must still be present and contiguous.
	if !strings.Contains(joined, strings.Repeat("x", 10)) {
		t.Fatalf("wrapped tail missing:\n%s", joined)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > 30 {
			t.Errorf("line %d width = %d, want <= 30: %q", i, w, l)
		}
	}
}

func TestRenderMarkdownQuoteStaysWithinWidth(t *testing.T) {
	// The "▍ " quote marker is added after wrapping, so the wrapped text must
	// account for it; otherwise every quote line overflows by two columns.
	src := "> " + strings.Repeat("word ", 40)
	lines := renderMarkdown(src, 30)
	for i, l := range lines {
		if w := lipgloss.Width(l); w > 30 {
			t.Errorf("quote line %d width = %d, want <= 30: %q", i, w, l)
		}
	}
}

func TestColorizeDiff(t *testing.T) {
	lines := colorizeDiff("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n-old\n+new\n context")
	if len(lines) != 7 {
		t.Fatalf("lines = %d, want 7", len(lines))
	}
	ansi := func(s string) bool { return strings.Contains(s, "\x1b[") }
	if !strings.Contains(lines[3], "@@") || !ansi(lines[3]) {
		t.Errorf("meta line unstyled: %q", lines[3])
	}
	if !strings.Contains(lines[4], "-old") || !ansi(lines[4]) {
		t.Errorf("deletion line unstyled: %q", lines[4])
	}
	if !strings.Contains(lines[5], "+new") || !ansi(lines[5]) {
		t.Errorf("addition line unstyled: %q", lines[5])
	}
	if !strings.Contains(lines[0], "diff --git") || !ansi(lines[0]) {
		t.Errorf("header line unstyled: %q", lines[0])
	}
}

func TestColorizeDiffEmpty(t *testing.T) {
	lines := colorizeDiff("")
	if len(lines) != 1 || !strings.Contains(lines[0], "no changes") {
		t.Fatalf("empty diff = %v", lines)
	}
}

func TestZipColumns(t *testing.T) {
	left := []string{"a", "b"}
	right := []string{"x"}
	rows := zipColumns(left, right, 4, 3)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if !strings.Contains(rows[0], "x") {
		t.Errorf("row 0 missing right column: %q", rows[0])
	}
	if lipgloss.Width(rows[1]) != 4+1+3 {
		t.Errorf("row 1 width = %d, want 8", lipgloss.Width(rows[1]))
	}
}

func TestShortRev(t *testing.T) {
	if got := shortRev("abcdef1234567890"); got != "abcdef123456" {
		t.Errorf("shortRev = %q", got)
	}
	if got := shortRev(""); got != "" {
		t.Errorf("shortRev empty = %q", got)
	}
}
