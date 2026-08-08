"""eval/run.py -- CLI entry point for running eval benchmarks.

Usage::

    python -m eval.run --benchmark evalplus [--model qwen3:4b] [--limit 10] [--output-dir eval_results]
    python -m eval.run --benchmark swebench  [--model qwen3:4b] [--limit 10] [--output-dir eval_results]

Phase 4 (2026-08-08): CLI is now a thin manifest-validator + HarnessRun launcher.
For benchmarks that expose load_instances(), instances are fed through HarnessRun
which handles budget, checkpoint, error classification, scorer dispatch, and the
canonical artifact tree.  Legacy benchmarks that only expose run(driver, limit)
continue to work via a compatibility shim.
"""

from __future__ import annotations

import importlib
import json
import os
import subprocess
import sys
import time
import uuid
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
        "dry_run": False,
        "smoke": False,
    }

    i = 0
    while i < len(argv):
        flag = argv[i]
        if flag in ("--benchmark", "-b"):
            if i + 1 < len(argv) and not argv[i + 1].startswith("-"):
                args["benchmark"] = argv[i + 1]
                i += 2
            else:
                args["benchmark"] = "list"
                i += 1
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
        "  -b                      List available benchmarks (same as `-b list`)\n"
        "  --model, -m NAME        LLM model name [default: qwen3:4b]\n"
        "  --limit, -n N           Max instances to evaluate [default: 10]\n"
        "  --output-dir, -o DIR    Directory for result files [default: eval_results]\n"
        "  --base-url URL          LLM API base URL [default: http://127.0.0.1:11434/v1]\n"
        "  --no-runner             Skip ConversationRunner path, use direct LLM only\n"
        "  --dry-run               Validate manifest only, do not solve instances\n"
        "  --smoke                 Run a single instance for smoke testing\n"
        "  --help, -h              Show this message\n"
        "\n"
        "env vars:\n"
        "  LOCAL_LLM_BASE_URL      LLM API base URL (overrides --base-url)\n"
        "  LOCAL_LLM_MODEL         LLM model name (overrides --model)\n"
        "  LOCAL_LLM_API_KEY       LLM API key (used unless --api-key is given)\n"
    )


def _list_benchmarks() -> list[str]:
    """Return names of benchmark modules that expose a module-level run()."""
    import pkgutil

    import eval.benchmarks as pkg

    names: list[str] = []
    for mod_info in pkgutil.iter_modules(pkg.__path__):
        if mod_info.name.startswith("_"):
            continue
        try:
            mod = importlib.import_module(f"eval.benchmarks.{mod_info.name}")
        except Exception:
            continue
        if callable(getattr(mod, "run", None)):
            names.append(mod_info.name)
    return sorted(names)


# ---------------------------------------------------------------------------
# Git helpers (from manifest.py pattern)
# ---------------------------------------------------------------------------


def _git_head() -> str:
    try:
        out = subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"],
            capture_output=True, text=True, encoding="utf-8", errors="replace",
        )
        return out.stdout.strip() if out.returncode == 0 else "unknown"
    except Exception:
        return "unknown"


def _git_dirty_hash() -> str:
    try:
        out = subprocess.run(
            ["git", "diff", "--no-ext-diff"],
            capture_output=True, text=True, encoding="utf-8", errors="replace",
        )
        if not out.stdout:
            return "0" * 64
        import hashlib
        return hashlib.sha256(out.stdout.encode("utf-8")).hexdigest()
    except Exception:
        return ""


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    """Entry point.  Returns 0 on success, 1 on errors, 2 on harness fatal."""
    if argv is None:
        argv = sys.argv[1:]

    args = _parse_args(argv)
    benchmark_name = args["benchmark"]
    model: str = args["model"]
    limit: int = args["limit"]
    output_dir = Path(args["output_dir"])
    base_url: str = args["base_url"]
    use_runner: bool = args["use_runner"]
    dry_run: bool = args.get("dry_run", False)
    smoke: bool = args.get("smoke", False)
    cache_dir: str = args.get("cache", "")

    # ---- List available benchmarks ----
    if benchmark_name == "list":
        available = _list_benchmarks()
        print("Available benchmarks:")
        if not available:
            print("  (none -- create eval/benchmarks/<name>.py with a run() function)")
        for name in available:
            print(f"  {name}")
        return 0

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
            "Available benchmarks (run `python -m eval.run -b list` to see them): "
            + ", ".join(_list_benchmarks()),
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
        from eval.benchmarks.beir import BeirDataset, load_offline as _load_beir_offline

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
        ranked = {query_id: [] for query_id in query_ids}
        path = bench.write_predictions(ranked, output_dir) if hasattr(bench, "write_predictions") else output_dir / "predictions.jsonl"
        print(f"[eval] predictions        : {path.resolve()}")
        return 0

    # ---- Build HarnessRun manifest ----
    run_id = f"{benchmark_name}-{model.replace('/','-').replace(':','-')}-{uuid.uuid4().hex[:8]}"

    if dry_run:
        print(f"[dry-run] benchmark : {benchmark_name}")
        print(f"[dry-run] model     : {model}")
        print(f"[dry-run] run_id    : {run_id}")
        print("dry-run OK: manifest validated, no instances will be solved")
        return 0

    if smoke:
        limit = 1

    # ---- Create driver (AgentAdapter) ----
    from eval.driver_headless import create_driver

    adapter = create_driver(model=model, base_url=base_url, use_runner=use_runner)

    # ---- Create HarnessRun ----
    from eval.harness import HarnessRun, Budget, RunArtifacts

    artifacts = RunArtifacts(run_id=run_id, root=str(output_dir))
    budget = Budget(
        wall_clock_seconds=float(os.environ.get("EVAL_BUDGET_SECONDS", "3600")),
        max_tokens=int(os.environ.get("EVAL_BUDGET_TOKENS", "500000")),
        max_cost=float(os.environ.get("EVAL_BUDGET_COST", "10.0")),
        max_output_bytes=int(os.environ.get("EVAL_BUDGET_OUTPUT_BYTES", "5000000")),
    )
    harness = HarnessRun(
        run_id=run_id,
        artifacts=artifacts,
        budget=budget,
        adapter=adapter,
    )

    # ---- Load instances ----
    instances: list[Any] = []
    if hasattr(benchmark_mod, "load_instances"):
        instances = benchmark_mod.load_instances(limit=limit)
        harness_path = True
    else:
        # Legacy: benchmarks with run() but no load_instances().
        # Fall back to the existing run(driver, limit) contract — the benchmark
        # handles its own instance loop, scoring, and output writing.
        harness_path = False

    print(f"[eval] benchmark : {benchmark_name}")
    print(f"[eval] model     : {model}")
    print(f"[eval] base_url  : {base_url}")
    print(f"[eval] limit     : {limit}")
    print(f"[eval] use_runner: {use_runner}")
    print(f"[eval] output    : {output_dir.resolve()}")
    print(f"[eval] harness   : {'HarnessRun (native)' if harness_path else 'legacy benchmark.run()'}")
    print()

    if harness_path:
        # ---- HarnessRun path ----
        t0 = time.perf_counter()
        try:
            result = harness.run(instances)
        except Exception as exc:
            print(f"FATAL: harness run failed: {exc}", file=sys.stderr)
            import traceback
            traceback.print_exc()
            return 2

        elapsed = time.perf_counter() - t0
        s = result["summary"]
        print(f"\n=== {benchmark_name} ===")
        print(f"  run_id       : {run_id}")
        print(f"  instances    : {s['total']} total, {s.get('ok',0)} ok, {s.get('failed',0)} failed, {s.get('skipped',0)} skipped")
        print(f"  categories   : {s.get('by_category', {})}")
        print(f"  wall_clock   : {elapsed:.1f}s")
        print(f"  artifacts    : {artifacts.root}")
        return 0 if s.get("failed", 0) == 0 else 1

    else:
        # ---- Legacy benchmark.run() path ----
        t0 = time.perf_counter()
        try:
            results: list[Any] = benchmark_mod.run(adapter, limit=limit)
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
        import csv
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
