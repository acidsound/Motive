package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	llm "github.com/acidsound/Motive/internal/model"
)

// attachItem is a pending (or recorded) attachment with its pre-rendered
// inline thumbnail escape sequence. The thumb is computed once when the
// attachment is added; View() re-emits it on every frame without re-decoding
// or re-encoding the image.
type attachItem struct {
	llm.Attachment
	thumb string
}

// dirEntry is one row of the attach file browser.
type dirEntry struct {
	name  string
	path  string
	isDir bool
	size  int64
}

// dirPicker is the attach-file overlay: a directory browser plus a path /
// filter entry bar. Empty input browses with ↑/↓/enter; typing either filters
// the current directory by name (no path separators) or resolves a relative /
// absolute / ~ path when it contains a separator or starts with ~.
type dirPicker struct {
	dir     string
	entries []dirEntry
	input   textinput.Model
	list    list.Model
	err     string
	hint    string
}

func newDirPicker(baseDir string, width, height int) dirPicker {
	input := textinput.New()
	input.Placeholder = "path or filter…"
	input.Prompt = ""
	input.SetWidth(max(1, width-2))

	l := list.New(nil, list.NewDefaultDelegate(), max(40, width-6), max(10, height-8))
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)

	p := dirPicker{dir: baseDir, input: input, list: l}
	p.refresh()
	return p
}

// refresh reloads the current directory, reapplies the filter, and updates
// the hint. It is called after every navigation or input change.
func (p *dirPicker) refresh() {
	p.reloadEntries()
	p.applyFilter()
	p.updateHint()
}

func (p *dirPicker) reloadEntries() {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		p.err = err.Error()
		p.entries = nil
		return
	}
	p.err = ""
	parent := filepath.Dir(p.dir)
	des := []dirEntry{{name: "..", path: parent, isDir: true}}
	for _, e := range entries {
		de := dirEntry{name: e.Name(), path: filepath.Join(p.dir, e.Name()), isDir: e.IsDir()}
		if !de.isDir {
			if info, ierr := e.Info(); ierr == nil {
				de.size = info.Size()
			}
		}
		des = append(des, de)
	}
	sort.Slice(des[1:], func(i, j int) bool {
		a, b := des[1:][i], des[1:][j]
		if a.isDir != b.isDir {
			return a.isDir // directories first
		}
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	})
	p.entries = des
}

// pathLike reports whether the typed text should be treated as a path
// (contains a separator or is a home reference) instead of a name filter.
func pathLike(text string) bool {
	return strings.Contains(text, "/") || strings.HasPrefix(text, "~")
}

func (p *dirPicker) applyFilter() {
	text := strings.TrimSpace(p.input.Value())
	shown := p.entries
	if text != "" && !pathLike(text) {
		needle := strings.ToLower(text)
		shown = nil
		for _, e := range p.entries {
			if strings.Contains(strings.ToLower(e.name), needle) {
				shown = append(shown, e)
			}
		}
	}
	items := make([]list.Item, 0, len(shown))
	for i := range shown {
		e := &shown[i]
		title := e.name
		desc := ""
		if e.isDir {
			if e.name != ".." {
				title += "/"
			}
			desc = "dir"
		} else {
			desc = humanSize(e.size)
		}
		items = append(items, pickerItem{title: title, desc: desc, value: e})
	}
	p.list.SetItems(items)
}

// resolveTypedPath expands the typed text against the current directory, the
// workspace root, and the process working directory. Returns the absolute
// path and whether it is a directory; ok is false when nothing matches.
func (p *dirPicker) resolveTypedPath(text string) (path string, isDir bool, ok bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false, false
	}
	if strings.HasPrefix(text, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			text = filepath.Join(home, strings.TrimPrefix(text, "~"))
		}
	}
	candidates := []string{text}
	if !filepath.IsAbs(text) {
		candidates = []string{
			filepath.Join(p.dir, text),
			filepath.Join(wsRoot(), text),
		}
		if cwd, err := os.Getwd(); err == nil {
			candidates = append(candidates, filepath.Join(cwd, text))
		}
	}
	for _, c := range candidates {
		info, err := os.Stat(c)
		if err != nil {
			continue
		}
		return c, info.IsDir(), true
	}
	return "", false, false
}

func (p *dirPicker) updateHint() {
	text := strings.TrimSpace(p.input.Value())
	if text == "" {
		p.hint = "↑/↓ browse · enter attach · type a path or name to filter"
		return
	}
	if pathLike(text) {
		if path, isDir, ok := p.resolveTypedPath(text); ok {
			kind := "file"
			if isDir {
				kind = "directory"
			}
			p.hint = "enter: attach " + kind + " " + path
		} else {
			p.hint = "no such path: " + text
		}
		return
	}
	n := 0
	for _, e := range p.entries {
		if strings.Contains(strings.ToLower(e.name), strings.ToLower(text)) {
			n++
		}
	}
	if n == 0 {
		p.hint = "no matches — enter a path with / to attach elsewhere"
	} else {
		p.hint = fmt.Sprintf("%d match(es) — enter attaches the highlighted one", n)
	}
}

// wsRoot returns the workspace root when known (the dirPicker is constructed
// with it), falling back to the process working directory.
func wsRoot() string {
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// openAttachPicker opens the attach overlay rooted at the workspace root (or
// the cwd when no workspace is wired).
func (m *model) openAttachPicker() tea.Cmd {
	base := "."
	if m.rt != nil && m.rt.WS != nil && m.rt.WS.Root != "" {
		base = m.rt.WS.Root
	}
	m.attach = newDirPicker(base, m.width, m.height)
	m.overlay = overlayAttach
	return m.attach.input.Focus()
}

// handleAttachKey routes keys while the attach overlay is open.
func (m *model) handleAttachKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		m.overlay = overlayNone
		return m, nil
	}
	if key == "esc" || key == string(m.keys.Stop) {
		if m.attach.input.Value() != "" {
			m.attach.input.SetValue("")
			m.attach.refresh()
			return m, nil
		}
		m.overlay = overlayNone
		return m, nil
	}
	if key == "enter" {
		return m.commitAttachSelection()
	}
	if m.attach.input.Value() == "" {
		// Browsing mode: navigation keys drive the list; everything else
		// (printable keys, "/" for absolute paths) starts path entry.
		switch key {
		case "up", "k", "down", "j", "pgup", "pgdown", "home", "end", "g", "G":
			var cmd tea.Cmd
			m.attach.list, cmd = m.attach.list.Update(msg)
			return m, cmd
		case "backspace", "left", "h":
			parent := filepath.Dir(m.attach.dir)
			if parent != m.attach.dir {
				m.attach.dir = parent
				m.attach.refresh()
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.attach.input, cmd = m.attach.input.Update(msg)
	m.attach.refresh()
	return m, cmd
}

// updateAttach handles non-key messages (blink, resize) while the attach
// overlay is open.
func (m *model) updateAttach(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.attach.input.SetWidth(max(1, msg.Width-2))
		m.attach.list.SetSize(max(40, msg.Width-6), max(10, msg.Height-8))
	default:
		var cmd tea.Cmd
		m.attach.input, cmd = m.attach.input.Update(msg)
		m.attach.refresh()
		return m, cmd
	}
	return m, nil
}

// commitAttachSelection resolves the current state on enter: a typed path
// first, otherwise the highlighted browser entry. Directories navigate,
// files attach and close the overlay.
func (m *model) commitAttachSelection() (tea.Model, tea.Cmd) {
	if text := strings.TrimSpace(m.attach.input.Value()); text != "" {
		if path, isDir, ok := m.attach.resolveTypedPath(text); ok {
			if isDir {
				m.attach.dir = path
				m.attach.input.SetValue("")
				m.attach.refresh()
				return m, nil
			}
			m.attachPath(path)
			return m, nil
		}
		// Filter mode: fall through to the highlighted (filtered) entry.
	}
	item := m.attach.list.SelectedItem()
	if item == nil {
		return m, nil
	}
	pi, ok := item.(pickerItem)
	if !ok {
		return m, nil
	}
	entry, ok := pi.value.(*dirEntry)
	if !ok {
		return m, nil
	}
	if entry.isDir {
		if entry.name == ".." {
			parent := filepath.Dir(m.attach.dir)
			if parent == m.attach.dir {
				return m, nil
			}
			m.attach.dir = parent
		} else {
			m.attach.dir = entry.path
		}
		m.attach.input.SetValue("")
		m.attach.refresh()
		return m, nil
	}
	m.attachPath(entry.path)
	return m, nil
}

// attachPath detects the file type, appends it to the pending attachments,
// and closes the overlay. Errors (missing file, directory, unreadable) are
// shown in the overlay instead of closing it.
func (m *model) attachPath(path string) {
	att, err := llm.DetectAttachment(path)
	if err != nil {
		m.attach.err = err.Error()
		return
	}
	m.attachments = append(m.attachments, attachItem{Attachment: att, thumb: thumbnail(att.Path)})
	m.overlay = overlayNone
	m.notice = ""
}

// attachmentsModel converts pending attachments to the persisted form.
func attachmentsModel(atts []attachItem) []llm.Attachment {
	if len(atts) == 0 {
		return nil
	}
	out := make([]llm.Attachment, 0, len(atts))
	for _, a := range atts {
		out = append(out, a.Attachment)
	}
	return out
}

// attachmentLines renders one line per attachment for the transcript and the
// input preview. Images get an inline thumbnail when the terminal supports
// it; videos and other files get an icon chip with name and size.
func attachmentLines(atts []attachItem, width int) []string {
	var out []string
	for _, a := range atts {
		var line string
		switch a.Kind {
		case "image":
			if a.thumb != "" {
				line = a.thumb + " " + a.Name + " (" + humanSize(a.Size) + ")"
			} else {
				line = "🖼 " + a.Name + " (" + humanSize(a.Size) + ")"
			}
		case "video":
			line = "🎬 " + a.Name + " (" + humanSize(a.Size) + ")"
		default:
			line = "📎 " + a.Name + " (" + humanSize(a.Size) + ")"
		}
		out = append(out, wrapStyled(line, width)...)
	}
	return out
}
