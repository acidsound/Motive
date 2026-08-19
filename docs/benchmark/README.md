# llama-server benchmark

This benchmark is intentionally outside the Motive runtime. It is a diagnostic harness for separating latency caused by prompt size, Motive's tool schemas, tool-call generation, and a multi-message tool round trip.

## What it tests

Four cases are available:

- `plain`: large prompt, no tools.
- `tools`: the same prompt plus Motive's 9 tool definitions; the request is instructed not to call a tool.
- `tool_call`: the same prompt plus the 9 tool definitions and a forced `read_file` tool call.
- `roundtrip`: a forced tool call followed by a synthetic tool result and one more assistant request.

Each case is run at controlled prompt sizes. `/tokenize` is used when available so the seed text is calibrated to the requested token count. A warmup run is excluded from the measurements.

The tool schema and synthetic observation are explicit fixtures:

- `benchmark_tools.json`
- `benchmark_tool_result.txt`
- `benchmark_prompt.txt`

This makes the experiment reproducible and keeps benchmark inputs separate from Motive's implementation.

## Recommended experiment

Keep the llama-server command line unchanged during the experiment. Use the same model, context size, KV-cache quantization, batch sizes, speculative decoding, temperature, and reasoning configuration that produced the Motive latency spike.

```bash
python3 docs/benchmark/benchmark_llama_server.py \
  --url http://100.72.102.121:18080/v1 \
  --model Qwen3.8-27B \
  --tokens 8000 10000 12000 \
  --runs 3 \
  --warmup 1 \
  --output-tokens 64
```

The four cases are enabled by default. To run a subset:

```bash
python3 docs/benchmark/benchmark_llama_server.py \
  --url http://100.72.102.121:18080/v1 \
  --model Qwen3.8-27B \
  --tokens 8000 10000 12000 \
  --runs 3 \
  --cases plain tools tool_call
```

## Output

CSV-like measurements go to stdout:

```text
case,target_tokens,actual_tokens,run,latency_s,completion_tokens,response_kind,response_bytes
```

Summary statistics and a simple diagnostic are written to stderr. The diagnostic is deliberately conservative: it flags a large tool-call multiplier, a large tool-schema multiplier, or reports that no large tool-specific multiplier was observed.

## How to interpret it

Compare the median latency at the same target size:

```text
plain → prompt-size baseline
  ↓
tools → cost of carrying Motive's tool schema
  ↓
tool_call → cost of generating a tool call with those tools
  ↓
roundtrip → cost of another model turn after a tool observation
```

Typical conclusions:

- `plain` is slow at the same sizes → investigate llama-server prompt processing or server configuration.
- `plain` is fast, `tools` becomes slow → tool schema/context overhead is significant.
- `plain` and `tools` are fast, `tool_call` becomes slow → tool-call/reasoning generation is the main suspect.
- `tool_call` is reasonable but `roundtrip` grows sharply → the multi-message tool loop or accumulated context is the main suspect.
- All four are fast while Motive is slow → inspect Motive's exact message serialization, reasoning fields, request parameters, or loop semantics rather than blaming prompt length alone.

This benchmark is intended to answer the current diagnostic question and then stop. It is not a general-purpose model benchmark.
