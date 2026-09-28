package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
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

// newSessionItem is the sentinel value of the "New session" entry pinned at
// the top of the session picker: selecting it starts a fresh session.
type newSessionItem struct{}

// buildSessionItems maps session summaries to picker entries. A "New session"
// entry is always pinned at the top so a fresh session can be started from
// the picker even when no sessions are stored yet.
func buildSessionItems(summaries []session.Summary) []list.Item {
	items := make([]list.Item, 0, len(summaries)+1)
	items = append(items, pickerItem{
		title: "＋ New session",
		desc:  "start a fresh session",
		value: newSessionItem{},
	})
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

// modelSearch is the model picker's type-ahead name search. The picker keeps
// it focused, so the user just starts typing a model name and the list narrows
// live — there is no separate filter mode to enter, and no key to learn.
//
// The search owns the query text and the candidate list of the active provider
// tab, so the picker can re-apply the query whenever the list is refilled
// (provider tab switch, re-fetch) instead of silently discarding what the user
// typed.
type modelSearch struct {
	input textinput.Model
	// items is the full candidate list of the active tab.
	items []list.Item
	// shown is what the picker list renders: all items while the query is
	// empty, otherwise the ranked matches.
	shown []list.Item
	// count is the number of matches for a non-empty query.
	count int
	// pending is true while the active tab's models are being fetched: the
	// query is kept but no match verdict is reported yet.
	pending bool
	// err is a search-side message (currently: no match) shown after the
	// query instead of an empty list.
	err string
}

func newModelSearch(width int) modelSearch {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "type a model name to search…"
	in.CharLimit = 96
	in.SetWidth(max(1, width-2))
	// A real terminal cursor rather than the bubble-rendered virtual one: the
	// picker frame positions it on the search row.
	in.SetVirtualCursor(false)
	return modelSearch{input: in}
}

// reset drops the query and the candidates. Called when the picker opens and
// when it closes, so a reopened picker never starts on a stale filter.
func (s *modelSearch) reset() {
	s.input.Reset()
	s.beginFetch()
}

// beginFetch keeps the typed query but empties the candidates. Called while a
// provider tab's list is in flight: the picker must never show one provider's
// models under another provider's tab, and a "no match" message must not flash
// for a list that has not arrived yet. The query survives, so the search is
// re-applied to the models as soon as they land.
func (s *modelSearch) beginFetch() {
	s.items = nil
	s.shown = nil
	s.count = 0
	s.err = ""
	s.pending = true
}

// setItems replaces the candidate list and re-applies the current query, so a
// refetch keeps searching instead of discarding what the user typed.
func (s *modelSearch) setItems(items []list.Item) {
	s.pending = false
	s.items = items
	s.apply()
}

// query is the search text with surrounding spaces trimmed.
func (s *modelSearch) query() string { return strings.TrimSpace(s.input.Value()) }

// active reports whether a query is in effect.
func (s *modelSearch) active() bool { return s.query() != "" }

// apply narrows the candidates to the query. An empty query means "no search
// active": the full list stays visible and nothing is pre-selected beyond the
// list's own cursor. While a fetch is pending no verdict is reported at all.
func (s *modelSearch) apply() {
	s.err = ""
	q := s.query()
	if s.pending {
		s.shown = nil
		s.count = 0
		return
	}
	if q == "" || len(s.items) == 0 {
		// Nothing typed, or nothing to search yet (fetch failed or the
		// provider lists no models): no verdict, the picker's own error line
		// already explains an empty tab.
		s.shown = s.items
		s.count = 0
		return
	}
	matches := rankModels(q, s.items)
	s.count = len(matches)
	if len(matches) == 0 {
		s.err = "no model matches “" + q + "”"
		s.shown = nil
		return
	}
	s.shown = matches
}

// focus starts the cursor blink.
func (s *modelSearch) focus() tea.Cmd { return s.input.Focus() }

// blur hides the cursor.
func (s *modelSearch) blur() { s.input.Blur() }

// searchPrefix is the label in front of the query. Its display width is the
// column offset the entry's own cursor position has to be shifted by to land
// on the real cell in the rendered row.
const searchPrefix = "  ⌕ "

// searchCursorOffset is the display width of searchPrefix.
func searchCursorOffset() int { return lipgloss.Width(searchPrefix) }

// searchLine renders the search row: the query (or its placeholder) followed
// by the live match count, or the no-match message. While a fetch is pending
// no verdict is shown — the picker's loading line already explains the wait.
func (s modelSearch) searchLine() string {
	// The text input pads its value to its own width; trailing spaces are
	// dropped so the match-count suffix sits right behind the query instead of
	// being pushed to the far edge of the row.
	line := styleSearchQuery.Render(searchPrefix) + strings.TrimRight(s.input.View(), " ")
	switch {
	case s.err != "":
		line += "  " + styleError.Render("✖ "+s.err)
	case s.active() && !s.pending:
		noun := "matches"
		if s.count == 1 {
			noun = "match"
		}
		line += "  " + styleDim.Render(fmt.Sprintf("%d %s", s.count, noun))
	}
	return line
}

// modelsSearchTerm is the comparison form of a model id or query: lower-cased
// with the separators model ids are built from dropped, so "claude35sonnet"
// finds "claude-3.5-sonnet".
func modelsSearchTerm(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || unicode.IsSpace(r):
			return -1
		}
		return r
	}, s)
}

// rankModels orders the candidates by how well they match the query: an exact
// id wins, then a prefix match, then a plain substring, then a subsequence (so
// "cso" finds "claude-sonnet-opus"). Within a tier the shorter id wins, then
// the earlier original position, so the ranking is deterministic.
func rankModels(query string, items []list.Item) []list.Item {
	q := modelsSearchTerm(query)
	if q == "" || len(items) == 0 {
		return nil
	}
	type scored struct {
		item  list.Item
		rank  int
		size  int
		order int
	}
	scoredList := make([]scored, 0, len(items))
	for i, it := range items {
		pi, ok := it.(pickerItem)
		if !ok {
			continue
		}
		id := modelEntryID(pi)
		rank, ok := modelMatchRank(q, modelsSearchTerm(id), id)
		if !ok {
			continue
		}
		scoredList = append(scoredList, scored{item: it, rank: rank, size: len([]rune(id)), order: i})
	}
	if len(scoredList) == 0 {
		return nil
	}
	sort.SliceStable(scoredList, func(i, j int) bool {
		a, b := scoredList[i], scoredList[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.size != b.size {
			return a.size < b.size
		}
		return a.order < b.order
	})
	out := make([]list.Item, 0, len(scoredList))
	for _, s := range scoredList {
		out = append(out, s.item)
	}
	return out
}

// modelMatchRank scores one candidate against the normalised query. Lower is
// better; ok is false when the candidate does not match at all.
func modelMatchRank(q, target, rawTarget string) (int, bool) {
	switch {
	case rawTarget == q || target == q:
		return 0, true
	case strings.HasPrefix(target, q):
		return 1, true
	case strings.Contains(target, q):
		return 2, true
	case isSubsequence(q, target):
		return 3, true
	}
	return 0, false
}

// isSubsequence reports whether every rune of q appears in target in order.
func isSubsequence(q, target string) bool {
	r := []rune(q)
	pos := 0
	for _, c := range target {
		if pos < len(r) && c == r[pos] {
			pos++
		}
	}
	return pos == len(r)
}

// modelEntryID is the searchable model id of a picker row: the title with the
// "  (current)" marker and any "provider · " prefix removed, so typing the
// bare model name finds it.
func modelEntryID(pi pickerItem) string {
	title := strings.TrimSuffix(pi.title, "  (current)")
	if i := strings.Index(title, " · "); i >= 0 {
		title = title[i+len(" · "):]
	}
	return title
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

// shortID8 shortens a session id for compact display in the status line.
func shortID8(id string) string {
	if len(id) > 15 {
		return id[:15]
	}
	return id
}

var (
	styleDim          = lipgloss.NewStyle().Faint(true)
	styleDiffHeader   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorPrompt))
	styleDiffMeta     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorEffort))
	styleDiffAdd      = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a"))
	styleDiffDel      = lipgloss.NewStyle().Foreground(lipgloss.Color(colorError))
	stylePanelHeading = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorPrompt))
	styleSearchQuery  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorPrompt))
)
