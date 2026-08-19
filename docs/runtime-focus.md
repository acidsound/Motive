# Motive runtime focus

Motive is a model-centric LLM runtime. The runtime should preserve and expose model capability rather than evolve into a coding-agent-specific self-observation system.

## In scope

- reasoning effort as a model request parameter
- execution budget as an independent runtime safety boundary
- tool-call round trips and message semantics
- streaming and runtime telemetry
- TUI controls for runtime parameters

## Out of scope

- model self-observation prompts
- source-code structural summaries
- LSP-driven self-navigation
- self-modification orchestration

## Current defaults

- reasoning effort: `low`
- recovery escalation: one `xhigh` turn after a tool failure
- execution budget: 32 steps / 128 tool calls / 30 minutes by default
- hard limits: 256 steps / 1024 tool calls / 120 minutes

The recovery escalation is runtime policy. It must not be implemented by injecting runtime telemetry into the model conversation.
