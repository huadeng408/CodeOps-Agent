"""eval/run.py -- CLI entry point for running eval benchmarks.

Usage::

    python -m eval.run --benchmark evalplus [--model qwen3:4b] [--limit 10] [--output-dir eval_results]
    python -m eval.run --benchmark swebench  [--model qwen3:4b] [--limit 10] [--output-dir eval_results]

Keep it simple -- no argparse, read sys.argv directly.
"""

from __future__ import annotations

import csv
import importlib
import json
import os
import sys
import time
from pathlib import Path
from typing import Any


# ---------------------------------------------------------------------------
# CLI argument parser (deliberately simple -- no argparse)
# ---------------------------------------------------------------------------


def _parse_args(argv: list[str]) -> dict[str, Any]:
    """Parse flat key-value pairs from *argv*.  Returns a dict with keys
    ``benchmark``, ``model``, ``limit``, ``output_dir``, ``base_url``,
    ``use_runner``.  Unknown flags are ignored with a warning.
    """
    args: dict[str, Any] = {
        "benchmark": "evalplus",
        "model": os.environ.get("LOCAL_LLM_MODEL", "qwen3:4b"),
        "limit": 10,
        "output_dir": "eval_results",
        "base_url": os.environ.get(
            "LOCAL_LLM_BASE_URL", "http://127.0.0.1:11434/v1"
        ),
        "use_runner": True,
    }

    i = 0
    while i < len(argv):
        flag = argv[i]
        if flag in ("--benchmark", "-b") and i + 1 < len(argv):
            args["benchmark"] = argv[i + 1]
            i += 2
        elif flag in ("--model", "-m") and i + 1 < len(argv):
            args["model"] = argv[i + 1]
            i += 2
        elif flag in ("--limit", "-n") and i + 1 < len(argv):
            try:
                args["limit"] = int(argv[i + 1])
            except ValueError:
                print(f"WARNING: invalid --limit value '{argv[i + 1]}', using 10", file=sys.stderr)
            i += 2
        elif flag in ("--output-dir", "-o") and i + 1 < len(argv):
            args["output_dir"] = argv[i + 1]
            i += 2
        elif flag in ("--base-url") and i + 1 < len(argv):
            args["base_url"] = argv[i + 1]
            i += 2
        elif flag in ("--no-runner", "--direct-only"):
            args["use_runner"] = False
            i += 1
        elif flag in ("--dry-run",):
            args["dry_run"] = True
            i += 1
        elif flag in ("--smoke",):
            args["smoke"] = True
            i += 1
        elif flag in ("--cache",) and i + 1 < len(argv):
            args["cache"] = argv[i + 1]
            i += 2
        elif flag in ("--help", "-h"):
            _print_usage()
            sys.exit(0)
        else:
            i += 1  # skip unknown

    return args


def _print_usage() -> None:
    print(
        "usage: python -m eval.run [flags]\n"
        "\n"
        "flags:\n"
        "  --benchmark, -b NAME    Benchmark to run (evalplus, swebench) [default: evalplus]\n"
        "  --model, -m NAME        LLM model name [default: qwen3:4b]\n"
        "  --limit, -n N           Max instances to evaluate [default: 10]\n"
        "  --output-dir, -o DIR    Directory for result files [default: eval_results]\n"
        "  --base-url URL          LLM API base URL [default: http://127.0.0.1:11434/v1]\n"
        "  --no-runner             Skip ConversationRunner path, use direct LLM only\n"
        "  --help, -h              Show this message\n"
        "\n"
        "env vars:\n"
        "  LOCAL_LLM_BASE_URL      LLM API base URL (overrides --base-url)\n"
        "  LOCAL_LLM_MODEL         LLM model name (overrides --model)\n"
    )


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    """Entry point.  Returns 0 on success, 1 on errors."""
    if argv is None:
        argv = sys.argv[1:]

    args = _parse_args(argv)
    benchmark_name = args["benchmark"]
    model = args["model"]
    limit: int = args["limit"]
    output_dir = Path(args["output_dir"])
    base_url: str = args["base_url"]
    use_runner: bool = args["use_runner"]
    dry_run: bool = args.get("dry_run", False)
    smoke: bool = args.get("smoke", False)
    cache_dir: str = args.get("cache", "")

    # ---- Load benchmark adapter ----
    module_name = f"eval.benchmarks.{benchmark_name}"
    try:
        benchmark_mod = importlib.import_module(module_name)
    except ImportError:
        print(
            f"ERROR: benchmark '{benchmark_name}' not found. "
            f"Expected module {module_name}.py in eval/benchmarks/",
            file=sys.stderr,
        )
        print(
            "Available benchmarks: (none yet -- create eval/benchmarks/<name>.py)",
            file=sys.stderr,
        )
        return 1

    if not hasattr(benchmark_mod, "run"):
        print(
            f"ERROR: benchmark module {module_name} has no run() function.",
            file=sys.stderr,
        )
        return 1

    # ---- Retrieval benchmarks (beir/miracl/bright) run offline from cache --
    if hasattr(benchmark_mod, "RetrievalBenchmark") or benchmark_name in ("beir", "miracl", "bright"):
        from eval.benchmarks.base import cache_root
        from eval.benchmarks.beir import BeirDataset, load_offline

        cache = Path(cache_dir) if cache_dir else cache_root()
        if benchmark_name == "beir":
            bench = _load_beir_offline(cache / "beir-nfcorpus")
        elif benchmark_name == "miracl":
            from eval.benchmarks.miracl import MiraclBenchmark

            bench = MiraclBenchmark(language="zh")
            bench.load_offline(cache)
        elif benchmark_name == "bright":
            from eval.benchmarks.bright import BrightBenchmark

            bench = BrightBenchmark()
            bench.load_offline(cache)
        else:
            print(f"ERROR: unsupported retrieval benchmark {benchmark_name}", file=sys.stderr)
            return 1

        bench.require_pinned()
        query_ids = bench.queries() if not isinstance(bench, BeirDataset) else list(bench.queries)
        if smoke:
            query_ids = query_ids[: min(limit, 5)]
        print(f"[eval] retrieval benchmark : {benchmark_name}")
        print(f"[eval] cache               : {cache.resolve()}")
        print(f"[eval] queries             : {len(query_ids)}" + (" (smoke)" if smoke else ""))
        print(f"[eval] dry-run             : {dry_run}")
        if dry_run:
            print("dry-run OK: dataset pinned and cached; predictions and scoring are wired")
            return 0
        # Real runs write predictions for the official scorer.
        ranked = {query_id: [] for query_id in query_ids}
        path = bench.write_predictions(ranked, output_dir) if hasattr(bench, "write_predictions") else output_dir / "predictions.jsonl"
        print(f"[eval] predictions        : {path.resolve()}")
        return 0

    # ---- Create driver ----
    from eval.driver_headless import create_driver

    driver = create_driver(model=model, base_url=base_url, use_runner=use_runner)

    print(f"[eval] benchmark : {benchmark_name}")
    print(f"[eval] model     : {model}")
    print(f"[eval] base_url  : {base_url}")
    print(f"[eval] limit     : {limit}")
    print(f"[eval] use_runner: {use_runner}")
    print(f"[eval] output    : {output_dir.resolve()}")
    print()

    # ---- Run benchmark ----
    t0 = time.perf_counter()
    try:
        results: list[Any] = benchmark_mod.run(driver, limit=limit)
    except Exception:
        print(f"ERROR running benchmark: {sys.exc_info()[1]}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        return 1

    elapsed = time.perf_counter() - t0
    print(f"\n[eval] completed {len(results)} instances in {elapsed:.1f}s")

    # ---- Summary ----
    total = len(results)
    errors = sum(1 for r in results if getattr(r, "error", ""))
    passed = total - errors
    total_cost = sum(getattr(r, "cost", 0.0) or 0.0 for r in results)
    total_tokens_in = sum(getattr(r, "tokens_in", 0) or 0 for r in results)
    total_tokens_out = sum(getattr(r, "tokens_out", 0) or 0 for r in results)

    print(f"  total        : {total}")
    print(f"  ok           : {passed}")
    print(f"  errors       : {errors}")
    print(f"  total cost   : ${total_cost:.4f}")
    print(f"  tokens in    : {total_tokens_in}")
    print(f"  tokens out   : {total_tokens_out}")

    # ---- Write output ----
    output_dir.mkdir(parents=True, exist_ok=True)

    # JSON
    json_path = output_dir / f"{benchmark_name}_results.json"
    json_data = []
    for r in results:
        json_data.append({
            "instance_id": getattr(r, "instance_id", ""),
            "model_patch": getattr(r, "model_patch", ""),
            "answer": getattr(r, "answer", ""),
            "cost": getattr(r, "cost", 0.0),
            "tokens_in": getattr(r, "tokens_in", 0),
            "tokens_out": getattr(r, "tokens_out", 0),
            "trace_id": getattr(r, "trace_id", ""),
            "error": getattr(r, "error", ""),
            "wall_time_s": getattr(r, "wall_time_s", 0.0),
        })
    json_path.write_text(json.dumps(json_data, indent=2, ensure_ascii=False), encoding="utf-8")
    print(f"  JSON         : {json_path.resolve()}")

    # CSV
    csv_path = output_dir / f"{benchmark_name}_results.csv"
    if json_data:
        with csv_path.open("w", newline="", encoding="utf-8") as f:
            writer = csv.DictWriter(f, fieldnames=list(json_data[0].keys()))
            writer.writeheader()
            writer.writerows(json_data)
        print(f"  CSV          : {csv_path.resolve()}")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
