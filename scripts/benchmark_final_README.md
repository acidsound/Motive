# Final llama-server diagnostic

Run from the repository root after pulling:

```bash
python3 scripts/benchmark_final.py \
  --url http://100.72.102.121:18080/v1 \
  --model Qwen3.8-27B \
  --tokens 8000 10000 12000 \
  --runs 3 \
  --warmup 1 \
  --output-tokens 64
```

The benchmark runs five cases:

- `plain`: large prompt, no tools.
- `reasoning`: the same prompt plus a reasoning workload.
- `tools`: the Motive tool schema is present, but tool use is disallowed by the request.
- `required_tool`: `tool_choice=required` is sent and the run is marked failed when no actual tool call is returned.
- `roundtrip`: required tool call, synthetic tool result, and a second assistant turn.

The script also uses the server's native `/tokenize` endpoint when available, including when the supplied URL ends in `/v1`, so `actual_tokens` should no longer be `?` on a normal llama-server deployment.

Keep the llama-server command line fixed during the benchmark. The current experiment is intended to separate prompt size, reasoning workload, tool-schema overhead, actual tool-call generation, and multi-turn history. Once these results are collected, stop benchmarking and return to Motive Runtime work.
