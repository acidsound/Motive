package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFromFile(t *testing.T) {
	t.Setenv("MOTIVE_CONFIG", writeTempConfig(t, `
default_provider = "gateway"

[[providers]]
name = "local"
base_url = "http://127.0.0.1:8080/v1"
model = "qwen-27b"
reasoning_effort = "low"

[[providers]]
name = "gateway"
base_url = "http://127.0.0.1:8787/v1"
model = "qwen3.8-27b"
models = ["deepseek-v4-pro", "qwen3.8-27b"]
reasoning_effort = "medium"
`))
	t.Setenv("MOTIVE_BASE_URL", "")
	t.Setenv("MOTIVE_MODEL", "")
	t.Setenv("MOTIVE_REASONING_EFFORT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(cfg.Providers))
	}
	if cfg.Default == nil || cfg.Default.Name != "gateway" {
		t.Fatalf("default provider = %+v, want gateway", cfg.Default)
	}
	if cfg.Default.BaseURL != "http://127.0.0.1:8787/v1" {
		t.Errorf("default base_url = %q", cfg.Default.BaseURL)
	}
	if cfg.Default.ReasoningEffort != "medium" {
		t.Errorf("default effort = %q, want medium", cfg.Default.ReasoningEffort)
	}
	models := cfg.Default.AllModels()
	want := []string{"qwen3.8-27b", "deepseek-v4-pro"}
	if len(models) != len(want) {
		t.Fatalf("AllModels = %v, want %v", models, want)
	}
	for i := range want {
		if models[i] != want[i] {
			t.Fatalf("AllModels = %v, want %v", models, want)
		}
	}
}

func TestLoadDefaultsToFirstProviderWithoutDefaultField(t *testing.T) {
	t.Setenv("MOTIVE_CONFIG", writeTempConfig(t, `
[[providers]]
name = "a"
base_url = "http://a/v1"
model = "m1"

[[providers]]
name = "b"
base_url = "http://b/v1"
model = "m2"
`))
	t.Setenv("MOTIVE_BASE_URL", "")
	t.Setenv("MOTIVE_MODEL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default.Name != "a" {
		t.Fatalf("default = %q, want a", cfg.Default.Name)
	}
}

func TestLoadEnvOnlyDefaults(t *testing.T) {
	t.Setenv("MOTIVE_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("MOTIVE_BASE_URL", "http://example.test:9999/v1")
	t.Setenv("MOTIVE_MODEL", "env-model")
	t.Setenv("MOTIVE_REASONING_EFFORT", "xhigh")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.Providers))
	}
	if cfg.Default.BaseURL != "http://example.test:9999/v1" || cfg.Default.Model != "env-model" {
		t.Fatalf("default = %+v", cfg.Default)
	}
	if cfg.Default.ReasoningEffort != "xhigh" {
		t.Errorf("effort = %q, want xhigh", cfg.Default.ReasoningEffort)
	}
	if !strings.Contains(cfg.StateDir, ".motive") {
		t.Errorf("state dir = %q, want home .motive", cfg.StateDir)
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	t.Setenv("MOTIVE_CONFIG", writeTempConfig(t, `
[[providers]]
name = "local"
base_url = "http://127.0.0.1:8080/v1"
model = "file-model"
`))
	t.Setenv("MOTIVE_BASE_URL", "http://env.example/v1")
	t.Setenv("MOTIVE_MODEL", "env-model")
	t.Setenv("MOTIVE_REASONING_EFFORT", "invalid")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default.BaseURL != "http://env.example/v1" {
		t.Errorf("base_url = %q, want env override", cfg.Default.BaseURL)
	}
	if cfg.Default.Model != "env-model" {
		t.Errorf("model = %q, want env override", cfg.Default.Model)
	}
	if cfg.Default.ReasoningEffort != "low" {
		t.Errorf("effort = %q, want low fallback for invalid value", cfg.Default.ReasoningEffort)
	}
}
