# Model-Delegated Decomposition & Unit Boundary Protocol

> **Status: Form 0 selected and realized in code.** This is the single, merged
> specification that supersedes `docs/epic-boundary-protocol.md` and
> `docs/model-delegated-decomposition.md` (both removed; see §14). It records
> the decomposition design, the minimum-evidence boundary protocol, and the
> concrete result of the Form 0 experiment — together with what the current
> implementation actually does.
>
> Where a claim is verifiable in the repository it is tagged with the same
> evidence classes as `stable-semantics.md` §1: **[SOURCE]** (source code),
> **[TEST]** (executable test), **[OBSERVED]** (real runtime observation), and
> **[UNKNOWN]** / working hypothesis. Design reasoning is **[RATIONALE]**.
>
> **Form 1 (`execute_unit`) is not implemented and is not planned.** It was
> evaluated against four concrete conditions, three of which were closed by
> minimal runtime changes that do not add a tool (§10). What remains is a
> convenience improvement, not a loss recovery.

## 1. Origin and the structural problem

The discussion began from a single runtime error:

```
✖ execution budget exceeded: 32 steps
```

That error is a **symptom**, not the disease. The disease is a structural
property of the execution model:

```
one user request  ==  one model context  ==  one bounded execution
```

An EPIC task does not fit one bounded execution. No amount of intra-execution
context management (trimming, compaction, overflow handling) changes this,
because even a perfectly compacted context is finite and the step/duration/
tool-call budget is still a hard boundary. A compaction merely raises the
ceiling of a single execution; the EPIC task still exceeds that ceiling.
**[RATIONALE]**

## 2. Two orthogonal tracks

These two questions are different axes and must not be conflated:

- **Track A — context lifecycle:** how to keep *one* execution alive longer by
  trimming/compacting context when it grows. This extends a single execution's
  ceiling. It does not resolve the EPIC problem.
- **Track B — decomposition (the intended direction):** how to split a large
  task into multiple *independent, recomposable* bounded executions. This keeps
  each execution small and multiplies them.

Track B is the answer to the `execution budget exceeded` question. Track A is a
supporting sub-problem (each sub-execution still benefits from compact context)
and must not be treated as the main line. Track A remains out of scope here
(context lifecycle actions are not yet implemented per `stable-semantics.md`
§23). **[DECISION][RATIONALE]**

## 3. Guiding principles (faithful to existing invariants)

1. **Decomposition is model behavior, expressed as data — not a runtime
   planner.** Motive deliberately does not depend on a planner layer or
   sub-agent layer in the execution path (`stable-semantics.md` §2). The
   division of an EPIC into subtasks is a model skill, recorded as files in the
   workspace, not as an orchestrator inside Motive. **[SOURCE][DECISION]**

2. **Fresh context per execution is a feature, not a limitation.**
   `stable-semantics.md` §3 states every execution starts from a newly
   constructed model context. Under decomposition this is exactly what we want:
   each subtask is judged from a clean context with only its brief and the
   shared workspace state. **[SOURCE][DECISION]**

3. **Workspace + Git are the coordination medium between executions.**
   The persistent world is files and Git state, not chat history. Between two
   executions the only shared state is the workspace and its revisions.
   **[SOURCE][DECISION]**

4. **Exceptions are handled at the execution boundary, not by growing one
   context.** A sub-execution that exceeds its budget or fails returns a
   *structured result* (what was attempted, what changed, what remains) as data.
   The next execution re-plans the remaining pieces. This is recomposition, not
   infinite retry inside a single growing context. **[RATIONALE]**

5. **Decomposition is fallible model judgment — absorb its errors, do not
   prevent them.** See §8. This is a distinct failure axis from ordinary tool
   failure. **[RATIONALE]**

## 4. The invariant that shapes the protocol

> Runtime must never judge task decomposition or task completion on the model's
> behalf.

This is preserved structurally, not just by convention:

- **Unit selection** (what to do next) is the model writing a `brief.md` file.
  The runtime only executes what the model asks for.
- **Unit completion** (is the EPIC done) is judged by the model ceasing to act
  and emitting a final non-tool response — the existing termination rule in
  `runtime.go` (`if len(msg.ToolCalls) == 0 { return ... }`). The runtime never
  declares "EPIC finished". **[SOURCE]**
- **Task-level correctness** (did the unit satisfy its exit criteria) is judged
  by the model: the parent **re-judges** from `brief.md` + the Git delta in a
  fresh context at recomposition (§7). The unit does not pre-answer this in a
  required `result.md`; a `result.md` note is written only for forward intent
  the diff cannot express. **[RATIONALE][OBSERVED]**
- **What the runtime *does* judge is mechanical and boundary-local only**:
  budget exceeded, tool failures, revision delta, clean loop exit. These are
  facts the model cannot see for itself from outside a discarded context.
  **[SOURCE]**

## 5. Minimum-evidence task artifact protocol

The protocol's job is to leave **minimum execution evidence** so a fresh,
discarded-context execution can reconstitute what a unit did — without replaying
the unit's context. With Workspace + Git as the primary state, that minimum
evidence is:

1. **Git revision delta (`base_rev → result_rev`) + the workspace files at
   `result_rev`** — the primary, durable record of what actually changed. It is
   recorded on every termination path (clean, budget, error, cancel).
   **[SOURCE]**
2. **`brief.md`** — the unit's input contract: what it was supposed to do.
3. **Boundary status** — `completed | budget-exceeded | error` + the consumed
   budget (steps / tool calls / failures). The one fact a model outside the
   discarded context cannot re-derive on its own. **[SOURCE][TEST]**

Everything else — "was the exit criterion met?", "what remains?" — is
**re-judged** by the next fresh execution reading `brief.md` + the diff. A fresh
re-judgment is more trustworthy than a stale self-evaluation left inside a
discarded context, so the protocol does not require the unit to pre-answer these.
**[RATIONALE]**

The structured **boundary status** (§5.3) is the one thing the protocol adds on
top of what already existed. The Git delta and the workspace were already
there; `Runtime.Execute` previously returned only `(string, error)`, so a parent
had to re-run `git` and re-read the session to learn the delta and step count.

### 5.1 `brief.md`

`brief.md` is the input contract for one fresh bounded execution. It states the
unit's goal, scope, inputs, exit criteria, and budget hint. It is coordination
scaffolding (control plane, not deliverable) and is gitignored. **[RATIONALE]**

### 5.2 `result.md` — reconsidered, no longer the output contract

The original draft made `result.md` the unit's output contract. Re-examined
against the minimum evidence above, **it is not needed for reconstitution**:
Git delta + brief + boundary status suffice (§5). The one case where a
model-written note is still justified is **forward intent that the diff cannot
express** — e.g. "the brief's API assumption differs from reality; the next
unit should re-scope around X." Only then does `result.md` exist, as a model
note, not as a required output. It is gitignored (temporary artifact outside
the project configuration). **[RATIONALE][OBSERVED]**

```
motive.tasks/            # gitignored coordination scaffolding
  plan.md                 # decomposition: subtasks, dependency order, composition instructions
  0001/
    brief.md              # goal, scope, inputs, exit criteria, budget hint
    result.md             # optional: forward-intent note only (see §5.2)
  0002/
    brief.md
  ...
```

- `brief.md` is the input contract for one fresh bounded execution.
- `result.md` is **optional** and exists only for forward intent the diff
  cannot carry.
- `plan.md` is owned by the model and may be rewritten as units report back
  (blocked pieces get re-scoped or split further).
- The durable, load-bearing record is the **Git delta + boundary status**, not
  these files. `motive.tasks/` is gitignored per §5.2; the actual code/doc
  change is exactly what the Git delta captures. **[SOURCE][RATIONALE]**

### 5.3 Boundary record (runtime, mechanical)

`UnitBoundary` (`internal/runtime/runtime.go`) is the mechanical, runtime-written
record of one bounded execution (a unit): status, revision delta, and budget
usage. It carries only facts the runtime can observe — task-level judgment (exit
criteria, coherence with the plan) stays with the model. **[SOURCE]**

```go
type UnitBoundary struct {
    Status         string // completed | budget-exceeded | error
    Steps, MaxSteps, ToolCalls, MaxToolCalls, ToolFailures int
    BaseRevision, ResultRevision string
    ElapsedMS      int64
    Text           string // final response, or best-effort partial text on failure
    Error          string
}
```

On failure, `Text` holds the best-effort partial assistant text, so forward
intent survives the boundary. **[SOURCE][TEST]**

## 6. Stage-by-stage protocol (mapped to current code)

| Stage | Who decides | Artifact left | Info passed to next execution | Current code change point |
|---|---|---|---|---|
| **1. EPIC intake** | runtime records; model judges scope | session `Entry` (user request), `start` trace | request string in context + `base_revision` | already exists |
| **2. Unit selection** | **model** | `motive.tasks/NNNN/brief.md` (+ `plan.md` rewrite) | brief content + shared git workspace | model behavior via existing tools |
| **3. Execute (unit)** | **model** decides actions; runtime runs the loop | workspace files the unit writes; unit's final response | durable state lands in workspace | existing `Runtime.Execute` |
| **4. Verify** | runtime = mechanical; **model** = task-level | boundary record (revisions, budget usage, status) | status + `base_rev → result_rev` + failure count | `finish()` writes `UnitBoundary` |
| **5. Persist evidence** | runtime writes boundary record; model *optionally* writes `result.md` | `unit` session `Entry` (runtime, mechanical) + optional `result.md` (gitignored) | boundary status + rev delta (+ note path if present) | one-shot CLI appends `unit` entry |
| **6. Execution boundary** | runtime returns compact summary (forward intent on error) | result string to parent; `unit` boundary entry in session | status, budget used, text | `finish()` + `main.go` stderr delivery |
| **7. Next unit reconstruction** | **model** | `plan.md` rewrite + next `brief.md` | `brief.md` + git workspace (durable medium, not parent context) | model behavior via existing tools |

The parent's own budgeted loop only ever performs *cheap* steps: write a brief →
run a unit → read its result → write the next brief. The heavy work runs inside
separate, fresh-context bounded executions, each with its own budget. This is
how one `Execute()` stops being the single consumer of the budget. **[RATIONALE]**

## 7. Verification — the only stage where judgment is split

Verification must not become hidden runtime judgment. Split it explicitly:

- **Runtime (mechanical, allowed):** did the unit loop exit cleanly, hit the
  step/tool/duration budget, or fail a model request? What is
  `base_revision → result_revision`? How many tool calls and failures? This is
  exactly the data `UnitBoundary` records on every termination path via
  `finish()`. **[SOURCE][TEST]**
- **Model (semantic, delegated):** did the unit meet the exit criteria written
  in its `brief.md`? Is the resulting workspace coherent with the plan? Runtime
  never reads these judgments. The unit does **not** pre-answer them in a
  required `result.md`: the parent **re-judges** from `brief.md` + the Git delta
  in a fresh context, which is more trustworthy than a stale self-evaluation.
  **[RATIONALE][OBSERVED]**

The boundary is where a *wrong* split surfaces (§8): an over-sized unit comes
back `budget-exceeded`, a mis-scoped unit comes back `blocked`, and the parent
reacts by rewriting `plan.md`. The runtime does not repair the plan; it only
reports the mechanical facts that make the repair cheap. **[RATIONALE]**

## 8. Decomposition is fallible — the recovery loop is the design

The single most error-prone step is the split itself. The model can misjudge:

- **the dependency structure** — two "independent" subtasks actually share
  state, so their Git deltas do not compose into a coherent whole;
- **the size** — a "subtask" still exceeds one execution budget, i.e. it was
  not decomposed far enough;
- **the task itself** — the plan targets the wrong goal, so subtasks are
  correct but irrelevant.

Because decomposition is model judgment, wrong splits are **expected events, not
bugs to be eliminated**. The design's job is to make a wrong split *cheap to
detect and cheap to recover from*, which reduces to three properties:

1. **A wrong split surfaces as a boundary event, never as silent corruption.**
   Errors appear as either (a) a `budget-exceeded` or `error` boundary status,
   or (b) an uncomposable set of Git deltas at recomposition time. Both are
   observable at an execution boundary, so the next execution can react.
   **[SOURCE][OBSERVED]**

2. **`plan.md` is explicitly a hypothesis, not a contract.** Its first version
   is expected to be partly wrong. It exists to be rewritten as sub-executions
   report back. The protocol separates the *plan* (mutable, model-owned) from
   the *artifacts* (immutable once written, Git-recorded) — so a replan never
   destroys evidence of what was attempted. **[RATIONALE]**

3. **Decomposition itself must not overflow the decomposing context.** Writing
   the whole `plan.md` in one model context can itself blow the budget. So
   decomposition must be expressed incrementally: emit subtask 0001's
   `brief.md`, delegate it, and record the outcome before planning the next
   chunk. The plan is grown across executions, not produced in one shot. The
   act of planning must obey the same bounded-execution discipline it imposes
   on the subtasks. **[RATIONALE]**

The protocol is therefore not demonstrated by "two subtasks that succeed." It is
demonstrated by a *wrong split that is detected at the boundary and repaired by
a replan*, because that is the case the design actually solves for.

### 8.1 Termination modes and the recovery ladder

A unit terminates in exactly three ways, and each leaves a different durable
trace:

| Termination | Durable evidence left | What the next execution reads |
|---|---|---|
| **completed** | Git delta (full) + brief + boundary status (+ optional `result.md` note) | brief + `git diff base..HEAD` → re-judge exit criteria; read the note if present |
| **budget-exceeded** | Git delta (partial) + brief + boundary status + best-effort `Text` | re-scoped `plan.md` + brief + `git diff base..HEAD` + status + text → re-scope / continue |
| **error / crash** (model or network failure mid-unit) | Git delta (durable work before the crash) + boundary record with partial `Text` + brief + unit session | unit session transcript + `git diff base..HEAD` + brief → resume from the interruption point |

The **recovery ladder** is the ordered set of channels a parent reads to
reconstitute a unit — richest intent first, noisiest last:

1. **`result.md`** (model-written, semantic) — exists only on clean completion.
2. **Boundary record** (`UnitBoundary`, runtime-written, mechanical: status,
   rev delta, budget, failures, best-effort text) — exists on *every* return,
   including budget-exceeded. **[SOURCE][TEST]**
3. **Transcript** (the unit's session, read via `session_log` with an explicit
   `session_id`) — exists on *every* one-shot CLI execution, including crashes;
   richest but noisiest. **[SOURCE]**

A crash-with-no-result case is a first-class failure mode, not an edge case: a
unit that dies mid-turn leaves no `result.md` at all. Its recovery path is to
read the unit's session transcript + boundary record, re-scope the brief from
what the record shows was already done, and redistribute. This completes §8's
claim that "the recovery loop is the design": every way a unit can fail now has
a named, low-cost recovery.

**Honesty caveat (code-verified):** the transcript rung of the ladder depends on
the unit running in its own session. The one-shot CLI now creates a per-run unit
session (§11), so sub-executions do have their own transcript and a runtime
`unit` boundary entry. The richer in-progress-streaming persistence (partial
assistant entry on interrupt) lives in the TUI path, not the runtime loop, so a
unit executed through the one-shot CLI gets its boundary record and any
already-emitted assistant text, but not a streaming-style in-progress entry the
TUI would persist. The demonstrated forward-intent path is the error-path text
delivery (§10.1), which is fully closed by tests. **[SOURCE][TEST]**

## 9. The primitive decision: Form 0 vs Form 1

Everything above works with today's tools *except* the structured boundary
result, which is now implemented in code without any new tool. The original
question was which form the missing primitive would take:

- **Form 0 — zero runtime change for the parent (pure model behavior):** the
  model writes a `brief.md`, then invokes the runtime on it through the existing
  `shell` tool, e.g. `motive run "<subtask brief>"`. The parent receives stdout/
  stderr plus whatever the unit wrote to files. Status is string-parsed, the rev
  delta requires the parent to re-run `git`, and any in-progress reasoning is
  lost unless the unit wrote it to a file.
- **Form 1 — one thin first-class tool:** add a single `execute_unit` tool that
  runs a fresh bounded execution against a `brief.md` in **its own session** and
  returns a structured `UnitBoundary` in-band, with a boundary entry visible in
  the parent's session. This is a generic re-entrancy primitive, not a planner
  layer, and does not violate the no-sub-agent invariant in spirit because the
  model still owns all decomposition.

**Form 0 was selected and implemented.** Form 1 was justified only if **at
least one** of four conditions was demonstrated in a real run. Three were
closed by minimal runtime changes that do not add `execute_unit`; the fourth
remains a convenience improvement, not a loss recovery (§10).

### 9.1 The four justification conditions

1. **C1 — Lossy status channel:** shell strings alone cannot reliably
   distinguish `completed` / `budget-exceeded` / `model-error`, and
   reconstitution depends on that distinction.
2. **C2 — In-context rev delta absence:** the parent needs the unit's base→result
   delta as a structured value in its own context; re-running `git` per unit is
   cost/noise-prohibitive.
3. **C3 — Forward intent loss on failure:** an error/budget unit leaves intent
   that is (a) not in the diff, (b) not in a file, and (c) not recoverable. The
   re-derivation cost is material. This is the sharpest condition: a hard fact,
   not a soft judgment.
4. **C4 — Telemetry continuity:** unit boundaries must be visible in the
   parent's session/telemetry for self-observation (`stable-semantics.md` §14);
   a subprocess is invisible to the parent's `session_log`.

## 10. Form 0 experiment results (C1–C4)

A real experiment ran against this repository under the §7.1 smallest-EPIC
scenario ("add a bounded, workspace-scoped `git log` tool + unit tests") using
Form 0 only. Details live in `docs/experiment-form0.md`; the findings and their
closure are summarized here. **[OBSERVED]**

| Unit | Exit | Status signal | Outcome |
|---|---|---|---|
| 0001 (recon) | 0 | stdout | wrote 0002's brief; no source change |
| 0002 (impl, `max_steps=6`) | 1 | stderr `execution budget exceeded: 6 steps`; stdout **empty** | **all implementation actually complete** (185 lines); final message swallowed by the budget error |
| 0003 (recompose, `max_steps=32`) | 0 | stdout | re-judged evidence, found everything complete, all tests green |

**Findings vs the four conditions:**

- **C1 — partially demonstrated.** The stderr string is parseable for this
  specific error but fragile; and **stdout is empty on error**, so the unit's
  final message is lost. The most painful loss was the unit's final reasoning,
  not the status code per se.
- **C2 — demonstrated.** The parent had to manually record HEAD before each
  unit and re-run `git diff` in its own context; it mis-read the diff in this
  experiment. Cost is real but small for a single unit.
- **C3 — sharply demonstrated.** 0002 finished all work but hit the step cap; its
  final message was never emitted and was unrecoverable without a session. This
  was the strongest argument for Form 1.
- **C4 — demonstrated.** One-shot CLI created no session; sub-executions were
  invisible to the parent's `session_log`.

### 10.1 Follow-up closures (C3/C4), no `execute_unit`

The intent loss (C3) and telemetry gap (C4) were **closed with a minimal
3-layer change that does not add `execute_unit` and no session machinery beyond
reusing the existing store**:

1. **Runtime** (`internal/runtime/runtime.go`): a `finish()` helper now runs on
   every termination path (clean/budget/model-error/cancel) and writes the
   `UnitBoundary` record; error paths return the accumulated assistant text
   (`strings.Join(trace, "\n\n")`) instead of discarding it; the same text lands
   in the boundary record's `text` field. The system prompt nudges units to
   state what remains alongside their final tool call, so the *never-emitted*
   half of the loss is spoken before the cap. **[SOURCE][TEST]**
2. **CLI** (`cmd/motive/main.go`): each one-shot run creates its own unit session,
   prints its id in-band on stderr (`[motive] unit session: <id>`), appends a
   runtime-written `unit` boundary entry, and delivers the partial text in-band
   on failure so a parent receives the unit's own narrative. **[SOURCE]**
3. **Protocol & tooling**: `session_log` gains an optional explicit `session_id`
   so a parent can read any sub-execution's boundary/transcript directly; the
   `unit` role is documented and kept whole in `FormatEntry`. **[SOURCE][TEST]**

`TestExecuteBudgetExceededPreservesTrace` reproduces the 0002 case and asserts
the forward intent survives; `TestExecuteRecordsUnitBoundary` asserts every
termination path (budget-exceeded and clean) writes the mechanical record. Both
pass. **[TEST]**

### 10.2 Overall verdict

3 of 4 conditions were demonstrated in a real run (C2, C3, C4; C1 partially).
The "at least one" bar is met. **However**, C3 and C4 were then **closed by
minimal runtime changes that do not add `execute_unit`**. What remains of the
Form 1 justification is **C2** (structured base→result rev delta in the parent's
context), which the experiment showed is real but small (one extra `git` command
per unit). The case for Form 1 is now weak: the sharpest demonstrated conditions
are closed without it, and its remaining value is a convenience improvement, not
a loss recovery. **Form 0 is therefore the selected and implemented form.**
**[OBSERVED][DECISION]**

## 11. What is realized in code (current implementation)

These are the load-bearing, verified pieces of the protocol today:

- `internal/runtime/runtime.go` — `UnitBoundary` struct + `String()` JSON; a
  `UnitBoundary` sink field; `finish()` helper on **every** termination path
  (clean, budget, model error, cancel); error paths return the accumulated
  assistant text so forward intent survives; system-prompt nudge to state what
  remains before the cap. **[SOURCE][TEST]**
- `cmd/motive/main.go` — per-run unit session for one-shot executions; in-band
  `[motive] unit session: <id>` on stderr; `unit` boundary entry append; the
  unit's session id exposed to `session_log`; partial text delivered on failure.
  **[SOURCE]**
- `internal/tools/tools.go` — `session_log` gains optional `session_id`;
  `git_log` tool (the EPIC deliverable). **[SOURCE][TEST]**
- `internal/workspace/workspace.go` — `GitLog` / `GitLogContext` (bounded,
  clamped to [1,50]). **[SOURCE][TEST]**
- `internal/session/session.go` — `unit` role documented; `FormatEntry` keeps the
  boundary JSON whole (4000-char limit instead of 80). **[SOURCE][TEST]**
- Tests: `runtime_test.go` (`TestExecuteBudgetExceededPreservesTrace`,
  `TestExecuteRecordsUnitBoundary`), `workspace_test.go` (`TestGitLogBounded`,
  `TestGitLogClampsN`), `tools_test.go` (`TestSessionLogToolExplicitID`,
  `TestGitLogTool*`), `session_test.go`
  (`TestFormatEntryUnitBoundaryNotTruncated`). All pass. **[TEST]**
- Verified live: a model-error path produced a unit session with the request
  entry plus a `unit` boundary entry (`"status":"error"`, rev delta, budget
  usage, error detail). **[OBSERVED]**

## 12. What remains [UNKNOWN] / deferred

- **Structured base→result rev delta in the parent's context (C2):** deferred.
  The experiment showed the cost is one extra `git` command per unit. It is not
  being pursued with an `execute_unit` tool. **[DECISION]**
- **`execute_unit` tool (Form 1 re-entrancy primitive):** not implemented, not
  planned. Form 0 is selected (§9). No code corresponds to the earlier
  `UnitRunner` interface / `runBounded` extraction / `execute_unit` handler
  drafts; those were superseded by the realized boundary machinery (§11).
  **[DECISION][SOURCE]**
- **Multi-unit orchestration as an automated pattern:** the boundary machinery
  is implemented and verified, but a *generalized* decomposition workflow
  (model reliably planning/writing briefs and recomposing across many units) is
  a model skill demonstrated by one experiment, not a runtime guarantee.
  **[OBSERVED][UNKNOWN]**
- **Track A (context trimming/compaction):** still out of scope; context
  lifecycle actions are not implemented (`stable-semantics.md` §23).
  **[UNKNOWN]**
- **A crash before any assistant text is emitted in a unit without a session:**
  now mitigated for the one-shot CLI (it always creates a session and writes a
  boundary record), but the richer in-progress-streaming persistence remains in
  the TUI path. The demonstrated forward-intent case (budget-exceeded after
  tool calls) is fully closed. **[SOURCE][TEST]**

## 13. Explicitly unchanged / out of scope

- **Runtime never decomposes or completes tasks.** No planner, no sub-agent, no
  policy that reads `result.md` and makes task decisions (§4). **[SOURCE]**
- **Context lifecycle (Track A)** remains out of scope; each unit still benefits
  from a small fresh context but that is not the EPIC mechanism.
- **`stable-semantics.md`** is not amended by this document's design claims;
  the realized pieces here correspond to existing `[SOURCE][TEST]` facts there
  (bounded execution, fresh context, session-as-transcript, git revision
  records, self-observation). **[RATIONALE]**
- **No Git commit/push semantics change** — the model still controls revision
  actions through the existing tools. **[SOURCE]**
- **Autonomous policy self-modification** (`stable-semantics.md` §20 item 5) is a
  later, separate frontier and does not block this protocol. **[RATIONALE]**

## 14. Document disposition

This document is the single canonical specification for model-delegated
decomposition and the unit boundary protocol. It merges and supersedes:

- `docs/epic-boundary-protocol.md` — removed (was a Form 1 concretization draft
  with `execute_unit`/`UnitRunner`/`runBounded` change points that were never
  implemented; superseded by §5–§11).
- `docs/model-delegated-decomposition.md` — removed (was a "working hypothesis"
  with an unresolved Form 0/1 decision; superseded by §1–§12 with the decision
  now resolved to Form 0).

The Form 0 experiment record is retained as `docs/experiment-form0.md` for
historical evidence; its conclusion is summarized in §10.

## Appendix: evidence checklist for the exit criteria

- [x] Form 0/1 decision resolved to **Form 0** (§9, §10.2).
- [x] `execute_unit` / Form 1 concretization removed, matching code (§12).
- [x] Protocol and decomposition design finalized against current code (§5–§7,
  §11) and the real experiment (§10).
- [x] Two documents merged into one consistent specification (this file).
- [x] Old documents retired and cross-references updated (§14).
- [x] Implementation verified: `go test ./...` passes (§11).
