package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Units overlay state: a selectable list of unit boundary records with a
// cursor (조회 → 선택 → 상세 flow), plus a detail view showing one unit's
// full transcript.
type unitsPanel struct {
	units    []unitRecord
	scroll   int
	cursor   int  // selected row in the list; -1 when nothing is selectable
	detail   int  // index into units; -1 when the list is shown
	detailLn []string
}

var (
	styleOK = lipgloss.NewStyle().Foreground(lipgloss.Color(colorIdle))
)

func (u *unitsPanel) reset() {
	u.units = nil
	u.scroll = 0
	u.cursor = 0
	u.detail = -1
	u.detailLn = nil
}

// visibleRows is how many list rows fit above the footer hint.
const visibleRows = 3

// clampCursor keeps the cursor inside the list and scrolls the window so the
// cursor row is always visible.
func (u *unitsPanel) clampCursor() {
	if len(u.units) == 0 {
		u.cursor = -1
		return
	}
	if u.cursor < 0 {
		u.cursor = 0
	}
	if u.cursor > len(u.units)-1 {
		u.cursor = len(u.units) - 1
	}
	if u.cursor < u.scroll {
		u.scroll = u.cursor
	}
	if u.cursor >= u.scroll+visibleRows {
		u.scroll = u.cursor - visibleRows + 1
	}
}

// view renders the overlay body (list or detail) for the given height and
// width. Detail lines are soft-wrapped at width so nothing is clipped.
func (u *unitsPanel) view(width, height int) string {
	if u.detail >= 0 && u.detail < len(u.units) {
		rec := u.units[u.detail]
		var b strings.Builder
		b.WriteString(stylePanelHeading.Render("Unit " + rec.SessionID))
		b.WriteString("  " + styleDim.Render(fmt.Sprintf("(%d lines)", len(u.detailLn))))
		b.WriteString("\n")
		start := u.scroll
		if start > len(u.detailLn) {
			start = len(u.detailLn)
		}
		end := min(len(u.detailLn), start+max(1, height-3))
		for _, l := range u.detailLn[start:end] {
			// Soft-wrap each line at the terminal width so long transcript
			// entries stay fully readable instead of being clipped.
			for _, w := range wrapStyled(l, width) {
				b.WriteString(w)
				b.WriteString("\n")
			}
		}
		b.WriteString(styleDim.Render("  ↑/↓ or pgup/pgdn scroll · esc back to list"))
		return b.String()
	}
	lines := unitLines(u.units, u.cursor)
	start := u.scroll
	if start > len(lines) {
		start = len(lines)
	}
	end := min(len(lines), start+max(1, height-2))
	var b strings.Builder
	for _, l := range lines[start:end] {
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString(styleDim.Render("  ↑/↓ select · enter transcript · esc close"))
	return b.String()
}
