# Model-Delegated Decomposition (EPIC divide-and-conquer)

> **Status: working hypothesis / design proposal.**
> This document is a proposal, not stable semantics. It must not be treated as an
> invariant until it is realized in source, tests, and an explicit project decision
> in `stable-semantics.md`. Its purpose is to keep the discussion on one axis.

## 1. Origin

The discussion began from a single runtime error:

```
✖ execution budget exceeded: 32 steps
```

That error is a **symptom**, not the disease. The disease is a structural property of
the current execution model:

```
one user request  ==  one model context  ==  one bounded execution
```

An EPIC task does not fit one bounded execution. No amount of intra-execution context
management (trimming, compaction, overflow handling) changes this, because even a
perfectly compacted context is finite and the step/duration/tool-call budget is still
a hard boundary. A compaction merely raises the ceiling of a single execution; the
EPIC task still exceeds that ceiling.

## 2. Two orthogonal tracks

These two questions are different axes and must not be conflated:

- **Track A — context lifecycle (current drift):** how to keep *one* execution alive
  longer by trimming/compacting context when it grows. This extends a single
  execution's ceiling. It does not resolve the EPIC problem.
- **Track B — decomposition (the intended direction):** how to split a large task
  into multiple *independent, recomposable* bounded executions. This keeps each
  execution small and multiplies them.

Track B is the answer to the `execution budget exceeded` question. Track A is a
supporting sub-problem (each sub-execution still benefits from compact context) and
must not be treated as the main line.

## 3. Guiding principles (faithful to existing invariants)

1. **Decomposition is model behavior, expressed as data — not a runtime planner.**
   Motive deliberately does not depend on a planner layer or sub-agent layer in the
   execution path. So the division of an EPIC into subtasks is a model skill, recorded
   as files in the workspace, not as an orchestrator inside Motive.

2. **Fresh context per execution is a feature, not a limitation.**
   `stable-semantics.md` §3 states every execution starts from a newly constructed
   model context. Under decomposition this is exactly what we want: each subtask is
   judged from a clean context with only its brief and the shared workspace state.

3. **Workspace + Git are the coordination medium between executions.**
   §3 states the persistent world is files and Git state, not chat history. Between
   two executions the only shared state is the workspace and its revisions. The task
   artifact format (below) is built on this, not on memory or context resumption.

4. **Exceptions are handled at the execution boundary, not by growing one context.**
   A sub-execution that exceeds its budget or fails returns a *structured result*
   (what was attempted, what changed, what remains) as data. The next execution
   re-plans the remaining pieces. This is recomposition, not infinite retry inside a
   single growing context.

5. **Decomposition is fallible model judgment — absorb its errors, do not prevent
   them.** See §5. This is a distinct failure axis from §3.4.

## 4. The task artifact protocol (working proposal)

The protocol's job is to leave **minimum execution evidence** so a fresh,
discarded-context execution can reconstitute what a unit did — without replaying the
unit's context. With Workspace + Git as the primary state, that minimum evidence is:

1. **Git revision delta (`base_rev → result_rev`) + the workspace files at
   `result_rev`** — the primary, durable record of what actually changed. Already
   recorded on every termination path (clean, budget, error, cancel).
2. **`brief.md`** — the unit's input contract: what it was supposed to do.
3. **Boundary status** — `completed | budget-exceeded | error` + the consumed budget
   (steps / tool calls / failures). The one fact a model outside the discarded context
   cannot re-derive on its own.

Everything else — "was the exit criterion met?", "what remains?" — is **re-judged** by
the next fresh execution reading `brief.md` + the diff. A fresh re-judgment is more
trustworthy than a stale self-evaluation left inside a discarded context, so the
protocol does not require the unit to pre-answer these.

The only thing the protocol adds on top of what already exists is the **structured
boundary status** (§6). Today `Runtime.Execute` returns only `(string, error)`: the
revision delta and step count are unstructured, so a parent would have to re-run `git`
and re-read the session. The structured `unitResult` (status + base→result rev + steps
+ failures) is the single addition. Git delta and the workspace are already there.

### 4.1 `result.md` — reconsidered, no longer the output contract

The original draft made `result.md` the unit's output contract. Re-examined against the
minimum evidence above, **it is not needed for reconstitution**: Git delta + brief +
boundary status suffice (§4). The one case where a model-written note is still
justified is **forward intent that the diff cannot express** — e.g. "the brief's API
assumption differs from reality; the next unit should re-scope around X." Only then
does `result.md` exist, as a model note, not as a required output.

If kept, it is a **temporary artifact outside the project configuration**: it lives in
the workspace (the next execution must read it, and it must survive between executions)
but is **gitignored**, so it stays out of the revision history. The project
configuration is the actual code/doc change — exactly what the Git delta captures.
`plan.md` and `brief.md` are the same kind of coordination scaffolding (control plane,
not deliverable) and are gitignored for the same reason.

```
motive.tasks/            # gitignored coordination scaffolding
  plan.md                 # decomposition: subtasks, dependency order, composition instructions
  0001/
    brief.md              # goal, scope, inputs, exit criteria, budget hint
    result.md             # optional: forward-intent note only (see §4.1)
  0002/
    brief.md
  ...
```

- `brief.md` is the input contract for one fresh bounded execution.
- `result.md` is **optional** and exists only for forward intent the diff cannot carry.
- `plan.md` is owned by the model and may be rewritten as units report back (blocked
  pieces get re-scoped or split further).
- The durable, load-bearing record is the **Git delta + boundary status**, not these
  files.

## 5. Decomposition is fallible — the recovery loop is the design

The single most error-prone step is the split itself. The model can misjudge:

- **the dependency structure** — two "independent" subtasks actually share state, so
  their Git deltas do not compose into a coherent whole;
- **the size** — a "subtask" still exceeds one execution budget, i.e. it was not
  decomposed far enough;
- **the task itself** — the plan targets the wrong goal, so subtasks are correct but
  irrelevant.

Acknowledging this is not a caveat; it is the load-bearing design decision. Because
decomposition is model judgment, wrong splits are **expected events, not bugs to be
eliminated**. The design's job is to make a wrong split *cheap to detect and cheap to
recover from*, which reduces to three properties:

1. **A wrong split surfaces as a boundary event, never as silent corruption.**
   Errors appear as either (a) a `budget-exceeded` or `error` boundary status, or (b)
   an uncomposable set of Git deltas at recomposition time. Both are observable at an
   execution boundary, so the next execution can react. This is the same mechanism as
   §3.4 — recomposition is where decomposition fallibility is caught.

2. **`plan.md` is explicitly a hypothesis, not a contract.** Its first version is
   expected to be partly wrong. It exists to be rewritten as sub-executions report
   back. The protocol therefore separates the *plan* (mutable, model-owned) from the
   *artifacts* (immutable once written, Git-recorded) — so a replan never destroys
   evidence of what was attempted.

3. **Decomposition itself must not overflow the decomposing context.** Writing the
   whole `plan.md` in one model context can itself blow the budget. So decomposition
   must be expressed incrementally: emit subtask 0001's `brief.md`, delegate it, and
   record the outcome before planning the next chunk. The plan is grown across
   executions, not produced in one shot. The act of planning must obey the same
   bounded-execution discipline it imposes on the subtasks.

The consequence for §7's proof requirements: the protocol is not demonstrated by
"two subtasks that succeed." It is demonstrated by a *wrong split that is detected at
the boundary and repaired by a replan*, because that is the case the design actually
solves for.

### 5.1 Termination modes and the recovery ladder

A unit terminates in exactly three ways, and each leaves a different durable trace:

| Termination | Durable evidence left | What the next execution reads |
|---|---|---|
| **completed** | Git delta (full) + brief + boundary status (+ optional `result.md` note) | brief + `git diff base..HEAD` → re-judge exit criteria; read the note if present |
| **budget-exceeded** | Git delta (partial) + brief + boundary status | re-scoped `plan.md` + brief + `git diff base..HEAD` + status → re-scope / continue |
| **error / crash** (model or network failure mid-unit) | Git delta (durable work before the crash) + in-progress assistant content (error-path persistence) + brief + boundary status | transcript (in-progress reasoning) + `git diff base..HEAD` + brief → resume from the interruption point |

The **crash-with-no-result** case is a first-class failure mode, not an edge case: a
unit that dies mid-turn leaves no `result.md` at all. Its recovery path is to read the
unit's transcript (the in-progress turns, persisted on the error path), re-scope the
brief from what the transcript shows was already done, and redistribute. This completes
§5's claim that "the recovery loop is the design": every way a unit can fail now has a
named, low-cost recovery.

The **recovery ladder** is the ordered set of channels a parent reads to reconstitute a
unit — richest intent first, noisiest last:

1. **`result.md`** (model-written, semantic) — exists only on clean completion.
2. **Boundary record** (runtime-written, mechanical: status, rev delta, budget,
   failures) — exists on *every* return, including budget-exceeded.
3. **Transcript** (the unit's actual turns) — exists on *every* execution including
   crashes; richest but noisiest.

The parent reads `result.md` → (if absent) the boundary record → (if status=error) the
transcript. **Honesty caveat (code-verified):** the third rung is only as good as its
plumbing. Today the error-path persistence of in-progress work lives in the TUI
(`tui.go finishTurn`), not the runtime path, and the one-shot CLI path creates no
session at all — so a unit with no session of its own leaves **zero** recoverable
records on a crash. The transcript rung is a design target that depends on the unit
running in its own session with error-persistence lowered into the runtime path; it is
not yet a fact for sub-executions.

## 6. The missing primitive (deliberately minimal)

Everything above works with today's tools *except* one thing: there is no way for one
execution to start a fresh bounded execution on a subtask and receive a *structured*
result. Two candidate forms:

- **Form 0 — zero runtime change (pure model behavior):** the model writes a
  `brief.md`, then invokes the runtime on it through the existing `shell` tool, e.g.
  `motive run "<subtask brief>"`. Code-verified: the one-shot CLI path creates **no
  session and appends no boundary record** — the parent receives only stdout (clean
  text) / stderr (error string) plus whatever the unit wrote to files. Status is
  string-parsed, the rev delta requires the parent to re-run `git`, and any in-progress
  reasoning is lost unless the unit wrote it to a file.
- **Form 1 — one thin first-class tool:** add a single `execute_unit` tool that runs a
  fresh bounded execution against a `brief.md` in **its own session** and returns a
  structured `unitResult` (status + base→result rev + steps + failures) in-band, with a
  boundary entry visible in the parent's `session_log`. This is a generic re-entrancy
  primitive, not a planner layer, and does not violate the no-sub-agent invariant in
  spirit because the model still owns all decomposition.

**Form 0/1 is not yet selected.** Form 1 is justified only if **at least one** of the
following is demonstrated in a real run (this concretizes the old "materially worse"
test against the actual code):

1. **Lossy status channel** — shell strings alone cannot reliably distinguish
   `completed` / `budget-exceeded` / `model-error`, and reconstitution depends on that
   distinction.
2. **In-context rev delta absence** — the parent needs the unit's base→result delta as
   a structured value in its own context, and re-running `git` per unit is
   cost/noise-prohibitive.
3. **Forward intent loss on failure** — an error/budget unit leaves intent that is
   (a) not in the diff, (b) not in a file, and (c) not recoverable (the CLI path uses
   no session, so the transcript is gone). The re-derivation cost is material. This is
   the sharpest condition: a hard fact, not a soft judgment.
4. **Telemetry continuity** — unit boundaries must be visible in the parent's
   session/telemetry for self-observation (`stable-semantics.md` §20.1), and a
   subprocess is invisible to the parent's `session_log`.

If **none** of these is demonstrated in a real run, **Form 0 is sufficient** and Form 1
is not justified. No new principle is introduced — this is the "materially worse" test
made concrete.

## 7. What must become stable semantics (only after proof)

Before any of this can be promoted from working hypothesis to stable semantics, it
must be exercised:

- a real EPIC task decomposed into ≥2 sub-executions that each complete within one
  budget;
- at least one sub-execution that exceeds budget and is recomposed at the boundary,
  with the parent producing a valid final result;
- **at least one deliberately wrong split** (bad dependency cut or over-sized subtask)
  that surfaces at the boundary and is repaired by a `plan.md` rewrite — see §5;
- verification that sub-execution results compose from the Git delta + `brief.md` +
  boundary status (the minimum evidence, §4) without replaying any sub-execution's
  context.

Until then, the decomposition protocol in §4, the fallibility handling in §5, and the
primitive in §6 remain **[UNKNOWN]** / working hypothesis, consistent with the
classification rules in `stable-semantics.md` §1.

### 7.1 The smallest real EPIC scenario (verification target)

In this repository (Motive): **add a bounded, workspace-scoped `git log` tool + unit
tests.** It is real work (no `git log` tool exists today), decomposes naturally into
reconnaissance → implementation, and the implementation unit can be scoped small enough
to make a budget-exceeded deterministic. Not circular.

- **0001 (reconnaissance, read-only, small budget):** inspect the existing
  `git_status`/`git_diff` implementation pattern (workspace method signature, tool
  case, `Definitions` entry) and record 0002's implementation contract in
  `motive.tasks/0001/brief.md`. Completes within budget.
- **0002 (implementation, deliberately over-scoped):** `Workspace.GitLog(n)` + the
  `git log` tool case + `Definitions` entry + test. True cost is ~8–12 steps, but set
  `budget_hint(max_steps=6)` below that → **deterministic budget-exceeded** = an
  over-sized subtask (wrong split) surfacing at the boundary.
- **0003 (recomposition):** read 0002's evidence, re-scope, and complete.

**The evidence chain 0003 (fresh context) reads to reconstitute:**

1. `plan.md` — the parent's re-scoped plan, rewritten after 0002 reported
   budget-exceeded.
2. `motive.tasks/0002/brief.md` — the original contract.
3. `git diff <0002.base_rev>..<HEAD>` — **primary**: how far 0002 got (e.g. `GitLog`
   exists in workspace.go but the tool case/test do not). 0003 reads the diff, not the
   discarded context.
4. **Boundary status** — `budget-exceeded, steps=6/6, base→result rev`. Form 0: shell
   exit code + stderr string. Form 1: structured `unitResult`. Both tell 0003 "it did
   not finish."
5. **Workspace files at HEAD** — the actual partial state.

From (1)–(5), 0003 re-judges "0002 got as far as the workspace method; the case/test
are missing; finish the rest" and reconstitutes from durable state (Git delta + brief +
status) without replaying 0002's context — the core of the protocol.

## 8. Explicitly out of scope (to keep the discussion on axis)

- **Track A (context trimming/compaction) is not the answer** to `execution budget
  exceeded`. It may be pursued later as a supporting sub-problem, but it is not the
  EPIC divide-and-conquer mechanism.
- **Autonomous policy self-modification** (`stable-semantics.md` §20 item 5) is a later,
  separate frontier and does not block Track B.
- **An orchestrator/planner inside Motive** is intentionally avoided; decomposition
  stays model-owned and data-recorded.
