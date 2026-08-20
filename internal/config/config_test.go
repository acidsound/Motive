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

// clearBehaviorEnv unsets every behavior env var so file-based tests are
// isolated from the ambient environment.
func clearBehaviorEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"MOTIVE_BASE_URL", "MOTIVE_MODEL", "MOTIVE_API_KEY", "MOTIVE_REASONING_EFFORT",
		"MOTIVE_TEMPERATURE", "MOTIVE_MAX_TOKENS", "MOTIVE_STATE_DIR", "MOTIVE_WORKSPACE",
		"MOTIVE_MAX_STEPS", "MOTIVE_EXECUTION_MINUTES", "MOTIVE_MAX_TOOL_CALLS", "MOTIVE_MAX_CONTEXT_TOKENS",
	} {
		t.Setenv(k, "")
	}
}

func TestResolveBoundedInt(t *testing.T) {
	const key = "MOTIVE_TEST_RESOLVE"
	t.Setenv(key, "")
	if got := resolveBoundedInt(key, 0, 32, 256); got != 32 {
		t.Fatalf("env+file unset = %d, want default 32", got)
	}
	if got := resolveBoundedInt(key, 64, 32, 256); got != 64 {
		t.Fatalf("file set = %d, want 64", got)
	}
	if got := resolveBoundedInt(key, 999, 32, 256); got != 256 {
		t.Fatalf("file capped = %d, want 256", got)
	}
	t.Setenv(key, "10")
	if got := resolveBoundedInt(key, 64, 32, 256); got != 10 {
		t.Fatalf("env wins over file = %d, want 10", got)
	}
	t.Setenv(key, "999")
	if got := resolveBoundedInt(key, 64, 32, 256); got != 256 {
		t.Fatalf("env capped = %d, want 256", got)
	}
	t.Setenv(key, "0")
	if got := resolveBoundedInt(key, 64, 32, 256); got != 64 {
		t.Fatalf("env zero falls back to file = %d, want 64", got)
	}
}

func TestLoadTemperatureFromFile(t *testing.T) {
	clearBehaviorEnv(t)
	t.Setenv("MOTIVE_CONFIG", writeTempConfig(t, `
[[providers]]
name = "local"
base_url = "http://127.0.0.1:8080/v1"
model = "m"
temperature = 0.2
max_tokens = 4096
`))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Default.EffectiveTemperature(); got != 0.2 {
		t.Errorf("temperature = %v, want 0.2", got)
	}
	if cfg.Default.MaxTokens != 4096 {
		t.Errorf("max_tokens = %d, want 4096", cfg.Default.MaxTokens)
	}
}

func TestLoadTemperatureZeroIsHonored(t *testing.T) {
	clearBehaviorEnv(t)
	t.Setenv("MOTIVE_CONFIG", writeTempConfig(t, `
[[providers]]
name = "local"
base_url = "http://127.0.0.1:8080/v1"
model = "m"
temperature = 0
`))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Default.EffectiveTemperature(); got != 0 {
		t.Errorf("temperature = %v, want 0 (explicit zero must be honored)", got)
	}
}

func TestLoadTemperatureDefaultWhenUnset(t *testing.T) {
	clearBehaviorEnv(t)
	t.Setenv("MOTIVE_CONFIG", writeTempConfig(t, `
[[providers]]
name = "local"
base_url = "http://127.0.0.1:8080/v1"
model = "m"
`))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Default.EffectiveTemperature(); got != 0.6 {
		t.Errorf("temperature = %v, want default 0.6", got)
	}
	if cfg.Default.MaxTokens != 0 {
		t.Errorf("max_tokens = %d, want 0 (no limit)", cfg.Default.MaxTokens)
	}
}

func TestLoadTemperatureAndMaxTokensEnvOverrideFile(t *testing.T) {
	clearBehaviorEnv(t)
	t.Setenv("MOTIVE_CONFIG", writeTempConfig(t, `
[[providers]]
name = "local"
base_url = "http://127.0.0.1:8080/v1"
model = "m"
temperature = 0.2
max_tokens = 4096
`))
	t.Setenv("MOTIVE_TEMPERATURE", "0.9")
	t.Setenv("MOTIVE_MAX_TOKENS", "1234")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Default.EffectiveTemperature(); got != 0.9 {
		t.Errorf("temperature = %v, want env 0.9", got)
	}
	if cfg.Default.MaxTokens != 1234 {
		t.Errorf("max_tokens = %d, want env 1234", cfg.Default.MaxTokens)
	}
}

func TestLoadStateDirAndWorkspaceFromFile(t *testing.T) {
	clearBehaviorEnv(t)
	t.Setenv("MOTIVE_CONFIG", writeTempConfig(t, `
state_dir = "/tmp/motive-state"
workspace = "/tmp/motive-ws"
[[providers]]
name = "local"
base_url = "http://127.0.0.1:8080/v1"
model = "m"
`))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateDir != "/tmp/motive-state" {
		t.Errorf("state_dir = %q, want /tmp/motive-state", cfg.StateDir)
	}
	if cfg.Workspace != "/tmp/motive-ws" {
		t.Errorf("workspace = %q, want /tmp/motive-ws", cfg.Workspace)
	}
}

func TestLoadStateDirAndWorkspaceEnvOverrideFile(t *testing.T) {
	clearBehaviorEnv(t)
	t.Setenv("MOTIVE_CONFIG", writeTempConfig(t, `
state_dir = "/tmp/file-state"
workspace = "/tmp/file-ws"
[[providers]]
name = "local"
base_url = "http://127.0.0.1:8080/v1"
model = "m"
`))
	t.Setenv("MOTIVE_STATE_DIR", "/tmp/env-state")
	t.Setenv("MOTIVE_WORKSPACE", "/tmp/env-ws")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateDir != "/tmp/env-state" {
		t.Errorf("state_dir = %q, want env /tmp/env-state", cfg.StateDir)
	}
	if cfg.Workspace != "/tmp/env-ws" {
		t.Errorf("workspace = %q, want env /tmp/env-ws", cfg.Workspace)
	}
}

func TestLoadBudgetFromFile(t *testing.T) {
	clearBehaviorEnv(t)
	t.Setenv("MOTIVE_CONFIG", writeTempConfig(t, `
max_steps = 10
execution_minutes = 5
max_tool_calls = 50
max_context_tokens = 200000
[[providers]]
name = "local"
base_url = "http://127.0.0.1:8080/v1"
model = "m"
`))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxSteps != 10 {
		t.Errorf("max_steps = %d, want 10", cfg.MaxSteps)
	}
	if cfg.ExecutionMinutes != 5 {
		t.Errorf("execution_minutes = %d, want 5", cfg.ExecutionMinutes)
	}
	if cfg.MaxToolCalls != 50 {
		t.Errorf("max_tool_calls = %d, want 50", cfg.MaxToolCalls)
	}
	if cfg.MaxContextTokens != 200000 {
		t.Errorf("max_context_tokens = %d, want 200000", cfg.MaxContextTokens)
	}
}

func TestLoadBudgetDefaultsWhenNoFile(t *testing.T) {
	clearBehaviorEnv(t)
	t.Setenv("MOTIVE_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxSteps != DefaultMaxSteps {
		t.Errorf("max_steps = %d, want default %d", cfg.MaxSteps, DefaultMaxSteps)
	}
	if cfg.ExecutionMinutes != DefaultMaxMinutes {
		t.Errorf("execution_minutes = %d, want default %d", cfg.ExecutionMinutes, DefaultMaxMinutes)
	}
	if cfg.MaxToolCalls != DefaultMaxToolCalls {
		t.Errorf("max_tool_calls = %d, want default %d", cfg.MaxToolCalls, DefaultMaxToolCalls)
	}
	if cfg.MaxContextTokens != 0 {
		t.Errorf("max_context_tokens = %d, want 0", cfg.MaxContextTokens)
	}
}
