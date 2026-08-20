# Motive

Motive is a small model-centric execution runtime. The model is the reasoning and planning component; Motive provides a workspace, tools, execution, and revision-aware environment.

The initial implementation deliberately avoids an agent framework and plugin system. It talks to any OpenAI-compatible `/v1/chat/completions` endpoint and gives the model direct access to files, shell, web search/fetch, and Git state.

## Current loop

```text
request
  -> context compiler
  -> OpenAI-compatible model
  -> tool call
  -> local execution / observation
  -> model
  -> ...
  -> final response
```

Each user request starts with a fresh model context. The persistent world is the workspace, its files, and Git state rather than chat history.

## Build

By policy, the binary is always built into `bin/`. `bin/` is git-ignored, so the
binary itself is never committed; only the source is.

```bash
go build -o bin/motive ./cmd/motive
```

## Run

```bash
./bin/motive --tui
./bin/motive "inspect this project and fix the failing test"
./bin/motive --tui -r   # open the session picker on start
```

Configuration:

```text
MOTIVE_BASE_URL   default: http://127.0.0.1:8080/v1
MOTIVE_MODEL      default: Qwen3.8-27B
MOTIVE_API_KEY    optional
MOTIVE_WORKSPACE  default: current directory
MOTIVE_STATE_DIR  session storage, default: ~/.motive
```

`OPENAI_BASE_URL`, `OPENAI_MODEL`, and `OPENAI_API_KEY` are accepted as fallbacks.

### Providers

Named providers and the active one can be set in a TOML file (default
`~/.config/motive/config.toml`, override with `MOTIVE_CONFIG`):

```toml
default_provider = "dp4090"

[[providers]]
name = "dp4090"
base_url = "http://100.72.102.121:8080/v1"
model = "Qwen3.8-27B-UD-Q4_K_XL.gguf"
reasoning_effort = "low"

[[providers]]
name = "gateway"
base_url = "http://127.0.0.1:8787/v1"
model = "qwen3.8-27b"
models = ["deepseek-v4-pro", "gemma-4-31b"]
```

`model` is the default id; `models` adds extra selectable ids. Environment
variables (`MOTIVE_BASE_URL`, `MOTIVE_MODEL`, `MOTIVE_API_KEY`) always override
the active provider. Without a config file, the environment variables form a
single "default" provider.

### TUI

The TUI streams model output live with reasoning shown dimmed, renders
lightweight markdown, and persists every turn to a JSONL session file that
`-r` can resume. While a run is in progress:

- `esc` stops the run: the in-flight request is canceled, the partial output
  is persisted, and a `stopped` entry is recorded in the transcript.
- `enter` submits to the running execution. The enter mode is cycled with
  `ctrl+\`: **steer** injects the message into the run at the next step
  boundary (after tool results, or instead of finishing); **queue** appends
  it to a FIFO that is processed as fresh turns after the current one ends.
- `ctrl+c` quits; while busy it stops the run first so the partial output is
  persisted before exit.

Controls (rebindable via `MOTIVE_KEY_<NAME>`, e.g.
`MOTIVE_KEY_SCROLL_UP=ctrl+u`):

```text
enter          run (busy: steer/queue)   ctrl+e   cycle reasoning effort
shift+enter    newline                   ctrl+r   session picker
ctrl+d         git diff view             ctrl+t   toggle tools
ctrl+\         cycle steer/queue (busy)  ctrl+/   toggle help
ctrl+k / ctrl+j                         scroll up / down
ctrl+shift+k / ctrl+shift+j             page up / down
up / down           prompt history (empty input)
ctrl+g         bookmark                  ctrl+l   clear transcript
esc            stop run (busy) / close help
ctrl+c         quit
```

## Tools exposed to the model

- `read_file`, `write_file`, `edit_file`, `delete_file`
- `list_files`, `glob`, `search_files`
- `shell`
- `web_search`, `web_fetch`
- `git_status`, `git_diff`

The tool set is intentionally concrete. There is no planner, sub-agent layer, memory manager, or plugin registry in the execution path.

## Status

Prototype, but already an end-to-end execution loop. The next work should focus on context quality, execution tracing, Git revision records, and a richer TUI rather than adding framework layers.
