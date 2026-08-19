package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/web"
	"github.com/acidsound/Motive/internal/workspace"
)

const MaxToolResultBytes = 64 << 10

var diagnosticPattern = regexp.MustCompile(`(?m)([^\s:][^\n:]*):(\d+):(\d+):\s*(.+)$`)

type Executor struct {
	WS *workspace.Workspace

	mu       sync.Mutex
	observed map[string]string
}

func Definitions() []model.Tool {
	obj := func(props map[string]model.ToolProperty, required ...string) model.Parameters {
		return model.Parameters{Type: "object", Properties: props, Required: required}
	}
	return []model.Tool{
		{Type: "function", Function: model.ToolFunction{Name: "read_file", Description: "Read a UTF-8 text file in the workspace.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}}, "path")}},
		{Type: "function", Function: model.ToolFunction{Name: "write_file", Description: "Create or replace a UTF-8 text file.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}, "content": {Type: "string"}}, "path", "content")}},
		{Type: "function", Function: model.ToolFunction{Name: "delete_file", Description: "Delete a file in the workspace.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}}, "path")}},
		{Type: "function", Function: model.ToolFunction{Name: "list_files", Description: "List files below a workspace directory.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}})}},
		{Type: "function", Function: model.ToolFunction{Name: "search_files", Description: "Search text in workspace files.", Parameters: obj(map[string]model.ToolProperty{"query": {Type: "string"}}, "query")}},
		{Type: "function", Function: model.ToolFunction{Name: "shell", Description: "Execute a shell command in the workspace.", Parameters: obj(map[string]model.ToolProperty{"command": {Type: "string"}}, "command")}},
		{Type: "function", Function: model.ToolFunction{Name: "web_search", Description: "Search the public web.", Parameters: obj(map[string]model.ToolProperty{"query": {Type: "string"}}, "query")}},
		{Type: "function", Function: model.ToolFunction{Name: "git_status", Description: "Show Git branch, revision and working-tree changes.", Parameters: obj(map[string]model.ToolProperty{})}},
		{Type: "function", Function: model.ToolFunction{Name: "git_diff", Description: "Show the current unstaged Git diff.", Parameters: obj(map[string]model.ToolProperty{})}},
	}
}

func (e *Executor) Run(ctx context.Context, name, raw string) (string, error) {
	var args map[string]any
	if strings.TrimSpace(raw) == "" {
		args = map[string]any{}
	} else if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", fmt.Errorf("invalid %s arguments: %w", name, err)
	}
	str := func(key string) string { v, _ := args[key].(string); return v }

	var (
		result string
		err    error
	)
	switch name {
	case "read_file":
		result, err = e.readFile(ctx, str("path"))
	case "write_file":
		if err = e.WS.WriteContext(ctx, str("path"), str("content")); err == nil {
			result = "written " + str("path")
		}
	case "delete_file":
		if err = e.WS.DeleteContext(ctx, str("path")); err == nil {
			result = "deleted " + str("path")
		}
	case "list_files":
		result, err = e.WS.ListContext(ctx, str("path"))
	case "search_files":
		result, err = e.WS.SearchContext(ctx, str("query"))
	case "shell":
		result, err = e.WS.ShellContext(ctx, str("command"))
	case "web_search":
		result, err = web.Search(str("query"))
	case "git_status":
		result, err = e.WS.GitStatusContext(ctx)
	case "git_diff":
		result, err = e.WS.GitDiffContext(ctx)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
	if err != nil {
		result = "ERROR: " + err.Error()
	}
	result = addDiagnostics(result)
	if err != nil {
		return Truncate(result), err
	}
	return Truncate(result), nil
}

func (e *Executor) readFile(ctx context.Context, path string) (string, error) {
	content, err := e.WS.ReadContext(ctx, path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(hash[:])
	lines := 0
	if content != "" {
		lines = strings.Count(content, "\n")
		if !strings.HasSuffix(content, "\n") {
			lines++
		}
	}

	e.mu.Lock()
	if e.observed == nil {
		e.observed = make(map[string]string)
	}
	previous, seen := e.observed[path]
	e.observed[path] = digest
	e.mu.Unlock()

	status := "first_read"
	if seen && previous == digest {
		status = "unchanged"
	} else if seen {
		status = "changed"
	}
	return fmt.Sprintf("[observation]\npath=%s\nbytes=%d\nlines=%d\nsha256=%s\nstatus=%s\n\n%s", path, len(content), lines, digest, status, content), nil
}

func addDiagnostics(result string) string {
	matches := diagnosticPattern.FindAllStringSubmatch(result, -1)
	if len(matches) == 0 {
		return result
	}
	var b strings.Builder
	b.WriteString(result)
	b.WriteString("\n\n[diagnostics]")
	for _, match := range matches {
		if len(match) != 5 {
			continue
		}
		b.WriteString("\nfile=")
		b.WriteString(strings.TrimSpace(match[1]))
		b.WriteString(" line=")
		b.WriteString(match[2])
		b.WriteString(" column=")
		b.WriteString(match[3])
		b.WriteString(" message=")
		b.WriteString(strings.TrimSpace(match[4]))
	}
	return b.String()
}

// Truncate bounds s to MaxToolResultBytes without splitting a UTF-8 rune.
func Truncate(s string) string {
	if len(s) <= MaxToolResultBytes {
		return s
	}
	cut := MaxToolResultBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n\n[tool result truncated: %d bytes total, showing first %d bytes]", len(s), cut)
}
