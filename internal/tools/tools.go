package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"

	observation "github.com/acidsound/Motive/internal/context"
	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/web"
	"github.com/acidsound/Motive/internal/workspace"
)

const MaxToolResultBytes = 64 << 10

type Executor struct {
	WS *workspace.Workspace
	observations *observation.Tracker
}

func (e *Executor) tracker() *observation.Tracker {
	if e.observations == nil { e.observations = observation.NewTracker() }
	return e.observations
}

func Definitions() []model.Tool {
	obj := func(props map[string]model.ToolProperty, required ...string) model.Parameters {
		return model.Parameters{Type: "object", Properties: props, Required: required}
	}
	return []model.Tool{
		{Type: "function", Function: model.ToolFunction{Name: "read_file", Description: "Read a UTF-8 text file in the workspace. The result includes compact file metadata so repeated reads can be avoided.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}}, "path")}},
		{Type: "function", Function: model.ToolFunction{Name: "write_file", Description: "Create or replace a UTF-8 text file.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}, "content": {Type: "string"}}, "path", "content")}},
		{Type: "function", Function: model.ToolFunction{Name: "delete_file", Description: "Delete a file in the workspace.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}}, "path")}},
		{Type: "function", Function: model.ToolFunction{Name: "list_files", Description: "List files below a workspace directory.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}})}},
		{Type: "function", Function: model.ToolFunction{Name: "search_files", Description: "Search text in workspace files.", Parameters: obj(map[string]model.ToolProperty{"query": {Type: "string"}}, "query")}},
		{Type: "function", Function: model.ToolFunction{Name: "symbol_lookup", Description: "Use gopls for precise Go symbol navigation. Provide path, line, column, and kind=definition or references.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}, "line": {Type: "integer"}, "column": {Type: "integer"}, "kind": {Type: "string"}}, "path", "line", "column", "kind")}},
		{Type: "function", Function: model.ToolFunction{Name: "shell", Description: "Execute a shell command in the workspace.", Parameters: obj(map[string]model.ToolProperty{"command": {Type: "string"}}, "command")}},
		{Type: "function", Function: model.ToolFunction{Name: "web_search", Description: "Search the public web.", Parameters: obj(map[string]model.ToolProperty{"query": {Type: "string"}}, "query")}},
		{Type: "function", Function: model.ToolFunction{Name: "git_status", Description: "Show Git branch, revision and working-tree changes.", Parameters: obj(map[string]model.ToolProperty{})}},
		{Type: "function", Function: model.ToolFunction{Name: "git_diff", Description: "Show the current unstaged Git diff.", Parameters: obj(map[string]model.ToolProperty{})}},
	}
}

func (e *Executor) Run(ctx context.Context, name, raw string) (string, error) {
	var args map[string]any
	if strings.TrimSpace(raw) == "" { args = map[string]any{} } else if err := json.Unmarshal([]byte(raw), &args); err != nil { return "", fmt.Errorf("invalid %s arguments: %w", name, err) }
	str := func(key string) string { v, _ := args[key].(string); return v }
	intArg := func(key string) int { v, _ := args[key].(float64); return int(v) }
	var result string
	var err error
	switch name {
	case "read_file":
		path := str("path")
		result, err = e.WS.ReadContext(ctx, path)
		if err == nil { result += "\n\n" + e.tracker().Observe(path, []byte(result)).String() }
	case "write_file":
		if err = e.WS.WriteContext(ctx, str("path"), str("content")); err == nil { result = "written " + str("path") }
	case "delete_file":
		if err = e.WS.DeleteContext(ctx, str("path")); err == nil { result = "deleted " + str("path") }
	case "list_files":
		result, err = e.WS.ListContext(ctx, str("path"))
	case "search_files":
		result, err = e.WS.SearchContext(ctx, str("query"))
	case "symbol_lookup":
		result, err = e.symbolLookup(ctx, str("path"), intArg("line"), intArg("column"), str("kind"))
	case "shell":
		result, err = e.WS.ShellContext(ctx, str("command"))
		result = appendDiagnostics(result)
	case "web_search":
		result, err = web.Search(str("query"))
	case "git_status":
		result, err = e.WS.GitStatusContext(ctx)
	case "git_diff":
		result, err = e.WS.GitDiffContext(ctx)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
	if err != nil { return result, err }
	return Truncate(result), nil
}

func (e *Executor) symbolLookup(ctx context.Context, path string, line, column int, kind string) (string, error) {
	if line < 1 || column < 1 { return "", fmt.Errorf("line and column must be >= 1") }
	if kind != "definition" && kind != "references" { return "", fmt.Errorf("kind must be definition or references") }
	if _, err := exec.LookPath("gopls"); err != nil { return "", fmt.Errorf("gopls is not installed") }
	return e.WS.ShellContext(ctx, fmt.Sprintf("gopls %s %q", kind, fmt.Sprintf("%s:%d:%d", path, line, column)))
}

func appendDiagnostics(result string) string {
	lines := strings.Split(result, "\n")
	var diagnostics []string
	for _, line := range lines { if looksLikeDiagnostic(strings.TrimSpace(line)) { diagnostics = append(diagnostics, strings.TrimSpace(line)) } }
	if len(diagnostics) == 0 { return result }
	return result + "\n\n[diagnostics]\n" + strings.Join(diagnostics, "\n")
}

func looksLikeDiagnostic(line string) bool {
	parts := strings.Split(line, ":")
	if len(parts) < 3 { return false }
	for _, p := range parts[len(parts)-2:] {
		p = strings.TrimSpace(strings.TrimSuffix(p, ")"))
		if p == "" { return false }
		for _, r := range p { if r < '0' || r > '9' { return false } }
	}
	return true
}

func Truncate(s string) string {
	if len(s) <= MaxToolResultBytes { return s }
	cut := MaxToolResultBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) { cut-- }
	return s[:cut] + fmt.Sprintf("\n\n[tool result truncated: %d bytes total, showing first %d bytes]", len(s), cut)
}
