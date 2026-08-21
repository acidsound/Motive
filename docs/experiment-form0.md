# Form 0 Experiment — Model-Delegated Decomposition (§7.1)

**Date:** 2026-08-21  
**Scenario:** "add a bounded, workspace-scoped `git log` tool + unit tests"  
**Form:** 0 — zero runtime change (pure model behavior via `motive run "<brief>"`)

## Setup

- **Parent:** the current Motive session (model + tools).  
- **Sub-execution:** `motive run "<brief>"` — one-shot CLI, no session, no boundary record.  
- **Budget for 0001:** `MOTIVE_MAX_STEPS=16` (read-only recon).  
- **Budget for 0002:** `MOTIVE_MAX_STEPS=6` (deliberately below true cost ~8–12 steps).  
- **Budget for 0003:** `MOTIVE_MAX_STEPS=32` (recomposition).  
- **Evidence chain for 0003:** `plan.md` + `0002/brief.md` + `git diff HEAD` + workspace files. Base rev manually recorded by parent.

## Execution

| Unit | Exit | Status signal | Work done | Parent's observation |
|------|------|---------------|-----------|---------------------|
| 0001 | 0 | stdout (clean text) | Wrote `motive.tasks/0002/brief.md`; no source changes. | Status clear. No git delta (deliverable is gitignored). |
| 0002 | 1 | stderr: `execution budget exceeded: 6 steps`; stdout **empty** | All implementation + tests complete (workspace.go, tools.go, workspace_test.go, tools_test.go — 185 lines added). | Parent mis-read diff (truncated with `head -60`) and thought tests were partial. Actually **100% complete**. The unit's final message was swallowed by the budget error. |
| 0003 | 0 | stdout (clean text) | Re-judged evidence; found everything complete. No changes. `go test ./...` all green. | Correct re-judgment; parent's error was caught by the fresh execution. |

## Findings vs the 4 Form 1 Justification Conditions

### C1. Lossy status channel

**Partially demonstrated.** The stderr string `"execution budget exceeded: 6 steps"` is parseable for this specific error, but:
- The distinction between `budget-exceeded`, `model-error`, and `completed` relies on string matching an error message, which is fragile (model error text may change with API version, locale, or configuration).
- Most critically: **stdout is empty on error** — the unit's final message (its intent, summary, forward plan) is completely lost. Without a session, there is no way to recover it.
- The parent has no machine-readable step count or tool-call count; it must parse the stderr string to know "6 of how many?".

**Verdict: condition partially met.** The most painful loss is the unit's final reasoning, not the status code per se.

### C2. In-context rev delta absence

**Demonstrated.** The parent must:
1. Manually record `git rev-parse HEAD` before each sub-execution (no structured pre/post record).
2. Re-run `git diff HEAD` (or `git diff <base>..HEAD`) to see what the unit changed.
3. Interpret the diff in the parent's own context — which is a significant cost (the parent reads the full diff text, not a structured summary).

Cost: one extra `git` command per unit, plus the parent's context budget for reading the diff. In this experiment the parent mis-read the diff, which a structured `unitResult` (Form 1) would have avoided.

**Verdict: condition met.** The cost is real, but small for a single unit. For many units it would compound.

### C3. Forward intent loss on failure

**Sharply demonstrated.** 0002 completed all its work but hit the step cap on its 6th tool call. Its final message — which would have said "I'm done, tests pass, review needed" — was never emitted. The parent saw only stderr and empty stdout. The intent (what 0002 thought remained, if anything) was unrecoverable.

The reconstitution from Git delta + brief + workspace files **did work** (the fresh execution correctly found everything done), but the parent had to spend more budget to re-derive what 0002 already knew. In a more complex scenario where the unit had partial work and a nuanced next-step plan, the loss would be severe.

The unit did not write a `result.md` note (as it would need to anticipate the error and write before the budget cap — a plausible but fragile strategy).

**Verdict: condition met.** This is the strongest argument for Form 1.

**Follow-up (closed):** the intent loss was closed with a minimal 3-layer change —
no `execute_unit` tool, no session machinery:
1. **Runtime** (`internal/runtime/runtime.go`): every error path now returns the
   accumulated assistant text (`strings.Join(trace, "\n\n")`) instead of discarding
   it; the same text lands in the boundary record's `text` field.
2. **CLI** (`cmd/motive/main.go`): on failure the partial text is delivered in-band
   on stderr after the error line, so a parent receives the unit's own narrative.
3. **Protocol** (system prompt): units are asked to state what remains alongside
   their final tool call, so the *never-emitted* half of the loss is spoken before
   the cap.

`TestExecuteBudgetExceededPreservesTrace` reproduces the 0002 case (MaxSteps=1, tool
call in flight) and asserts the forward intent survives. The crash-before-any-message
case still needs a session (Form 1 territory); the demonstrated case is fully closed.

### C4. Telemetry continuity

**Demonstrated.** The one-shot CLI creates no session. Verified:
- No new `.jsonl` file appeared in `~/.motive/sessions/` for any of the three units.
- The `session_log` tool inside a sub-execution would fail (no session to read from).
- The parent's `session_log` sees only the parent's own history — the sub-executions are invisible.

For the "self-observation" requirement (`stable-semantics.md` §20.1), this is a gap: the model cannot observe its own decomposition pattern across units.

**Verdict: condition met.**

**Follow-up (closed):** one-shot runs now create their own session and append a
runtime-written boundary record (`role: "unit"`, one compact JSON line: status,
base→result rev, steps / tool calls / failures, elapsed, text, error). The session
id is printed in-band on stderr (`[motive] unit session: <id>`), and `session_log`
accepts an explicit `session_id`, so a parent execution can read any sub-execution's
boundary record directly — sub-executions are no longer invisible. Verified live on
the model-error path (model server unreachable): the unit session file contained the
request entry plus the `unit` boundary entry with `"status":"error"` and the
connection-error detail. The unit's own `session_log` also works now (its session id
is set before execution), and the TUI picker lists unit sessions.

## Overall Verdict

**3 of 4 conditions were demonstrated in a real run** (C2, C3, C4; C1 partially).  
Per the agreement in `docs/model-delegated-decomposition.md` §6:

> Form 1 is justified only if **at least one** of these is demonstrated.

The "at least one" bar is met. The strongest case was **C3 (forward intent loss)**, the "sharpest condition: a hard fact, not a soft judgment."

However, the experiment also showed that the reconstitution protocol **works** — the fresh execution correctly re-judged the state from Git delta + brief + boundary status. The overhead is real but bounded.

**Post-experiment update:** C3 and C4 have since been **closed by minimal runtime
changes that do not add `execute_unit`** (see the follow-ups above): forward intent
survives via error-path text delivery, and unit boundaries are durable telemetry via
per-unit sessions with a structured `role: "unit"` record. What remains of the Form 1
justification is **C2** (structured base→result rev delta in the parent's context),
which the experiment showed is real but small (one extra `git` command per unit). The
case for Form 1 is now weak: the sharpest demonstrated conditions are closed without
it, and its remaining value is a convenience improvement, not a loss recovery.

## Recommendations

1. **C3 is closed** (error-path text delivery + in-band stderr + system-prompt nudge). No Form 1 needed for it.
2. **C4 is closed** (per-unit session + runtime-written `role: "unit"` boundary record + `session_log` explicit `session_id`). A parent can read any unit's boundary directly.
3. **C1 is partially addressed:** the boundary record's structured `status` field removes the parent's need to string-match stderr; the stderr string itself remains the fallback.
4. Remaining Form 1 justification is **C2** (structured base→result rev delta). Decide later whether the one extra `git` command per unit is worth an `execute_unit` tool; nothing else currently justifies it.
5. Even without Form 1, the protocol (Git delta + brief + boundary status) enables reconstitution — the overhead is in the parent's budget and the risk of misinterpretation.

## Files changed (EPIC deliverable)

- `.gitignore` — added `motive.tasks/` (gitignored coordination scaffolding per §4.1).
- `internal/workspace/workspace.go` — `GitLog`, `GitLogContext`, `strconv` import.
- `internal/tools/tools.go` — `git_log` Definitions entry + switch case.
- `internal/workspace/workspace_test.go` — `TestGitLogBounded`, `TestGitLogClampsN`.
- `internal/tools/tools_test.go` — `TestGitLogTool`, `TestGitLogToolDefault`, `TestGitLogToolInvalidJSON`.

## Follow-up changes (C3/C4 closures, no `execute_unit`)

- `internal/runtime/runtime.go` — `UnitBoundary` record + sink, `finish()` helper on
  every termination path (clean/budget/model-error/cancel), error paths return the
  accumulated assistant text instead of discarding it; system-prompt nudge to state
  what remains before the cap.
- `cmd/motive/main.go` — per-run unit session for one-shot executions, in-band
  `[motive] unit session: <id>` on stderr, boundary entry append, unit's session id
  exposed to `session_log`.
- `internal/tools/tools.go` — `session_log` gains optional `session_id`.
- `internal/session/session.go` — `unit` role documented; boundary JSON kept whole in
  `FormatEntry` (4000-char limit instead of 80).
- Tests: `runtime_test.go` (`TestExecuteBudgetExceededPreservesTrace`,
  `TestExecuteRecordsUnitBoundary`), `tools_test.go` (`TestSessionLogToolExplicitID`),
  `session_test.go` (`TestFormatEntryUnitBoundaryNotTruncated`).
- Verified live: model-error path produced a unit session with request entry +
  boundary entry (`status:"error"`, rev delta, budget usage, error detail).