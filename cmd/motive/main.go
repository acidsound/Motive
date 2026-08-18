package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/acidsound/Motive/internal/config"
	"github.com/acidsound/Motive/internal/model"
	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/session"
	"github.com/acidsound/Motive/internal/tui"
)

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute}
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
	sess, err := session.NewStore(cfg.StateDir + "/sessions")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sessions: %v\n", err)
		os.Exit(1)
	}

	client := &model.Client{
		BaseURL:         strings.TrimRight(cfg.Default.BaseURL, "/"),
		Model:           cfg.Default.Model,
		APIKey:          cfg.Default.APIKey,
		Temperature:     envFloat("MOTIVE_TEMPERATURE", 0.6),
		MaxTokens:       envInt("MOTIVE_MAX_TOKENS", 0),
		ReasoningEffort: cfg.Default.ReasoningEffort,
		HTTP:            newHTTPClient(),
	}

	rt := runtime.New(client)
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
	result, err := rt.Execute(context.Background(), flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
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

func envFloat(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	var parsed float64
	if _, err := fmt.Sscanf(v, "%f", &parsed); err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(v, "%d", &parsed); err != nil {
		return fallback
	}
	return parsed
}
