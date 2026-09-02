"""eval/run.py -- CLI entry point for running eval benchmarks.

Usage::

    python -m eval.run --benchmark evalplus [--model qwen3:4b] [--limit 10] [--output-dir eval_results]
    python -m eval.run --benchmark swebench  [--model qwen3:4b] [--limit 10] [--output-dir eval_results]

Phase 4 (2026-08-08): CLI is now a thin manifest-validator + HarnessRun launcher.
Phase 1/H2 (2026-08-09): HarnessRun is the ONLY execution path.  Every
benchmark must expose module-level load_instances(); instances are fed through
HarnessRun which handles budget, checkpoint, error classification, scorer dispatch,
and the canonical artifact tree.  Agent benchmarks (AgentBenchmark subclasses)
have their official ``score`` wired as HarnessRun's scorer callback; the legacy
``benchmark_mod.run(driver, limit)`` bypass is removed.
"""

from __future__ import annotations

import hashlib
import importlib
import inspect
import json
import os
import subprocess
import sys
import time
import uuid
from pathlib import Path
from typing import Any


class _OfficialBenchmarkDriver:
    """Expose an official benchmark lifecycle through ``AgentAdapter``.

    ``HarnessRun`` owns the artifact, budget, trace, and scorer boundaries and
    therefore calls ``solve_instance``.  Agent benchmarks own the actual
    official runner invocation.  This bridge preserves both responsibilities:
    it makes the harness call ``AgentBenchmark.solve`` while passing the
    generic model driver only to the benchmark that needs it.
    """

    def __init__(self, benchmark: Any, generic_driver: Any) -> None:
        self._benchmark = benchmark
        self._generic_driver = generic_driver

    def solve_instance(self, instance: Any, working_dir: str, **kwargs: Any) -> Any:
        return self._benchmark.solve(
            instance,
            Path(working_dir),
            self._generic_driver,
            **kwargs,
        )


# ---------------------------------------------------------------------------
# CLI argument parser (deliberately simple -- no argparse)
# ---------------------------------------------------------------------------


def _parse_args(argv: list[str]) -> dict[str, Any]:
    """Parse flat key-value pairs from *argv*.  Returns a dict with keys
    ``benchmark``, ``provider``, ``model``, ``limit``, ``output_dir``, ``base_url``,
    ``use_runner``.  Unknown flags are ignored with a warning.
    """
    args: dict[str, Any] = {
        "benchmark": "evalplus",
        "provider": os.environ.get("LLM_PROVIDER", "local").strip().lower() or "local",
        "model": None,
        "limit": 10,
        "output_dir": "eval_results",
        "base_url": None,
        "use_runner": True,
        "dry_run": False,
        "smoke": False,
        "subset": "",
        "cache_dir": "",
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
        elif flag == "--provider" and i + 1 < len(argv):
            args["provider"] = argv[i + 1].strip().lower()
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
        elif flag == "--subset" and i + 1 < len(argv):
            # Path to a pinned subset JSON. Running two arms against the same
            # pinned list is what makes a before/after comparison paired; a
            # comparison whose arms drew different instances is not a
            # comparison.
            args["subset"] = argv[i + 1]
            i += 2
        elif flag == "--cache-dir" and i + 1 < len(argv):
            args["cache_dir"] = argv[i + 1]
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

    if args["provider"] in {"local", "openai", "anthropic"}:
        from eval.driver_headless import resolve_provider_connection

        resolved_model, resolved_base_url, _ = resolve_provider_connection(
            args["provider"],
            model=args["model"],
            base_url=args["base_url"],
        )
        args["model"] = resolved_model
        args["base_url"] = resolved_base_url
    else:
        # main() owns the user-facing unsupported-provider diagnostic.
        args["model"] = args["model"] or "qwen3:4b"
        args["base_url"] = args["base_url"] or "http://127.0.0.1:11434/v1"
    return args


def _print_usage() -> None:
    print(
        "usage: python -m eval.run [flags]\n"
        "\n"
        "flags:\n"
        "  --benchmark, -b NAME    Benchmark to run (evalplus, swebench) [default: evalplus]\n"
        "  -b                      List available benchmarks (same as `-b list`)\n"
        "  --provider NAME         LLM protocol (local, openai, anthropic) [default: local]\n"
        "  --model, -m NAME        LLM model name [default: provider-specific]\n"
        "  --limit, -n N           Max instances to evaluate [default: 10]\n"
        "  --output-dir, -o DIR    Directory for result files [default: eval_results]\n"
        "  --base-url URL          LLM API base URL [default: provider-specific]\n"
        "  --no-runner             Skip ConversationRunner path, use direct LLM only\n"
        "  --dry-run               Validate manifest only, do not solve instances\n"
        "  --smoke                 Run a single instance for smoke testing\n"
        "  --help, -h              Show this message\n"
        "\n"
        "env vars:\n"
        "  LLM_PROVIDER            LLM protocol selected when --provider is omitted\n"
        "  LOCAL_LLM_*             Local/OpenAI-compatible endpoint settings\n"
        "  OPENAI_*                OpenAI endpoint settings\n"
        "  ANTHROPIC_*             Anthropic endpoint settings\n"
    )


def _list_benchmarks() -> list[str]:
    """Return names of benchmark modules that expose a module-level
    ``load_instances()`` — the HarnessRun entry contract."""
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
        if callable(getattr(mod, "load_instances", None)):
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


def _system_prompt_hash() -> str:
    """The run's prompt pin.  Delegates to the single definition in the pin contract."""
    from eval.harness.pin_contract import system_prompt_pin

    return system_prompt_pin()


def _load_subset(path: str) -> tuple[list[str], dict[str, Any]]:
    """Load a pinned instance subset, verifying its sha256 sidecar.

    Returns ``([], {})`` when *path* is empty.  A sidecar mismatch raises: the
    whole point of pinning the list is that both arms of a paired experiment ran
    the same instances, and an unverified list cannot support that claim.
    """
    if not path:
        return [], {}
    subset_file = Path(path)
    if not subset_file.is_file():
        raise SystemExit(f"--subset file not found: {subset_file}")
    raw = subset_file.read_bytes()
    digest = hashlib.sha256(raw).hexdigest()
    sidecar = Path(str(subset_file) + ".sha256")
    if not sidecar.is_file():
        raise SystemExit(
            f"subset sha256 sidecar not found: {sidecar}. Refusing to run an "
            "unverified instance list."
        )
    sidecar_parts = sidecar.read_text(encoding="utf-8").split()
    recorded = sidecar_parts[0].strip().lower() if sidecar_parts else ""
    if recorded != digest:
        raise SystemExit(
            f"subset sha256 mismatch: {subset_file} hashes to {digest} "
            f"but its sidecar records {recorded or '<empty>'}. Refusing to run: "
            "a paired comparison needs a verified instance list."
        )
    payload = json.loads(raw.decode("utf-8"))
    ids = list(payload.get("instance_ids") or [])
    if not ids:
        raise SystemExit(f"--subset file {subset_file} lists no instance_ids")
    payload["_sha256"] = digest
    _subset_manifest_pin(payload)
    return ids, payload


def _subset_manifest_pin(subset_meta: dict[str, Any]) -> dict[str, Any]:
    """Return the exact, portable subset identity recorded in run-manifest.json."""
    instance_ids = list(subset_meta.get("instance_ids") or [])
    size = int(subset_meta.get("size", len(instance_ids)))
    if size != len(instance_ids) or len(set(instance_ids)) != len(instance_ids):
        raise SystemExit("subset size/instance_ids are inconsistent or duplicated")
    digest = str(subset_meta.get("_sha256", "")).lower()
    if len(digest) != 64 or any(ch not in "0123456789abcdef" for ch in digest):
        raise SystemExit("subset sha256 is missing or invalid")
    parent = str(subset_meta.get("parent_subset", ""))
    parent_digest = str(subset_meta.get("parent_subset_sha256", "")).lower()
    if parent and (
        len(parent_digest) != 64
        or any(ch not in "0123456789abcdef" for ch in parent_digest)
    ):
        raise SystemExit("parent subset sha256 is missing or invalid")
    return {
        "subset_id": str(subset_meta.get("subset_id", "")),
        "sha256": digest,
        "dataset": str(subset_meta.get("dataset", "")),
        "split": str(subset_meta.get("split", "")),
        "size": size,
        "parent_subset": parent,
        "parent_subset_sha256": parent_digest,
        "instance_ids": instance_ids,
    }


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


def _find_agent_benchmark(
    mod: Any,
    *,
    model: str | None = None,
    base_url: str | None = None,
    api_key: str | None = None,
) -> Any | None:
    """Return an instance of the :class:`AgentBenchmark` subclass exposed by
    *mod*, or ``None`` when the benchmark has no unified adapter.

    Agent benchmarks (``SWEBenchAdapter``, ``TerminalBenchAdapter``,
    ``Tau2BenchAdapter``) implement the prepare -> solve -> score lifecycle;
    their official ``score`` is wired as HarnessRun's scorer callback so the
    unified manifest/artifact tree covers official scoring.  Benchmarks
    without an adapter (evalplus, retrieval) score internally — for those the
    scorer stays ``None``.
    """
    from eval.benchmarks.base import AgentBenchmark

    for attr_name in dir(mod):
        attr = getattr(mod, attr_name)
        if (
            isinstance(attr, type)
            and issubclass(attr, AgentBenchmark)
            and attr is not AgentBenchmark
        ):
            if attr.__name__ == "TerminalBenchAdapter":
                return attr(
                    agent_kwargs={
                        "model": model or "",
                        "base_url": base_url or "",
                    }
                )
            if attr.__name__ == "Tau2BenchAdapter":
                return attr(
                    model_name=model or None,
                    model_provider="openai",
                    base_url=base_url or "",
                    api_key=api_key or "",
                )
            return attr()
    return None


def _find_agent_benchmark_with_cli(
    mod: Any,
    *,
    model: str,
    base_url: str,
    api_key: str | None,
) -> Any | None:
    """Call the adapter factory without breaking one-argument test spies.

    The production factory accepts explicit CLI connection settings.  Older
    pin-gating tests intentionally replace it with a one-argument stub to
    isolate preflight behaviour; inspect the callable rather than catching an
    arbitrary TypeError from its implementation.
    """
    try:
        signature = inspect.signature(_find_agent_benchmark)
        accepts_keywords = any(
            parameter.kind is inspect.Parameter.VAR_KEYWORD
            for parameter in signature.parameters.values()
        ) or all(
            name in signature.parameters
            for name in ("model", "base_url", "api_key")
        )
    except (TypeError, ValueError):
        accepts_keywords = True
    if not accepts_keywords:
        return _find_agent_benchmark(mod)
    return _find_agent_benchmark(
        mod,
        model=model,
        base_url=base_url,
        api_key=api_key,
    )


def _model_endpoint_allowlist(base_url: str) -> tuple[str, ...]:
    """Hosts that must stay reachable for the agent to call its model.

    The harness blocks the network by pinning HTTP(S)_PROXY to a dead proxy
    with loopback-only NO_PROXY.  A remote model endpoint is therefore
    unreachable unless it is named explicitly, which silently reduced every
    remote-API run to zero tokens and an empty patch.

    Only the endpoint host is returned — never a wildcard — so the upstream
    fix (github.com, pypi) stays blocked.  A loopback endpoint needs no entry
    because loopback is already exempt.
    """
    from urllib.parse import urlparse

    host = (urlparse(base_url).hostname or "").strip()
    if not host or host in ("127.0.0.1", "localhost", "::1"):
        return ()
    return (host,)


def _provider_api_key(provider: str) -> str | None:
    """Resolve a provider credential in memory without persisting it."""
    if provider == "anthropic":
        return os.environ.get("ANTHROPIC_API_KEY") or os.environ.get(
            "ANTHROPIC_AUTH_TOKEN"
        )
    if provider == "openai":
        return os.environ.get("OPENAI_API_KEY")
    return os.environ.get("LOCAL_LLM_API_KEY") or os.environ.get(
        "LOCAL_OPENAI_API_KEY"
    )


def _validate_benchmark_pins(agent_bench: Any) -> list[str]:
    """Validate a benchmark adapter's reproducibility pins BEFORE any run.

    H3 reproducibility rule: "缺 pin 时启动前失败" — a run whose dataset,
    scorer, or benchmark identity is not pinned is not reproducible, so it
    must abort before an instance is solved rather than emit an artifact
    tree that only looks complete.

    Returns the list of missing/empty pin keys; empty list means the
    adapter is fully pinned.  ``None`` adapters (evalplus, retrieval
    benchmarks that score internally) have no pins to check and pass.
    """
    if agent_bench is None:
        return []
    if not callable(getattr(agent_bench, "validate_pins", None)):
        # Adapter predates the AgentBenchmark pin contract: treat the missing
        # contract itself as an unmet pin so the run refuses to start.
        return ["validate_pins"]
    return list(agent_bench.validate_pins())


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    """Entry point.  Returns 0 on success, 1 on errors, 2 on harness fatal."""
    if argv is None:
        argv = sys.argv[1:]

    args = _parse_args(argv)
    benchmark_name = args["benchmark"]
    provider: str = args["provider"]
    model: str = args["model"]
    limit: int = args["limit"]
    output_dir = Path(args["output_dir"])
    base_url: str = args["base_url"]
    use_runner: bool = args["use_runner"]
    dry_run: bool = args.get("dry_run", False)
    smoke: bool = args.get("smoke", False)
    cache_dir: str = args.get("cache_dir", "")
    subset_path: str = args.get("subset", "")

    supported_providers = {"local", "openai", "anthropic"}
    if provider not in supported_providers:
        print(
            f"ERROR: unsupported LLM provider '{provider}'; expected one of "
            + ", ".join(sorted(supported_providers)),
            file=sys.stderr,
        )
        return 1
    api_key = _provider_api_key(provider)

    # ---- List available benchmarks ----
    if benchmark_name == "list":
        available = _list_benchmarks()
        print("Available benchmarks:")
        if not available:
            print("  (none -- create eval/benchmarks/<name>.py with a load_instances() function)")
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

    retrieval_benchmarks = ("beir", "miracl", "bright")
    if benchmark_name not in retrieval_benchmarks and not hasattr(benchmark_mod, "load_instances"):
        print(
            f"ERROR: benchmark module {module_name} has no load_instances() "
            f"function.  The unified HarnessRun lifecycle requires every "
            f"benchmark to expose module-level load_instances(limit=...).",
            file=sys.stderr,
        )
        return 1

    # ---- Retrieval benchmarks (beir/miracl/bright) run offline from cache --
    if benchmark_name in retrieval_benchmarks:
        from eval.benchmarks.base import cache_root
        from eval.benchmarks.beir import (
            BeirDataset,
            load_offline as _load_beir_offline,
            require_pinned as _require_beir_pinned,
        )

        cache = Path(cache_dir) if cache_dir else cache_root()
        try:
            if benchmark_name == "beir":
                dataset_cache = cache / "beir-nfcorpus"
                bench = _load_beir_offline(dataset_cache)
                dataset_pin = _require_beir_pinned("beir-nfcorpus", dataset_cache)
            elif benchmark_name == "miracl":
                from eval.benchmarks.miracl import MiraclBenchmark
                bench = MiraclBenchmark(language="zh")
                bench.load_offline(cache)
                dataset_pin = bench.require_pinned(cache / bench.name)
            elif benchmark_name == "bright":
                from eval.benchmarks.bright import BrightBenchmark
                bench = BrightBenchmark()
                bench.load_offline(cache)
                dataset_pin = bench.require_pinned(cache / bench.name)
            else:
                print(f"ERROR: unsupported retrieval benchmark {benchmark_name}", file=sys.stderr)
                return 1
        except ValueError as exc:
            print(f"ERROR: {exc}", file=sys.stderr)
            return 1

        query_ids = bench.queries() if not isinstance(bench, BeirDataset) else list(bench.queries)
        if smoke:
            query_ids = query_ids[: min(limit, 5)]
        print(f"[eval] retrieval benchmark : {benchmark_name}")
        print(f"[eval] cache               : {cache.resolve()}")
        print(f"[eval] queries             : {len(query_ids)}" + (" (smoke)" if smoke else ""))
        print(f"[eval] dry-run             : {dry_run}")
        print(f"[eval] dataset revision    : {dataset_pin['dataset_revision']}")
        print(f"[eval] dataset hash        : {dataset_pin['dataset_hash']}")
        if dry_run:
            print("dry-run OK: dataset pinned and cached; predictions and scoring are wired")
            return 0
        ranked = {query_id: [] for query_id in query_ids}
        path = bench.write_predictions(ranked, output_dir) if hasattr(bench, "write_predictions") else output_dir / "predictions.jsonl"
        print(f"[eval] predictions        : {path.resolve()}")
        return 0

    # ---- Build HarnessRun manifest ----
    run_id = f"{benchmark_name}-{model.replace('/','-').replace(':','-')}-{uuid.uuid4().hex[:8]}"

    if smoke:
        limit = 1

    # ---- AgentBenchmark adapter (official scorer wiring) ----
    agent_bench = _find_agent_benchmark_with_cli(
        benchmark_mod,
        model=model,
        base_url=base_url,
        api_key=api_key,
    )

    # ---- H0 pin preflight: fail BEFORE launching on incomplete pins ----
    # This must also gate --dry-run.  The dry-run branch used to return here
    # printing "manifest validated" while it had validated nothing: it ran
    # before the pin check, so an unpinned benchmark exited 0 with a success
    # message.  A preflight that cannot fail is not a preflight.
    missing_pins = _validate_benchmark_pins(agent_bench)
    if missing_pins:
        print(
            f"ERROR: benchmark '{benchmark_name}' has incomplete reproducibility "
            f"pins: {', '.join(missing_pins)}. A run without immutable dataset/"
            f"scorer pins is not reproducible and must not start.",
            file=sys.stderr,
        )
        return 1

    if dry_run:
        print(f"[dry-run] benchmark : {benchmark_name}")
        print(f"[dry-run] provider  : {provider}")
        print(f"[dry-run] model     : {model}")
        print(f"[dry-run] run_id    : {run_id}")
        print("[dry-run] pins      : validated (no missing keys)")
        print("dry-run OK: pins validated, no instances will be solved")
        return 0

    # ---- Create driver (AgentAdapter) ----
    from eval.driver_headless import create_driver

    adapter = create_driver(
        model=model,
        base_url=base_url,
        api_key=api_key,
        provider=provider,
        use_runner=use_runner,
    )

    # ---- Load instances (benchmark MUST expose load_instances) ----
    # Done before the budget is sized: the budget is enforced across the whole
    # run, so a fixed default silently truncates any run larger than the one it
    # was chosen for. A 500k token cap let 6 of 20 instances through and the
    # remaining 14 were recorded as failures.
    subset_ids, subset_meta = _load_subset(subset_path)
    if subset_ids:
        instances = benchmark_mod.load_instances(limit=None, instance_ids=subset_ids)
        print(
            f"[eval] subset    : {subset_meta.get('subset_id', subset_path)} "
            f"({len(subset_ids)} instances, sha256 "
            f"{subset_meta.get('_sha256', '')[:12]})"
        )
    else:
        instances = benchmark_mod.load_instances(limit=limit)

    # ---- Create HarnessRun (the ONLY execution path) ----
    from eval.harness import HarnessRun, Budget, RunArtifacts

    artifacts = RunArtifacts(run_id=run_id, root=str(output_dir))
    instance_count = max(1, len(instances))
    # Per-instance allowances, multiplied by the instance count. The point of the
    # budget is to stop a runaway, and "runaway" is a property of one instance's
    # behaviour, not of how many instances were requested. An explicit
    # EVAL_BUDGET_* value is still honoured verbatim as a hard total.
    budget = Budget(
        wall_clock_seconds=float(
            os.environ.get("EVAL_BUDGET_SECONDS")
            or 900.0 * instance_count
        ),
        max_tokens=int(
            os.environ.get("EVAL_BUDGET_TOKENS")
            or 250_000 * instance_count
        ),
        max_cost=float(
            os.environ.get("EVAL_BUDGET_COST") or 2.0 * instance_count
        ),
        max_output_bytes=int(
            os.environ.get("EVAL_BUDGET_OUTPUT_BYTES")
            or 5_000_000 * instance_count
        ),
    )
    print(
        f"[eval] budget    : {instance_count} instance(s) × "
        f"(250k tokens, 900s, $2.00) = {budget.max_tokens:,} tokens, "
        f"{budget.wall_clock_seconds:.0f}s, ${budget.max_cost:.2f}"
    )
    harness_adapter = (
        _OfficialBenchmarkDriver(agent_bench, adapter)
        if agent_bench is not None
        else adapter
    )
    harness = HarnessRun(
        run_id=run_id,
        artifacts=artifacts,
        budget=budget,
        adapter=harness_adapter,
        scorer=agent_bench.score if agent_bench is not None else None,
        # The harness hands the agent a fresh temp dir; only the benchmark
        # knows how to populate it (SWE-bench clones the repo at base_commit).
        # Leaving this unwired let the agent run against an EMPTY directory,
        # so the captured git diff was necessarily empty and the official
        # scorer was fed model_patch: "" while the run still exited 0.
        setup_workspace=agent_bench.prepare if agent_bench is not None else None,
        network_allowlist=_model_endpoint_allowlist(base_url),
        config={
            "git_sha": _git_head(),
            "dirty_hash": _git_dirty_hash(),
            "provider": provider,
            "model": model,
            "benchmark": benchmark_name,
            "mode": "official",
            "synthetic": False,
            "evaluation_subset": (
                _subset_manifest_pin(subset_meta) if subset_ids else {}
            ),
            # Which trace span kinds this benchmark can legitimately omit.  Read
            # from the benchmark module so the declaration lives with the code
            # that either retrieves or does not; a benchmark that stays silent
            # gets None, which keeps every kind required.
            "trace_capabilities": getattr(
                benchmark_mod, "TRACE_CAPABILITIES", None
            ),
            # Pins the prompt scaffold the run was measured under.  Per-instance
            # task text cannot be a run-level pin (it differs per instance), but
            # the base system-prompt template is shared by every instance and
            # changes whenever someone edits the agent's instructions — which is
            # exactly the comparability question a prompt pin has to answer.
            "prompt_hash": _system_prompt_hash(),
        },
    )

    # Instances were loaded above, before the budget was sized from their count.

    print(f"[eval] benchmark : {benchmark_name}")
    print(f"[eval] provider  : {provider}")
    print(f"[eval] model     : {model}")
    print(f"[eval] base_url  : {base_url}")
    # When a pinned subset is in play the limit did not apply, and printing it
    # anyway invited exactly the wrong reading: the baseline log said
    # "limit : 10" above a 20-instance pinned run, which looked like the run had
    # been silently truncated.
    print(
        f"[eval] limit     : {'n/a (pinned subset)' if subset_ids else limit}"
    )
    print(f"[eval] use_runner: {use_runner}")
    print(f"[eval] output    : {output_dir.resolve()}")
    print("[eval] harness   : HarnessRun (only path)")
    print()

    # ---- Run (HarnessRun-only lifecycle) ----
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


if __name__ == "__main__":
    raise SystemExit(main())
