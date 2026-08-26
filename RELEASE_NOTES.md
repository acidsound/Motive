## Motive v0.1.0

Changes since v0.0.0:

### Release Pipeline
- Replaced GoReleaser with a self-contained tag-triggered GitHub Actions workflow
- Added Linux ARM64 (Android/Termux) build
- Added macOS universal binary (Intel + Apple Silicon)
- All releases now produce: Linux amd64, Linux arm64, Windows amd64, macOS universal + SHA256 checksums

### CLI
- One-shot progress animation with phase labels for faster feedback

### Simplification
- Removed unit/decomposition runtime semantics (runtime, TUI, tools, telemetry)
- Removed unit provenance views and key bindings
- Removed decomposition protocol documentation and guidance
- Documentation now reflects the simplified single-session execution model

### Documentation
- README fully translated to English
- Documented supported release platforms and first-run setup
