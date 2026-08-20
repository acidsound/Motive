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

A minimal, model-written format. Three kinds of files, all ordinary workspace files
so Git is the revision record:

```
motive.tasks/
  plan.md                 # decomposition: subtasks, dependency order, composition instructions
  0001/
    brief.md              # goal, scope, inputs, expected output path, exit criteria, budget hint
    result.md             # done|blocked, what changed, what remains, base→result revisions
  0002/
    brief.md
    result.md
  ...
```

- `brief.md` is the input contract for one fresh bounded execution.
- `result.md` is the output contract: a compact, structured summary that a later
  execution can read without replaying the sub-execution's context.
- `plan.md` is owned by the model and may be rewritten as sub-executions report back
  (blocked pieces get re-scoped or split further).

## 5. Decomposition is fallible — the recovery loop is the design

The single most error-prone step is the split itself. The model can misjudge:

- **the dependency structure** — two "independent" subtasks actually share state, so
  their `result.md` files do not compose into a coherent whole;
- **the size** — a "subtask" still exceeds one execution budget, i.e. it was not
  decomposed far enough;
- **the task itself** — the plan targets the wrong goal, so subtasks are correct but
  irrelevant.

Acknowledging this is not a caveat; it is the load-bearing design decision. Because
decomposition is model judgment, wrong splits are **expected events, not bugs to be
eliminated**. The design's job is to make a wrong split *cheap to detect and cheap to
recover from*, which reduces to three properties:

1. **A wrong split surfaces as a boundary event, never as silent corruption.**
   Errors appear as either (a) a `blocked` or over-budget `result.md`, or (b) an
   uncomposable set of `result.md` files at recomposition time. Both are observable at
   an execution boundary, so the next execution can react. This is the same mechanism
   as §3.4 — recomposition is where decomposition fallibility is caught.

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

## 6. The missing primitive (deliberately minimal)

Everything above works with today's tools *except* one thing: there is no way for one
execution to start a fresh bounded execution on a subtask and receive its result. Two
candidate forms, smallest first:

- **Form 0 — zero runtime change (pure model behavior):** the model writes a
  `brief.md`, then invokes the runtime on it through the existing `shell` tool, e.g.
  `motive run "<subtask brief>"`. The sub-execution's output and `result.md` land in
  workspace files. Decomposition, fan-out, and recomposition are entirely model
  choices. Motive's runtime stays exactly as it is.
- **Form 1 — one thin first-class tool:** add a single `execute_task` tool that runs a
  fresh bounded execution against a `brief.md` and returns a compact summary. This is a
  generic re-entrancy primitive, not a planner layer, and does not violate the
  no-sub-agent invariant in spirit because the model still owns all decomposition.

Form 0 should be tried first: it proves the protocol with zero runtime change and
keeps Motive's execution path untouched. Form 1 is only justified if Form 0 is shown
to be materially worse (e.g. sub-execution telemetry is lost through the shell).

## 7. What must become stable semantics (only after proof)

Before any of this can be promoted from working hypothesis to stable semantics, it
must be exercised:

- a real EPIC task decomposed into ≥2 sub-executions that each complete within one
  budget;
- at least one sub-execution that exceeds budget and is recomposed at the boundary,
  with the parent producing a valid final result;
- **at least one deliberately wrong split** (bad dependency cut or over-sized subtask)
  that surfaces at the boundary and is repaired by a `plan.md` rewrite — see §5;
- verification that sub-execution results compose from `result.md` files without
  replaying any sub-execution's context.

Until then, the decomposition protocol in §4, the fallibility handling in §5, and the
primitive in §6 remain **[UNKNOWN]** / working hypothesis, consistent with the
classification rules in `stable-semantics.md` §1.

## 8. Explicitly out of scope (to keep the discussion on axis)

- **Track A (context trimming/compaction) is not the answer** to `execution budget
  exceeded`. It may be pursued later as a supporting sub-problem, but it is not the
  EPIC divide-and-conquer mechanism.
- **Autonomous policy self-modification** (`stable-semantics.md` §20 item 5) is a later,
  separate frontier and does not block Track B.
- **An orchestrator/planner inside Motive** is intentionally avoided; decomposition
  stays model-owned and data-recorded.
