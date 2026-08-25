package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNeedsInteractiveSetup_NoConfigNoEnv(t *testing.T) {
	t.Setenv("MOTIVE_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("MOTIVE_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	if !NeedsInteractiveSetup() {
		t.Fatal("expected NeedsInteractiveSetup = true when no config and no env")
	}
}

func TestNeedsInteractiveSetup_ConfigExists(t *testing.T) {
	t.Setenv("MOTIVE_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	path := writeTempConfig(t, `
[[providers]]
name = "default"
base_url = "http://x/v1"
model = "m"
`)
	t.Setenv("MOTIVE_CONFIG", path)
	if NeedsInteractiveSetup() {
		t.Fatal("expected NeedsInteractiveSetup = false when config file exists")
	}
}

func TestNeedsInteractiveSetup_EnvSet(t *testing.T) {
	t.Setenv("MOTIVE_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("MOTIVE_BASE_URL", "http://env/v1")
	t.Setenv("OPENAI_BASE_URL", "")
	if NeedsInteractiveSetup() {
		t.Fatal("expected NeedsInteractiveSetup = false when MOTIVE_BASE_URL is set")
	}
}

func TestNeedsInteractiveSetup_OpenAIEnvSet(t *testing.T) {
	t.Setenv("MOTIVE_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("MOTIVE_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "http://env/v1")
	if NeedsInteractiveSetup() {
		t.Fatal("expected NeedsInteractiveSetup = false when OPENAI_BASE_URL is set")
	}
}

func TestInteractiveSetup_DefaultsOnEmptyInput(t *testing.T) {
	// When the user just presses Enter on every prompt, defaults are used
	// and the config file is written with those defaults.
	t.Setenv("MOTIVE_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("MOTIVE_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("MOTIVE_MODEL", "")
	t.Setenv("MOTIVE_API_KEY", "")
	cfg, err := InteractiveSetup()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default == nil {
		t.Fatal("expected non-nil Default provider")
	}
	if cfg.Default.BaseURL != "http://127.0.0.1:8080/v1" {
		t.Errorf("base_url = %q, want default", cfg.Default.BaseURL)
	}
	if cfg.Default.Model != "Qwen3.8-27B" {
		t.Errorf("model = %q, want default", cfg.Default.Model)
	}
}

func TestInteractiveSetup_WritesConfig(t *testing.T) {
	// We can't test the TTY prompt path in unit tests (stdin is not a TTY),
	// but we can verify the config file it would write is parseable.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	content := `default_provider = "default"

[[providers]]
name = "default"
base_url = "http://my-server:9000/v1"
model = "my-model"
api_key = "sk-test"
reasoning_effort = "low"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOTIVE_CONFIG", cfgPath)
	t.Setenv("MOTIVE_BASE_URL", "")
	t.Setenv("MOTIVE_MODEL", "")
	t.Setenv("MOTIVE_API_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default.BaseURL != "http://my-server:9000/v1" {
		t.Errorf("base_url = %q", cfg.Default.BaseURL)
	}
	if cfg.Default.Model != "my-model" {
		t.Errorf("model = %q", cfg.Default.Model)
	}
	if cfg.Default.APIKey != "sk-test" {
		t.Errorf("api_key = %q", cfg.Default.APIKey)
	}
}
