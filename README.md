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
./bin/motive --attach shot.png "what is in this image?"
```

`--attach` accepts repeatable file paths (images, videos, or any file).
Relative paths resolve against the cwd, then the workspace root. Images are
downscaled to at most 1280 px on the longest edge, re-encoded, and inlined
into the request as data URIs when they fit the 20 MB inline limit, keeping
the request body small for vision backends; videos and any other file type
are passed as path references the model can read with the workspace tools
(video frames can be extracted with the shell tool, e.g. `ffmpeg`).

## Configuration

Every setting can come from an environment variable or from a TOML config file.
Environment variables always win over the file, so the file is a complete,
persistent default and the environment is a per-invocation override. The one
exception is `MOTIVE_CONFIG`, which points at the file itself.

### Config file

Default location `~/.config/motive/config.toml` (override with `MOTIVE_CONFIG`):

```toml
# Top level
default_provider = "gateway"   # which provider is active
state_dir = "~/.motive"        # session storage root   (MOTIVE_STATE_DIR)
workspace = "."                # workspace root         (MOTIVE_WORKSPACE); empty = cwd

# Execution budget (all optional, capped at hard maximums)
max_steps = 64                 # MOTIVE_MAX_STEPS
execution_minutes = 30         # MOTIVE_EXECUTION_MINUTES
max_tool_calls = 128           # MOTIVE_MAX_TOOL_CALLS
max_context_tokens = 0         # MOTIVE_MAX_CONTEXT_TOKENS; 0 = no limit

# Named providers
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
api_key = ""                   # optional
reasoning_effort = "medium"    # low | medium | high | xhigh | max
temperature = 0.6              # sampling temperature; omit for the 0.6 default
max_tokens = 0                 # response cap; 0 = no limit
```

`model` is the default id; `models` adds extra selectable ids. `temperature`
accepts `0` (an explicit zero is honored, not treated as "unset"). Without a
config file, the environment variables form a single "default" provider.

### Environment variables

| Variable | Config key | Default |
| --- | --- | --- |
| `MOTIVE_CONFIG` | — (points at the file) | `~/.config/motive/config.toml` |
| `MOTIVE_BASE_URL` | `base_url` | `http://127.0.0.1:8080/v1` |
| `MOTIVE_MODEL` | `model` | `Qwen3.8-27B` |
| `MOTIVE_API_KEY` | `api_key` | — |
| `MOTIVE_REASONING_EFFORT` | `reasoning_effort` | `low` |
| `MOTIVE_TEMPERATURE` | `temperature` | `0.6` |
| `MOTIVE_MAX_TOKENS` | `max_tokens` | `0` (no limit) |
| `MOTIVE_WORKSPACE` | `workspace` | current directory |
| `MOTIVE_STATE_DIR` | `state_dir` | `~/.motive` |
| `MOTIVE_MAX_STEPS` | `max_steps` | `64` |
| `MOTIVE_EXECUTION_MINUTES` | `execution_minutes` | `30` |
| `MOTIVE_MAX_TOOL_CALLS` | `max_tool_calls` | `128` |
| `MOTIVE_MAX_CONTEXT_TOKENS` | `max_context_tokens` | `0` (no limit) |

`OPENAI_BASE_URL`, `OPENAI_MODEL`, and `OPENAI_API_KEY` are accepted as
fallbacks for the endpoint settings when no `MOTIVE_*` value is set.

### Session storage

Session transcripts are stored **outside the workspace**, namespaced per
workspace under the state dir:

```text
~/.motive/<workspace-namespace>/<session-id>.jsonl
```

`<workspace-namespace>` is `<basename>-<12-hex>` derived from the absolute
workspace root (e.g. `Motive-9f2c1a4b7e8d`), so the same workspace always maps
to the same namespace and transcripts of different workspaces never mix. The
workspace itself never holds Motive session records or recovery state. An
empty workspace root resolves to the current working directory.

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
enter          run (busy: steer/queue)   alt+e    cycle reasoning effort
shift+enter    newline                   ctrl+r   session picker
ctrl+d         git diff view             ctrl+t   toggle tools
alt+a          attach file               ctrl+y   paste clipboard image
ctrl+\         cycle steer/queue (busy)  ctrl+/   toggle help
ctrl+k / ctrl+j                         scroll up / down
ctrl+shift+k / ctrl+shift+j             page up / down
up / down      prompt history            alt+m    model picker
ctrl+l         clear input
esc            stop run (busy) / close help
ctrl+c         quit
```

`alt+m` opens the model picker: configured providers are listed as horizontal
tabs on top, and the active tab's models below. `←`/`→` or `h`/`l` switch the
provider tab (wrapping), `↑`/`↓` selects a model, `enter` applies it — this
switches the live endpoint, model, and the provider's sampling settings for
subsequent turns. Endpoints without a working `/models` endpoint fall back to
the provider's configured model list.

macOS 터미널의 "natural text editing" 프로필이 `cmd+backspace`→`ctrl+u`,
`cmd+←`→`ctrl+a`, `cmd+→`→`ctrl+e`를 보내기 때문에, 이 세 readline 키는
오버레이 바인딩에 쓰지 않는다(각각 alt+u, alt+a, alt+e로 대체).
`MOTIVE_KEY_<NAME>`으로 언제든 재바인딩할 수 있다.

`alt+a` opens a file browser rooted at the workspace: type a path (absolute,
`~`, or relative) or filter the current directory by name, then `enter` to
attach. `ctrl+y` grabs an image from the clipboard (macOS via osascript,
Linux via `wl-paste`/`xclip`); if the terminal supports inline images
(iTerm2, kitty, ghostty, WezTerm) a thumbnail preview is shown next to the
pending attachments above the input box. Attachments are carried into the
session transcript and can be submitted without any prompt text (a bare image
alone is a valid turn).

### Steer / queue policy

While a run is in progress, `enter` does not start a new turn; it submits to
the running execution in one of two modes (cycled with `ctrl+\`):

- **steer** injects the message into the *current* run. The runtime drains it
  at the next step boundary — right after the tool results/observation block
  of the running step, or instead of finishing when the model has produced its
  final answer — so the model sees the steer as a new user message and keeps
  going in the same context.
- **queue** appends the message to a FIFO. Nothing changes for the current
  run; each queued message is processed as a fresh turn, one at a time, after
  the current turn ends (and after any earlier queued turns).

The steer path is a bounded channel (capacity 16). Submitting while it is full
does not block: the message silently falls back to the queue instead, so input
is never lost. `esc` / `ctrl+c` while busy drops the queue (queued turns must
not start just to be stopped by the exit); a run interrupted by an error or
stop still continues with any queued turns that were submitted before it.

## Tools exposed to the model

- `read_file`, `write_file`, `edit_file`, `delete_file`
- `list_files`, `glob`, `search_files`
- `shell`
- `web_search`, `web_fetch`
- `git_status`, `git_diff`, `git_log`

The tool set is intentionally concrete. There is no planner, sub-agent layer, memory manager, or plugin registry in the execution path.

## Design rationale

See [docs/design-rationale.md](docs/design-rationale.md) (English) / [docs/design-rationale.ko.md](docs/design-rationale.ko.md) (한국어) for a persuasive explanation of Motive's design decisions:

- Why **stateless** (fresh context per request)
- How **context** is determined
- Why **no context compaction** (decomposition over compression)
- Motive's unique system advantages

## Design docs

- [docs/stable-semantics.md](docs/stable-semantics.md) — canonical semantics of the current implementation.
- [docs/decomposition.md](docs/decomposition.md) — model-delegated decomposition and unit boundary protocol (Form 0, realized in code).
- [docs/experiment-form0.md](docs/experiment-form0.md) — the Form 0 decomposition experiment record.

## Status

Prototype, but already an end-to-end execution loop. The next work should focus on context quality, execution tracing, Git revision records, and a richer TUI rather than adding framework layers.
