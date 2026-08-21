package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/acidsound/Motive/internal/config"
	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/session"
	"github.com/acidsound/Motive/internal/tui"
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

func main() {
	tuiMode := flag.Bool("tui", false, "start the terminal UI")
	verbose := flag.Bool("v", false, "show execution telemetry")
	resume := flag.Bool("r", false, "open the TUI session picker on start")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	sessDir := cfg.StateDir + "/sessions"
	sess, err := session.NewStore(sessDir)
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
	if *verbose {
		rt.Trace = verboseTrace
	}

	if *tuiMode || flag.NArg() == 0 {
		if err := tui.Run(rt, cfg, sess, *resume); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

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
			_ = sess.Append(unitID, session.Entry{
				TS:             time.Now(),
				Role:           "unit",
				Content:        rec.String(),
				BaseRevision:   rec.BaseRevision,
				ResultRevision: rec.ResultRevision,
				ElapsedMS:      rec.ElapsedMS,
			})
		}
	}

	result, err := rt.Execute(context.Background(), flag.Arg(0))
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
		fmt.Fprintf(os.Stderr, "%s: tool %s, result=%dB, %s, total_tools=%d, reasoning=%s\n",
			prefix, event.ToolName, event.ToolResultBytes, formatDuration(event.Latency), event.TotalToolCalls, event.ReasoningEffort)
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
