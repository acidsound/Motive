package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	"github.com/acidsound/Motive/internal/config"
	llm "github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/runtime"
)

// modelsServer serves an OpenAI-compatible /models endpoint with the given ids.
func modelsServer(t *testing.T, ids ...string) *httptest.Server {
	t.Helper()
	var data []string
	for _, id := range ids {
		data = append(data, fmt.Sprintf(`{"id":%q,"object":"model"}`, id))
	}
	body := `{"object":"list","data":[` + strings.Join(data, ",") + `]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newPickerTestModel(providers []config.Provider, activeBaseURL, activeModel string) model {
	m := newModel(
		&runtime.Runtime{Model: &llm.Client{BaseURL: activeBaseURL, Model: activeModel}},
		&config.Config{Providers: providers},
		nil, false,
	)
	m.width = 80
	m.height = 24
	return m
}

func pickerListTitles(m model) []string {
	var out []string
	for _, it := range m.list.Items() {
		if pi, ok := it.(pickerItem); ok {
			out = append(out, pi.title)
		}
	}
	return out
}

// openPickerFor runs the picker's initial fetch synchronously and applies the
// resulting modelsMsg, leaving the picker open on the active provider.
func openPickerFor(t *testing.T, m *model) {
	t.Helper()
	cmd := m.openModelPickerCmd()
	if cmd == nil {
		t.Fatal("openModelPickerCmd returned nil")
	}
	msg, ok := cmd().(modelsMsg)
	if !ok {
		t.Fatalf("unexpected message type %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("initial fetch failed: %v", msg.err)
	}
	_, _ = m.openModelPicker(msg)
}

func TestModelPickerOpensOnActiveProvider(t *testing.T) {
	srvA := modelsServer(t, "a-1", "a-2")
	srvB := modelsServer(t, "b-1")
	m := newPickerTestModel([]config.Provider{
		{Name: "alpha", BaseURL: srvA.URL, Model: "a-1"},
		{Name: "beta", BaseURL: srvB.URL, Model: "b-1"},
	}, srvB.URL, "b-1")

	openPickerFor(t, &m)

	if m.overlay != overlayModelPicker {
		t.Fatalf("overlay = %v, want model picker", m.overlay)
	}
	if m.providerIdx != 1 {
		t.Errorf("providerIdx = %d, want 1 (active provider)", m.providerIdx)
	}
	if got := pickerListTitles(m); len(got) != 1 || !strings.HasPrefix(got[0], "b-1") {
		t.Errorf("list = %v, want b-1", got)
	}
	if !strings.Contains(pickerListTitles(m)[0], "(current)") {
		t.Errorf("current model not marked: %v", pickerListTitles(m))
	}
}

func TestModelPickerSwitchProviderWithKeys(t *testing.T) {
	srvA := modelsServer(t, "a-1", "a-2")
	srvB := modelsServer(t, "b-1")
	m := newPickerTestModel([]config.Provider{
		{Name: "alpha", BaseURL: srvA.URL, Model: "a-1"},
		{Name: "beta", BaseURL: srvB.URL, Model: "b-1"},
	}, srvB.URL, "b-1")
	openPickerFor(t, &m)

	// h (left) moves to the previous provider tab and refetches.
	_, cmd := m.handleOverlayKey(teaKey("h"))
	if cmd == nil {
		t.Fatal("h did not return a fetch command")
	}
	msg, ok := cmd().(modelsMsg)
	if !ok || msg.err != nil {
		t.Fatalf("h fetch: %T %v", cmd(), msg.err)
	}
	_, _ = m.openModelPicker(msg)
	if m.providerIdx != 0 {
		t.Errorf("providerIdx = %d, want 0 after h", m.providerIdx)
	}
	if got := pickerListTitles(m); len(got) != 2 || !strings.HasPrefix(got[0], "a-1") {
		t.Errorf("list = %v, want a-1/a-2", got)
	}

	// l (right) wraps back to the last tab.
	_, cmd = m.handleOverlayKey(teaKey("l"))
	if cmd == nil {
		t.Fatal("l did not return a fetch command")
	}
	msg, _ = cmd().(modelsMsg)
	_, _ = m.openModelPicker(msg)
	if m.providerIdx != 1 {
		t.Errorf("providerIdx = %d, want 1 after l (wrap)", m.providerIdx)
	}

	// left/right arrows do the same.
	_, cmd = m.handleOverlayKey(teaKey("left"))
	if cmd == nil {
		t.Fatal("left did not return a fetch command")
	}
	msg, _ = cmd().(modelsMsg)
	_, _ = m.openModelPicker(msg)
	if m.providerIdx != 0 {
		t.Errorf("providerIdx = %d, want 0 after left", m.providerIdx)
	}
}

func TestModelPickerStaleResponseDropped(t *testing.T) {
	srvA := modelsServer(t, "a-1")
	srvB := modelsServer(t, "b-1")
	m := newPickerTestModel([]config.Provider{
		{Name: "alpha", BaseURL: srvA.URL, Model: "a-1"},
		{Name: "beta", BaseURL: srvB.URL, Model: "b-1"},
	}, srvB.URL, "b-1")
	openPickerFor(t, &m)

	// A response tagged for a tab the user is no longer on must be ignored.
	_, _ = m.openModelPicker(modelsMsg{providerIdx: 0, models: []llm.ModelInfo{{ID: "stale"}}, current: "b-1"})
	if m.providerIdx != 1 {
		t.Errorf("providerIdx changed by stale message: %d", m.providerIdx)
	}
	if got := pickerListTitles(m); len(got) != 1 || !strings.HasPrefix(got[0], "b-1") {
		t.Errorf("list replaced by stale message: %v", got)
	}
}

func TestModelPickerApplySwitchesProviderAndModel(t *testing.T) {
	srvA := modelsServer(t, "a-1", "a-2")
	srvB := modelsServer(t, "b-1")
	temp := 0.25
	m := newPickerTestModel([]config.Provider{
		{Name: "alpha", BaseURL: srvA.URL, Model: "a-1"},
		{Name: "beta", BaseURL: srvB.URL, Model: "b-1", ReasoningEffort: "high", Temperature: &temp, MaxTokens: 1234},
	}, srvA.URL, "a-1")
	openPickerFor(t, &m)

	// Move to beta and apply the (single) listed model.
	_, cmd := m.handleOverlayKey(teaKey("l"))
	msg, _ := cmd().(modelsMsg)
	_, _ = m.openModelPicker(msg)
	_, _ = m.handleOverlayKey(teaKey("enter"))

	c := m.rt.Model
	if c.BaseURL != strings.TrimRight(srvB.URL, "/") {
		t.Errorf("BaseURL = %q, want %q", c.BaseURL, srvB.URL)
	}
	if c.Model != "b-1" {
		t.Errorf("Model = %q, want b-1", c.Model)
	}
	if c.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want high", c.ReasoningEffort)
	}
	if c.Temperature != 0.25 {
		t.Errorf("Temperature = %v, want 0.25", c.Temperature)
	}
	if c.MaxTokens != 1234 {
		t.Errorf("MaxTokens = %d, want 1234", c.MaxTokens)
	}
	if m.overlay != overlayNone {
		t.Errorf("overlay = %v, want none after apply", m.overlay)
	}
}

func TestModelPickerFallsBackToConfiguredModels(t *testing.T) {
	// A provider whose endpoint is unreachable falls back to its configured
	// model list instead of failing the picker.
	m := newPickerTestModel([]config.Provider{
		{Name: "offline", BaseURL: "http://127.0.0.1:1/v1", Model: "cfg-1", Models: []string{"cfg-2"}},
	}, "http://127.0.0.1:1/v1", "cfg-1")

	cmd := m.openModelPickerCmd()
	msg, ok := cmd().(modelsMsg)
	if !ok {
		t.Fatalf("unexpected message type %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("fetch should fall back, got error: %v", msg.err)
	}
	_, _ = m.openModelPicker(msg)
	if got := pickerListTitles(m); len(got) != 2 || !strings.HasPrefix(got[0], "cfg-1") || !strings.HasPrefix(got[1], "cfg-2") {
		t.Errorf("list = %v, want cfg-1/cfg-2", got)
	}
}

// When the default provider is unreachable and has no configured fallback
// models, the picker still opens so the user can switch to another provider
// tab that works.
func TestModelPickerOpensOnUnreachableDefaultWithOtherProviders(t *testing.T) {
	srvB := modelsServer(t, "b-1", "b-2")
	m := newPickerTestModel([]config.Provider{
		{Name: "offline", BaseURL: "http://127.0.0.1:1/v1", Model: "", Models: []string{}},
		{Name: "beta", BaseURL: srvB.URL, Model: "b-1"},
	}, "http://127.0.0.1:1/v1", "")

	cmd := m.openModelPickerCmd()
	msg, ok := cmd().(modelsMsg)
	if !ok {
		t.Fatalf("unexpected message type %T", cmd())
	}
	if msg.err == nil {
		t.Fatalf("expected fetch error for unreachable provider with no fallback models")
	}
	_, _ = m.openModelPicker(msg)
	// The picker must be open despite the fetch error because other tabs exist.
	if m.overlay != overlayModelPicker {
		t.Fatalf("picker did not open on unreachable default with other providers")
	}
	if m.modelLoadErr == "" {
		t.Errorf("modelLoadErr should contain the fetch error")
	}
	// Switch to the working provider tab and verify models load.
	_, cmd2 := m.handleOverlayKey(teaKey("right"))
	if cmd2 == nil {
		t.Fatal("right did not return a fetch command")
	}
	msg2, ok2 := cmd2().(modelsMsg)
	if !ok2 || msg2.err != nil {
		t.Fatalf("switch to beta failed: %T %v", cmd2(), msg2.err)
	}
	_, _ = m.openModelPicker(msg2)
	if m.providerIdx != 1 {
		t.Errorf("providerIdx = %d, want 1 after right", m.providerIdx)
	}
	if got := pickerListTitles(m); len(got) != 2 || !strings.HasPrefix(got[0], "b-1") || !strings.HasPrefix(got[1], "b-2") {
		t.Errorf("list = %v, want b-1/b-2", got)
	}
}

func TestProviderTabLine(t *testing.T) {
	srvA := modelsServer(t, "a-1")
	srvB := modelsServer(t, "b-1")
	m := newPickerTestModel([]config.Provider{
		{Name: "alpha", BaseURL: srvA.URL, Model: "a-1"},
		{Name: "beta", BaseURL: srvB.URL, Model: "b-1"},
	}, srvB.URL, "b-1")
	openPickerFor(t, &m)

	line := m.providerTabLine(80)
	if !strings.Contains(stripANSI(line), "[beta]") {
		t.Errorf("active tab not highlighted: %q", line)
	}
	if !strings.Contains(stripANSI(line), "alpha") {
		t.Errorf("inactive tab missing: %q", line)
	}
}

func TestModelPickerViewShowsTabsAndHint(t *testing.T) {
	srvA := modelsServer(t, "a-1")
	srvB := modelsServer(t, "b-1")
	m := newPickerTestModel([]config.Provider{
		{Name: "alpha", BaseURL: srvA.URL, Model: "a-1"},
		{Name: "beta", BaseURL: srvB.URL, Model: "b-1"},
	}, srvB.URL, "b-1")
	openPickerFor(t, &m)

	view := stripANSI(m.pickerView(80, 24).Content)
	if !strings.Contains(view, "[beta]") {
		t.Errorf("picker view missing provider tabs: %q", view)
	}
	if !strings.Contains(view, "←/→ provider") {
		t.Errorf("picker view missing tab hint: %q", view)
	}
	if !strings.Contains(view, "type to search") {
		t.Errorf("picker view missing search hint: %q", view)
	}
	if !strings.Contains(view, "type a model name to search") {
		t.Errorf("picker view missing search entry: %q", view)
	}
}

// The picker searches by model name as the user types: no mode-switch key, and
// the best-ranked match is the highlighted row, so enter applies it directly.
func TestModelPickerTypeAheadSearch(t *testing.T) {
	srv := modelsServer(t,
		"claude-3.5-sonnet",
		"claude-3.5-opus",
		"gpt-4o-2024-08-06",
		"gpt-4o-mini",
		"llama-3.3-70b-instruct",
	)
	m := newPickerTestModel([]config.Provider{
		{Name: "alpha", BaseURL: srv.URL, Model: "gpt-4o-mini"},
	}, srv.URL, "gpt-4o-mini")
	openPickerFor(t, &m)

	// Typing narrows the list live and keeps the query visible.
	for _, r := range "gpt" {
		_, _ = m.handleOverlayKey(teaKey(string(r)))
	}
	if got := m.search.query(); got != "gpt" {
		t.Errorf("query = %q, want gpt", got)
	}
	if got := pickerListTitles(m); len(got) != 2 {
		t.Errorf("filtered list = %v, want the two gpt models", got)
	}
	if !strings.Contains(stripANSI(m.pickerView(80, 24).Content), "2 matches") {
		t.Errorf("view missing match count: %q", stripANSI(m.pickerView(80, 24).Content))
	}

	// Refining the query re-ranks; the shortest match leads, and enter applies
	// it without the user moving the cursor.
	for _, r := range "mini" {
		_, _ = m.handleOverlayKey(teaKey(string(r)))
	}
	_, _ = m.handleOverlayKey(teaKey("enter"))
	if m.rt.Model.Model != "gpt-4o-mini" {
		t.Errorf("applied model = %q, want gpt-4o-mini", m.rt.Model.Model)
	}
	if m.overlay != overlayNone {
		t.Errorf("overlay = %v, want none after apply", m.overlay)
	}
	// Closing resets the search, so the next open starts on the full list.
	if m.search.query() != "" {
		t.Errorf("query survived close: %q", m.search.query())
	}
}

// The picker frame carries a real cursor to the search row so the terminal
// shows where the query is edited, and the search row stays on one line.
func TestModelPickerSearchRowAndCursor(t *testing.T) {
	srv := modelsServer(t, "gpt-4o", "gpt-4o-mini")
	m := newPickerTestModel([]config.Provider{
		{Name: "alpha", BaseURL: srv.URL, Model: "gpt-4o"},
	}, srv.URL, "gpt-4o")
	openPickerFor(t, &m)

	// Before typing, the cursor sits at the start of the query field: after
	// the two-space indent and the magnifier.
	view := m.pickerView(80, 24)
	if view.Cursor == nil {
		t.Fatal("picker view has no cursor while searching is possible")
	}
	if got, want := view.Cursor.X, searchCursorOffset(); got != want {
		t.Errorf("cursor X = %d, want %d", got, want)
	}
	if view.Cursor.Y != 2 {
		t.Errorf("cursor Y = %d, want 2 (heading + tab row)", view.Cursor.Y)
	}

	for _, r := range "gpt-4o" {
		_, _ = m.handleOverlayKey(teaKey(string(r)))
	}
	view = m.pickerView(80, 24)
	if view.Cursor == nil || view.Cursor.X != searchCursorOffset()+6 {
		t.Errorf("cursor did not follow the query: %+v", view.Cursor)
	}
	line := stripANSI(view.Content)
	if !strings.HasPrefix(line, "Select model\n[alpha]\n  ⌕ gpt-4o  2 matches") {
		t.Errorf("search row rendered wrong: %q", line)
	}
}

// Model ids are matched loosely: separators are ignored and a subsequence is
// enough, so the user does not have to type the id exactly.
func TestModelPickerSearchIsLoose(t *testing.T) {
	items := []list.Item{
		pickerItem{title: "claude-3.5-sonnet", value: llm.ModelInfo{ID: "claude-3.5-sonnet"}},
		pickerItem{title: "claude-3.5-opus", value: llm.ModelInfo{ID: "claude-3.5-opus"}},
		pickerItem{title: "gpt-4o-mini", value: llm.ModelInfo{ID: "gpt-4o-mini"}},
	}
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"claude35sonnet", "claude-3.5-sonnet"}, // separators ignored
		{"sonnet", "claude-3.5-sonnet"},         // substring
		{"cso", "claude-3.5-sonnet"},            // subsequence
		{"SONNET", "claude-3.5-sonnet"},         // case-insensitive
		{"gpt-4o-mini", "gpt-4o-mini"},          // exact id
	} {
		got := rankModels(tc.query, items)
		if len(got) == 0 {
			t.Errorf("query %q matched nothing", tc.query)
			continue
		}
		if first := got[0].(pickerItem).title; first != tc.want {
			t.Errorf("query %q best match = %q, want %q", tc.query, first, tc.want)
		}
	}
}

// A query that matches nothing says so instead of showing an empty list, and
// esc first clears the query (back to the full list) before closing.
func TestModelPickerSearchNoMatchAndEsc(t *testing.T) {
	srv := modelsServer(t, "a-1", "b-1")
	m := newPickerTestModel([]config.Provider{
		{Name: "alpha", BaseURL: srv.URL, Model: "a-1"},
	}, srv.URL, "a-1")
	openPickerFor(t, &m)

	for _, r := range "zzz" {
		_, _ = m.handleOverlayKey(teaKey(string(r)))
	}
	if m.search.err == "" {
		t.Error("no-match message missing")
	}
	if !strings.Contains(stripANSI(m.pickerView(80, 24).Content), "no model matches") {
		t.Errorf("view missing no-match message: %q", stripANSI(m.pickerView(80, 24).Content))
	}
	// Enter with no match must not apply anything.
	_, _ = m.handleOverlayKey(teaKey("enter"))
	if m.rt.Model.Model != "a-1" {
		t.Errorf("model changed to %q on an empty search", m.rt.Model.Model)
	}

	// esc clears the query and keeps the picker open on the full list.
	_, _ = m.handleOverlayKey(teaKey("esc"))
	if m.overlay != overlayModelPicker {
		t.Fatalf("overlay = %v, want picker still open after clearing search", m.overlay)
	}
	if m.search.query() != "" {
		t.Errorf("query not cleared: %q", m.search.query())
	}
	if got := pickerListTitles(m); len(got) != 2 {
		t.Errorf("list after clearing search = %v, want both models", got)
	}
	_, _ = m.handleOverlayKey(teaKey("esc"))
	if m.overlay != overlayNone {
		t.Errorf("overlay = %v, want closed", m.overlay)
	}
}

// Switching provider tabs keeps the typed query and re-applies it to the new
// tab's models, so searching across providers does not start over.
func TestModelPickerSearchSurvivesProviderSwitch(t *testing.T) {
	srvA := modelsServer(t, "gpt-4o", "gpt-4o-mini")
	srvB := modelsServer(t, "claude-3.5-sonnet", "gpt-4.1")
	m := newPickerTestModel([]config.Provider{
		{Name: "alpha", BaseURL: srvA.URL, Model: "gpt-4o"},
		{Name: "beta", BaseURL: srvB.URL, Model: "claude-3.5-sonnet"},
	}, srvA.URL, "gpt-4o")
	openPickerFor(t, &m)

	for _, r := range "gpt" {
		_, _ = m.handleOverlayKey(teaKey(string(r)))
	}
	_, cmd := m.handleOverlayKey(teaKey("right"))
	msg, ok := cmd().(modelsMsg)
	if !ok || msg.err != nil {
		t.Fatalf("tab switch fetch: %T %v", cmd(), msg.err)
	}
	_, _ = m.openModelPicker(msg)
	if m.search.query() != "gpt" {
		t.Errorf("query lost on tab switch: %q", m.search.query())
	}
	if got := pickerListTitles(m); len(got) != 1 || !strings.HasPrefix(got[0], "gpt-4.1") {
		t.Errorf("list = %v, want only gpt-4.1 from the new tab", got)
	}
}

// h and l switch provider tabs only while nothing is typed: once the user
// searches, they are ordinary model-name characters.
func TestModelPickerHLAreTabsOnlyBeforeSearch(t *testing.T) {
	srvA := modelsServer(t, "gpt-4h", "gpt-4l")
	srvB := modelsServer(t, "other")
	m := newPickerTestModel([]config.Provider{
		{Name: "alpha", BaseURL: srvA.URL, Model: "gpt-4h"},
		{Name: "beta", BaseURL: srvB.URL, Model: "other"},
	}, srvA.URL, "gpt-4h")
	openPickerFor(t, &m)

	if _, cmd := m.handleOverlayKey(teaKey("l")); cmd == nil {
		t.Error("l did not switch provider tabs")
	} else {
		msg, _ := cmd().(modelsMsg)
		_, _ = m.openModelPicker(msg)
	}
	if m.providerIdx != 1 {
		t.Fatalf("providerIdx = %d, want 1 after l", m.providerIdx)
	}
	// Back to alpha and type an h: it must land in the query, not move tabs.
	_, cmd := m.handleOverlayKey(teaKey("left"))
	msg, _ := cmd().(modelsMsg)
	_, _ = m.openModelPicker(msg)
	_, _ = m.handleOverlayKey(teaKey("g"))
	_, _ = m.handleOverlayKey(teaKey("h"))
	if m.providerIdx != 0 {
		t.Errorf("providerIdx moved to %d while typing", m.providerIdx)
	}
	if m.search.query() != "gh" {
		t.Errorf("query = %q, want gh", m.search.query())
	}
}

// stripANSI removes ANSI escape sequences so tests can assert on plain text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
