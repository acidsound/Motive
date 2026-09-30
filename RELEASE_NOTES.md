## Motive v0.6.0

Changes since v0.5.0:

### Slash Commands (for keyboards without keybindings)
- Every binding-critical action is now reachable by typing a slash command into the prompt: `/attach`, `/diff`, `/effort`, `/help`, `/image`, `/models`, `/new`, `/queue`, `/recovery`, `/sessions`, `/steer`, `/system`, `/tools`, `/quit`
- `/effort low|medium|high|xhigh|max|off` sets the reasoning effort; bare `/effort` cycles it
- `/steer <text>` steers the running task, `/queue <text>` queues input while busy, `/diff` shows the git diff — all usable while a task is running
- Tab completes the slash word under the cursor; typing `/` lists matching commands inline with descriptions
- Unambiguous prefixes complete in one Tab press (e.g. `/eff` → `/effort`); ambiguous ones list the candidates
- Commands that are unavailable while running show a clear notice instead of silently failing
- `//text` escapes out of command parsing and sends a literal `/text` to the model
- Unknown commands report an error without sending anything to the model

### Tests
- Added tests for command matching and completion, prefix disambiguation, busy-state gating, the `//` escape hatch, and inline command listing
