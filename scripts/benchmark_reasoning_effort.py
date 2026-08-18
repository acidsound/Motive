#!/usr/bin/env python3
import argparse
import json
import statistics
import time
import urllib.error
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent


def post(url, payload, timeout):
    body = json.dumps(payload, ensure_ascii=False).encode()
    req = urllib.request.Request(
        url,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    started = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            data = json.load(resp)
        elapsed = time.perf_counter() - started
        return elapsed, data, None
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        elapsed = time.perf_counter() - started
        return elapsed, None, f"HTTP {exc.code}: {detail}"


def extract(resp):
    msg = ((resp.get("choices") or [{}])[0].get("message") or {})
    usage = resp.get("usage") or {}
    timings = resp.get("timings") or {}
    reasoning = msg.get("reasoning_content") or msg.get("reasoning") or ""
    calls = msg.get("tool_calls") or []
    return {
        "finish": ((resp.get("choices") or [{}])[0].get("finish_reason") or ""),
        "reasoning_tokens": timings.get("predicted_n"),
        "reasoning_bytes": len(reasoning.encode("utf-8")) if isinstance(reasoning, str) else 0,
        "completion_tokens": usage.get("completion_tokens"),
        "tool_calls": len(calls),
        "prompt_tokens": usage.get("prompt_tokens"),
        "predicted_ms": timings.get("predicted_ms"),
        "prompt_ms": timings.get("prompt_ms"),
        "response_text": msg.get("content") or "",
        "reasoning_text": reasoning,
    }


def run_case(base_url, model, name, task, timeout, max_tokens):
    messages = [
        {
            "role": "system",
            "content": (
                "You are evaluating a software-engineering execution task. "
                "Do not modify files. Analyze the task carefully and return a concise, actionable plan."
            ),
        },
        {"role": "user", "content": task},
    ]

    variants = {
        "default": {},
        "low_top_level": {"reasoning_effort": "low"},
        "medium_top_level": {"reasoning_effort": "medium"},
        "xhigh_top_level": {"reasoning_effort": "xhigh"},
        "low_kwargs": {"chat_template_kwargs": {"reasoning_effort": "low"}},
        "medium_kwargs": {"chat_template_kwargs": {"reasoning_effort": "medium"}},
        "xhigh_kwargs": {"chat_template_kwargs": {"reasoning_effort": "xhigh"}},
    }

    extra = variants[name]
    payload = {
        "model": model,
        "messages": messages,
        "temperature": 0.6,
        "max_tokens": max_tokens,
        **extra,
    }
    elapsed, resp, error = post(base_url.rstrip("/") + "/chat/completions", payload, timeout)
    if error:
        return {
            "case": name,
            "latency_s": elapsed,
            "status": "error",
            "error": error,
        }
    info = extract(resp)
    info.update({"case": name, "latency_s": elapsed, "status": "ok"})
    return info


def main():
    ap = argparse.ArgumentParser(description="Probe Qwen3.8 reasoning_effort handling in llama-server.")
    ap.add_argument("--url", default="http://127.0.0.1:8080/v1")
    ap.add_argument("--model", required=True)
    ap.add_argument("--runs", type=int, default=3)
    ap.add_argument("--warmup", type=int, default=1)
    ap.add_argument("--max-tokens", type=int, default=512)
    ap.add_argument("--timeout", type=float, default=300)
    args = ap.parse_args()

    task = (HERE / "benchmark_reasoning_effort_prompt.txt").read_text(encoding="utf-8").strip()
    cases = [
        "default",
        "low_top_level",
        "medium_top_level",
        "xhigh_top_level",
        "low_kwargs",
        "medium_kwargs",
        "xhigh_kwargs",
    ]

    print(
        "case,run,latency_s,prompt_tokens,reasoning_tokens,reasoning_bytes,completion_tokens,prompt_ms,predicted_ms,tool_calls,finish,status"
    )

    results = {case: [] for case in cases}

    for case in cases:
        for _ in range(args.warmup):
            run_case(args.url, args.model, case, task, args.timeout, args.max_tokens)
        for run in range(1, args.runs + 1):
            result = run_case(args.url, args.model, case, task, args.timeout, args.max_tokens)
            results[case].append(result)
            if result["status"] != "ok":
                print(f"{case},{run},{result['latency_s']:.3f},?,?,?,?,?,?,?,?,error")
                print(f"# {case} ERROR: {result['error']}")
                continue
            print(
                f"{case},{run},{result['latency_s']:.3f},"
                f"{result.get('prompt_tokens','?')},"
                f"{result.get('reasoning_tokens','?')},"
                f"{result.get('reasoning_bytes','?')},"
                f"{result.get('completion_tokens','?')},"
                f"{result.get('prompt_ms','?')},"
                f"{result.get('predicted_ms','?')},"
                f"{result.get('tool_calls',0)},"
                f"{result.get('finish','')},ok"
            )

    print("\n=== summary ===")
    for case in cases:
        good = [r for r in results[case] if r.get("status") == "ok"]
        if not good:
            print(f"{case:18s} ERROR")
            continue
        latency = statistics.median(r["latency_s"] for r in good)
        reasoning = [r["reasoning_tokens"] for r in good if isinstance(r.get("reasoning_tokens"), (int, float))]
        predicted = [r["predicted_ms"] for r in good if isinstance(r.get("predicted_ms"), (int, float))]
        print(
            f"{case:18s} latency={latency:.3f}s "
            f"reasoning_tokens={statistics.median(reasoning) if reasoning else '?'} "
            f"predicted_ms={statistics.median(predicted) if predicted else '?'}"
        )

    print("\n=== interpretation ===")
    print("Compare *_top_level against *_kwargs. If only chat_template_kwargs changes reasoning, use kwargs in Motive.")
    print("If low/medium/xhigh all produce nearly identical reasoning token counts, reasoning_effort is not effective in this server/model path.")
    print("The current server has --reasoning-budget 8196. Per-request thinking_budget_tokens cannot override a CLI budget; testing that requires restarting llama-server without --reasoning-budget.")
    print("Qwen3.8 officially defines xhigh, medium and low reasoning effort; there is no high tier in the Qwen3.8 template.")


if __name__ == "__main__":
    main()
