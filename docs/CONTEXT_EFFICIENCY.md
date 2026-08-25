# Context Efficiency

## Checklist

- [x] Structured observation state for files and diagnostics
- [x] File content hashing to recognize repeated observations
- [x] Basic Go file structure summary (bytes, lines, functions, exports)
- [x] Build/test/shell diagnostic extraction with file and line context
- [x] Observation state is injected only after tool turns
- [ ] LSP-backed definition/reference lookup
- [ ] Relevance filtering for large observation state
- [ ] End-to-end tool-call reduction validation on real tasks

## Current implementation

`internal/observation` keeps session-local state. `read_file` observations record a SHA-256 content hash and a lightweight Go source summary. Shell and tool errors are parsed for file/line diagnostics. Runtime state is added to the next model turn rather than replaying the full observation history on every request.

This phase deliberately does not add an embedding/vector retrieval layer. The next context source is expected to be deterministic symbol lookup, preferably through an available Go LSP implementation.
