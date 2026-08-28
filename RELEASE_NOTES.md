## Motive v0.2.0

Changes since v0.1.0:

### Reasoning Effort
- Added support for reasoning-effort `"off"` in addition to low/medium/high
- Hardened tool-call follow-ups: model messages that continue a tool sequence are now handled reliably, reducing dropped tool loops

### Release Pipeline
- Fixed checksum generation on the macOS CI runner (`shasum` instead of `sha256sum`)
- Added installation guide to README

### Documentation
- Clarified Motive execution boundaries (what the runtime does and does not do)
- Synced Korean design rationale

### Housekeeping
- Added a pre-commit hook for formatting/checks
- Added a local request-dump proxy script for debugging
- Ignored locally built `motive` binary in Git
