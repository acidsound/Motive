package tools

import (
	"encoding/json"
	"fmt"

	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/web"
	"github.com/acidsound/Motive/internal/workspace"
)

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

func (e *Executor) Run(name, raw string) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", fmt.Errorf("invalid %s arguments: %w", name, err)
	}
	str := func(key string) string { v, _ := args[key].(string); return v }

	switch name {
	case "read_file":
		return e.WS.Read(str("path"))
	case "write_file":
		if err := e.WS.Write(str("path"), str("content")); err != nil { return "", err }
		return "written " + str("path"), nil
	case "delete_file":
		if err := e.WS.Delete(str("path")); err != nil { return "", err }
		return "deleted " + str("path"), nil
	case "list_files":
		return e.WS.List(str("path"))
	case "search_files":
		return e.WS.Search(str("query"))
	case "shell":
		return e.WS.Shell(str("command"))
	case "web_search":
		return web.Search(str("query"))
	case "git_status":
		return e.WS.GitStatus()
	case "git_diff":
		return e.WS.GitDiff()
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}
