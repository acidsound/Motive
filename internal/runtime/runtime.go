package runtime

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	ReasoningEffort      string
	BaseRevision         string
	ResultRevision       string
	Error                error
}

type ExecutionBudget struct {
	MaxSteps    int
	MaxDuration time.Duration
}

type Observation struct {
	Step             int
	ToolFailures     int
	LastToolFailure  bool
	LastModelLatency time.Duration
	LastPredictedMS  float64
	LastPredictedN   int
}

type Runtime struct {
	Model    *model.Client
	WS       *workspace.Workspace
	Exec     *tools.Executor
	MaxSteps int
	Budget   ExecutionBudget
	Trace    func(TraceEvent)
}

func New(client *model.Client) *Runtime {
	root := os.Getenv("MOTIVE_WORKSPACE")
	ws := workspace.New(root)
	steps := envInt("MOTIVE_MAX_STEPS", 32)
	duration := time.Duration(envInt("MOTIVE_EXECUTION_MINUTES", 30)) * time.Minute
	return &Runtime{
		Model:    client,
		WS:       ws,
		Exec:     &tools.Executor{WS: ws},
		MaxSteps: steps,
		Budget:   ExecutionBudget{MaxSteps: steps, MaxDuration: duration},
	}
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func (r *Runtime) ContextBlock() string {
	status, statusErr := r.WS.GitStatus()
	files, _ := r.WS.List(".")
	var b strings.Builder
	b.WriteString(systemPrompt)
	b.WriteString("\n\nWorkspace: ")
	b.WriteString(r.WS.Root)
	if head := r.WS.GitHEAD(); head != "" {
		b.WriteString("\nGit HEAD: ")
		b.WriteString(head)
	}
	if statusErr == nil && status != "" {
		b.WriteString("\nGit status:\n")
		b.WriteString(status)
	}
	if len(files) > 6000 {
		files = truncateUTF8(files, 6000) + "\n..."
	}
	if files != "" {
		b.WriteString("\nWorkspace files:\n")
		b.WriteString(files)
	}
	return b.String()
}

func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
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
	budget := r.Budget
	if budget.MaxSteps <= 0 {
		budget.MaxSteps = r.MaxSteps
	}
	if budget.MaxDuration <= 0 {
		budget.MaxDuration = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget.MaxDuration)
	defer cancel()

	r.emit(TraceEvent{Kind: "start", MaxSteps: budget.MaxSteps, MessageCount: len(messages), ReasoningEffort: r.Model.GetReasoningEffort(), BaseRevision: baseRevision})
	obs := Observation{}
	effort := r.Model.GetReasoningEffort()

	for step := 0; step < budget.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			r.emit(TraceEvent{Kind: "finish", Step: step, MaxSteps: budget.MaxSteps, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
			return "", err
		}

		stepNumber := step + 1
		r.emit(TraceEvent{Kind: "model_start", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), ReasoningEffort: effort, TotalElapsed: time.Since(started)})
		msg, stats, err := r.Model.ChatWithEffort(ctx, messages, toolDefs, effort)
		if err != nil {
			r.emit(TraceEvent{Kind: "model_end", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), RequestBytes: stats.RequestBytes, EstimatedInputTokens: stats.EstimatedInputTokens, ResponseBytes: stats.ResponseBytes, Latency: stats.Latency, ServerTimings: stats.ServerTimings, ReasoningEffort: effort, TotalElapsed: time.Since(started), Error: err})
			r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
			return "", err
		}
		obs.LastModelLatency = stats.Latency
		if stats.ServerTimings != nil {
			obs.LastPredictedMS = stats.ServerTimings.PredictedMS
			obs.LastPredictedN = stats.ServerTimings.PredictedN
		}
		r.emit(TraceEvent{Kind: "model_end", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), ToolCalls: len(msg.ToolCalls), RequestBytes: stats.RequestBytes, EstimatedInputTokens: stats.EstimatedInputTokens, ResponseBytes: stats.ResponseBytes, Latency: stats.Latency, ServerTimings: stats.ServerTimings, ReasoningEffort: effort, TotalElapsed: time.Since(started)})
		messages = append(messages, msg)
		if msg.Content != "" {
			trace = append(trace, msg.Content)
		}
		if len(msg.ToolCalls) == 0 {
			if msg.Content == "" {
				err := fmt.Errorf("model finished without a response")
				r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
				return "", err
			}
			r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD()})
			return strings.Join(trace, "\n\n"), nil
		}

		toolFailed := false
		for _, call := range msg.ToolCalls {
			toolStarted := time.Now()
			var result string
			if call.Function.Name == "" {
				result = "ERROR: tool call has an empty name and was ignored"
				toolFailed = true
			} else {
				var err error
				result, err = r.Exec.Run(ctx, call.Function.Name, call.Function.Arguments)
				if err != nil {
					result = "ERROR: " + err.Error()
					toolFailed = true
				}
			}
			if strings.HasPrefix(result, "ERROR: ") {
				obs.ToolFailures++
			}
			r.emit(TraceEvent{Kind: "tool", Step: stepNumber, MaxSteps: budget.MaxSteps, ToolName: call.Function.Name, ToolResultBytes: len(result), Latency: time.Since(toolStarted), TotalElapsed: time.Since(started), ReasoningEffort: effort})
			messages = append(messages, model.Message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
		obs.LastToolFailure = toolFailed

		// Observe the completed turn and adapt the next turn. Normal execution
		// stays cheap; recovery after a tool failure gets one xhigh turn.
		if toolFailed {
			effort = "xhigh"
		} else if effort == "xhigh" {
			effort = r.Model.GetReasoningEffort()
		} else {
			effort = r.Model.GetReasoningEffort()
		}
	}

	err := fmt.Errorf("execution budget exceeded: %d steps", budget.MaxSteps)
	r.emit(TraceEvent{Kind: "finish", Step: budget.MaxSteps, MaxSteps: budget.MaxSteps, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
	return "", err
}
