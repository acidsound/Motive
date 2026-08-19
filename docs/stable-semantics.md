# Motive Stable Semantics

> Canonical semantic description of the current Motive system.
>
> This document is intentionally narrower than an architecture document. It records observable meaning and project invariants, not implementation details that may change without changing Motive's meaning.

## 1. Status and evidence

Each statement is classified by its evidence:

- **[SOURCE]** — directly established by the current source code.
- **[TEST]** — established by an executable test or verification command.
- **[DECISION]** — an explicit project design decision.
- **[OBSERVED]** — established by an actual runtime observation.
- **[UNKNOWN]** — not sufficiently established yet.

If a statement is not supported by one of these forms of evidence, it must not be treated as stable semantics.

## 2. Identity

Motive is a small, model-centric software execution runtime. The model is the reasoning and planning component; Motive supplies the workspace, tools, execution, and revision-aware environment. **[SOURCE][DECISION]**

Motive deliberately does not depend on an agent framework, plugin system, planner layer, sub-agent layer, or memory manager in the execution path. **[SOURCE]**

Motive communicates with an OpenAI-compatible `/v1/chat/completions` endpoint. **[SOURCE]**

## 3. Context and persistence

Each user request starts with a fresh model context. Motive must not depend on unseen chat history to execute a request. **[SOURCE][DECISION]**

The persistent world of an execution is the workspace, its files, and its Git state rather than prior chat history. **[SOURCE]**

The runtime compiles initial context from the system instruction, workspace root, Git HEAD, Git status when available, and a workspace file listing. The file listing is bounded before being placed in the context. **[SOURCE]**

The model is expected to inspect the workspace when necessary rather than receiving the entire workspace implicitly. **[SOURCE]**

## 4. Execution model

The fundamental execution loop is:

```text
user request
  -> context compilation
  -> model request
  -> model response
  -> tool execution / observation
  -> model request
  -> ...
  -> final response
```

**[SOURCE]**

A model response without tool calls terminates the execution and becomes the final response, provided the response contains non-empty content. **[SOURCE]**

A model response containing tool calls causes those calls to be executed and their results appended to the model context before the next model turn. **[SOURCE]**

Multiple tool calls returned in one model response are executed within the same execution turn. **[SOURCE]**

After tool execution, Motive appends a compact runtime observation to the model context so the model can see budget usage, failures, latency, reasoning effort, elapsed time, and revisions without inferring them from tool output. **[SOURCE][DECISION]**

## 5. Tools

The model has direct access to concrete workspace, shell, web, and Git operations. The currently documented tool set is:

- `read_file`
- `write_file`
- `delete_file`
- `list_files`
- `search_files`
- `shell`
- `web_search`
- `git_status`
- `git_diff`

**[SOURCE]**

Tool results are fed back to the model as tool messages. A tool failure is represented to the model as an `ERROR:` result rather than silently terminating the execution. **[SOURCE]**

The exact safety, timeout, and authorization semantics of every tool are implementation-specific unless separately established by tests or source review.

## 6. Git and revisions

Git state is part of the observable workspace state. The runtime records the Git revision at execution start and at execution completion in its trace events and runtime observations. **[SOURCE]**

A successful execution may therefore be associated with a base revision and a result revision. **[SOURCE]**

Whether every externally visible workspace mutation must result in a commit is **[UNKNOWN]**; the current runtime does not establish such an invariant.

## 7. Configuration and providers

Motive supports named providers configured via a TOML file (default path `~/.config/motive/config.toml`, overridable with `MOTIVE_CONFIG`). Each provider specifies a base URL, a default model, optional additional model IDs, an optional API key, and an optional reasoning effort. **[SOURCE]**

The active provider is selected by the `default_provider` field in the config file; if absent, the first listed provider is active. **[SOURCE]**

Environment variables (`MOTIVE_BASE_URL`, `MOTIVE_MODEL`, `MOTIVE_API_KEY`, `MOTIVE_REASONING_EFFORT`) always override the active provider's file values, preserving the historical single-endpoint behavior. **[SOURCE]**

Without a config file, environment variables form a single implicit "default" provider. **[SOURCE]**


The state directory for session storage defaults to `~/.motive` and is overridable with `MOTIVE_STATE_DIR`. **[SOURCE]**

## 8. Session persistence

Motive persists conversation transcripts as append-only JSONL files under the state directory. Each entry records a timestamp, role (user/assistant/error), content, optional reasoning, optional tool calls, base/result Git revisions, and elapsed time. **[SOURCE]**

A new session is created on the first user submission in the TUI. Subsequent turns append to the same session file. **[SOURCE]**

The TUI provides a session picker (`Ctrl+R` or `--tui -r`) that lists prior sessions by ID, creation time, preview text, revision, and tool-call count. Selecting a session restores its transcript into the TUI view. **[SOURCE][TEST]**

Session persistence is a transcript record, not a model context. Resuming a session does not restore model context; the next execution still starts with a fresh model context per the invariant in §3. **[SOURCE][DECISION]**

## 9. Reasoning effort

Reasoning effort is a runtime model parameter. Motive accepts the full standard vocabulary — `low`, `medium`, `high`, `xhigh`, `max` — and passes the selected value through to the model server; a provider that does not support a level rejects it rather than Motive silently substituting a different level. Only unknown or empty values fall back to `low`. **[SOURCE][DECISION]**

The supported normalized values are:

```text
low, medium, high, xhigh, max
```

**[SOURCE][DECISION]**

The current default is `low`. **[SOURCE][DECISION]**

The configured effort is sent to the model request both as `reasoning_effort` and as the `reasoning_effort` chat-template argument. **[SOURCE]**

During execution, the runtime may temporarily escalate to `xhigh` after a tool failure, then restore the configured default effort on a subsequent turn. **[SOURCE][DECISION]**

The TUI exposes the five recognized levels and cycles them with `Ctrl+E`. **[SOURCE]**

The precise mapping between these symbolic effort levels and model-specific compute is provider/model dependent and remains **[UNKNOWN]** at the Motive semantic layer.

## 10. Temperature

Temperature is an explicit model request parameter. The configured value is serialized even when it is `0`. **[SOURCE]**

The current default is `0.6`. **[SOURCE][DECISION]**

The semantic meaning of a particular temperature value remains that of the underlying model server; Motive does not define model-specific decoding behavior. **[SOURCE]**

## 11. Execution budget and termination

Every execution has three bounded resources: maximum model/tool-loop steps, maximum elapsed duration, and maximum tool calls. The defaults are 32 steps, 30 minutes, and 128 tool calls. **[SOURCE]**

Runtime configuration is clamped to hard upper bounds of 256 steps, 120 minutes, and 1024 tool calls. **[SOURCE]**

Exceeding any budget terminates execution with an `execution budget exceeded` error. Duration exhaustion cancels the execution context. **[SOURCE]**

The execution budget is a safety boundary on the runtime loop. It is not equivalent to a model reasoning/thinking budget. **[SOURCE][DECISION]**

A provider-specific per-turn reasoning budget remains outside Motive's execution budget semantics. **[SOURCE][DECISION]**

## 12. Failure and recovery

An empty user request is rejected before execution. **[SOURCE]**

A model request failure terminates the execution with an error. **[SOURCE]**

A tool failure does not by itself terminate the execution; the error is returned to the model, allowing the model to recover or choose another action. **[SOURCE]**

A tool failure currently causes the next model turn to use `xhigh` reasoning effort. **[SOURCE]**

The configured default is restored after the recovery turn. **[SOURCE]**

Repeated-failure escalation beyond this one-turn recovery policy is **[UNKNOWN]**.

## 13. Observation and telemetry

The runtime exposes trace events containing execution state such as:

- step number and maximum steps
- tool name and per-execution tool-call count
- tool-result size
- request/response sizes
- estimated input tokens
- model latency
- total execution elapsed time
- server-side prompt/prediction timing when supplied by the model server
- reasoning effort
- base and result Git revisions
- execution errors

**[SOURCE]**

The model client understands llama-server-style timing information including prompt tokens/time, predicted tokens/time, cache tokens, and speculative-draft counters when supplied in the response. **[SOURCE]**

Telemetry is observational data. It does not by itself imply that Motive has learned a policy or autonomously optimized itself. **[DECISION]**

## 14. Self-observation

Self-observation is now a defined runtime capability: after each tool-bearing turn, Motive provides the model with a bounded, structured observation containing execution budget usage, tool failures, recent model latency/prediction timing, current reasoning effort, elapsed/remaining time, and Git revisions. **[SOURCE][DECISION]**

The observation is deliberately compact and does not include hidden chain-of-thought. It describes runtime state rather than exposing private reasoning content. **[DECISION]**

Automatic anomaly detection or policy learning from these observations is not implemented and remains **[UNKNOWN]**.

## 15. Self-modification

Motive can expose file-writing, deletion, shell, and Git operations to the model, so the model can modify its workspace when the request and tool permissions allow it. **[SOURCE]**

For workspace modification, the runtime instructs the model to verify resulting state before claiming success and not to claim commits, pushes, tests, or builds without confirming tool output. **[SOURCE][DECISION]**

Motive does not automatically commit or push model changes. The model must invoke the corresponding Git operations when explicitly asked and when the available tools permit them. **[SOURCE]**

Rollback, approval workflows, and automatic self-rewrite policies are **[UNKNOWN]** and are intentionally not part of the current self-modification contract.

## 16. Validated current state

The project has reached a verified baseline suitable for continuing implementation:

- `gofmt -l .` is clean after formatting `internal/model/client.go`. **[TEST]**
- `go test ./...` passes. **[TEST]**
- `go vet ./...` passes. **[TEST]**
- `git diff --check` passes. **[TEST]**
- A GitHub Actions run subsequently passed after the formatting correction. **[OBSERVED]**
- A real Motive execution using `MOTIVE_TEMPERATURE=0.6` and `MOTIVE_REASONING_EFFORT=low` successfully completed a code-review-and-fix task, including verification and Git commit/push when explicitly requested. **[OBSERVED]**

The llama-server benchmark phase is considered complete. Tests across `low`, `medium`, and `xhigh` at the tested context sizes established that reasoning effort affects latency, but did not reproduce the 60–80 second Motive stalls as a simple tool-calling multiplier. **[OBSERVED]**

The benchmark is therefore retained as diagnostic tooling, not as the next development focus. Further investigation of long executions should use Motive's own execution telemetry and request semantics rather than expanding the standalone benchmark matrix. **[DECISION]**

The observed long Motive turns with thousands of predicted reasoning tokens establish a concrete investigation target, but do not by themselves establish the root cause. The root cause remains **[UNKNOWN]** until reproduced and localized with Motive telemetry.

## 17. Terminal UI

The TUI is a first-class interface for interactive execution. It streams model output live, renders lightweight markdown (headings, code blocks, lists, bold, inline code, blockquotes, horizontal rules), and displays reasoning in a dimmed style. **[SOURCE]**

The TUI supports scrollback, prompt history navigation, transcript bookmarks, tool-call collapsing, and a git diff overlay. **[SOURCE]**

All key bindings are rebindable via `MOTIVE_KEY_<NAME>` environment variables. Defaults follow a jcode-inspired layout. **[SOURCE]**

The TUI status bar displays the active model, reasoning effort, step/tool/elapsed counters, execution budget, Git revision range, and session ID. **[SOURCE]**

## 18. Stable invariants

1. **Fresh request context:** a user request is executed from a newly constructed model context rather than assumed prior chat history. **[SOURCE]**
2. **Workspace as persistent world:** files and Git state are the persistent execution state. **[SOURCE]**
3. **Model-centric execution:** the model performs reasoning/planning while Motive provides execution and tools. **[SOURCE]**
4. **Concrete tool loop:** tool calls and their results form the iterative execution mechanism. **[SOURCE]**
5. **Bounded execution:** an execution cannot exceed its configured step, duration, or tool-call budget. **[SOURCE]**
6. **Observable revision state:** execution records its base and resulting Git revisions. **[SOURCE]**
7. **Explicit reasoning configuration:** reasoning effort is a runtime parameter with `low` as the current default and `low/medium/high/xhigh/max` as the recognized normalized levels, passed through to the provider. **[SOURCE][DECISION]**
8. **Recovery escalation is temporary:** failure-driven `xhigh` escalation does not replace the configured default effort. **[SOURCE]**
9. **Telemetry is observational:** execution telemetry describes what happened and is not itself evidence that autonomous adaptation is implemented. **[DECISION]**
10. **Runtime self-observation is bounded:** model-visible observations contain execution metadata, not hidden reasoning content. **[DECISION]**
11. **Self-modification is model-directed:** workspace changes occur through the existing tools and are not silently committed or pushed by the runtime. **[SOURCE][DECISION]**
12. **Session persistence is transcript-only:** JSONL session files record what happened but do not restore model context; each execution still starts fresh. **[SOURCE][DECISION]**
13. **Provider switching is config-file only:** endpoint/model changes happen only via the config file; the runtime never switches providers autonomously. **[DECISION]**
14. **Validated baseline:** formatting, tests, vetting, and diff checks are expected to remain green before advancing to the next semantic layer. **[TEST][DECISION]**

## 19. Explicitly unresolved semantics

These areas must remain marked unresolved until verified by source, tests, or explicit project decisions:

- provider-specific reasoning/thinking budget semantics
- complete recovery/escalation policy for repeated failures
- tool-specific authorization and safety invariants
- automatic anomaly detection or policy learning from telemetry
- rollback semantics for autonomous modifications
- whether and when an execution must create a Git commit
- cross-session/project memory beyond the repository state (session persistence is a transcript record, not learned memory)
- root cause of unusually long reasoning generations in real Motive executions
- whether the current self-observation data is sufficient for reliable anomaly detection

## 20. Next semantic frontier

The next work should proceed in this order:

1. **Execution budget semantics:** make the distinction between runtime loop budget and per-turn model reasoning budget explicit and observable.
2. **Reasoning telemetry:** expose enough bounded telemetry to identify abnormal reasoning generation without exposing hidden chain-of-thought.
3. **TUI reasoning control:** retain `low` as the default and allow deliberate user changes among `low`, `medium`, `high`, `xhigh`, and `max`.
4. **Recovery policy:** verify and constrain failure-driven escalation and restoration behavior.
5. **Self-observation:** use the bounded runtime observations to detect execution anomalies before introducing autonomous policy changes.
6. **Self-modification:** only after observation semantics are stable, evaluate model-directed modification policies and safeguards.

This ordering is a project decision, not a claim that later capabilities are already implemented. **[DECISION]**

## 21. Context reconstruction rule

This document is intended to be sufficient to reconstruct the semantic identity and current execution model of Motive without relying on the conversation that produced it.

A future change that alters a statement in this document must either:

1. preserve the existing semantic invariant and update only implementation evidence, or
2. explicitly change the stable semantics and record that change as a project decision.

The document must not silently convert planned behavior, model assumptions, or observations into stable semantics.
