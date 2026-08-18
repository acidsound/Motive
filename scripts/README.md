# llama-server benchmark

This benchmark is intentionally outside the Motive runtime. It measures how the configured llama-server behaves as the prompt grows, so runtime/context changes can be compared against a fixed server baseline.

## Run

From the repository root:

```bash
python3 scripts/benchmark_llama_server.py \
  --url http://100.72.102.121:18080/v1 \
  --model Qwen3.8-27B \
  --tokens 6000 7000 8000 9000 10000 11000 \
  --runs 3 \
  --output-tokens 32
```

The default seed is `scripts/benchmark_prompt.txt`.

The script first tries llama-server's native `/tokenize` endpoint to calibrate each prompt. If that endpoint is unavailable, it falls back to a byte-based approximation, and the `actual_tokens` column is shown as `?`.

Output is CSV-like on stdout:

```text
target_tokens,actual_tokens,run,latency_s,completion_tokens
```

Median/min/max timing summaries are written to stderr after each target size.

For this experiment, keep the llama-server command line fixed while running the benchmark. In particular, do not change context size, KV cache quantization, batch settings, speculative decoding, or reasoning settings between runs. The purpose is to identify whether a latency discontinuity exists around the prompt sizes observed in Motive.
