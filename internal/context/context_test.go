package context

import (
	"strings"
	"testing"
)

func TestObserveTracksUnchangedFile(t *testing.T) {
	tracker := NewTracker()
	first := tracker.Observe("main.go", []byte("package main\n\nfunc Main() {}\n"))
	second := tracker.Observe("main.go", []byte("package main\n\nfunc Main() {}\n"))
	if first.AlreadyRead { t.Fatal("first observation must not be marked already read") }
	if !second.AlreadyRead { t.Fatal("second identical observation must be marked already read") }
	if first.Hash != second.Hash { t.Fatal("hash changed for identical content") }
	if first.Functions != 1 || first.Exported != 1 { t.Fatalf("unexpected Go structure: functions=%d exports=%d", first.Functions, first.Exported) }
}

func TestObserveDetectsChangedFile(t *testing.T) {
	tracker := NewTracker()
	tracker.Observe("main.go", []byte("package main\nfunc main() {}\n"))
	changed := tracker.Observe("main.go", []byte("package main\nfunc main() {}\nfunc Extra() {}\n"))
	if changed.AlreadyRead { t.Fatal("changed content must not be marked already read") }
	if !strings.Contains(changed.String(), "functions=2") { t.Fatalf("missing structure summary: %s", changed.String()) }
}
