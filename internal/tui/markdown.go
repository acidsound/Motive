package tui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Lightweight markdown rendering for the transcript: fenced code blocks,
// headings, blockquotes, rules, lists, and inline bold / inline code / italics.
// The goal is readability in a terminal, not spec compliance.

var (
	mdBoldPat   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalicPat = regexp.MustCompile(`\*([^*\s][^*]*?)\*`)
	mdCodePat   = regexp.MustCompile("`([^`]+)`")

	mdStyleHeading = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorPrompt))
	mdStyleCode    = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTool))
	mdStyleQuote   = lipgloss.NewStyle().Faint(true).Italic(true)
	mdStyleHR      = lipgloss.NewStyle().Faint(true)
	mdStyleRule    = lipgloss.NewStyle().Faint(true)
	mdStyleCodeFg  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTool)).Bold(true)
)

// renderMarkdown converts markdown text into wrapped, styled terminal lines.
func renderMarkdown(text string, width int) []string {
	if width < 20 {
		width = 20
	}
	text = strings.TrimRight(text, "\n")
	lines := strings.Split(text, "\n")
	var out []string
	var code []string
	inCode := false
	flush := func() {
		if !inCode {
			return
		}
		out = append(out, mdStyleRule.Render(strings.Repeat("─", max(4, width-2))))
		for _, l := range code {
			out = append(out, renderCodeLine(l, width))
		}
		out = append(out, mdStyleRule.Render(strings.Repeat("─", max(4, width-2))))
		code = nil
		inCode = false
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inCode {
			if isFence(trimmed) {
				flush()
				continue
			}
			code = append(code, line)
			continue
		}
		if isFence(trimmed) {
			inCode = true
			code = nil
			continue
		}
		switch {
		case trimmed == "":
			out = append(out, "")
		case isHeading(trimmed):
			out = append(out, wrapStyled(mdStyleHeading.Render(stripHeading(trimmed)), width)...)
		case isHR(trimmed):
			out = append(out, mdStyleHR.Render(strings.Repeat("─", max(4, width-2))))
		case strings.HasPrefix(trimmed, ">"):
			inner := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			for _, l := range wrapStyled(applyInline(inner), width) {
				out = append(out, mdStyleQuote.Render("▍ "+l))
			}
		case isListLine(trimmed):
			indent := listIndent(line)
			for _, l := range wrapStyled(applyInline(trimmed), width) {
				out = append(out, indent+l)
			}
		default:
			out = append(out, wrapStyled(applyInline(trimmed), width)...)
		}
	}
	flush()
	return out
}

func isFence(s string) bool {
	return len(s) >= 3 && (strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~"))
}

func renderCodeLine(line string, width int) string {
	if lipgloss.Width(line) > width {
		line = ansi.Truncate(line, width, "…")
	}
	return mdStyleCodeFg.Render(line)
}

func isHeading(s string) bool {
	return strings.HasPrefix(s, "#")
}

func stripHeading(s string) string {
	s = strings.TrimLeft(s, "#")
	return strings.TrimSpace(s)
}

func isHR(s string) bool {
	if s != "---" && s != "***" && s != "___" {
		return false
	}
	return true
}

func isListLine(s string) bool {
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") || strings.HasPrefix(s, "+ ") {
		return true
	}
	return listNumberRe.MatchString(s)
}

var listNumberRe = regexp.MustCompile(`^\d+[.)]\s`)

func listIndent(line string) string {
	leading := len(line) - len(strings.TrimLeft(line, " \t"))
	return strings.Repeat(" ", leading)
}

// wrapStyled wraps plain (already partially styled) text at the given width,
// preserving embedded ANSI sequences in the width calculation.
func wrapStyled(text string, width int) []string {
	wrapped := lipgloss.Wrap(text, width, "")
	return strings.Split(wrapped, "\n")
}

func applyInline(line string) string {
	line = mdBoldPat.ReplaceAllStringFunc(line, func(m string) string {
		inner := mdBoldPat.FindStringSubmatch(m)[1]
		return lipgloss.NewStyle().Bold(true).Render(inner)
	})
	line = mdCodePat.ReplaceAllStringFunc(line, func(m string) string {
		inner := mdCodePat.FindStringSubmatch(m)[1]
		return mdStyleCode.Render(inner)
	})
	line = mdItalicPat.ReplaceAllStringFunc(line, func(m string) string {
		inner := mdItalicPat.FindStringSubmatch(m)[1]
		return lipgloss.NewStyle().Italic(true).Render(inner)
	})
	return line
}
