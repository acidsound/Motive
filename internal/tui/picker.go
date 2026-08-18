package tui

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
	"github.com/acidsound/Motive/internal/config"
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

// modelSelection is the value attached to a model picker entry.
type modelSelection struct {
	provider *config.Provider
	model    string
}

// buildModelItems lists every provider/model combination, marking the active
// one. Efforts are intentionally not expanded here: effort stays a global
// per-turn control (ctrl+e), matching Motive's single-effort contract.
func buildModelItems(cfg *config.Config, activeModel, activeProvider string) []list.Item {
	var items []list.Item
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		for _, model := range p.AllModels() {
			active := p.Name == activeProvider && model == activeModel
			marker := " "
			if active {
				marker = "●"
			}
			effort := p.ReasoningEffort
			if effort == "" {
				effort = "low"
			}
			items = append(items, pickerItem{
				title: model,
				desc:  marker + " " + p.Name + " · " + p.BaseURL + " · effort " + effort,
				value: modelSelection{provider: p, model: model},
			})
		}
	}
	return items
}

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

// buildPanel composes the side panel: workspace file tree, git status, and
// TODO items when a TODO file exists.
func buildPanel(files, gitStatus, todos string) []string {
	out := []string{stylePanelHeading.Render("Workspace")}
	for _, l := range strings.Split(strings.TrimRight(files, "\n"), "\n") {
		if l == "" {
			continue
		}
		if strings.HasSuffix(l, "/") {
			out = append(out, stylePanelDir.Render("📁 "+l))
		} else {
			out = append(out, stylePanelFile.Render(l))
		}
	}
	if gitStatus != "" {
		out = append(out, stylePanelHeading.Render("Git"))
		for _, l := range strings.Split(strings.TrimRight(gitStatus, "\n"), "\n") {
			if l == "" {
				continue
			}
			switch {
			case strings.HasPrefix(l, "M"), strings.HasPrefix(l, "MM"):
				out = append(out, styleGitModified.Render(l))
			case strings.HasPrefix(l, "A"), strings.HasPrefix(l, "??"):
				out = append(out, styleGitAdded.Render(l))
			case strings.HasPrefix(l, "D"):
				out = append(out, styleGitDeleted.Render(l))
			default:
				out = append(out, styleDim.Render(l))
			}
		}
	}
	if todos != "" {
		out = append(out, stylePanelHeading.Render("TODO"))
		for _, l := range strings.Split(strings.TrimRight(todos, "\n"), "\n") {
			if strings.TrimSpace(l) == "" {
				continue
			}
			out = append(out, styleTodo.Render("☐ "+l))
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
	stylePanelDir     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTool))
	stylePanelFile    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAssistant))
	styleGitModified  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorEffort))
	styleGitAdded     = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a"))
	styleGitDeleted   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorError))
	styleTodo         = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAssistant))
)
