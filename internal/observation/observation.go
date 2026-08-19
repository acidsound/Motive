package observation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type File struct {
	Path       string
	Hash       string
	Bytes      int
	Lines      int
	Functions  int
	Exports    int
	ObservedAt int
}

type Diagnostic struct {
	Path    string
	Line    int
	Column  int
	Message string
}

type State struct {
	mu           sync.Mutex
	Files        map[string]File
	Diagnostics  []Diagnostic
	ToolFailures int
}

func New() *State { return &State{Files: map[string]File{}} }

func (s *State) ObserveFile(path, content string, step int) (File, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path = strings.TrimSpace(path)
	h := sha256.Sum256([]byte(content))
	f := File{
		Path:       path,
		Hash:       hex.EncodeToString(h[:]),
		Bytes:      len(content),
		Lines:      lineCount(content),
		Functions:  countMatches(goFuncRE, content),
		Exports:    countExportedDecls(content),
		ObservedAt: step,
	}
	old, exists := s.Files[path]
	unchanged := exists && old.Hash == f.Hash
	if unchanged {
		f.ObservedAt = old.ObservedAt
	}
	s.Files[path] = f
	return f, unchanged
}

func (s *State) ObserveDiagnostic(result string) []Diagnostic {
	diagnostics := parseDiagnostics(result)
	if len(diagnostics) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range diagnostics {
		found := false
		for _, old := range s.Diagnostics {
			if old == d {
				found = true
				break
			}
		}
		if !found {
			s.Diagnostics = append(s.Diagnostics, d)
		}
	}
	return diagnostics
}

func (s *State) ObserveToolFailure() {
	s.mu.Lock()
	s.ToolFailures++
	s.mu.Unlock()
}

func (s *State) Context() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	b.WriteString("[motive observation context]\n")
	if len(s.Files) > 0 {
		b.WriteString("Observed files:\n")
		for _, f := range s.Files {
			fmt.Fprintf(&b, "- %s hash=%s bytes=%d lines=%d functions=%d exports=%d step=%d\n", f.Path, f.Hash[:12], f.Bytes, f.Lines, f.Functions, f.Exports, f.ObservedAt)
		}
	}
	if len(s.Diagnostics) > 0 {
		b.WriteString("Known diagnostics:\n")
		for _, d := range s.Diagnostics {
			location := d.Path
			if d.Line > 0 {
				location += ":" + strconv.Itoa(d.Line)
				if d.Column > 0 {
					location += ":" + strconv.Itoa(d.Column)
				}
			}
			fmt.Fprintf(&b, "- %s %s\n", location, d.Message)
		}
	}
	if s.ToolFailures > 0 {
		fmt.Fprintf(&b, "Tool failures observed: %d\n", s.ToolFailures)
	}
	return b.String()
}

var goFuncRE = regexp.MustCompile(`(?m)^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
var diagnosticRE = regexp.MustCompile(`(?m)([^\s:]+\.go):(\d+)(?::(\d+))?:\s*(.+)`)

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return 1 + strings.Count(s, "\n")
}

func countMatches(re *regexp.Regexp, s string) int { return len(re.FindAllStringSubmatch(s, -1)) }

func countExportedDecls(s string) int {
	count := 0
	for _, m := range goFuncRE.FindAllStringSubmatch(s, -1) {
		if len(m) > 1 && len(m[1]) > 0 && m[1][0] >= 'A' && m[1][0] <= 'Z' {
			count++
		}
	}
	return count
}

func parseDiagnostics(result string) []Diagnostic {
	matches := diagnosticRE.FindAllStringSubmatch(result, -1)
	out := make([]Diagnostic, 0, len(matches))
	for _, m := range matches {
		line, _ := strconv.Atoi(m[2])
		column := 0
		if m[3] != "" {
			column, _ = strconv.Atoi(m[3])
		}
		out = append(out, Diagnostic{Path: m[1], Line: line, Column: column, Message: strings.TrimSpace(m[4])})
	}
	return out
}
