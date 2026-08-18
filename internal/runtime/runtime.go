package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/tools"
	"github.com/acidsound/Motive/internal/workspace"
)

const systemPrompt = `You are Motive, a model-centric software execution runtime. Work directly on the user's workspace instead of merely describing code. Each user request is an independent execution request: do not assume unseen chat history. Inspect the workspace when needed, use tools decisively, make concrete file changes when asked, run tests or builds when useful, and report what actually happened. Prefer the smallest relevant context and avoid reading unrelated files. You may use shell, filesystem, web search, and git tools.`

type TraceEvent struct {
	Kind                 string
	Step                 int
	MaxSteps             int
	MessageCount         int
	ToolName             string
	ToolCalls            int
	ToolResultBytes      int
	RequestBytes         int
	EstimatedInputTokens int
	ResponseBytes        int
	Latency              time.Duration
	TotalElapsed         time.Duration
	ServerTimings        *model.ServerTimings
	BaseRevision         string
	ResultRevision       string
	Error                error
}

type Runtime struct {
	Model    *model.Client
	WS       *workspace.Workspace
	Exec     *tools.Executor
	MaxSteps int
	Trace    func(TraceEvent)
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
	if head := r.WS.GitHEAD(); head != "" {
		b.WriteString("\nGit HEAD: ")
		b.WriteString(head)
	}
	if status != "" {
		b.WriteString("\nGit status:\n")
		b.WriteString(status)
	}
	if len(files) > 6000 {
		files = files[:6000] + "\n..."
	}
	if files != "" {
		b.WriteString("\nWorkspace files:\n")
		b.WriteString(files)
	}
	return b.String()
}

func (r *Runtime) emit(event TraceEvent) {
	if r.Trace != nil {
		r.Trace(event)
	}
}

func (r *Runtime) Execute(ctx context.Context, request string) (string, error) {
	if strings.TrimSpace(request) == "" {
		return "", fmt.Errorf("request is empty")
	}
	started := time.Now()
	baseRevision := r.WS.GitHEAD()
	messages := []model.Message{{Role: "system", Content: r.ContextBlock()}, {Role: "user", Content: request}}
	toolDefs := tools.Definitions()
	trace := []string{}

	r.emit(TraceEvent{Kind: "start", MaxSteps: r.MaxSteps, MessageCount: len(messages), BaseRevision: baseRevision})

	for step := 0; step < r.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			r.emit(TraceEvent{Kind: "finish", Step: step, MaxSteps: r.MaxSteps, TotalElapsed: time.Since(started), BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
			return "", err
		}

		stepNumber := step + 1
		r.emit(TraceEvent{Kind: "model_start", Step: stepNumber, MaxSteps: r.MaxSteps, MessageCount: len(messages), TotalElapsed: time.Since(started)})
		msg, stats, err := r.Model.Chat(ctx, messages, toolDefs)
		if err != nil {
			r.emit(TraceEvent{Kind: "model_end", Step: stepNumber, MaxSteps: r.MaxSteps, MessageCount: len(messages), RequestBytes: stats.RequestBytes, EstimatedInputTokens: stats.EstimatedInputTokens, ResponseBytes: stats.ResponseBytes, Latency: stats.Latency, ServerTimings: stats.ServerTimings, TotalElapsed: time.Since(started), Error: err})
			r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: r.MaxSteps, TotalElapsed: time.Since(started), BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
			return "", err
		}
		r.emit(TraceEvent{Kind: "model_end", Step: stepNumber, MaxSteps: r.MaxSteps, MessageCount: len(messages), ToolCalls: len(msg.ToolCalls), RequestBytes: stats.RequestBytes, EstimatedInputTokens: stats.EstimatedInputTokens, ResponseBytes: stats.ResponseBytes, Latency: stats.Latency, ServerTimings: stats.ServerTimings, TotalElapsed: time.Since(started)})
		messages = append(messages, msg)
		if msg.Content != "" {
			trace = append(trace, msg.Content)
		}
		if len(msg.ToolCalls) == 0 {
			if msg.Content == "" {
				err := fmt.Errorf("model finished without a response")
				r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: r.MaxSteps, TotalElapsed: time.Since(started), BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
				return "", err
			}
			r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: r.MaxSteps, TotalElapsed: time.Since(started), BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD()})
			return strings.Join(trace, "\n\n"), nil
		}

		for _, call := range msg.ToolCalls {
			if call.Function.Name == "" {
				continue
			}
			toolStarted := time.Now()
			result, err := r.Exec.Run(ctx, call.Function.Name, call.Function.Arguments)
			resultBytes := len(result)
			if err != nil {
				result = "ERROR: " + err.Error()
			}
			r.emit(TraceEvent{Kind: "tool", Step: stepNumber, MaxSteps: r.MaxSteps, ToolName: call.Function.Name, ToolResultBytes: resultBytes, Latency: time.Since(toolStarted), TotalElapsed: time.Since(started)})
			messages = append(messages, model.Message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
	}

	err := fmt.Errorf("tool loop exceeded %d steps", r.MaxSteps)
	r.emit(TraceEvent{Kind: "finish", Step: r.MaxSteps, MaxSteps: r.MaxSteps, TotalElapsed: time.Since(started), BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
	return "", err
}
