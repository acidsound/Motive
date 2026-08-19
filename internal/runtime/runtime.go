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

const systemPrompt = `You are Motive, a model-centric software execution runtime. Work directly on the user's workspace instead of merely describing code. Each user request is an independent execution request: do not assume unseen chat history. Inspect the workspace when needed, use tools decisively, make concrete file changes when asked, run tests or builds when useful, and report what actually happened. Prefer the smallest relevant context and avoid reading unrelated files. You may use shell, filesystem, web search, and git tools. When modifying the workspace, verify the resulting state before claiming success; never claim a commit, push, test, or build unless tool output confirms it.`

type TraceEvent struct {
	Kind                 string
	Step                 int
	MaxSteps             int
	MessageCount         int
	ToolName             string
	ToolCalls            int
	TotalToolCalls       int
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
	Text                 string
	Reasoning            string
	Error                error
}

type ExecutionBudget struct {
	MaxSteps     int
	MaxDuration  time.Duration
	MaxToolCalls int
}

type Runtime struct {
	Model    *model.Client
	WS       *workspace.Workspace
	Exec     *tools.Executor
	MaxSteps int
	Budget   ExecutionBudget
	Trace    func(TraceEvent)
	Stream   bool
}

const (
	defaultMaxSteps     = 32
	defaultMaxMinutes   = 30
	defaultMaxToolCalls = 128
	maxAllowedSteps     = 256
	maxAllowedMinutes   = 120
	maxAllowedToolCalls = 1024
)

func New(client *model.Client) *Runtime {
	root := os.Getenv("MOTIVE_WORKSPACE")
	ws := workspace.New(root)
	steps := boundedEnvInt("MOTIVE_MAX_STEPS", defaultMaxSteps, maxAllowedSteps)
	minutes := boundedEnvInt("MOTIVE_EXECUTION_MINUTES", defaultMaxMinutes, maxAllowedMinutes)
	toolCalls := boundedEnvInt("MOTIVE_MAX_TOOL_CALLS", defaultMaxToolCalls, maxAllowedToolCalls)
	return &Runtime{
		Model:    client,
		WS:       ws,
		Exec:     &tools.Executor{WS: ws},
		MaxSteps: steps,
		Budget: ExecutionBudget{
			MaxSteps:     steps,
			MaxDuration:  time.Duration(minutes) * time.Minute,
			MaxToolCalls: toolCalls,
		},
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

func boundedEnvInt(key string, fallback, maximum int) int {
	value := envInt(key, fallback)
	if value > maximum {
		return maximum
	}
	return value
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
	if budget.MaxSteps > maxAllowedSteps {
		budget.MaxSteps = maxAllowedSteps
	}
	if budget.MaxDuration <= 0 {
		budget.MaxDuration = defaultMaxMinutes * time.Minute
	}
	if budget.MaxDuration > maxAllowedMinutes*time.Minute {
		budget.MaxDuration = maxAllowedMinutes * time.Minute
	}
	if budget.MaxToolCalls <= 0 {
		budget.MaxToolCalls = defaultMaxToolCalls
	}
	if budget.MaxToolCalls > maxAllowedToolCalls {
		budget.MaxToolCalls = maxAllowedToolCalls
	}
	ctx, cancel := context.WithTimeout(ctx, budget.MaxDuration)
	defer cancel()

	defaultEffort := r.Model.GetReasoningEffort()
	effort := defaultEffort
	r.emit(TraceEvent{Kind: "start", MaxSteps: budget.MaxSteps, MessageCount: len(messages), ReasoningEffort: effort, BaseRevision: baseRevision})

	totalToolCalls := 0
	for step := 0; step < budget.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			r.emit(TraceEvent{Kind: "finish", Step: step, MaxSteps: budget.MaxSteps, TotalToolCalls: totalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
			return "", err
		}

		stepNumber := step + 1
		r.emit(TraceEvent{Kind: "model_start", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), TotalToolCalls: totalToolCalls, ReasoningEffort: effort, TotalElapsed: time.Since(started)})
		var msg model.Message
		var stats model.ChatStats
		var err error
		if r.Stream {
			msg, stats, err = r.Model.ChatStream(ctx, messages, toolDefs, effort, func(delta model.StreamDelta) {
				r.emit(TraceEvent{Kind: "delta", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), TotalToolCalls: totalToolCalls, ReasoningEffort: effort, TotalElapsed: time.Since(started), Text: delta.Content, Reasoning: delta.Reasoning})
			})
		} else {
			msg, stats, err = r.Model.ChatWithEffort(ctx, messages, toolDefs, effort)
		}
		if err != nil {
			r.emit(TraceEvent{Kind: "model_end", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), TotalToolCalls: totalToolCalls, RequestBytes: stats.RequestBytes, EstimatedInputTokens: stats.EstimatedInputTokens, ResponseBytes: stats.ResponseBytes, Latency: stats.Latency, ServerTimings: stats.ServerTimings, ReasoningEffort: effort, TotalElapsed: time.Since(started), Error: err})
			r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: totalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
			return "", err
		}
		r.emit(TraceEvent{Kind: "model_end", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), ToolCalls: len(msg.ToolCalls), TotalToolCalls: totalToolCalls, RequestBytes: stats.RequestBytes, EstimatedInputTokens: stats.EstimatedInputTokens, ResponseBytes: stats.ResponseBytes, Latency: stats.Latency, ServerTimings: stats.ServerTimings, ReasoningEffort: effort, TotalElapsed: time.Since(started)})
		if msg.Role == "" {
			msg.Role = "assistant"
		}
		messages = append(messages, msg)
		if msg.Content != "" {
			trace = append(trace, msg.Content)
		}
		if len(msg.ToolCalls) == 0 {
			if msg.Content == "" {
				err := fmt.Errorf("model finished without a response")
				r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: totalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
				return "", err
			}
			r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: totalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD()})
			return strings.Join(trace, "\n\n"), nil
		}

		toolFailed := false
		for _, call := range msg.ToolCalls {
			if totalToolCalls >= budget.MaxToolCalls {
				err := fmt.Errorf("execution budget exceeded: %d tool calls", budget.MaxToolCalls)
				r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: totalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
				return "", err
			}
			toolStarted := time.Now()
			var result string
			if call.Function.Name == "" {
				result = "ERROR: tool call has an empty name and was ignored"
				toolFailed = true
			} else {
				result, err = r.Exec.Run(ctx, call.Function.Name, call.Function.Arguments)
				if err != nil {
					result = "ERROR: " + err.Error()
					toolFailed = true
				}
			}
			totalToolCalls++
			r.emit(TraceEvent{Kind: "tool", Step: stepNumber, MaxSteps: budget.MaxSteps, ToolName: call.Function.Name, ToolCalls: 1, TotalToolCalls: totalToolCalls, ToolResultBytes: len(result), Latency: time.Since(toolStarted), TotalElapsed: time.Since(started), ReasoningEffort: effort})
			messages = append(messages, model.Message{Role: "tool", ToolCallID: call.ID, Content: result})
		}

		// Recovery is a runtime policy, not model self-observation: a failed
		// tool turn gets one higher-effort retry, then execution returns to the
		// user's configured default effort after the next successful turn.
		if toolFailed {
			effort = "xhigh"
		} else {
			effort = defaultEffort
		}
	}

	err := fmt.Errorf("execution budget exceeded: %d steps", budget.MaxSteps)
	r.emit(TraceEvent{Kind: "finish", Step: budget.MaxSteps, MaxSteps: budget.MaxSteps, TotalToolCalls: totalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
	return "", err
}
