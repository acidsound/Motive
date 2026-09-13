## Motive v0.4.0

Changes since v0.3.0:

### System Prompt (Standing Instructions)
- Pass a prompt after the `-tui` flag to inject standing instructions into every turn of the TUI session
- Example: `motive -tui "Always answer in Korean. Be concise."`
- The prompt is displayed at the top of the TUI (collapsed by default, first line only)
- Toggle full prompt visibility with `alt+s` (configurable via `MOTIVE_KEY_SYS_PROMPT_TOGGLE`)
- Status bar shows `sys⏸` indicator when the prompt is expanded

### Tests
- Added tests for `ExtraSystemPrompt` in `ContextBlock()`
- Added tests for system prompt line rendering (collapsed, expanded, truncation)
- Added test for `alt+s` toggle keybinding
- Added test verifying system prompt appears in `View()` output
