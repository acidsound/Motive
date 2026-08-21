// Package config loads Motive runtime configuration: named providers, the
// active provider, and the session state directory. A config file is optional;
// environment variables remain the primary source when no file exists.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Execution budget defaults and hard caps. These are the single source of
// truth for the budget knobs; Load resolves them into Config and the runtime
// enforces the caps on every run.
const (
	DefaultMaxSteps         = 64
	DefaultMaxMinutes       = 30
	DefaultMaxToolCalls     = 128
	MaxAllowedSteps         = 256
	MaxAllowedMinutes       = 120
	MaxAllowedToolCalls     = 1024
	MaxAllowedContextTokens = 1_000_000
)

// Provider describes one OpenAI-compatible endpoint plus the models it serves.
type Provider struct {
	Name            string   `toml:"name"`
	BaseURL         string   `toml:"base_url"`
	Model           string   `toml:"model"`
	Models          []string `toml:"models"`
	APIKey          string   `toml:"api_key"`
	ReasoningEffort string   `toml:"reasoning_effort"`
	// Temperature is a pointer so an explicit 0 is honored instead of being
	// collapsed into the default; nil means "unset, use the Motive default".
	Temperature *float64 `toml:"temperature"`
	// MaxTokens of 0 means "no limit" (the request omits max_tokens).
	MaxTokens int `toml:"max_tokens"`
}

// EffectiveTemperature returns the provider's sampling temperature, applying
// the Motive default when the field is unset.
func (p *Provider) EffectiveTemperature() float64 {
	if p.Temperature != nil {
		return *p.Temperature
	}
	return 0.6
}

// AllModels returns the provider's selectable model ids: the default model
// followed by any explicitly listed extras, deduplicated.
func (p *Provider) AllModels() []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range append([]string{p.Model}, p.Models...) {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// fileConfig is the on-disk shape of config.toml. Every field that an
// environment variable can set has a file counterpart, so a config file can be
// a complete replacement for the environment (MOTIVE_CONFIG excepted, since it
// points at this very file).
type fileConfig struct {
	DefaultProvider  string     `toml:"default_provider"`
	StateDir         string     `toml:"state_dir"`
	Workspace        string     `toml:"workspace"`
	MaxSteps         int        `toml:"max_steps"`
	ExecutionMinutes int        `toml:"execution_minutes"`
	MaxToolCalls     int        `toml:"max_tool_calls"`
	MaxContextTokens int        `toml:"max_context_tokens"`
	Providers        []Provider `toml:"providers"`
}

// Config is the resolved runtime configuration. Every field is fully resolved
// (environment wins over the file, then the built-in default) so consumers
// never re-read the environment.
type Config struct {
	Providers []Provider
	Default   *Provider
	StateDir  string
	Workspace string
	// Execution budget, resolved and already capped at the allowed maximums.
	MaxSteps         int
	ExecutionMinutes int
	MaxToolCalls     int
	MaxContextTokens int
}

// ConfigPath returns the config file path: MOTIVE_CONFIG if set, else the
// platform user-config directory. The state dir and workspace are resolved in
// Load because they can also come from the file itself.
func ConfigPath() string {
	if p := strings.TrimSpace(os.Getenv("MOTIVE_CONFIG")); p != "" {
		return p
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "motive", "config.toml")
	}
	return ""
}

// Load reads the config file (if any), folds in environment overrides, and
// resolves the active provider and every runtime setting. Environment variables
// always win over the file, mirroring the historical standalone env vars.
func Load() (*Config, error) {
	var fc fileConfig
	if configPath := ConfigPath(); configPath != "" {
		if data, err := os.ReadFile(configPath); err == nil {
			if err := toml.Unmarshal(data, &fc); err != nil {
				return nil, err
			}
		}
	}

	providers := fc.Providers
	if len(providers) == 0 {
		// Environment-only setup, preserving the original Motive defaults.
		providers = []Provider{{
			Name:            "default",
			BaseURL:         env("MOTIVE_BASE_URL", env("OPENAI_BASE_URL", "http://127.0.0.1:8080/v1")),
			Model:           env("MOTIVE_MODEL", env("OPENAI_MODEL", "Qwen3.8-27B")),
			APIKey:          env("MOTIVE_API_KEY", env("OPENAI_API_KEY", "")),
			ReasoningEffort: env("MOTIVE_REASONING_EFFORT", "low"),
		}}
	}

	name := strings.TrimSpace(fc.DefaultProvider)
	var active *Provider
	if name != "" {
		for i := range providers {
			if providers[i].Name == name {
				active = &providers[i]
				break
			}
		}
	}
	if active == nil {
		active = &providers[0]
	}

	// Environment variables always win over the file for the active provider.
	if v := strings.TrimSpace(os.Getenv("MOTIVE_BASE_URL")); v != "" {
		active.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("MOTIVE_MODEL")); v != "" {
		active.Model = v
	}
	if v := os.Getenv("MOTIVE_API_KEY"); v != "" {
		active.APIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("MOTIVE_REASONING_EFFORT")); v != "" {
		active.ReasoningEffort = v
	}
	if v := envFloatPtr("MOTIVE_TEMPERATURE"); v != nil {
		active.Temperature = v
	}
	if v := envIntPtr("MOTIVE_MAX_TOKENS"); v != nil {
		active.MaxTokens = *v
	}
	active.ReasoningEffort = normalizeEffort(active.ReasoningEffort)

	// Deployment settings: environment wins over the file, then the default.
	stateDir := strings.TrimSpace(os.Getenv("MOTIVE_STATE_DIR"))
	if stateDir == "" {
		stateDir = strings.TrimSpace(fc.StateDir)
	}
	if stateDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			stateDir = filepath.Join(home, ".motive")
		}
	}

	workspace := strings.TrimSpace(os.Getenv("MOTIVE_WORKSPACE"))
	if workspace == "" {
		workspace = strings.TrimSpace(fc.Workspace)
	}

	return &Config{
		Providers:        providers,
		Default:          active,
		StateDir:         stateDir,
		Workspace:        workspace,
		MaxSteps:         resolveBoundedInt("MOTIVE_MAX_STEPS", fc.MaxSteps, DefaultMaxSteps, MaxAllowedSteps),
		ExecutionMinutes: resolveBoundedInt("MOTIVE_EXECUTION_MINUTES", fc.ExecutionMinutes, DefaultMaxMinutes, MaxAllowedMinutes),
		MaxToolCalls:     resolveBoundedInt("MOTIVE_MAX_TOOL_CALLS", fc.MaxToolCalls, DefaultMaxToolCalls, MaxAllowedToolCalls),
		MaxContextTokens: resolveBoundedInt("MOTIVE_MAX_CONTEXT_TOKENS", fc.MaxContextTokens, 0, MaxAllowedContextTokens),
	}, nil
}

// normalizeEffort normalises the reasoning-effort string to the Motive
// vocabulary (low / medium / high / xhigh / max). Unknown or empty values fall
// back to "low". Must match the vocabulary in model/client.go normalizeEffort.
func normalizeEffort(v string) string {
	n := strings.ToLower(strings.TrimSpace(v))
	switch n {
	case "low", "medium", "high", "xhigh", "max":
		return n
	default:
		return "low"
	}
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// envFloatPtr returns a pointer to the parsed env value, or nil when unset or
// unparseable. A pointer is used so callers can distinguish "not set" from a
// legitimate zero.
func envFloatPtr(key string) *float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

// envIntPtr returns a pointer to the parsed non-negative env value, or nil when
// unset or unparseable.
func envIntPtr(key string) *int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed < 0 {
		return nil
	}
	return &parsed
}

// parsePositiveInt parses a strictly-positive integer, reporting whether the
// value was present and valid. Zero and negative values are treated as unset,
// matching the historical env-int behavior for the budget knobs.
func parsePositiveInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// resolveBoundedInt resolves an execution budget knob: the environment variable
// wins, then the config file value, then the fallback default; the result is
// capped at maximum.
func resolveBoundedInt(envKey string, fileVal, fallback, maximum int) int {
	value := fallback
	if fileVal > 0 {
		value = fileVal
	}
	if n, ok := parsePositiveInt(os.Getenv(envKey)); ok {
		value = n
	}
	if value > maximum {
		return maximum
	}
	return value
}
