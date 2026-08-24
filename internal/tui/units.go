package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/session"
)

// unitRecord is one entry in the units overlay: a unit boundary record plus
// the session id it came from, so the transcript can be opened from the panel.
type unitRecord struct {
	SessionID string
	Boundary  runtime.UnitBoundary
}

// loadUnits scans every stored session for `unit` boundary entries and
// returns them newest first. Sessions without boundaries are skipped.
func (m *model) loadUnits() []unitRecord {
	var out []unitRecord
	if m.sess == nil {
		return out
	}
	summaries, err := m.sess.List()
	if err != nil {
		return out
	}
	for _, sum := range summaries {
		entries, err := m.sess.Load(sum.ID)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Role != "unit" {
				continue
			}
			var b runtime.UnitBoundary
			if err := json.Unmarshal([]byte(e.Content), &b); err != nil {
				b = runtime.UnitBoundary{Status: "unknown", Error: e.Content}
			}
			out = append(out, unitRecord{SessionID: sum.ID, Boundary: b})
		}
	}
	return out
}

// unitLines renders the units overlay body: one line per unit boundary record.
// The row at cursor is highlighted with "▸" so the selection is visible before
// enter is pressed.
func unitLines(units []unitRecord, cursor int) []string {
	if len(units) == 0 {
		return []string{styleDim.Render("no unit executions recorded")}
	}
	out := make([]string, 0, len(units)+1)
	out = append(out, stylePanelHeading.Render(fmt.Sprintf("Unit executions (%d)", len(units))))
	for i, u := range units {
		b := u.Boundary
		marker := "   "
		if i == cursor {
			marker = stylePrompt.Render(" ▸ ")
		}
		line := fmt.Sprintf(" %s %s · %s · steps %d/%d · tools %d",
			marker,
			shortID8(u.SessionID), statusStyle(b.Status).Render(b.Status), b.Steps, b.MaxSteps, b.ToolCalls)
		if b.BaseRevision != "" || b.ResultRevision != "" {
			line += " · Δrev " + shortRev(b.BaseRevision) + ".." + shortRev(b.ResultRevision)
		}
		if b.Error != "" {
			line += "\n    " + styleError.Render(truncateRunes(firstLine(b.Error, 90), 90))
		} else if t := strings.TrimSpace(b.Text); t != "" {
			line += "\n    " + styleDim.Render(truncateRunes(firstLine(t, 90), 90))
		}
		out = append(out, line)
	}
	return out
}

func shortID8(id string) string {
	if len(id) > 15 {
		return id[:15]
	}
	return id
}

// firstLine returns the first line of s, truncated to limit runes.
func firstLine(s string, limit int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

// statusStyle colors a boundary status: green for completed, amber for
// budget-exceeded, red for error/unknown.
func statusStyle(status string) (s lipgloss.Style) {
	switch status {
	case "completed":
		return styleOK
	case "budget-exceeded":
		return styleEffort
	default:
		return styleError
	}
}

// formatUnitDetail renders the full transcript of one unit session for the
// detail view (opened with enter from the units list).
func formatUnitDetail(entries []session.Entry) string {
	var b strings.Builder
	for _, e := range entries {
		switch e.Role {
		case "user":
			b.WriteString(styleUser.Render("❯ " + firstLine(e.Content, 200)))
		case "assistant":
			b.WriteString(styleAssistant.Render(firstLine(e.Content, 200)))
		case "unit":
			b.WriteString(styleBookmark.Render("⏹ boundary: " + e.Content))
		default:
			b.WriteString(styleDim.Render(e.Role + ": " + firstLine(e.Content, 120)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

var _ = strconv.Itoa // keep strconv if formatting changes drop it
