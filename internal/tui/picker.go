package tui

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
	"github.com/acidsound/Motive/internal/session"
)

// pickerItem adapts arbitrary values to bubbles/list's DefaultItem interface.
type pickerItem struct {
	title string
	desc  string
	value any
}

func (i pickerItem) Title() string       { return i.title }
func (i pickerItem) Description() string { return i.desc }
func (i pickerItem) FilterValue() string { return i.title + " " + i.desc }

// buildSessionItems maps session summaries to picker entries.
func buildSessionItems(summaries []session.Summary) []list.Item {
	items := make([]list.Item, 0, len(summaries))
	for _, s := range summaries {
		rev := shortRev(s.ResultRevision)
		if rev == "" {
			rev = "-"
		}
		items = append(items, pickerItem{
			title: s.ID + "  " + s.Created.Local().Format("01-02 15:04"),
			desc:  s.Preview + " · rev " + rev + " · " + strconv.Itoa(s.ToolCalls) + " tools · " + strconv.Itoa(s.Lines) + " lines",
			value: s,
		})
	}
	return items
}

// colorizeDiff turns raw git diff output into styled terminal lines.
func colorizeDiff(diff string) []string {
	if strings.TrimSpace(diff) == "" {
		return []string{styleDim.Render("no changes")}
	}
	var out []string
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---"):
			out = append(out, styleDiffHeader.Render(l))
		case strings.HasPrefix(l, "@@"):
			out = append(out, styleDiffMeta.Render(l))
		case strings.HasPrefix(l, "+"):
			out = append(out, styleDiffAdd.Render(l))
		case strings.HasPrefix(l, "-"):
			out = append(out, styleDiffDel.Render(l))
		case strings.HasPrefix(l, "diff "):
			out = append(out, styleDiffHeader.Render(l))
		default:
			out = append(out, styleDim.Render(l))
		}
	}
	return out
}

func shortRev(rev string) string {
	rev = strings.TrimSpace(rev)
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

var (
	styleDim          = lipgloss.NewStyle().Faint(true)
	styleDiffHeader   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorPrompt))
	styleDiffMeta     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorEffort))
	styleDiffAdd      = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a"))
	styleDiffDel      = lipgloss.NewStyle().Foreground(lipgloss.Color(colorError))
	stylePanelHeading = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorPrompt))
)
