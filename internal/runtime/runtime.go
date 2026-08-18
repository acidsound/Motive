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
	Error                error
}

type ExecutionBudget struct {
	MaxSteps     int
	MaxDuration  time.Duration
	MaxToolCalls int
}

type Observation struct {
	Step             int
	ToolFailures     int
	TotalToolCalls   int
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

	r.emit(TraceEvent{Kind: "start", MaxSteps: budget.MaxSteps, MessageCount: len(messages), ReasoningEffort: r.Model.GetReasoningEffort(), BaseRevision: baseRevision})
	obs := Observation{}
	effort := r.Model.GetReasoningEffort()

	for step := 0; step < budget.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			r.emit(TraceEvent{Kind: "finish", Step: step, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.TotalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
			return "", err
		}

		stepNumber := step + 1
		r.emit(TraceEvent{Kind: "model_start", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), TotalToolCalls: obs.TotalToolCalls, ReasoningEffort: effort, TotalElapsed: time.Since(started)})
		msg, stats, err := r.Model.ChatWithEffort(ctx, messages, toolDefs, effort)
		if err != nil {
			r.emit(TraceEvent{Kind: "model_end", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), TotalToolCalls: obs.TotalToolCalls, RequestBytes: stats.RequestBytes, EstimatedInputTokens: stats.EstimatedInputTokens, ResponseBytes: stats.ResponseBytes, Latency: stats.Latency, ServerTimings: stats.ServerTimings, ReasoningEffort: effort, TotalElapsed: time.Since(started), Error: err})
			r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.TotalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
			return "", err
		}
		obs.LastModelLatency = stats.Latency
		if stats.ServerTimings != nil {
			obs.LastPredictedMS = stats.ServerTimings.PredictedMS
			obs.LastPredictedN = stats.ServerTimings.PredictedN
		}
		r.emit(TraceEvent{Kind: "model_end", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), ToolCalls: len(msg.ToolCalls), TotalToolCalls: obs.TotalToolCalls, RequestBytes: stats.RequestBytes, EstimatedInputTokens: stats.EstimatedInputTokens, ResponseBytes: stats.ResponseBytes, Latency: stats.Latency, ServerTimings: stats.ServerTimings, ReasoningEffort: effort, TotalElapsed: time.Since(started)})
		messages = append(messages, msg)
		if msg.Content != "" {
			trace = append(trace, msg.Content)
		}
		if len(msg.ToolCalls) == 0 {
			if msg.Content == "" {
				err := fmt.Errorf("model finished without a response")
				r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.TotalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
				return "", err
			}
			r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.TotalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD()})
			return strings.Join(trace, "\n\n"), nil
		}

		toolFailed := false
		for _, call := range msg.ToolCalls {
			if obs.TotalToolCalls >= budget.MaxToolCalls {
				err := fmt.Errorf("execution budget exceeded: %d tool calls", budget.MaxToolCalls)
				r.emit(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.TotalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
				return "", err
			}
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
			obs.TotalToolCalls++
			if strings.HasPrefix(result, "ERROR: ") {
				obs.ToolFailures++
			}
			r.emit(TraceEvent{Kind: "tool", Step: stepNumber, MaxSteps: budget.MaxSteps, ToolName: call.Function.Name, ToolCalls: 1, TotalToolCalls: obs.TotalToolCalls, ToolResultBytes: len(result), Latency: time.Since(toolStarted), TotalElapsed: time.Since(started), ReasoningEffort: effort})
			messages = append(messages, model.Message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
		obs.LastToolFailure = toolFailed

		// Make runtime state visible to the model without requiring it to infer
		// latency, failures, or remaining budget from tool output alone.
		messages = append(messages, model.Message{Role: "system", Content: obs.context(stepNumber, budget, started, effort, baseRevision, r.WS.GitHEAD())})

		// Observe the completed turn and adapt the next turn. Normal execution
		// stays cheap; recovery after a tool failure gets one xhigh turn.
		if toolFailed {
			effort = "xhigh"
		} else {
			effort = r.Model.GetReasoningEffort()
		}
	}

	err := fmt.Errorf("execution budget exceeded: %d steps", budget.MaxSteps)
	r.emit(TraceEvent{Kind: "finish", Step: budget.MaxSteps, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.TotalToolCalls, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err})
	return "", err
}

func (o Observation) context(step int, budget ExecutionBudget, started time.Time, effort, baseRevision, resultRevision string) string {
	remainingSteps := budget.MaxSteps - step
	remainingTools := budget.MaxToolCalls - o.TotalToolCalls
	if remainingSteps < 0 {
		remainingSteps = 0
	}
	if remainingTools < 0 {
		remainingTools = 0
	}
	return fmt.Sprintf("[motive self-observation]\nstep=%d/%d\nremaining_steps=%d\ntool_calls=%d/%d\ntool_failures=%d\nlast_tool_failed=%t\ncurrent_reasoning_effort=%s\nlast_model_latency=%s\nlast_predicted_tokens=%d\nlast_predicted_latency=%.0fms\nelapsed=%s\nremaining_time=%s\nbase_revision=%s\nresult_revision=%s",
		step, budget.MaxSteps, remainingSteps, o.TotalToolCalls, budget.MaxToolCalls, o.ToolFailures, o.LastToolFailure, effort, o.LastModelLatency.Round(time.Millisecond), o.LastPredictedN, o.LastPredictedMS, time.Since(started).Round(time.Millisecond), maxDuration(0, budget.MaxDuration-time.Since(started)).Round(time.Second), shortRevision(baseRevision), shortRevision(resultRevision))
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func shortRevision(revision string) string {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return "-"
	}
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}
