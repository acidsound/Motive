## Motive v0.3.0

Changes since v0.2.0:

### Token Metrics
- Added client-side tok/s estimation during streaming (no server timings required)
- Request `stream_options.include_usage` to receive token usage from the model server
- Fallback `ServerTimings` calculation from usage data when the server does not report timings
- Live tok/s display in the TUI status bar updates as deltas arrive

### Tests
- Added test for client-side timing calculation from stream usage
- Added test for live tok/s on streaming deltas
