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

// motiveGuide is the model-visible self-reference content returned by the
// motive tool. It is deliberately short: enough for the model to recall how to
// operate and how to recover from an interrupted run without re-reading large
// documentation.
const motiveGuide = `Motive is a model-centric software execution runtime. You are the model driving it.

Operating principles:
- Work directly on the user's workspace; do not merely describe code.
- Each request is an independent execution: assume no unseen chat history.
- Inspect the workspace when needed; prefer the smallest relevant context.
- Use tools decisively: read/write/search files, run shell, search the web, and
  inspect Git state.
- When you modify files, verify the resulting state before claiming success.
- Never claim a commit, push, test, or build unless tool output confirms it.

Editing files:
- Use edit_file for targeted, surgical edits. old_string is matched literally
  (no regex, no sed/perl) so it is portable to any machine. It must appear
  exactly once unless you pass replace_all; if it matches multiple times the
  tool refuses so you can give more context. For full rewrites use write_file.

Recovering from interrupted work:
- Your current session id is included in your context block.
- If a previous run in this session was interrupted or failed before finishing,
  call session_log to read the latest entries of this session's transcript
  (a .jsonl file) and see where the last run left off, then continue from there
  rather than starting over.

EPIC / decomposition principles (confirmed; canonical detail in
docs/decomposition.md):
- Decomposition is model behavior expressed as data (motive.tasks/plan.md +
  brief.md), never a runtime planner or sub-agent.
- A large task does not fit one bounded execution. Split it into independent
  sub-executions, each in a fresh context with its own budget; write a brief.md,
  delegate via the shell tool, read the boundary result, then write the next
  brief. Keep each unit small.
- Workspace + Git are the only shared state between executions, not chat history.
- Runtime never judges task decomposition or completion. It records only
  mechanical boundary facts (status, base->result rev delta, budget usage);
  re-judge exit criteria yourself from brief.md + the Git delta in a fresh
  context.
- Decomposition is fallible. A wrong split surfaces as a boundary event
  (budget-exceeded / error / uncomposable diff) and is repaired by rewriting
  plan.md. Treat plan.md as a hypothesis to rewrite, never a contract.
- Plan incrementally: planning must itself obey the bounded-execution
  discipline. Do not try to write the whole plan.md in one context.`

// motiveGuidePreview is used in the tool definition description.
const motiveGuidePreview = "Reference Motive's own operating guidance, including how to recover from an interrupted run."

type Executor struct {
	WS *workspace.Workspace

	// SessionID is the active session whose transcript session_log reads. It is
	// set by the runtime before each execution.
	SessionID string
	// SessionLog, when non-nil, returns the last `lines` entries of the given
	// session's .jsonl transcript. It is injected by the caller so the tools
	// package does not need to depend on the session store directly.
	SessionLog func(sessionID string, lines int) (string, error)

	mu       sync.Mutex
	observed map[string]string
}

func Definitions() []model.Tool {
	obj := func(props map[string]model.ToolProperty, required ...string) model.Parameters {
		return model.Parameters{Type: "object", Properties: props, Required: required}
	}
	boolProp := func() model.ToolProperty { return model.ToolProperty{Type: "boolean"} }
	return []model.Tool{
		{Type: "function", Function: model.ToolFunction{Name: "read_file", Description: "Read a UTF-8 text file in the workspace.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}}, "path")}},
		{Type: "function", Function: model.ToolFunction{Name: "write_file", Description: "Create or replace a UTF-8 text file.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}, "content": {Type: "string"}}, "path", "content")}},
		{Type: "function", Function: model.ToolFunction{Name: "edit_file", Description: "Surgically replace a unique block of text in a file. old_string is matched literally (never a regex), so it needs no sed/perl and works on any platform. It must appear exactly once unless replace_all is true; otherwise the edit is refused as ambiguous.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}, "old_string": {Type: "string"}, "new_string": {Type: "string"}, "replace_all": boolProp()}, "path", "old_string", "new_string")}},
		{Type: "function", Function: model.ToolFunction{Name: "delete_file", Description: "Delete a file in the workspace.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}}, "path")}},
		{Type: "function", Function: model.ToolFunction{Name: "list_files", Description: "List files below a workspace directory.", Parameters: obj(map[string]model.ToolProperty{"path": {Type: "string"}})}},
		{Type: "function", Function: model.ToolFunction{Name: "glob", Description: "List workspace-relative paths matching a glob pattern. Supports * and ? per segment plus ** for any number of directories (e.g. **/*.go). Directories end with '/'.", Parameters: obj(map[string]model.ToolProperty{"pattern": {Type: "string"}}, "pattern")}},
		{Type: "function", Function: model.ToolFunction{Name: "search_files", Description: "Search text in workspace files.", Parameters: obj(map[string]model.ToolProperty{"query": {Type: "string"}}, "query")}},
		{Type: "function", Function: model.ToolFunction{Name: "shell", Description: "Execute a shell command in the workspace.", Parameters: obj(map[string]model.ToolProperty{"command": {Type: "string"}}, "command")}},
		{Type: "function", Function: model.ToolFunction{Name: "web_search", Description: "Search the public web.", Parameters: obj(map[string]model.ToolProperty{"query": {Type: "string"}}, "query")}},
		{Type: "function", Function: model.ToolFunction{Name: "web_fetch", Description: "Fetch a single http(s) URL and return its text content. Normalises HTML, plain text, and PDF to text. Does not run JavaScript, crawl links, or return binary data.", Parameters: obj(map[string]model.ToolProperty{"url": {Type: "string"}}, "url")}},
		{Type: "function", Function: model.ToolFunction{Name: "git_status", Description: "Show Git branch, revision and working-tree changes.", Parameters: obj(map[string]model.ToolProperty{})}},
		{Type: "function", Function: model.ToolFunction{Name: "git_diff", Description: "Show the current unstaged Git diff.", Parameters: obj(map[string]model.ToolProperty{})}},
		{Type: "function", Function: model.ToolFunction{Name: "git_log", Description: "Show the last n commit summaries, newest first (bounded to 1..50).", Parameters: obj(map[string]model.ToolProperty{"n": {Type: "integer", Description: "Number of commits to show (default 10, clamped to 1..50)"}}, "n")}},
		{Type: "function", Function: model.ToolFunction{Name: "session_log", Description: "Read the last N entries of a session's .jsonl transcript (default: the current session) so you can recover from an interrupted run or inspect a unit boundary record.", Parameters: obj(map[string]model.ToolProperty{"lines": {Type: "integer", Description: "Number of trailing transcript entries to return (default 5, max 20)"}, "session_id": {Type: "string", Description: "Session id to read; defaults to the current session"}})}},
		{Type: "function", Function: model.ToolFunction{Name: "motive", Description: motiveGuidePreview, Parameters: obj(map[string]model.ToolProperty{})}},
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
	boolArg := func(key string) bool { v, _ := args[key].(bool); return v }
	intArg := func(key string, fallback int) int {
		if v, ok := args[key].(float64); ok {
			return int(v)
		}
		return fallback
	}

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
	case "edit_file":
		result, err = e.WS.EditContext(ctx, str("path"), str("old_string"), str("new_string"), boolArg("replace_all"))
	case "delete_file":
		if err = e.WS.DeleteContext(ctx, str("path")); err == nil {
			result = "deleted " + str("path")
		}
	case "list_files":
		result, err = e.WS.ListContext(ctx, str("path"))
	case "glob":
		result, err = e.WS.GlobContext(ctx, str("pattern"))
	case "search_files":
		result, err = e.WS.SearchContext(ctx, str("query"))
	case "shell":
		result, err = e.WS.ShellContext(ctx, str("command"))
	case "web_search":
		result, err = web.Search(str("query"))
	case "web_fetch":
		result, err = web.Fetch(str("url"))
	case "git_status":
		result, err = e.WS.GitStatusContext(ctx)
	case "git_diff":
		result, err = e.WS.GitDiffContext(ctx)
	case "git_log":
		result, err = e.WS.GitLogContext(ctx, intArg("n", 10))
	case "session_log":
		result, err = e.sessionLog(str("session_id"), intArg("lines", 5))
	case "motive":
		result = motiveGuide
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

// sessionLog returns the tail of the given session transcript, defaulting to
// the active session when id is empty. Passing an explicit id lets a parent
// execution read a sub-execution's transcript (unit boundary record) directly.
func (e *Executor) sessionLog(id string, lines int) (string, error) {
	if e.SessionLog == nil {
		return "", fmt.Errorf("no session log available (run without a session)")
	}
	if id == "" {
		id = e.SessionID
	}
	if id == "" {
		return "", fmt.Errorf("no active session id")
	}
	if lines <= 0 {
		lines = 5
	}
	if lines > 20 {
		lines = 20
	}
	return e.SessionLog(id, lines)
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
