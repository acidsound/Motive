package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	if !strings.Contains(view, "h/l provider") {
		t.Errorf("picker view missing tab hint: %q", view)
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
