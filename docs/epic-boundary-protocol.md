# EPIC Boundary Protocol (runtime mapping)

> **Status: working design proposal.** This concretizes the decomposition ideas in
> `docs/model-delegated-decomposition.md` onto the *actual* Motive source structure.
> It is not yet realized in code, and must not be treated as stable semantics until
> implemented and verified (see `stable-semantics.md` §1 classification rules).

## 1. The concrete problem

Today `Runtime.Execute` (`internal/runtime/runtime.go`) runs

```
user request  ==  one model context  ==  one bounded loop  ==  one budget
```

The loop is `for step := 0; step < budget.MaxSteps; step++`. An EPIC request exhausts
the 32-step budget inside this single loop because the *entire* task — decomposition,
execution, and recomposition — is compressed into one model context. Nothing below can
change that; compaction only raises one context's ceiling.

## 2. The invariant that shapes the protocol

> Runtime must never judge task decomposition or task completion on the model's behalf.

This is preserved structurally, not just by convention:

- **Unit selection** (what to do next) is the model writing a `brief.md` file and then
  invoking a tool. The runtime only executes the tool call.
- **Unit completion** (is the EPIC done) is judged by the model ceasing to call
  `execute_unit` and emitting a final non-tool response — the *existing* termination
  rule in `runtime.go` (`if len(msg.ToolCalls) == 0 { return ... }`). The runtime never
  declares "EPIC finished".
- **Task-level correctness** (did the unit satisfy its exit criteria) is judged by the
  model: the unit writes its own `result.md` with a `done|blocked` status, and the
  parent re-judges at recomposition.
- **What the runtime *does* judge is mechanical and boundary-local only**: budget
  exceeded, tool failures, revision delta, clean loop exit. These are facts the model
  cannot see for itself from outside a discarded context.

## 3. The unit loop

```
EPIC intake ──► unit selection ──► Execute(unit) ──► verify ──► persist ──► boundary ──► next unit
   runtime        model                model/runtime   mixed      model+runtime  runtime      model
   (record)       (write brief +       (fresh context) (see §5)   (result.md +   (discard,     (read result.md,
                   call execute_unit)                              boundary record) return summary)  rewrite plan.md,
                                                                                                  write next brief)
```

The parent's own budgeted loop only ever performs *cheap* steps: write a brief →
`execute_unit` → read a summary → write the next brief. The heavy work runs inside a
nested, fresh-context bounded execution that has its own budget. This is exactly how
one `Execute()` stops being the single consumer of the 32-step budget.

## 4. Stage-by-stage protocol

| Stage | Who decides | Artifact left | Info passed to next execution | Current code change point |
|---|---|---|---|---|
| **1. EPIC intake** | runtime records; model judges scope | session `Entry` (user request), `start` trace | request string in context + `base_revision` | none (already exists) |
| **2. Unit selection** | **model** | `motive.tasks/NNNN/brief.md` (+ `plan.md` rewrite) | brief content + shared git workspace | new tool `execute_unit` |
| **3. Execute (unit)** | **model** decides actions; runtime runs the loop | workspace files the unit writes; unit's final response | nothing direct; durable state lands in workspace | extract reusable bounded loop |
| **4. Verify** | runtime = mechanical; **model** = task-level | boundary check result (revisions, budget usage, status) | status + `base_rev → result_rev` + failure count | new `unitResult` capture |
| **5. Persist evidence** | model writes `result.md`; runtime writes boundary record | `result.md` (model, semantic) + boundary `Entry` (runtime, mechanical) | paths to the artifacts | session `Store.Append` reuse |
| **6. Execution boundary** | runtime discards unit context, returns compact summary | tool-result message in parent context | unit id, revision delta, status, budget used, `result.md` path | `execute_unit` returns summary string |
| **7. Next unit reconstruction** | **model** | `plan.md` rewrite + next `brief.md` | `brief.md` + git workspace (durable medium, not parent context) | none (all tool ops exist) |

## 5. Verify — the only stage where judgment is split

Verification must not become hidden runtime judgment. Split it explicitly:

- **Runtime (mechanical, allowed):** did the unit loop exit cleanly, hit the step/tool/
  duration budget, or fail a model request? What is `base_revision → result_revision`?
  How many tool calls and failures? This is exactly the data `Observation` already
  records per turn; the boundary record is the same data collapsed to one line.
- **Model (semantic, delegated):** did the unit meet the exit criteria written in its
  `brief.md`? Is the resulting workspace coherent with the plan? Runtime never reads
  these judgments — the unit writes its own `result.md` (`done|blocked`), and the
  parent re-reads it at recomposition.

The boundary is where a *wrong* split surfaces (per `decomposition.md` §5): an
over-sized unit comes back `budget-exceeded`, a mis-scoped unit comes back `blocked`,
and the parent reacts by rewriting `plan.md`. The runtime does not repair the plan; it
only reports the mechanical facts that make the repair cheap.

## 6. Minimal change points mapped to current code

These are the *only* structural changes required. Everything else is reuse.

### 6.1 New re-entrancy primitive: `execute_unit` tool

Add to `tools.Definitions()` (`internal/tools/tools.go`):

```
execute_unit(brief_path: string, budget_hint?: {max_steps?, max_tool_calls?, max_minutes?})
```

This is the "Form 1" primitive from `decomposition.md` §6. It is a generic re-entrancy
hook, not a planner layer: the model still owns what the unit does and when the EPIC is
done.

**Cycle avoidance:** `runtime` imports `tools`, so `tools` cannot import `runtime`.
Give `tools.Executor` a runner hook (interface or func field) instead of a concrete
runtime reference:

```go
type UnitRunner interface {
    RunUnit(ctx context.Context, briefPath string,
            maxSteps, maxToolCalls, maxMinutes int) (string, error) // returns compact summary
}
```

`Executor` gains a `UnitRunner` field; `runtime.New` wires `r.executeUnit` into it. The
`execute_unit` handler in `tools.go` delegates to the hook. This keeps `tools` free of
`model`/`runtime` imports.

### 6.2 Extract the bounded loop so it is reusable (`runtime.go`)

The body of today's `Execute` is one unbroken loop. Split it:

- `runBounded(ctx, request string, budget ExecutionBudget) (unitResult, error)` — the
  existing loop verbatim, but returns a structured `unitResult` instead of only a joined
  trace string and relying on the outer caller to inspect `r.WS.GitHEAD()`.
- `Execute(ctx, request)` — keeps the current `(string, error)` public contract by
  wrapping `runBounded` with the default budget. Backward compatible; the single-context
  path is unchanged for non-EPIC requests.

`unitResult` holds the facts the boundary needs:

```go
type unitResult struct {
    Status      string // "completed" | "budget-exceeded" | "error"
    BaseRev     string
    ResultRev   string
    Steps       int
    ToolCalls   int
    ToolFailures int
    Text        string // final response (the unit's own closing statement)
    Err         error
}
```

### 6.3 `execute_unit` handler (`runtime.go`, `UnitRunner`)

1. Read `briefPath` via `r.WS.ReadContext` (the brief is the unit's input contract).
2. Build a **fresh** context, exactly as the non-decomposed path does: `system
   ContextBlock()` + `user <brief content>`. This honors the fresh-context invariant in
   `stable-semantics.md` §3 — the unit does **not** inherit the parent's context.
3. Resolve the unit budget: `budget_hint` from the tool args, clamped by the runtime's
   hard caps (`defaultMaxSteps`/`maxAllowed*` constants in `runtime.go`), defaulting to
   the runtime `ExecutionBudget`. The model's hint is advisory; the cap is mechanical.
4. Call `runBounded` under a fresh `context.WithTimeout`.
5. Persist the boundary record (§6.4).
6. Return the compact summary string to the parent as the tool result.

The returned summary (stage 6) is deliberately small, e.g.:

```
[unit-boundary]
id=0003 brief=motive.tasks/0003/brief.md status=completed
rev=abc1234→def5678 steps=24/32 tools=61/128 failures=0
result=motive.tasks/0003/result.md
```

### 6.4 Persist the boundary record (`session.go` reuse)

The unit boundary is an *immutable, runtime-written* fact that Git records alongside the
model's own `result.md`. `session.Entry` already has `Role`, `Content`, `BaseRevision`,
`ResultRevision`, `ElapsedMS`, and `Tools` — sufficient. Append one entry with
`Role: "unit"` and `Content` = JSON of `unitResult` + brief path. **No schema change in
`session.go` is required**; if we later want to distinguish unit boundaries from turn
entries in the picker, add an explicit `Kind` field, but that is optional.

## 7. How the 32-step budget is no longer consumed by one `Execute`

- The parent loop's budget counts only the parent's own turns: write brief, call
  `execute_unit`, read summary, write next brief → a handful of steps regardless of the
  EPIC's size.
- Each `execute_unit` runs a **fresh** bounded execution (`runBounded`) with its own
  budget and its own timeout. Heavy tool work, iteration, and failure recovery happen
  there, in a context that starts small.
- The parent's budget is not inherited by the unit; the unit's budget is not inherited by
  the next unit. The 32-step safety boundary still exists per execution — it is just no
  longer the ceiling of the whole EPIC.
- If a unit exceeds its budget, `runBounded` returns `status=budget-exceeded` and the
  parent re-plans. This is the recomposition path, identical to the failure axis already
  described in `decomposition.md` §5 and `stable-semantics.md` §11/§12.

## 8. Explicitly unchanged / out of scope

- **Runtime never decomposes or completes tasks.** No planner, no sub-agent, no policy
  that reads `result.md` and makes task decisions. (§2 above.)
- **Context lifecycle (Track A)** remains out of scope; each unit still benefits from a
  small fresh context but that is not the EPIC mechanism.
- **`stable-semantics.md` is not amended by this document.** This remains a working
  hypothesis until the protocol is implemented and demonstrated (per `decomposition.md`
  §7: including a *wrong split detected and repaired at the boundary*).
- **No Git commit/push semantics change** — the model still controls revision actions
  through the existing tools.
