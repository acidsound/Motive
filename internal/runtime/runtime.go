package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/acidsound/Motive/internal/config"
	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/tools"
	"github.com/acidsound/Motive/internal/workspace"
)

const systemPrompt = `You are Motive, a model-centric software execution runtime. Work directly on the user's workspace instead of merely describing code. Each user request is an independent execution request: do not assume unseen chat history. Inspect the workspace when needed, use tools decisively, make concrete file changes when asked, run tests or builds when useful, and report what actually happened. Prefer the smallest relevant context and avoid reading unrelated files. You may use shell, filesystem, web search, and git tools. When modifying the workspace, verify the resulting state before claiming success; never claim a commit, push, test, or build unless tool output confirms it. Each execution is budget-bounded: when you are near the step or tool budget, put a one-line statement of what remains and where to continue in your assistant message alongside your last tool call, so a budget cap does not lose your forward intent.`

type TraceEvent struct {
	Kind                 string
	Step                 int
	MaxSteps             int
	MessageCount         int
	ToolName             string
	ToolCalls            int
	TotalToolCalls       int
	MaxToolCalls         int
	ToolFailures         int
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
	ContextTokens        int
	PeakContextTokens    int
	MaxContextTokens     int
	ServerPromptN        int
	Text                 string
	Reasoning            string
	Error                error
}

type ExecutionBudget struct {
	MaxSteps     int
	MaxDuration  time.Duration
	MaxToolCalls int
}

// UnitBoundary is the mechanical, runtime-written record of one bounded
// execution (a unit): status, revision delta, and budget usage. It carries
// only facts the runtime can observe — task-level judgment (exit criteria,
// coherence with the plan) stays with the model. On failure, Text holds the
// best-effort partial assistant text, so forward intent survives the boundary.
type UnitBoundary struct {
	Status         string `json:"status"` // completed | budget-exceeded | error
	Steps          int    `json:"steps"`
	MaxSteps       int    `json:"max_steps"`
	ToolCalls      int    `json:"tool_calls"`
	MaxToolCalls   int    `json:"max_tool_calls"`
	ToolFailures   int    `json:"tool_failures"`
	BaseRevision   string `json:"base_revision,omitempty"`
	ResultRevision string `json:"result_revision,omitempty"`
	ElapsedMS      int64  `json:"elapsed_ms,omitempty"`
	Text           string `json:"text,omitempty"` // final response, or partial text on failure
	Error          string `json:"error,omitempty"`
}

// String renders the record as one compact JSON line for a boundary entry.
func (u UnitBoundary) String() string {
	b, err := json.Marshal(u)
	if err != nil {
		return fmt.Sprintf(`{"status":%q}`, u.Status)
	}
	return string(b)
}

type Observation struct {
	Step              int
	MaxSteps          int
	ToolCalls         int
	MaxToolCalls      int
	ToolFailures      int
	LastToolFailure   bool
	LastModelLatency  time.Duration
	LastPredictedMS   float64
	LastPredictedN    int
	ContextTokens     int
	PeakContextTokens int
	MaxContextTokens  int
	ContextOverflow   bool
	ServerPromptN     int
	Elapsed           time.Duration
	ReasoningEffort   string
	BaseRevision      string
	ResultRevision    string
}

// Format renders the observation as a compact, model-visible string.
// It is deliberately short to minimize context cost while exposing
// enough state for the model to recognize budget pressure, failures,
// and context growth.
func (o Observation) Format() string {
	var b strings.Builder
	b.WriteString("[execution-state]\n")
	fmt.Fprintf(&b, "step=%d/%d tools=%d/%d failures=%d", o.Step, o.MaxSteps, o.ToolCalls, o.MaxToolCalls, o.ToolFailures)
	if o.LastToolFailure {
		b.WriteString(" last=FAIL")
	}
	b.WriteString("\n")
	if o.MaxContextTokens > 0 {
		fmt.Fprintf(&b, "context=%d/%d peak=%d", o.ContextTokens, o.MaxContextTokens, o.PeakContextTokens)
		if o.ContextOverflow {
			b.WriteString(" OVERFLOW")
		}
	} else {
		fmt.Fprintf(&b, "context=%d peak=%d", o.ContextTokens, o.PeakContextTokens)
	}
	if o.ServerPromptN > 0 {
		fmt.Fprintf(&b, " server_prompt_n=%d", o.ServerPromptN)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "elapsed=%s effort=%s", o.Elapsed.Round(time.Second), o.ReasoningEffort)
	if o.ResultRevision != "" && o.ResultRevision != o.BaseRevision {
		fmt.Fprintf(&b, " rev=%s→%s", shortRev(o.BaseRevision), shortRev(o.ResultRevision))
	} else if o.BaseRevision != "" {
		fmt.Fprintf(&b, " rev=%s", shortRev(o.BaseRevision))
	}
	b.WriteString("\n")
	return b.String()
}

func shortRev(rev string) string {
	if len(rev) > 8 {
		return rev[:8]
	}
	return rev
}

// ContextAccounting measures model-context growth across an execution.
//
// Estimates apply the same bytes/4 heuristic the model client uses for
// EstimatedInputTokens to the message list alone; tool definitions are
// constant overhead, not accumulated context. Server-reported prompt tokens
// (ServerTimings.PromptN) are recorded separately when the model server
// supplies them. Motive does not assume a server context limit: MaxTokens is
// 0 (accounting only) unless the operator configures one.
type ContextAccounting struct {
	MaxTokens     int
	LastRequest   int
	ServerPromptN int
	PeakRequest   int
	Overflow      bool
}

// Record measures the current message list, updates the accounting, and
// returns the estimate for the given messages.
func (a *ContextAccounting) Record(messages []model.Message) int {
	est := estimateContextTokens(messages)
	a.LastRequest = est
	if est > a.PeakRequest {
		a.PeakRequest = est
	}
	a.Overflow = a.MaxTokens > 0 && est > a.MaxTokens
	return est
}

// estimateContextTokens approximates the token size of a message list using
// the same bytes/4 heuristic the model client applies to the serialized
// request body. It is an estimate, not a tokenizer count.
func estimateContextTokens(messages []model.Message) int {
	body, err := json.Marshal(messages)
	if err != nil {
		if len(messages) > 0 {
			return len(messages)
		}
		return 1
	}
	if est := len(body) / 4; est > 0 {
		return est
	}
	return 1
}

type Runtime struct {
	Model            *model.Client
	WS               *workspace.Workspace
	Exec             *tools.Executor
	MaxSteps         int
	MaxContextTokens int
	Budget           ExecutionBudget
	Trace            func(TraceEvent)
	Stream           bool
	// SessionID is the active session identifier, included in the model context
	// each turn so the model knows where its transcript is persisted. It is set
	// by the TUI before execution and is empty for one-shot CLI runs.
	SessionID string
	// SessionLog is injected so the session_log tool can read the tail of the
	// current session's .jsonl transcript. It lets a run that was interrupted by
	// a failure look at what happened last and continue instead of restarting.
	// When nil, the session_log tool reports that no log is available.
	SessionLog func(sessionID string, lines int) (string, error)
	// UnitBoundary is called once per execution with the mechanical boundary
	// record (status, revision delta, budget usage). The one-shot CLI wires it
	// so every unit run is persisted as durable telemetry; nil disables it
	// (TUI runs persist a full transcript already).
	UnitBoundary func(UnitBoundary)
	// Steer receives user messages that are injected into a running execution
	// at the next step boundary (after tool results, or instead of finishing).
	// Set by the TUI; nil disables steering (one-shot CLI runs).
	Steer chan string
}

// takeSteer returns a pending steer message without blocking, or "" when none
// is available (or steering is disabled).
func (r *Runtime) takeSteer() string {
	if r.Steer == nil {
		return ""
	}
	select {
	case s := <-r.Steer:
		return s
	default:
		return ""
	}
}

// New builds a runtime from the resolved configuration. The workspace root and
// the execution budget are read from cfg (already resolved: environment wins
// over the config file, then the built-in default, capped at the allowed max).
func New(client *model.Client, cfg *config.Config) *Runtime {
	ws := workspace.New(cfg.Workspace)
	return &Runtime{
		Model:            client,
		WS:               ws,
		Exec:             &tools.Executor{WS: ws},
		MaxSteps:         cfg.MaxSteps,
		MaxContextTokens: cfg.MaxContextTokens,
		Budget: ExecutionBudget{
			MaxSteps:     cfg.MaxSteps,
			MaxDuration:  time.Duration(cfg.ExecutionMinutes) * time.Minute,
			MaxToolCalls: cfg.MaxToolCalls,
		},
	}
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
	if r.SessionID != "" {
		b.WriteString("\nSession: ")
		b.WriteString(r.SessionID)
		b.WriteString("\nIf a previous run in this session was interrupted, call session_log to read the latest transcript entries and continue where it left off. Consult the motive tool for guidance on how to operate.")
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

// finish terminates an execution: it emits the finish trace event and, when a
// UnitBoundary sink is wired, records the mechanical boundary facts. text is
// the final assistant text on success or the best-effort partial text on
// failure. Every termination path (clean, budget, model error, cancel) must
// return through this helper so no boundary is ever skipped.
func (r *Runtime) finish(event TraceEvent, text string, err error) (string, error) {
	r.emit(event)
	if r.UnitBoundary != nil {
		status := "completed"
		if err != nil {
			status = "error"
			if strings.Contains(err.Error(), "budget exceeded") {
				status = "budget-exceeded"
			}
		}
		rec := UnitBoundary{
			Status:         status,
			Steps:          event.Step,
			MaxSteps:       event.MaxSteps,
			ToolCalls:      event.TotalToolCalls,
			MaxToolCalls:   event.MaxToolCalls,
			ToolFailures:   event.ToolFailures,
			BaseRevision:   event.BaseRevision,
			ResultRevision: event.ResultRevision,
			ElapsedMS:      event.TotalElapsed.Milliseconds(),
			Text:           text,
		}
		if err != nil {
			rec.Error = err.Error()
		}
		r.UnitBoundary(rec)
	}
	return text, err
}

// Execute runs a fresh execution and returns the final assistant text. On any
// error path the returned text is best-effort partial content: the assistant
// messages emitted before termination, so forward intent (what the unit did and
// what it was about to do) survives budget caps and model failures. If the run
// dies before emitting anything, no in-memory state is carried across calls;
// the caller can re-run and the model recovers by reading the session transcript
// through the session_log tool.
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
	if budget.MaxSteps > config.MaxAllowedSteps {
		budget.MaxSteps = config.MaxAllowedSteps
	}
	if budget.MaxDuration <= 0 {
		budget.MaxDuration = config.DefaultMaxMinutes * time.Minute
	}
	if budget.MaxDuration > config.MaxAllowedMinutes*time.Minute {
		budget.MaxDuration = config.MaxAllowedMinutes * time.Minute
	}
	if budget.MaxToolCalls <= 0 {
		budget.MaxToolCalls = config.DefaultMaxToolCalls
	}
	if budget.MaxToolCalls > config.MaxAllowedToolCalls {
		budget.MaxToolCalls = config.MaxAllowedToolCalls
	}
	ctx, cancel := context.WithTimeout(ctx, budget.MaxDuration)
	defer cancel()

	// Expose the active session to the tools so session_log can read the
	// persisted transcript of the current session.
	if r.Exec != nil {
		r.Exec.SessionID = r.SessionID
		r.Exec.SessionLog = r.SessionLog
	}

	ctxAcc := ContextAccounting{MaxTokens: r.MaxContextTokens}
	ctxTokens := ctxAcc.Record(messages)
	r.emit(TraceEvent{Kind: "start", MaxSteps: budget.MaxSteps, MessageCount: len(messages), ContextTokens: ctxTokens, PeakContextTokens: ctxAcc.PeakRequest, MaxContextTokens: r.MaxContextTokens, ReasoningEffort: r.Model.GetReasoningEffort(), BaseRevision: baseRevision})
	obs := Observation{}
	effort := r.Model.GetReasoningEffort()

	for step := 0; step < budget.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return r.finish(TraceEvent{Kind: "finish", Step: step + 1, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.ToolCalls, MaxToolCalls: budget.MaxToolCalls, ToolFailures: obs.ToolFailures, ContextTokens: ctxAcc.LastRequest, PeakContextTokens: ctxAcc.PeakRequest, MaxContextTokens: r.MaxContextTokens, ServerPromptN: ctxAcc.ServerPromptN, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err}, strings.Join(trace, "\n\n"), err)
		}

		stepNumber := step + 1
		ctxTokens = ctxAcc.Record(messages)
		r.emit(TraceEvent{Kind: "model_start", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), TotalToolCalls: obs.ToolCalls, ContextTokens: ctxTokens, PeakContextTokens: ctxAcc.PeakRequest, MaxContextTokens: r.MaxContextTokens, ReasoningEffort: effort, TotalElapsed: time.Since(started)})
		var msg model.Message
		var stats model.ChatStats
		var err error
		if r.Stream {
			msg, stats, err = r.Model.ChatStream(ctx, messages, toolDefs, effort, func(delta model.StreamDelta) {
				r.emit(TraceEvent{Kind: "delta", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), TotalToolCalls: obs.ToolCalls, ReasoningEffort: effort, TotalElapsed: time.Since(started), Text: delta.Content, Reasoning: delta.Reasoning})
			})
		} else {
			msg, stats, err = r.Model.ChatWithEffort(ctx, messages, toolDefs, effort)
		}
		if err != nil {
			r.emit(TraceEvent{Kind: "model_end", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), TotalToolCalls: obs.ToolCalls, RequestBytes: stats.RequestBytes, EstimatedInputTokens: stats.EstimatedInputTokens, ResponseBytes: stats.ResponseBytes, Latency: stats.Latency, ServerTimings: stats.ServerTimings, ContextTokens: ctxTokens, PeakContextTokens: ctxAcc.PeakRequest, MaxContextTokens: r.MaxContextTokens, ServerPromptN: ctxAcc.ServerPromptN, ReasoningEffort: effort, TotalElapsed: time.Since(started), Error: err})
			return r.finish(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.ToolCalls, MaxToolCalls: budget.MaxToolCalls, ToolFailures: obs.ToolFailures, ContextTokens: ctxAcc.LastRequest, PeakContextTokens: ctxAcc.PeakRequest, MaxContextTokens: r.MaxContextTokens, ServerPromptN: ctxAcc.ServerPromptN, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err}, strings.Join(trace, "\n\n"), err)
		}
		obs.LastModelLatency = stats.Latency
		if stats.ServerTimings != nil {
			obs.LastPredictedMS = stats.ServerTimings.PredictedMS
			obs.LastPredictedN = stats.ServerTimings.PredictedN
			ctxAcc.ServerPromptN = stats.ServerTimings.PromptN
		}
		r.emit(TraceEvent{Kind: "model_end", Step: stepNumber, MaxSteps: budget.MaxSteps, MessageCount: len(messages), ToolCalls: len(msg.ToolCalls), TotalToolCalls: obs.ToolCalls, RequestBytes: stats.RequestBytes, EstimatedInputTokens: stats.EstimatedInputTokens, ResponseBytes: stats.ResponseBytes, Latency: stats.Latency, ServerTimings: stats.ServerTimings, ContextTokens: ctxTokens, PeakContextTokens: ctxAcc.PeakRequest, MaxContextTokens: r.MaxContextTokens, ServerPromptN: ctxAcc.ServerPromptN, ReasoningEffort: effort, TotalElapsed: time.Since(started)})
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
				return r.finish(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.ToolCalls, MaxToolCalls: budget.MaxToolCalls, ToolFailures: obs.ToolFailures, ContextTokens: ctxAcc.LastRequest, PeakContextTokens: ctxAcc.PeakRequest, MaxContextTokens: r.MaxContextTokens, ServerPromptN: ctxAcc.ServerPromptN, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err}, strings.Join(trace, "\n\n"), err)
			}
			// The user steered the run before it finished: append the steer as
			// a user message and continue instead of returning.
			if steer := r.takeSteer(); steer != "" {
				messages = append(messages, model.Message{Role: "user", Content: steer})
				continue
			}
			return r.finish(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.ToolCalls, MaxToolCalls: budget.MaxToolCalls, ToolFailures: obs.ToolFailures, ContextTokens: ctxAcc.LastRequest, PeakContextTokens: ctxAcc.PeakRequest, MaxContextTokens: r.MaxContextTokens, ServerPromptN: ctxAcc.ServerPromptN, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD()}, strings.Join(trace, "\n\n"), nil)
		}

		toolFailed := false
		for _, call := range msg.ToolCalls {
			if obs.ToolCalls >= budget.MaxToolCalls {
				err := fmt.Errorf("execution budget exceeded: %d tool calls", budget.MaxToolCalls)
				return r.finish(TraceEvent{Kind: "finish", Step: stepNumber, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.ToolCalls, MaxToolCalls: budget.MaxToolCalls, ToolFailures: obs.ToolFailures, ContextTokens: ctxAcc.LastRequest, PeakContextTokens: ctxAcc.PeakRequest, MaxContextTokens: r.MaxContextTokens, ServerPromptN: ctxAcc.ServerPromptN, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err}, strings.Join(trace, "\n\n"), err)
			}
			toolStarted := time.Now()
			var result string
			if call.Function.Name == "" {
				result = "ERROR: tool call has an empty name and was ignored"
				toolFailed = true
			} else {
				var toolErr error
				result, toolErr = r.Exec.Run(ctx, call.Function.Name, call.Function.Arguments)
				if toolErr != nil {
					if result == "" {
						result = "ERROR: " + toolErr.Error()
					}
					toolFailed = true
				}
			}
			obs.ToolCalls++
			if strings.HasPrefix(result, "ERROR: ") {
				obs.ToolFailures++
			}
			r.emit(TraceEvent{Kind: "tool", Step: stepNumber, MaxSteps: budget.MaxSteps, ToolName: call.Function.Name, ToolCalls: 1, TotalToolCalls: obs.ToolCalls, ToolResultBytes: len(result), Latency: time.Since(toolStarted), TotalElapsed: time.Since(started), ReasoningEffort: effort})
			messages = append(messages, model.Message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
		obs.LastToolFailure = toolFailed
		obs.Step = stepNumber
		obs.MaxSteps = budget.MaxSteps
		obs.MaxToolCalls = budget.MaxToolCalls
		obs.ContextTokens = ctxAcc.LastRequest
		obs.PeakContextTokens = ctxAcc.PeakRequest
		obs.MaxContextTokens = r.MaxContextTokens
		obs.ContextOverflow = ctxAcc.Overflow
		obs.ServerPromptN = ctxAcc.ServerPromptN
		obs.Elapsed = time.Since(started)
		obs.ReasoningEffort = effort
		obs.BaseRevision = baseRevision
		obs.ResultRevision = r.WS.GitHEAD()

		if toolFailed {
			effort = "xhigh"
		} else {
			effort = r.Model.GetReasoningEffort()
		}

		// Append the runtime observation so the model can see execution state.
		messages = append(messages, model.Message{Role: "user", Content: obs.Format()})

		// Inject a steer that arrived while tools ran, so the next model call
		// sees it right after the observation.
		if steer := r.takeSteer(); steer != "" {
			messages = append(messages, model.Message{Role: "user", Content: steer})
		}
	}

	err := fmt.Errorf("execution budget exceeded: %d steps", budget.MaxSteps)
	return r.finish(TraceEvent{Kind: "finish", Step: budget.MaxSteps, MaxSteps: budget.MaxSteps, TotalToolCalls: obs.ToolCalls, MaxToolCalls: budget.MaxToolCalls, ToolFailures: obs.ToolFailures, ContextTokens: ctxAcc.LastRequest, PeakContextTokens: ctxAcc.PeakRequest, MaxContextTokens: r.MaxContextTokens, ServerPromptN: ctxAcc.ServerPromptN, TotalElapsed: time.Since(started), ReasoningEffort: effort, BaseRevision: baseRevision, ResultRevision: r.WS.GitHEAD(), Error: err}, strings.Join(trace, "\n\n"), err)
}
