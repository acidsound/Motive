# Motive Design Rationale

> **This document explains *why* Motive is built the way it is.**
> Where `stable-semantics.md` records *what the stable meaning is*, this document records the *reasons* and *context* behind that meaning.
> Classification: **[RATIONALE]** — the reasoning behind design decisions. It reflects project judgment, not source code or tests.
>
> 한국어 버전: [docs/design-rationale.ko.md](design-rationale.ko.md)

---

## 1. What Motive is

Motive is a **model-centric software execution runtime**.

- The **model performs the reasoning and planning**.
- **Motive supplies the workspace, tools, execution, and revision tracking**.

That is all. Agent frameworks, plugin systems, planner layers, sub-agents, and memory managers are **deliberately** absent.

---

## 2. The execution loop — how it works

```
user request
  → context compilation (ContextBlock: system prompt + workspace + Git state)
  → OpenAI-compatible model (/v1/chat/completions)
  → model response (may include tool calls)
  → tool execution → results appended to the message list
  → runtime observation appended
  → (repeat: model → tools → observation)
  → response without tool calls → returned as final response
```

**[SOURCE: internal/runtime/runtime.go, Execute method]**

Each iteration of this loop is called a **step**.
Steps, elapsed time, and tool-call count are bounded by an **execution budget**.
Exceeding the budget aborts the execution.

---

## 3. Why stateless

### 3.1 The core invariant

> Every user request starts from a **fresh model context**.
> Motive does not rely on unseen chat history to execute a request.

**[SOURCE: stable-semantics.md §3; runtime.go Execute — a fresh messages slice is built on every call]**

### 3.2 The reason

**The persistent world is the workspace, not the chat history.**

Agent frameworks typically treat conversation history as the persistent state.
Motive does the opposite: **files and Git state are the real persistent state; the model context is a disposable workbench.**

```
Traditional agents:  context = memory / summaries / history
Motive:              context = one-shot workbench, workspace = persistent store
```

### 3.3 Advantages

| Advantage | Description |
|-----------|-------------|
| **Reproducibility** | Same request → same initial context. No contamination from previous runs. |
| **Determinism** | Same Git HEAD and workspace state → always the same initial context. |
| **No context contamination** | No errors, duplicates, or contradictory instructions from prior turns. Each run starts from its own reasoning. |
| **No hidden state** | No state the runtime cannot see. Crash and restart = completely fresh start. |
| **Scalability** | Multiple executions can run in parallel without context conflicts. |

### 3.4 Example: session recovery

A session JSONL transcript is a **record, not context**.
An interrupted run is recovered by the model itself, which reads the tail of the transcript via the `session_log` tool.
The runtime holds no in-memory state and does not force recovery.

---

## 4. How context is determined

### 4.1 Context compilation (ContextBlock)

`Runtime.ContextBlock()` builds the initial system message from:

```
system: You are Motive, ...
Workspace: <workspace root path>
Git HEAD: <current commit hash>
Git status: <output of git status --short --branch>
Workspace files: <file listing (capped at 6000 bytes, excluding .git and node_modules)>
Session: <session id>  (only when running in the TUI)
```

**[SOURCE: runtime.go ContextBlock]**

### 4.2 Bounded file listing

The workspace file listing is capped (truncated) at 6000 bytes.
This keeps enough information for the model to grasp "what exists" while preventing the context from exploding on huge repositories.

**[SOURCE: runtime.go truncateUTF8]**

### 4.3 The model inspects directly

The initial context does not include the entire workspace contents.
The model inspects directly with `read_file`, `glob`, `search_files`, and `list_files` when needed.

**[SOURCE: stable-semantics.md §3]**

---

## 5. Why it is written this way

### 5.1 Deliberate simplicity

Motive **intentionally** does not include:

- **Agent frameworks** — LangChain, CrewAI, AutoGen, etc.
- **Plugin systems**
- **Planner layers**
- **Sub-agents**
- **Memory managers** — RAG, vector stores, summarization modules

The reasoning behind this decision:

1. **The model is the best planner there is.**
   Adding a planner layer constrains the model's reasoning ability and adds dependencies.

2. **More layers mean more failure points.**
   Each layer introduces its own error modes, latency, and context contamination.

3. **Motive is an execution environment, not an abstraction layer.**
   Its purpose is to give the model direct tools over files, shell, web, and Git.

### 5.2 Model-centric architecture

```
┌─────────────────────────────────────────┐
│              MODEL (planning, reasoning)│
│  tool call ──► execute ──► observe ──►  │
└─────────────────────────────────────────┘
│           MOTIVE (runtime)              │
│  workspace | shell | web | Git         │
└─────────────────────────────────────────┘
```

The model calls tools; Motive executes them.
Motive does not judge "what the model should do next." That judgment belongs entirely to the model.

### 5.3 Git as the backbone of persistence

Workspace + Git provide:

- **The reference point of the initial context** (Git HEAD)
- **A durable record of changes** (base revision → result revision)
- **The coordination medium between executions** (workspace + revision delta)

---

## 6. Why no compaction

### 6.1 The problem: compaction is not a root solution

Context trimming or compaction only **raises the ceiling of a single execution**.

```
Track A (context lifecycle):  extend the lifetime of one context
Track B (decomposition):      split into multiple independent executions
```

Motive chose **Track B**.
An EPIC task does not fit in a single context, no matter how well compressed.

**[SOURCE: docs/model-delegated-decomposition.md §2]**

### 6.2 Concrete problems with compaction

1. **Risk of exposing hidden reasoning**
   Summarizing/compressing context risks losing or distorting the model's reasoning.
   Motive only exposes the `[execution-state]` observation to the model;
   reasoning content is shown separately and is never compressed.

2. **Loss of reproducibility — "what was cut"**
   Compaction is irreversible.
   The original messages remain in the transcript, but the context the model actually saw can no longer be reconstructed.

3. **Compaction is itself another problem domain**
   Deciding which messages to keep/remove and how to summarize requires
   model-level judgment — which conflicts with the principle that "the model judges."

### 6.3 The alternative: fresh context per unit

Large tasks are split into **multiple fresh contexts**. Each unit has:

- its own execution budget
- its own timeout
- its own Git revision range
- its own session

Coordination between units happens through the workspace + Git delta.
This is the core of `docs/epic-boundary-protocol.md`.

### 6.4 Fresh re-judgment beats stale summaries

Compaction summarizes past context, so the current model sees a "summarized past."
The fresh-context approach re-judges **freshly** from the unit's `brief.md` + Git diff.

> **A summary carries the summarizer's bias.
> Fresh re-judgment reads the original evidence (brief + diff) directly.**

---

## 7. What makes Motive unique

### 7.1 Session = transcript, not context

- **A session is a JSONL file.**
- It only appends user/assistant/error/stopped entries.
- It does **not** store model context. Messages from previous runs are not restored.
- **[SOURCE: internal/session/session.go]**

The session log is readable by the model:

- `session_log` tool → reads the tail of the session
- `motive` tool → Motive's own operating guidance

This is the **recovery mechanism**: read from the transcript where the previous run left off and continue.

### 7.2 Runtime observation

After tool calls, Runtime appends an `[execution-state]` message to the model context:

```
[execution-state]
step=12/64 tools=34/128 failures=0 context=45231 peak=89210
elapsed=2m30s effort=low rev=abc1234→def5678
```

The observation is compressed to **3–4 lines** so the model can perceive budget pressure, failures, and context growth.

**[SOURCE: runtime.go Observation.Format]**

### 7.3 Context accounting

Before each step, Runtime estimates the context token count:

- bytes/4 heuristic (same as the model client)
- tracks max/peak estimates
- records server-provided prompt_n separately
- reports Overflow when a configured maximum is exceeded

**[SOURCE: runtime.go ContextAccounting]**

### 7.4 Reasoning effort

Five levels: `low` / `medium` / `high` / `xhigh` / `max`.
Default is `low`. On tool failure it temporarily escalates to `xhigh`, then returns.

**[SOURCE: model/client.go normalizeEffort, runtime.go toolFailed branch]**

### 7.5 Git revision records

Every execution records its start-time `base_revision` and end-time `result_revision`.
These appear in TraceEvents, session entries, and runtime observations.

### 7.6 Steer / queue policy

The user can intervene while a run is in progress:

- **Steer**: inject a message into the current run's context
- **Queue**: FIFO of messages processed after the current run ends

**[SOURCE: runtime.go takeSteer, README.md Steer/queue policy]**

### 7.7 Bounded execution

| Resource | Default | Hard cap |
|----------|---------|----------|
| Max steps | 64 | 256 |
| Max elapsed | 30 min | 120 min |
| Max tool calls | 128 | 1024 |

This is a safety boundary, not a model reasoning budget.
**[SOURCE: config.go, runtime.go]**

---

## 8. Summary of strengths

### 8.1 Traditional agent frameworks vs Motive

| Aspect | Traditional agents (LangChain, CrewAI, etc.) | Motive |
|--------|----------------------------------------------|--------|
| **State management** | context history + memory modules | only workspace + Git persist |
| **Session persistence** | conversation history saved/restored | JSONL transcript (no context) |
| **Planning** | planner chains/layers | the model plans with its own reasoning |
| **Tools** | plugin/toolkit ecosystems | a fixed set of 14 concrete tools |
| **Context management** | windowing/summarization/compaction | fresh per execution (no compaction) |
| **Execution budget** | none or separately configured | triple guard: steps/time/tool calls |
| **Execution isolation** | usually unclear (hidden) | explicit via Git rev ranges |
| **Recovery** | restore context from saved session | model reads `session_log` and recovers itself |
| **Scaling** | add modules/chains/agents | EPIC decomposition (fresh context per unit) |

### 8.2 Motive's specific strengths

1. **The model is the best planner** — full use of model reasoning with no extra layers.
2. **Transparency** — Git revisions, session transcripts, and trace events record every execution.
3. **Reproducible runs** — same workspace + Git HEAD → same initial context.
4. **Safe execution boundaries** — triple step/time/tool budget protects model runs.
5. **Failure resilience** — tool failure is information, not termination; escalation to `xhigh`; recovery via `session_log`.
6. **Runtime observation** — the model perceives its own execution state (context pressure, budget usage, failure rate).
7. **User intervention** — steer/queue lets the user redirect a run while it is in progress.

---

## 9. Frontier

- **Decomposition**: handle large tasks via the EPIC boundary protocol.

**[SOURCE: docs/model-delegated-decomposition.md, docs/epic-boundary-protocol.md]**

---

## Appendix: design decision summary

| Decision | Reason | Evidence |
|----------|--------|----------|
| **Stateless context** | reproducibility, contamination prevention, scalability | runtime.go Execute |
| **Workspace + Git = persistent state** | files and revisions are the only real state | stable-semantics.md §3 |
| **No agent framework** | the model is the best planner; extra layers are failure points | runtime.go, systemPrompt |
| **No compaction** | Track B (fresh context per unit) is the root solution | model-delegated-decomposition.md §2 |
| **Runtime observation** | the model must perceive its own execution to optimize it | runtime.go Observation.Format |
| **Session = transcript** | a record instead of context; re-judge rather than reconstruct | session.go |
| **Bounded execution** | safety boundary, separate from the model's reasoning budget | runtime.go, config.go |
| **Tool failure ≠ termination** | gives the model a chance to recover | runtime.go toolFailed |
| **xhigh escalation** | induces focused reasoning after failure | runtime.go effort switching |
