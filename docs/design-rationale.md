# Motive Design Rationale

> **This document explains *why* Motive is built the way it is.**
> Where `stable-semantics.md` records *what the stable meaning is*, this document records the *reasons* and *context* behind that meaning.
> Classification: **[RATIONALE]** — the reasoning behind design decisions. It reflects project judgment, not source code or tests.
>
> Korean version: [docs/design-rationale.ko.md](design-rationale.ko.md)

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

Each iteration of this loop is called a **step**. Steps, elapsed time, and tool-call count are bounded by an **execution budget**. Exceeding the budget interrupts the execution.

---

## 3. Why stateless

### 3.1 The core invariant

> Every user request starts from a **fresh model context**.
> Motive does not rely on unseen chat history to execute a request.

### 3.2 The reason

**The persistent world is the workspace, not the chat history.** Files and Git state are the durable state; the model context is a disposable workbench.

### 3.3 Session recovery

A session transcript is a **record, not context**. After an interrupted run, a later execution can inspect the available evidence and decide what to do. Motive does not prescribe continuation, retry, replanning, or any other recovery strategy.

---

## 4. How context is determined

`Runtime.ContextBlock()` provides the model with the workspace location, Git state, a bounded file listing, and the applicable system guidance. The model inspects files directly with its tools when needed.

The initial context deliberately does not contain the entire workspace or prior conversational history.

---

## 5. Why it is written this way

### 5.1 Deliberate simplicity

Motive intentionally does not include agent frameworks, plugin systems, planner layers, sub-agents, or memory managers.

The model is responsible for reasoning and choosing how to perform the work. Motive provides the execution environment and does not decide what the model should do next.

### 5.2 Git as durable evidence

Workspace + Git provide a visible reference point for an execution and a durable record of resulting changes. They can be inspected by later executions without reconstructing hidden model context.

---

## 6. Why no compaction

Compaction only raises the ceiling of a single execution; it does not make hidden context a reliable persistent state.

Motive therefore does not compact or restore prior model context. Each execution starts fresh. When an execution ends or is interrupted and further work is possible, a later execution can inspect the workspace, Git state, and available session evidence and decide for itself what to do.

Motive does **not** determine whether work should be split, how it should be split, or whether the next execution should continue, discard, retry, re-plan, or ask the user.

### 6.1 Fresh re-judgment beats stale summaries

> **A summary carries the summarizer's bias. Fresh re-judgment reads the available evidence directly.**

---

## 7. What makes Motive distinctive

### 7.1 Session = transcript, not context

A session is a JSONL record of observable execution events. It is not restored as hidden model context. The model may inspect the available session evidence through `session_log` when appropriate.

### 7.2 Runtime observation

During an execution, Motive reports mechanical facts such as step usage, tool usage, failures, elapsed time, and revision changes. These observations expose execution pressure without deciding the work strategy.

### 7.3 Git revision records

Executions record their observable revision boundary so later work can inspect what changed.

### 7.4 User intervention

The user may steer an active execution or queue later input. The model still determines how that input affects the work.

### 7.5 Bounded execution

Steps, elapsed time, and tool calls are bounded as safety limits. These are execution boundaries, not semantic task boundaries and not a model reasoning policy.

---

## 8. Summary of strengths

| Aspect | Motive |
|---|---|
| State management | workspace + Git as durable evidence; fresh model context per execution |
| Session persistence | transcript record, not restored context |
| Planning | entirely model-driven |
| Context management | no compaction or hidden history restoration |
| Execution budget | bounded steps, time, and tool calls |
| Execution isolation | observable workspace and Git revision boundaries |
| Recovery | a later execution can inspect evidence and decide what to do |
| Scaling | independent bounded executions; no runtime-prescribed decomposition strategy |

The central principle is unchanged:

> **Motive preserves execution boundaries and observable evidence. It does not reinterpret the user's work or prescribe the model's strategy for carrying it out.**
