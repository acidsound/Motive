#!/usr/bin/env python3
"""Measure llama-server latency for plain, tool-aware, and tool-call workloads.

The benchmark is intentionally outside Motive's runtime. It separates four
workloads that are relevant to Motive's slow model turns:

  plain       Large prompt, no tools.
  tools       Same prompt with Motive's tool definitions, but no tool call.
  tool_call   Same prompt with tools and a forced single tool call.
  roundtrip   Same prompt, forced tool call, then one assistant turn after a
              synthetic tool result.

This does not benchmark model quality. It is a diagnostic to identify whether
latency comes from prompt size, tool schemas, tool-call generation, or the
additional message structure of a tool loop.
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


DEFAULT_TOOLS = [
    {"type": "function", "function": {"name": "read_file", "description": "Read a UTF-8 text file in the workspace.", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}}},
    {"type": "function", "function": {"name": "write_file", "description": "Create or replace a UTF-8 text file.", "parameters": {"type": "object", "properties": {"path": {"type": "string"}, "content": {"type": "string"}}, "required": ["path", "content"]}}},
    {"type": "function", "function": {"name": "delete_file", "description": "Delete a file in the workspace.", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}}},
    {"type": "function", "function": {"name": "list_files", "description": "List files below a workspace directory.", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}},
    {"type": "function", "function": {"name": "search_files", "description": "Search text in workspace files.", "parameters": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]}}},
    {"type": "function", "function": {"name": "shell", "description": "Execute a shell command in the workspace.", "parameters": {"type": "object", "properties": {"command": {"type": "string"}}, "required": ["command"]}}},
    {"type": "function", "function": {"name": "web_search", "description": "Search the public web.", "parameters": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]}}},
    {"type": "function", "function": {"name": "git_status", "description": "Show Git branch, revision and working-tree changes.", "parameters": {"type": "object", "properties": {}}}},
    {"type": "function", "function": {"name": "git_diff", "description": "Show the current unstaged Git diff.", "parameters": {"type": "object", "properties": {}}}},
]


def http_json(url: str, payload: dict[str, Any], timeout: float) -> dict[str, Any]:
    body = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"}, method="POST")
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.load(resp)


def tokenize(base_url: str, text: str, timeout: float) -> int | None:
    try:
        data = http_json(base_url.rstrip("/") + "/tokenize", {"content": text}, timeout)
    except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError, json.JSONDecodeError):
        return None
    tokens = data.get("tokens")
    return len(tokens) if isinstance(tokens, list) else None


def calibrate(base_url: str, seed: str, target_tokens: int, timeout: float) -> tuple[str, int | None]:
    seed = seed.strip() + "\n"
    initial = tokenize(base_url, seed, timeout)
    if initial is None or initial == 0:
        return (seed * max(1, target_tokens * 4 // max(1, len(seed)))), None

    prompt = seed * max(1, target_tokens // initial)
    count = tokenize(base_url, prompt, timeout)
    if count is None:
        return prompt, None

    while count < target_tokens:
        candidate = prompt + seed
        candidate_count = tokenize(base_url, candidate, timeout)
        if candidate_count is None or candidate_count > target_tokens:
            break
        prompt, count = candidate, candidate_count

    # Trim complete lines until the prompt is at or below the requested size.
    if count > target_tokens:
        lines = prompt.splitlines(True)
        lo, hi, best, best_count = 0, len(lines), "", 0
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


def response_stats(response: dict[str, Any]) -> tuple[int | None, int]:
    usage = response.get("usage") or {}
    completion = usage.get("completion_tokens")
    choices = response.get("choices") or []
    response_bytes = len(json.dumps(response, ensure_ascii=False).encode("utf-8"))
    return (completion if isinstance(completion, int) else None), response_bytes


def request_once(
    api_url: str,
    model: str,
    messages: list[dict[str, Any]],
    tools: list[dict[str, Any]] | None,
    tool_choice: Any,
    output_tokens: int,
    temperature: float,
    timeout: float,
) -> tuple[float, dict[str, Any]]:
    payload: dict[str, Any] = {
        "model": model,
        "messages": messages,
        "max_tokens": output_tokens,
        "temperature": temperature,
    }
    if tools:
        payload["tools"] = tools
    if tool_choice is not None:
        payload["tool_choice"] = tool_choice

    started = time.perf_counter()
    response = http_json(api_url.rstrip("/") + "/chat/completions", payload, timeout)
    return time.perf_counter() - started, response


def classify_case(name: str, response: dict[str, Any]) -> str:
    choices = response.get("choices") or []
    if not choices:
        return "no_choice"
    message = choices[0].get("message") or {}
    if message.get("tool_calls"):
        return "tool_call"
    return "text"


def run_case(
    case: str,
    base_url: str,
    model: str,
    prompt: str,
    tools: list[dict[str, Any]],
    output_tokens: int,
    temperature: float,
    timeout: float,
) -> tuple[float, dict[str, Any], int | None]:
    system = "You are a deterministic benchmark participant. Do exactly what the user asks."
    messages: list[dict[str, Any]] = [{"role": "system", "content": system}, {"role": "user", "content": prompt}]

    if case == "plain":
        return (*request_once(base_url + "/chat/completions", model, messages, None, None, output_tokens, temperature, timeout), tokenize(base_url, prompt, timeout))

    if case == "tools":
        prompt = prompt + "\nDo not use any tools. Reply with one short sentence."
        messages[1]["content"] = prompt
        return (*request_once(base_url + "/chat/completions", model, messages, tools, "none", output_tokens, temperature, timeout), tokenize(base_url, prompt, timeout))

    if case == "tool_call":
        prompt = prompt + "\nUse the read_file tool exactly once to inspect the file README.md. Then stop."
        messages[1]["content"] = prompt
        choice = {"type": "function", "function": {"name": "read_file"}}
        return (*request_once(base_url + "/chat/completions", model, messages, tools, choice, output_tokens, temperature, timeout), tokenize(base_url, prompt, timeout))

    if case == "roundtrip":
        prompt = prompt + "\nUse the read_file tool exactly once to inspect README.md. After observing the result, briefly say whether you have enough information."
        messages[1]["content"] = prompt
        choice = {"type": "function", "function": {"name": "read_file"}}
        first_elapsed, first_response = request_once(base_url + "/chat/completions", model, messages, tools, choice, output_tokens, temperature, timeout)
        choices = first_response.get("choices") or []
        if not choices:
            return first_elapsed, first_response, tokenize(base_url, prompt, timeout)
        assistant = choices[0].get("message") or {}
        messages.append(assistant)
        tool_calls = assistant.get("tool_calls") or []
        if tool_calls:
            messages.append({"role": "tool", "tool_call_id": tool_calls[0].get("id", "bench"), "content": "README.md: synthetic benchmark tool result. The file exists and contains a normal software project description."})
        second_elapsed, second_response = request_once(base_url + "/chat/completions", model, messages, tools, "none", output_tokens, temperature, timeout)
        combined = dict(second_response)
        combined["_first_elapsed"] = first_elapsed
        combined["_second_elapsed"] = second_elapsed
        return second_elapsed, combined, tokenize(base_url, prompt, timeout)

    raise ValueError(f"unknown case: {case}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8080/v1", help="OpenAI-compatible /v1 URL")
    parser.add_argument("--model", required=True)
    parser.add_argument("--tokens", nargs="+", type=int, default=[8000, 10000, 12000])
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument("--warmup", type=int, default=1, help="warmup calls per case/size, excluded from statistics")
    parser.add_argument("--output-tokens", type=int, default=64)
    parser.add_argument("--temperature", type=float, default=0.0)
    parser.add_argument("--timeout", type=float, default=300.0)
    parser.add_argument("--cases", nargs="+", choices=["plain", "tools", "tool_call", "roundtrip"], default=["plain", "tools", "tool_call", "roundtrip"])
    parser.add_argument("--seed", type=Path, default=Path(__file__).with_name("benchmark_prompt.txt"))
    args = parser.parse_args()

    if args.runs < 1 or args.warmup < 0:
        parser.error("--runs must be >= 1 and --warmup must be >= 0")

    try:
        seed = args.seed.read_text(encoding="utf-8")
    except OSError as exc:
        print(f"cannot read seed: {exc}", file=sys.stderr)
        return 2

    print("case,target_tokens,actual_tokens,run,latency_s,completion_tokens,response_kind,response_bytes")
    summaries: dict[str, dict[int, list[float]]] = {case: {} for case in args.cases}

    for target in args.tokens:
        base_prompt, _ = calibrate(args.url, seed, target, args.timeout)
        for case in args.cases:
            measurements: list[float] = []
            for _ in range(args.warmup):
                try:
                    run_case(case, args.url, args.model, base_prompt, DEFAULT_TOOLS, args.output_tokens, args.temperature, args.timeout)
                except Exception as exc:
                    print(f"warmup failed case={case} target={target}: {exc}", file=sys.stderr)
                    return 1

            for run in range(1, args.runs + 1):
                try:
                    elapsed, response, actual_tokens = run_case(case, args.url, args.model, base_prompt, DEFAULT_TOOLS, args.output_tokens, args.temperature, args.timeout)
                except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError, json.JSONDecodeError, ValueError) as exc:
                    print(f"benchmark request failed case={case} target={target}: {exc}", file=sys.stderr)
                    return 1
                completion_tokens, response_bytes = response_stats(response)
                measurements.append(elapsed)
                summaries[case].setdefault(target, []).append(elapsed)
                actual_text = str(actual_tokens) if actual_tokens is not None else "?"
                completion_text = str(completion_tokens) if completion_tokens is not None else "?"
                print(f"{case},{target},{actual_text},{run},{elapsed:.3f},{completion_text},{classify_case(case, response)},{response_bytes}")

            if measurements:
                print(f"# case={case} target={target} median={statistics.median(measurements):.3f}s min={min(measurements):.3f}s max={max(measurements):.3f}s", file=sys.stderr)

    print("\n=== diagnostic summary ===", file=sys.stderr)
    for case in args.cases:
        points = summaries[case]
        medians = {target: statistics.median(values) for target, values in points.items()}
        if medians:
            print(f"{case}: " + ", ".join(f"{target}={value:.3f}s" for target, value in medians.items()), file=sys.stderr)

    if "plain" in summaries and "tool_call" in summaries:
        common_targets = sorted(set(summaries["plain"]) & set(summaries["tool_call"]))
        if common_targets:
            target = common_targets[-1]
            plain = statistics.median(summaries["plain"][target])
            tool_call = statistics.median(summaries["tool_call"][target])
            if tool_call > plain * 5 and tool_call > 5:
                print(f"DIAGNOSIS: tool-call generation is the dominant latency multiplier at {target} tokens ({plain:.3f}s plain vs {tool_call:.3f}s tool_call).", file=sys.stderr)
            elif tool_call > plain * 2:
                print(f"DIAGNOSIS: tool-call generation is materially slower at {target} tokens ({plain:.3f}s plain vs {tool_call:.3f}s tool_call).", file=sys.stderr)
            else:
                print(f"DIAGNOSIS: no large tool-call multiplier at {target} tokens ({plain:.3f}s plain vs {tool_call:.3f}s tool_call).", file=sys.stderr)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
