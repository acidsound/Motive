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

Tool results are fed back into the model as tool messages. A tool failure is represented to the model as an `ERROR:` result rather than silently terminating the execution. **[SOURCE]**

The exact safety, timeout, and authorization semantics of every tool are implementation-specific and are **[UNKNOWN]** here unless separately established by tests or source review.

## 6. Git and revisions

Git state is part of the observable workspace state. The runtime records the Git revision at execution start and at execution completion in its trace events. **[SOURCE]**

A successful execution may therefore be associated with a base revision and a result revision. **[SOURCE]**

Whether every externally visible workspace mutation must result in a commit is **[UNKNOWN]**; the current runtime does not establish such an invariant.

## 7. Reasoning effort

Reasoning effort is a runtime model parameter. The supported normalized values in the current client are:

```text
none, low, medium, high, xhigh, max
```

**[SOURCE]**

The current default is `low`. **[SOURCE]**

The configured effort is sent to the model request both as `reasoning_effort` and as the `reasoning_effort` chat-template argument. **[SOURCE]**

During execution, the runtime may temporarily escalate to `xhigh` after a tool failure, then restore the configured default effort on a subsequent turn. **[SOURCE]**

This escalation is a recovery mechanism, not a change to the user's configured default. **[SOURCE][DECISION]**

The precise mapping between these symbolic effort levels and model-specific compute is provider/model dependent and is **[UNKNOWN]** at the Motive semantic layer.

## 8. Temperature

Temperature is an explicit model request parameter. The configured value is serialized even when it is `0`. **[SOURCE]**

The current default is `0.6`. **[SOURCE]**

The semantic meaning of a particular temperature value remains that of the underlying model server; Motive does not define model-specific decoding behavior. **[SOURCE]**

## 9. Execution budget and termination

Every execution has a maximum step budget and a maximum execution duration. The current defaults are 32 steps and 30 minutes. **[SOURCE]**

Exceeding the step budget terminates execution with an `execution budget exceeded` error. **[SOURCE]**

Exceeding the duration causes the execution context to be cancelled. **[SOURCE]**

The execution budget is a safety boundary on the runtime loop. It is not equivalent to a model reasoning budget. **[SOURCE][DECISION]**

A finer-grained per-turn reasoning/output budget and its relationship to the global execution budget are **[UNKNOWN]** at the current stable-semantics level.

## 10. Failure and recovery

An empty user request is rejected before execution. **[SOURCE]**

A model request failure terminates the execution with an error. **[SOURCE]**

A tool failure does not by itself terminate the execution; the error is returned to the model, allowing the model to recover or choose another action. **[SOURCE]**

A tool failure currently causes the next model turn to use `xhigh` reasoning effort. **[SOURCE]**

The broader policy for repeated failures, recovery budgets, and escalation limits is **[UNKNOWN]** beyond the current implementation.

## 11. Observation and telemetry

The runtime exposes trace events containing execution state such as:

- step number and maximum steps
- message count
- tool name and tool-call count
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

The model client currently understands llama-server-style timing information including prompt tokens/time, predicted tokens/time, cache tokens, and speculative-draft counters when supplied in the response. **[SOURCE]**

These telemetry fields describe execution; they are not yet defined as a self-observation control policy. **[DECISION]**

A stable semantic contract for automated self-observation, anomaly detection, or adaptive execution based on telemetry is **[UNKNOWN]**.

## 12. Self-observation

Motive currently has execution telemetry and an observation structure inside the runtime, but a complete autonomous self-observation policy is not yet established. **[SOURCE][UNKNOWN]**

Therefore, self-observation must not be treated as an existing stable capability merely because telemetry exists.

## 13. Self-modification

Motive can expose file-writing, deletion, shell, and Git operations to the model, so the model can modify its workspace when the request and tool permissions allow it. **[SOURCE]**

A formal self-modification protocol, including semantic invariants, approval boundaries, rollback requirements, or automatic verification policy, is not yet established. **[UNKNOWN]**

Until such a protocol exists, self-modification must not be considered a stable autonomous capability beyond ordinary model-directed workspace modification.

## 14. Stable invariants

The following are the current semantic invariants:

1. **Fresh request context:** a user request is executed from a newly constructed model context rather than assumed prior chat history. **[SOURCE]**
2. **Workspace as persistent world:** files and Git state are the persistent execution state. **[SOURCE]**
3. **Model-centric execution:** the model performs reasoning/planning while Motive provides execution and tools. **[SOURCE]**
4. **Concrete tool loop:** tool calls and their results form the iterative execution mechanism. **[SOURCE]**
5. **Bounded execution:** an execution cannot exceed its configured step/time budget. **[SOURCE]**
6. **Observable revision state:** execution records its base and resulting Git revisions. **[SOURCE]**
7. **Explicit reasoning configuration:** reasoning effort is a runtime parameter with `low` as the current default. **[SOURCE]**
8. **Recovery escalation is temporary:** failure-driven `xhigh` escalation does not replace the configured default effort. **[SOURCE]**
9. **Telemetry is observational:** execution telemetry describes what happened and is not itself evidence that autonomous adaptation is implemented. **[SOURCE]**

## 15. Explicitly unresolved semantics

These areas must remain marked unresolved until verified by source, tests, or explicit project decisions:

- exact per-turn reasoning/output budget semantics
- relationship between reasoning budget and global execution budget
- TUI controls and their persistent/configuration semantics
- complete recovery/escalation policy for repeated failures
- tool-specific authorization and safety invariants
- formal self-observation policy
- formal self-modification protocol
- rollback semantics for autonomous modifications
- whether and when an execution must create a Git commit
- cross-session/project memory beyond the repository state

## 16. Context reconstruction rule

This document is intended to be sufficient to reconstruct the semantic identity and current execution model of Motive without relying on the conversation that produced it.

A future change that alters a statement in this document must either:

1. preserve the existing semantic invariant and update only implementation evidence, or
2. explicitly change the stable semantics and record that change as a project decision.

The document must not silently convert planned behavior, model assumptions, or observations into stable semantics.
