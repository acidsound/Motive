package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/tools"
	"github.com/acidsound/Motive/internal/workspace"
)

const systemPrompt = `You are Motive, a model-centric software execution runtime. Work directly on the user's workspace instead of merely describing code. Each user request is an independent execution request: do not assume unseen chat history. Inspect the workspace when needed, use tools decisively, make concrete file changes when asked, run tests or builds when useful, and report what actually happened. Prefer the smallest relevant context and avoid reading unrelated files. You may use shell, filesystem, web search, and git tools.`

type Runtime struct {
	Model *model.Client
	WS    *workspace.Workspace
	Exec  *tools.Executor
	MaxSteps int
}

func New(client *model.Client) *Runtime {
	root := os.Getenv("MOTIVE_WORKSPACE")
	ws := workspace.New(root)
	return &Runtime{Model: client, WS: ws, Exec: &tools.Executor{WS: ws}, MaxSteps: 32}
}

func (r *Runtime) ContextBlock() string {
	status, _ := r.WS.GitStatus()
	files, _ := r.WS.List(".")
	var b strings.Builder
	b.WriteString(systemPrompt)
	b.WriteString("\n\nWorkspace: ")
	b.WriteString(r.WS.Root)
	if head := r.WS.GitHEAD(); head != "" { b.WriteString("\nGit HEAD: "); b.WriteString(head) }
	if status != "" { b.WriteString("\nGit status:\n"); b.WriteString(status) }
	if len(files) > 6000 { files = files[:6000] + "\n..." }
	if files != "" { b.WriteString("\nWorkspace files:\n"); b.WriteString(files) }
	return b.String()
}

func (r *Runtime) Execute(ctx context.Context, request string) (string, error) {
	if strings.TrimSpace(request) == "" { return "", fmt.Errorf("request is empty") }
	messages := []model.Message{{Role: "system", Content: r.ContextBlock()}, {Role: "user", Content: request}}
	tools := tools.Definitions()
	trace := []string{}

	for step := 0; step < r.MaxSteps; step++ {
		msg, err := r.Model.Chat(ctx, messages, tools)
		if err != nil { return "", err }
		messages = append(messages, msg)
		if msg.Content != "" { trace = append(trace, msg.Content) }
		if len(msg.ToolCalls) == 0 {
			if msg.Content == "" { return "", fmt.Errorf("model finished without a response") }
			return strings.Join(trace, "\n\n"), nil
		}
		for _, call := range msg.ToolCalls {
			if call.Function.Name == "" { continue }
			trace = append(trace, fmt.Sprintf("[tool] %s", call.Function.Name))
			result, err := r.Exec.Run(call.Function.Name, call.Function.Arguments)
			if err != nil { result = "ERROR: " + err.Error() }
			messages = append(messages, model.Message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
	}
	return "", fmt.Errorf("tool loop exceeded %d steps", r.MaxSteps)
}

