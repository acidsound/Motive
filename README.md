# Motive

Motive is a minimal execution runtime for reasoning models.

The project starts from a simple premise: a capable model should not be wrapped in a large agent framework that dictates how it reasons or acts. Instead, Motive provides a small, explicit execution environment in which the model can observe state, use tools, modify a workspace, and produce a revision.

## Design direction

- Go binary with CLI and TUI interfaces.
- Model inference is external to Motive.
- Initial model boundary is the OpenAI-compatible API.
- No plugin system in the core runtime.
- Conversation is an interaction UI, not the authoritative source of memory.
- Context is compiled from current state, workspace, assets, memory, and revision history.
- Git is the authoritative history of source changes.
- Execution records connect user intent, compiled context, model actions, and resulting revisions.

## Initial execution loop

```text
request
  -> context compilation
  -> OpenAI-compatible model endpoint
  -> tool call
  -> execution / observation
  -> model
  -> final result
  -> workspace diff / revision
```

The first prototype deliberately keeps this loop small. Memory retrieval, semantic indexes, remote sandboxes, and additional providers are deferred until the core model-centric execution model has been validated.

## Status

Early prototype.
