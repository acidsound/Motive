// Package config loads Motive runtime configuration: named providers, the
// active provider, and the session state directory. A config file is optional;
// environment variables remain the primary source when no file exists.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Provider describes one OpenAI-compatible endpoint plus the models it serves.
type Provider struct {
	Name            string   `toml:"name"`
	BaseURL         string   `toml:"base_url"`
	Model           string   `toml:"model"`
	Models          []string `toml:"models"`
	APIKey          string   `toml:"api_key"`
	ReasoningEffort string   `toml:"reasoning_effort"`
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

type fileConfig struct {
	DefaultProvider string     `toml:"default_provider"`
	Providers       []Provider `toml:"providers"`
}

// Config is the resolved runtime configuration.
type Config struct {
	Providers []Provider
	Default   *Provider
	StateDir  string
}

// Paths used for configuration and session state.
func Paths() (configPath, stateDir string) {
	configPath = os.Getenv("MOTIVE_CONFIG")
	stateDir = os.Getenv("MOTIVE_STATE_DIR")
	if configPath == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			configPath = filepath.Join(dir, "motive", "config.toml")
		}
	}
	if stateDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			stateDir = filepath.Join(home, ".motive")
		}
	}
	return configPath, stateDir
}

// Load reads the config file (if any), folds in environment overrides, and
// resolves the active provider.
func Load() (*Config, error) {
	configPath, stateDir := Paths()
	var fc fileConfig
	if configPath != "" {
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

	// Environment variables always win over the file for the active provider,
	// mirroring the historical behavior of the standalone env vars.
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
	active.ReasoningEffort = normalizeEffort(active.ReasoningEffort)

	return &Config{Providers: providers, Default: active, StateDir: stateDir}, nil
}

func normalizeEffort(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "low", "medium", "xhigh":
		return strings.ToLower(strings.TrimSpace(v))
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
