#!/usr/bin/env python3
"""Benchmark llama-server prompt processing through its OpenAI-compatible API.

The benchmark deliberately keeps generation short and varies only prompt size.
It can optionally use llama-server's native /tokenize endpoint to calibrate the
prompt to target token counts before sending it through /v1/chat/completions.
"""

from __future__ import annotations

import argparse
import json
import statistics
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


def http_json(url: str, payload: dict[str, Any], timeout: float) -> dict[str, Any]:
    body = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.load(resp)


def tokenize(base_url: str, text: str, timeout: float) -> int | None:
    """Use llama-server's native /tokenize endpoint when available."""
    url = base_url.rstrip("/") + "/tokenize"
    try:
        data = http_json(url, {"content": text}, timeout)
    except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError, json.JSONDecodeError):
        return None
    tokens = data.get("tokens")
    if isinstance(tokens, list):
        return len(tokens)
    return None


def make_prompt(seed: str, target_tokens: int, current_tokens: int | None) -> str:
    if not seed.strip():
        raise ValueError("seed prompt is empty")
    if current_tokens is None or current_tokens <= 0:
        # Rough fallback when /tokenize is not available.
        repeats = max(1, round(target_tokens * 4 / len(seed)))
        return ((seed.rstrip() + "\n") * repeats)[: target_tokens * 4]

    repeats = max(1, target_tokens // current_tokens)
    prompt = (seed.rstrip() + "\n") * repeats
    return prompt


def calibrate(base_url: str, seed: str, target_tokens: int, timeout: float) -> tuple[str, int | None]:
    """Grow/shrink a repeated seed until close to the target token count."""
    initial = tokenize(base_url, seed, timeout)
    if initial is None:
        prompt = make_prompt(seed, target_tokens, None)
        return prompt, None

    if initial == 0:
        raise ValueError("/tokenize returned zero tokens for seed prompt")

    repeats = max(1, target_tokens // initial)
    prompt = (seed.rstrip() + "\n") * repeats
    count = tokenize(base_url, prompt, timeout)

    # Refine by adding/removing one seed at a time, then trim the final seed
    # proportionally if we are still above the target.
    while count is not None and count < target_tokens:
        candidate = prompt + seed.rstrip() + "\n"
        candidate_count = tokenize(base_url, candidate, timeout)
        if candidate_count is None or candidate_count > target_tokens:
            break
        prompt, count = candidate, candidate_count

    if count is not None and count > target_tokens:
        lines = prompt.splitlines(True)
        lo, hi = 0, len(lines)
        best = ""
        best_count = 0
        while lo <= hi:
            mid = (lo + hi) // 2
            candidate = "".join(lines[:mid])
            candidate_count = tokenize(base_url, candidate, timeout)
            if candidate_count is None:
                break
            if candidate_count <= target_tokens:
                best, best_count = candidate, candidate_count
                lo = mid + 1
            else:
                hi = mid - 1
        if best:
            prompt, count = best, best_count

    return prompt, count


def run_once(
    api_url: str,
    model: str,
    prompt: str,
    output_tokens: int,
    temperature: float,
    timeout: float,
) -> tuple[float, int | None]:
    payload = {
        "model": model,
        "messages": [
            {
                "role": "system",
                "content": "Reply with exactly one short sentence: benchmark complete.",
            },
            {"role": "user", "content": prompt},
        ],
        "max_tokens": output_tokens,
        "temperature": temperature,
    }
    started = time.perf_counter()
    response = http_json(api_url.rstrip("/") + "/chat/completions", payload, timeout)
    elapsed = time.perf_counter() - started

    usage = response.get("usage", {})
    completion_tokens = usage.get("completion_tokens")
    return elapsed, completion_tokens if isinstance(completion_tokens, int) else None


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8080/v1", help="OpenAI-compatible /v1 URL")
    parser.add_argument("--model", required=True, help="Model id sent to /v1/chat/completions")
    parser.add_argument("--tokens", nargs="+", type=int, default=[6000, 7000, 8000, 9000, 10000, 11000])
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument("--output-tokens", type=int, default=32)
    parser.add_argument("--temperature", type=float, default=0.0)
    parser.add_argument("--timeout", type=float, default=300.0)
    parser.add_argument("--seed", type=Path, default=Path(__file__).with_name("benchmark_prompt.txt"))
    args = parser.parse_args()

    if args.runs < 1:
        parser.error("--runs must be >= 1")
    if any(value <= 0 for value in args.tokens):
        parser.error("--tokens must contain positive values")

    try:
        seed = args.seed.read_text(encoding="utf-8")
    except OSError as exc:
        print(f"cannot read seed: {exc}", file=sys.stderr)
        return 2

    print("target_tokens,actual_tokens,run,latency_s,completion_tokens")

    for target in args.tokens:
        prompt, actual_tokens = calibrate(args.url, seed, target, args.timeout)
        measurements: list[float] = []

        for run in range(1, args.runs + 1):
            try:
                elapsed, completion_tokens = run_once(
                    args.url,
                    args.model,
                    prompt,
                    args.output_tokens,
                    args.temperature,
                    args.timeout,
                )
            except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError, json.JSONDecodeError) as exc:
                print(f"benchmark request failed at target {target}: {exc}", file=sys.stderr)
                return 1

            measurements.append(elapsed)
            actual_text = str(actual_tokens) if actual_tokens is not None else "?"
            completion_text = str(completion_tokens) if completion_tokens is not None else "?"
            print(f"{target},{actual_text},{run},{elapsed:.3f},{completion_text}")

        if measurements:
            print(
                f"# target={target} median={statistics.median(measurements):.3f}s "
                f"min={min(measurements):.3f}s max={max(measurements):.3f}s",
                file=sys.stderr,
            )

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
