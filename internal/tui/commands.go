package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// slashCommand is one input-box command: the keybindings' text-mode twin.
// Keybindings cannot reach every terminal (dumb terminals, tmux key
// collisions, remote terminals that swallow alt/ctrl chords), so every
// binding-critical action is also reachable as "/name ..." typed into the
// prompt. Tab completes the word under the cursor; typing "/" lists them.
type slashCommand struct {
	name string
	desc string
	// args is the argument hint shown in listings, or "" when the command
	// takes no arguments.
	args string
	// busyOK marks commands that still act while a run is in progress
	// (mode switches, view toggles); the rest are rejected while busy,
	// mirroring the keybinding guards.
	busyOK bool
}

// effortLevels is the reasoning-effort ladder, shared by cycleEffort and the
// /effort argument parser so the two can never drift apart.
var effortLevels = []string{"low", "medium", "high", "xhigh", "max", "off"}

// slashCommands returns the command registry in listing order. Names are
// lowercase; lookup lowercases the user's word.
func slashCommands() []slashCommand {
	return []slashCommand{
		{name: "attach", desc: "attach a file"},
		{name: "diff", desc: "show git diff", busyOK: true},
		{name: "effort", desc: "set or cycle effort", args: "[low|medium|high|xhigh|max|off]", busyOK: false},
		{name: "help", desc: "toggle help panel", busyOK: true},
		{name: "image", desc: "paste clipboard image"},
		{name: "models", desc: "switch model"},
		{name: "new", desc: "start a new session"},
		{name: "queue", desc: "queue input while busy", busyOK: true},
		{name: "recovery", desc: "recover last run"},
		{name: "sessions", desc: "switch session"},
		{name: "steer", desc: "steer the running task", busyOK: true},
		{name: "system", desc: "toggle system prompt", busyOK: true},
		{name: "tools", desc: "toggle tool details", busyOK: true},
		{name: "quit", desc: "quit", busyOK: true},
	}
}

// slashWord reports whether value is a single slash word ("/", "/sea",
// "/effort") that completion applies to. A value with whitespace is a command
// with arguments (or a plain prompt) and is not completed as a command word.
func slashWord(value string) (string, bool) {
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \t\n") {
		return "", false
	}
	return value, true
}

// slashMatches returns the commands whose "/name" starts with the given
// slash-word prefix. "/" matches everything.
func slashMatches(word string) []slashCommand {
	var out []slashCommand
	for _, c := range slashCommands() {
		if strings.HasPrefix("/"+c.name, word) {
			out = append(out, c)
		}
	}
	return out
}

// commonPrefix returns the longest common prefix of the given slash-words.
func commonPrefix(words []string) string {
	if len(words) == 0 {
		return ""
	}
	p := words[0]
	for _, w := range words[1:] {
		for !strings.HasPrefix(w, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// completeSlash handles tab: it completes a slash command word to the single
// match (adding a space when the command takes arguments) or to the longest
// common prefix when several match. Outside a slash-word context the key is
// passed through to the textarea, preserving its default behaviour.
func (m *model) completeSlash(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	value := m.input.Value()
	word, ok := slashWord(value)
	if !ok {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.syncInputHeight()
		return m, cmd
	}
	matches := slashMatches(word)
	if len(matches) == 0 {
		return m, nil
	}
	setValue := func(v string) {
		m.input.SetValue(v)
		m.input.MoveToEnd()
		m.syncInputHeight()
	}
	if len(matches) == 1 {
		completion := "/" + matches[0].name
		if matches[0].args != "" {
			completion += " "
		}
		setValue(completion)
		return m, nil
	}
	words := make([]string, 0, len(matches))
	for _, c := range matches {
		words = append(words, "/"+c.name)
	}
	if p := commonPrefix(words); p != "" && len(p) > len(word) {
		setValue(p)
	}
	return m, nil
}

// slashHintLines renders the live completion listing shown above the input
// while the user is typing a slash word: "/name  description" rows for the
// current matches, capped so a bare "/" never floods the transcript. It
// returns nil once a command is fully typed (nothing left to discover).
func (m model) slashHintLines() []string {
	word, ok := slashWord(m.input.Value())
	if !ok {
		return nil
	}
	matches := slashMatches(word)
	if len(matches) == 1 && "/"+matches[0].name == word {
		return nil
	}
	if len(matches) == 0 {
		return nil
	}
	const maxShown = 8
	nameW := 0
	for _, c := range matches {
		if n := len(c.name) + 1; n > nameW {
			nameW = n
		}
	}
	var out []string
	for i, c := range matches {
		if i == maxShown {
			out = append(out, styleDim.Render(fmt.Sprintf("  … %d more", len(matches)-maxShown)))
			break
		}
		line := "  /" + c.name + strings.Repeat(" ", nameW-len(c.name)-1)
		hint := c.desc
		if c.args != "" {
			hint += " " + c.args
		}
		out = append(out, stylePrompt.Render(line)+styleDim.Render(hint))
	}
	return out
}

// slashHelpRows returns the command rows for the help panel, shaped like the
// keybinding rows so one width computation covers both sections. The argument
// hints are omitted here: the panel column is sized by its widest row, and
// the full "/effort [levels]" usage is already shown inline by the live
// completion listing.
func slashHelpRows() []helpRow {
	var rows []helpRow
	for _, c := range slashCommands() {
		rows = append(rows, helpRow{keys: []string{"/" + c.name}, desc: c.desc})
	}
	return rows
}

// runSlash executes a typed slash command (request starts with "/"). Unknown
// commands surface as a notice instead of reaching the model, so a typo is
// visible immediately; "//text" is the escape hatch that sends a literal
// message starting with a slash. Commands never enter prompt history — they
// are actions, not prompts.
func (m *model) runSlash(request string) (tea.Model, tea.Cmd) {
	body := strings.TrimPrefix(request, "/")
	if body == "" {
		return m, nil
	}
	// "//..." escapes out of command parsing: send "/..." to the model.
	if strings.HasPrefix(body, "/") {
		return m.startTurn(strings.TrimPrefix(request, "/"), m.attachments)
	}
	name, arg, _ := strings.Cut(body, " ")
	name = strings.ToLower(name)
	arg = strings.TrimSpace(arg)

	for _, c := range slashCommands() {
		if c.name != name {
			continue
		}
		if m.busy && !c.busyOK {
			m.notice = "/" + name + " is not available while running (" + string(m.keys.Stop) + " to stop)"
			return m, nil
		}
		return m.execSlash(name, arg)
	}
	m.notice = "unknown command /" + name + " — type / to list commands"
	return m, nil
}

// execSlash performs the action behind a known command. Each case mirrors the
// matching keybinding handler exactly, including its guards.
func (m *model) execSlash(name, arg string) (tea.Model, tea.Cmd) {
	switch name {
	case "attach":
		return m, m.openAttachPicker()
	case "diff":
		m.openDiff()
		return m, nil
	case "effort":
		if arg == "" {
			m.cycleEffort()
			m.notice = "reasoning effort: " + m.rt.Model.GetReasoningEffort()
			return m, nil
		}
		level := strings.ToLower(arg)
		if !isEffortLevel(level) {
			m.notice = "usage: /effort " + strings.Join(effortLevels, "|")
			return m, nil
		}
		m.rt.Model.SetReasoningEffort(level)
		m.notice = "reasoning effort: " + level
		return m, nil
	case "help":
		m.toggleHelp()
		return m, nil
	case "image":
		return m, pasteImageCmd()
	case "models":
		if m.rt == nil || m.rt.Model == nil {
			return m, nil
		}
		return m, m.openModelPickerCmd()
	case "new":
		m.newSession()
		return m, nil
	case "queue":
		m.steerMode = false
		m.notice = "enter queues input for the next turn"
		return m, nil
	case "recovery":
		prompt := recoveryBootstrap
		if arg != "" {
			prompt += "\n\nUser note: " + arg
		}
		return m.startTurn(prompt, m.attachments)
	case "sessions":
		m.openPicker(overlaySessionPicker)
		return m, nil
	case "steer":
		m.steerMode = true
		m.notice = "enter steers the running execution"
		return m, nil
	case "system":
		if m.rt != nil && strings.TrimSpace(m.rt.ExtraSystemPrompt) != "" {
			m.sysPromptExpanded = !m.sysPromptExpanded
		} else {
			m.notice = "no extra system prompt is set"
		}
		return m, nil
	case "tools":
		m.toggleTools()
		return m, nil
	case "quit":
		if m.busy {
			m.queue = nil
			m.stopExecution()
			m.persistStopped()
		}
		return m, tea.Quit
	}
	m.notice = "unknown command /" + name
	return m, nil
}

func isEffortLevel(level string) bool {
	for _, l := range effortLevels {
		if l == level {
			return true
		}
	}
	return false
}
