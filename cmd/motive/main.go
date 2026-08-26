package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/acidsound/Motive/internal/config"
	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/session"
	"github.com/acidsound/Motive/internal/tui"
	"github.com/acidsound/Motive/internal/version"
)

// newModelClient builds the production model client from resolved config.
// The shared model.NewHTTPClient() factory (no total http.Client.Timeout) is
// used so the runtime/request context deadline remains the sole lifetime
// authority for streaming responses, matching the test-protected policy.
func newModelClient(cfg *config.Config) *model.Client {
	return &model.Client{
		BaseURL:         strings.TrimRight(cfg.Default.BaseURL, "/"),
		Model:           cfg.Default.Model,
		APIKey:          cfg.Default.APIKey,
		Temperature:     cfg.Default.EffectiveTemperature(),
		MaxTokens:       cfg.Default.MaxTokens,
		ReasoningEffort: cfg.Default.ReasoningEffort,
		HTTP:            model.NewHTTPClient(),
	}
}

// attachFlags collects repeatable --attach file arguments.
type attachFlags []string

func (f *attachFlags) String() string { return strings.Join(*f, ", ") }
func (f *attachFlags) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func main() {
	tuiMode := flag.Bool("tui", false, "start the terminal UI")
	verbose := flag.Bool("v", false, "show execution telemetry")
	silent := flag.Bool("silent", false, "print only the result; no progress output on stderr")
	resume := flag.Bool("r", false, "open the TUI session picker on start")
	showVersion := flag.Bool("version", false, "print the build version and exit")
	var attach attachFlags
	flag.Var(&attach, "attach", "attach a file (image/video/any); relative paths resolve against the cwd, then the workspace root")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	var cfg *config.Config
	var err error
	if config.NeedsInteractiveSetup() {
		cfg, err = config.InteractiveSetup()
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	// Session state is namespaced per workspace under the state dir
	// (~/.motive/<workspace-namespace>) so transcripts never mix across
	// workspaces and the workspace itself never holds Motive state.
	sess, err := session.NewStoreForWorkspace(filepath.Join(cfg.StateDir, "sessions"), cfg.Workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sessions: %v\n", err)
		os.Exit(1)
	}

	client := newModelClient(cfg)

	rt := runtime.New(client, cfg)
	// Wire the session log so the session_log tool can read the transcript.
	rt.SessionLog = func(id string, lines int) (string, error) {
		entries, err := sess.Tail(id, lines)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "[session %s: last %d entries]\n", id, len(entries))
		for _, e := range entries {
			b.WriteString(session.FormatEntry(e))
			b.WriteString("\n")
		}
		return b.String(), nil
	}
	// Verbose telemetry and the progress animation are mutually exclusive
	// (both render on stderr); --silent wins over -v.
	if *verbose && !*silent {
		rt.Trace = verboseTrace
	}

	if *tuiMode || flag.NArg() == 0 {
		if err := tui.Run(rt, cfg, sess, *resume); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// One-shot runs show the same working micro animation as the TUI on
	// stderr (spinner + phase label, live elapsed for prefill/reasoning),
	// unless --silent, -v, or a non-terminal stderr disables it. stdout stays
	// reserved for the final result, so piping captures only the answer.
	// Streaming is enabled for the CLI too so the phase transitions (prefill,
	// reasoning, answering, tooling) match the TUI's lifecycle live.
	var progress *cliProgress
	if !*silent && !*verbose && stderrIsTerminal() {
		progress = newCLIProgress(os.Stderr, cfg.Default.Name, cfg.Default.Model)
		prevTrace := rt.Trace
		rt.Trace = func(event runtime.TraceEvent) {
			if prevTrace != nil {
				prevTrace(event)
			}
			progress.trace(event)
		}
		progress.start()
	}
	rt.Stream = true

	// One-shot runs create a unit session so sub-execution telemetry is
	// durable (C4 closure). The session id is printed on stderr in-band so
	// a parent execution can read the boundary record via session_log.
	unitID := ""
	if id, err := sess.New(); err == nil {
		unitID = id
		fmt.Fprintf(os.Stderr, "[motive] unit session: %s\n", id)
		_ = sess.Append(id, session.Entry{TS: time.Now(), Role: "user", Content: flag.Arg(0), BaseRevision: rt.WS.GitHEAD()})
		rt.SessionID = id
		rt.UnitBoundary = func(rec runtime.UnitBoundary) {
			if unitID == "" {
				return
			}
			// The compact JSON line in Content is the single source of truth
			// (status, revision delta, budget usage); no duplicated Entry
			// fields are written alongside it.
			_ = sess.Append(unitID, session.Entry{
				TS:      time.Now(),
				Role:    "unit",
				Content: rec.String(),
			})
		}
	}

	// Resolve --attach paths: try the cwd first, then the workspace root, so
	// both relative-to-cwd and relative-to-workspace references work. The
	// workspace root also wins for absolute paths when the file lives there.
	var attachments []model.Attachment
	for _, p := range attach {
		att, err := model.DetectAttachment(p)
		if err != nil && rt.WS != nil && rt.WS.Root != "" {
			att, err = model.DetectAttachment(filepath.Join(rt.WS.Root, p))
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "attach %s: %v\n", p, err)
			os.Exit(1)
		}
		attachments = append(attachments, att)
	}

	result, err := rt.Execute(context.Background(), flag.Arg(0), attachments...)
	// Clear the progress line (if any) before any error/result output so the
	// terminal never keeps a stale spinner frame next to the answer.
	if progress != nil {
		progress.stop()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		// On failure the result holds the unit's partial assistant text —
		// the forward intent emitted before termination. Deliver it in-band
		// so a parent execution can reconstitute without re-deriving it.
		if result != "" {
			fmt.Fprintln(os.Stderr, "---")
			fmt.Fprintln(os.Stderr, result)
		}
		os.Exit(1)
	}
	fmt.Println(result)
}

func verboseTrace(event runtime.TraceEvent) {
	prefix := fmt.Sprintf("[motive] step %d/%d", event.Step, event.MaxSteps)
	switch event.Kind {
	case "start":
		fmt.Fprintf(os.Stderr, "[motive] execution started (base %s, reasoning=%s, budget=%d steps)\n", shortRevision(event.BaseRevision), event.ReasoningEffort, event.MaxSteps)
	case "model_start":
		fmt.Fprintf(os.Stderr, "%s: model request (%d messages, reasoning=%s, tools=%d)\n", prefix, event.MessageCount, event.ReasoningEffort, event.TotalToolCalls)
	case "model_end":
		if event.Error != nil {
			fmt.Fprintf(os.Stderr, "%s: model error after %s: %v\n", prefix, formatDuration(event.Latency), event.Error)
			return
		}
		fmt.Fprintf(os.Stderr, "%s: model response after %s, request=%dB (~%d tokens), response=%dB, tool_calls=%d, total_tools=%d, reasoning=%s\n",
			prefix, formatDuration(event.Latency), event.RequestBytes, event.EstimatedInputTokens, event.ResponseBytes, event.ToolCalls, event.TotalToolCalls, event.ReasoningEffort)
		if t := event.ServerTimings; t != nil {
			fmt.Fprintf(os.Stderr, "%s: server prompt=%d tok %.0fms, predicted=%d tok %.0fms, cache=%d tok\n",
				prefix, t.PromptN, t.PromptMS, t.PredictedN, t.PredictedMS, t.CacheN)
		}
	case "tool":
		// Surface what the call actually does (the shell command, the file a
		// read/write/edit touches, the web_search query, the fetched URL) so
		// verbose output is not just a tool name and a byte count.
		detail := ""
		if d := tui.ToolDetail(event.ToolName, event.ToolArgs, event.ToolResultLines, event.ToolResultBytes, event.ToolResultHead); d != "" {
			detail = " (" + d + ")"
		}
		fmt.Fprintf(os.Stderr, "%s: tool %s%s, result=%dB, %s, total_tools=%d, reasoning=%s\n",
			prefix, event.ToolName, detail, event.ToolResultBytes, formatDuration(event.Latency), event.TotalToolCalls, event.ReasoningEffort)
	case "finish":
		if event.Error != nil {
			fmt.Fprintf(os.Stderr, "[motive] execution failed after %s: %v\n", formatDuration(event.TotalElapsed), event.Error)
			return
		}
		fmt.Fprintf(os.Stderr, "[motive] execution finished in %s (base=%s result=%s tools=%d)\n",
			formatDuration(event.TotalElapsed), shortRevision(event.BaseRevision), shortRevision(event.ResultRevision), event.TotalToolCalls)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(10 * time.Millisecond).String()
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
