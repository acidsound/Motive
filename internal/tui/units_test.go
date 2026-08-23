package tui

import (
	"strings"
	"testing"

	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/session"
)

func TestUnitSessionIDDetection(t *testing.T) {
	head := "some output\n[motive] unit session: 20260823-191429-540000\n"
	id, ok := unitSessionID(head)
	if !ok || id != "20260823-191429-540000" {
		t.Fatalf("unitSessionID = %q,%v", id, ok)
	}
	if _, ok := unitSessionID("plain output only"); ok {
		t.Fatal("plain shell output must not be detected as a unit")
	}
	if _, ok := unitSessionID("[motive] unit session: with space"); ok {
		t.Fatal("malformed id must be rejected")
	}
}

func TestShellToolEventBecomesUnitChip(t *testing.T) {
	m := newTestModel()
	m.handleTrace(runtime.TraceEvent{Kind: "start"})
	m.handleTrace(runtime.TraceEvent{
		Kind:           "tool",
		ToolName:       "shell",
		ToolArgs:       `{"command":"motive run \"do it\""}`,
		ToolResultHead: "[motive] unit session: abc123\nexit=1",
	})
	last := m.messages[len(m.messages)-1]
	if len(last.units) != 1 || last.units[0].id != "abc123" {
		t.Fatalf("units = %+v", last.units)
	}
	lines := m.renderMessage(last, 100)
	found := false
	for _, l := range lines {
		if strings.Contains(l, "unit abc123") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unit chip not rendered: %v", lines)
	}
}

func TestUnitsOverlayListsBoundaries(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s.New()
	rec := runtime.UnitBoundary{Status: "budget-exceeded", Steps: 6, MaxSteps: 6, ToolCalls: 3,
		BaseRevision: "aaaa", ResultRevision: "bbbb", Text: "remaining: tests"}
	if err := s.Append(id, session.Entry{Role: "user", Content: "brief"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(id, session.Entry{Role: "unit", Content: rec.String()}); err != nil {
		t.Fatal(err)
	}

	m := newTestModel()
	m.sess = s
	m.openUnits()

	if len(m.units.units) != 1 {
		t.Fatalf("units = %d, want 1", len(m.units.units))
	}
	u := m.units.units[0]
	if u.Boundary.Status != "budget-exceeded" || u.SessionID != id {
		t.Fatalf("record = %+v", u)
	}
	body := m.units.view(80, 20)
	if !strings.Contains(body, "budget-exceeded") || !strings.Contains(body, "steps 6/6") {
		t.Fatalf("overlay body missing fields:\n%s", body)
	}
	if m.units.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 on open", m.units.cursor)
	}
}

func TestUnitsOverlayCursorNavigation(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		id, _ := s.New()
		ids = append(ids, id)
		b := runtime.UnitBoundary{Status: "completed"}
		if err := s.Append(id, session.Entry{Role: "unit", Content: b.String()}); err != nil {
			t.Fatal(err)
		}
	}
	m := newTestModel()
	m.sess = s
	m.openUnits()

	// Newest first: cursor starts at index 0, which is the newest session.
	mm, _ := m.handleUnitsKey("down")
	mm2, _ := mm.(*model).handleUnitsKey("down")
	m3 := mm2.(*model)
	if m3.units.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", m3.units.cursor)
	}
	// enter opens the row under the cursor.
	m4, _ := m3.handleUnitsKey("enter")
	m5 := m4.(*model)
	if m5.units.detail != 2 {
		t.Fatalf("detail = %d, want 2 (cursor row)", m5.units.detail)
	}
	if got := m5.units.units[m5.units.detail].SessionID; got != m3.units.units[2].SessionID {
		t.Fatalf("detail session = %s, want %s", got, m3.units.units[2].SessionID)
	}
	// esc backs out to the list with the cursor preserved.
	m6, _ := m5.handleUnitsKey("esc")
	m7 := m6.(*model)
	if m7.units.detail != -1 || m7.units.cursor != 2 || m7.overlay != overlayUnits {
		t.Fatalf("esc should return to list: detail=%d cursor=%d overlay=%v",
			m7.units.detail, m7.units.cursor, m7.overlay)
	}
}

func TestUnitsOverlayDetailShowsTranscript(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s.New()
	_ = s.Append(id, session.Entry{Role: "user", Content: "implement thing"})
	b := runtime.UnitBoundary{Status: "completed"}
	_ = s.Append(id, session.Entry{Role: "unit", Content: b.String()})

	m := newTestModel()
	m.sess = s
	m.height = 30
	m.openUnits()
	m2, _ := m.handleUnitsKey("enter")
	mm := m2.(*model)
	if mm.units.detail < 0 {
		t.Fatal("enter should open the detail view")
	}
	body := mm.units.view(80, 30)
	if !strings.Contains(body, "implement thing") || !strings.Contains(body, "completed") {
		t.Fatalf("detail body missing content:\n%s", body)
	}
	// esc returns to the list.
	m3, _ := mm.handleUnitsKey("esc")
	if m3.(*model).units.detail != -1 {
		t.Fatal("esc should return to the list")
	}
}

func TestCollapsedToolsSummaryEmpty(t *testing.T) {
	m := newModel(nil, nil, nil, false)
	if got := m.collapsedToolsSummary(nil); !strings.Contains(got, "0 tool calls") {
		t.Fatalf("unexpected summary: %q", got)
	}
}
