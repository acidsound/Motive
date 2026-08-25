package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NeedsInteractiveSetup returns true when the user has not yet configured
// Motive: no config file exists and no environment variables provide the
// essential connection settings (base URL).
func NeedsInteractiveSetup() bool {
	// A config file already exists — no setup needed.
	if p := ConfigPath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			return false
		}
	}
	// Environment variables already provide the base URL — no setup needed.
	if strings.TrimSpace(os.Getenv("MOTIVE_BASE_URL")) != "" {
		return false
	}
	if strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")) != "" {
		return false
	}
	return true
}

// isTTY reports whether the given file is connected to a terminal.
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// InteractiveSetup prompts the user on stdin for essential config values and
// writes them to the config file. It returns the fully-resolved Config.
// If stdin is not a TTY it falls back to Load() without prompting.
func InteractiveSetup() (*Config, error) {
	if !isTTY(os.Stdin) {
		// Non-interactive context (pipe, CI): just use defaults.
		return Load()
	}

	fmt.Println("Motive first-run setup")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	// API Endpoint
	fmt.Print("API Endpoint [http://127.0.0.1:8080/v1]: ")
	baseURL, _ := reader.ReadString('\n')
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080/v1"
	}

	// API Key (optional)
	fmt.Print("API Key (blank for none): ")
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)

	// Default Model (optional)
	fmt.Print("Default Model [Qwen3.8-27B]: ")
	model, _ := reader.ReadString('\n')
	model = strings.TrimSpace(model)
	if model == "" {
		model = "Qwen3.8-27B"
	}

	// Write the config file.
	cfgPath := ConfigPath()
	if cfgPath == "" {
		return nil, fmt.Errorf("cannot determine config path")
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	content := fmt.Sprintf(`default_provider = "default"

[[providers]]
name = "default"
base_url = "%s"
model = "%s"
api_key = "%s"
reasoning_effort = "low"
`, baseURL, model, apiKey)

	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("\nConfig written to %s\n", cfgPath)

	// Load the config we just wrote so env overrides still apply.
	return Load()
}
