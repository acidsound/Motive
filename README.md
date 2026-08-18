# Motive

Motive is a small model-centric execution runtime. The model is the reasoning and planning component; Motive provides a workspace, tools, execution, and revision-aware environment.

The initial implementation deliberately avoids an agent framework and plugin system. It talks to any OpenAI-compatible `/v1/chat/completions` endpoint and gives the model direct access to files, shell, web search, and Git state.

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

## Run

```bash
go build -o motive ./cmd/motive
./motive --tui
./motive "inspect this project and fix the failing test"
```

Configuration:

```text
MOTIVE_BASE_URL   default: http://127.0.0.1:8080/v1
MOTIVE_MODEL      default: Qwen3.8-27B
MOTIVE_API_KEY    optional
MOTIVE_WORKSPACE  default: current directory
```

`OPENAI_BASE_URL`, `OPENAI_MODEL`, and `OPENAI_API_KEY` are accepted as fallbacks.

## Tools exposed to the model

- `read_file`, `write_file`, `delete_file`
- `list_files`, `search_files`
- `shell`
- `web_search`
- `git_status`, `git_diff`

The tool set is intentionally concrete. There is no planner, sub-agent layer, memory manager, or plugin registry in the execution path.

## Status

Prototype, but already an end-to-end execution loop. The next work should focus on context quality, execution tracing, Git revision records, and a richer TUI rather than adding framework layers.
