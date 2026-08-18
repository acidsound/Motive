package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/web"
	"github.com/acidsound/Motive/internal/workspace"
)

const MaxToolResultBytes = 64 << 10

type Executor struct{ WS *workspace.Workspace }

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
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", fmt.Errorf("invalid %s arguments: %w", name, err)
	}
	str := func(key string) string { v, _ := args[key].(string); return v }

	var (
		result string
		err    error
	)
	switch name {
	case "read_file":
		result, err = e.WS.ReadContext(ctx, str("path"))
	case "write_file":
		if err = e.WS.WriteContext(ctx, str("path"), str("content")); err == nil { result = "written " + str("path") }
	case "delete_file":
		if err = e.WS.DeleteContext(ctx, str("path")); err == nil { result = "deleted " + str("path") }
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
		return result, err
	}
	return truncate(result), nil
}

func truncate(s string) string {
	if len(s) <= MaxToolResultBytes {
		return s
	}
	return s[:MaxToolResultBytes] + fmt.Sprintf("\n\n[tool result truncated: %d bytes total, showing first %d bytes]", len(s), MaxToolResultBytes)
}
