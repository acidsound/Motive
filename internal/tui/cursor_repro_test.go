package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
)

// Reproduce: multiline paste into the textarea, then check whether the
// rendered view height covers all visual lines and where the cursor lands.
func TestMultilinePasteCursor(t *testing.T) {
	in := textarea.New()
	in.SetPromptFunc(2, func(p textarea.PromptInfo) string {
		if p.LineNumber == 0 {
			return "> "
		}
		return "  "
	})
	in.ShowLineNumbers = false
	in.SetVirtualCursor(false)
	in.SetStyles(textarea.DefaultStyles(true))
	in.Focus()
	in.KeyMap.InsertNewline.SetEnabled(false)
	in.SetWidth(80)
	in.SetHeight(1)

	paste := strings.Repeat("hello world this is a fairly long line\n", 5) + "last line"
	in.InsertString(paste)
	m := model{input: in}
	m.syncInputHeight()

	view := in.View()
	viewLines := strings.Split(view, "\n")
	t.Logf("visualLineCount=%d inputH=%d viewRows=%d lineCount=%d",
		m.visualLineCount(), m.inputH, len(viewLines), in.LineCount())
	cur := in.Cursor()
	if cur != nil {
		t.Logf("cursor x=%d y=%d row=%d col=%d yOffset=%d", cur.X, cur.Y, in.Line(), in.Column(), in.ScrollYOffset())
		if cur.Y >= len(viewLines) {
			t.Errorf("cursor row %d outside rendered view (%d rows): last line invisible", cur.Y, len(viewLines))
		}
	}
	if m.visualLineCount() != len(viewLines) {
		t.Errorf("height estimate mismatch: visualLineCount=%d rendered=%d", m.visualLineCount(), len(viewLines))
	}
}

// The restore path in syncInputHeight moves the cursor down Line() times
// (logical lines), but CursorDown advances by soft-wrapped visual lines.
// With wrapping earlier lines this lands on the wrong row/column.
func TestSyncInputHeightRestoreWrapsWrongRow(t *testing.T) {
	in := newTestTextarea()
	in.SetWidth(20)
	in.SetHeight(1)

	// 3 logical lines; first two wrap at width 20.
	in.InsertString("a very long line that wraps around\nanother long wrapping line here\nshort")
	m := model{input: in}

	wantLine, wantCol := in.Line(), in.Column()
	m.syncInputHeight()
	t.Logf("before: line=%d col=%d after: line=%d col=%d", wantLine, wantCol, in.Line(), in.Column())
	if in.Line() != wantLine || in.Column() != wantCol {
		t.Errorf("cursor moved by syncInputHeight: want line=%d col=%d got line=%d col=%d",
			wantLine, wantCol, in.Line(), in.Column())
	}
}

// Pasting more lines than maxInputHeight: last line must still be visible
// via internal scrolling and the reported cursor must be inside the view.
func TestPasteBeyondMaxHeightLastLineVisible(t *testing.T) {
	in := newTestTextarea()
	in.SetWidth(80)
	in.SetHeight(1)

	var sb strings.Builder
	for i := 0; i < 25; i++ {
		sb.WriteString("line ")
		sb.WriteString(string(rune('A' + i)))
		sb.WriteByte('\n')
	}
	sb.WriteString("FINAL")
	in.InsertString(sb.String())

	m := model{input: in}
	m.syncInputHeight()
	viewLines := strings.Split(in.View(), "\n")
	cur := in.Cursor()
	t.Logf("inputH=%d viewRows=%d cursor y=%d yOffset=%d", m.inputH, len(viewLines), cur.Y, in.ScrollYOffset())
	if cur.Y < 0 || cur.Y >= len(viewLines) {
		t.Errorf("cursor row %d outside rendered view of %d rows", cur.Y, len(viewLines))
	}
	last := viewLines[len(viewLines)-1]
	if !strings.Contains(last, "FINAL") && !strings.Contains(strings.Join(viewLines, "|"), "FINAL") {
		t.Errorf("last pasted line not visible in view")
	}
}

func newTestTextarea() textarea.Model {
	in := textarea.New()
	in.SetPromptFunc(2, func(p textarea.PromptInfo) string {
		if p.LineNumber == 0 {
			return "> "
		}
		return "  "
	})
	in.ShowLineNumbers = false
	in.SetVirtualCursor(false)
	in.SetStyles(textarea.DefaultStyles(true))
	in.Focus()
	in.KeyMap.InsertNewline.SetEnabled(false)
	return in
}
