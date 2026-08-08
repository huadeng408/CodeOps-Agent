"""Download and prepare Terminal-Bench 2.0 and tau2-bench benchmark data.

Terminal-Bench 2.0
  Source: harborframework/terminal-bench-2.0 on HuggingFace
  Format: 89 task dirs each with instruction.md / Dockerfile / solution / tests
  Adapter expects: {family}.jsonl — one JSONL file with {name, description, ...} per line

tau2-bench
  Source: sierra-research/tau2-bench on GitHub
  Format: data/tau2/domains/{env}/tasks.json — one big JSON array per domain
  Adapter expects: {env}.jsonl — one JSONL file with {id, user, ...} per line

Usage::

    python scripts/prep_benchmark_data.py --all
    python scripts/prep_benchmark_data.py --terminalbench --out-dir eval/terminalbench_data
    python scripts/prep_benchmark_data.py --tau2bench --out-dir eval/tau2bench_data

After running, set env vars::

    $env:TERMINALBENCH_DATA_DIR = "eval/terminalbench_data"
    $env:TAU2_DATA_DIR = "eval/tau2bench_data"
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import urllib.request
from pathlib import Path
from typing import Any

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

TERMINALBENCH_HF_REPO = "https://huggingface.co/datasets/harborframework/terminal-bench-2.0"
TERMINALBENCH_HF_API = "https://huggingface.co/api/datasets/harborframework/terminal-bench-2.0"
TAU2BENCH_GITHUB_API = "https://api.github.com/repos/sierra-research/tau2-bench"
TAU2BENCH_RAW = "https://raw.githubusercontent.com/sierra-research/tau2-bench/main"

# Terminal-Bench: use a single family "terminalbench_2" with name = directory name,
# description = first paragraph of instruction.md, category = inferred from tags/path
DEFAULT_TERMINALBENCH_FAMILY = "terminalbench_2"

# tau2-bench domains to download
TAU2_DOMAINS = ["airline", "retail"]


# ---------------------------------------------------------------------------
# HTTP helpers
# ---------------------------------------------------------------------------


def _get_json(url: str, timeout: int = 30) -> Any:
    """GET *url*, return parsed JSON."""
    req = urllib.request.Request(url, headers={"User-Agent": "localcode-bench-prep/1.0"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        body = resp.read()
    return json.loads(body.decode("utf-8-sig"))


def _get_text(url: str, timeout: int = 30) -> str:
    """GET *url*, return decoded text."""
    req = urllib.request.Request(url, headers={"User-Agent": "localcode-bench-prep/1.0"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        body = resp.read()
    return body.decode("utf-8", errors="replace")


# ---------------------------------------------------------------------------
# Terminal-Bench
# ---------------------------------------------------------------------------


def _list_terminalbench_tasks() -> list[str]:
    """List task directory names from the HF dataset siblings."""
    print(f"[terminalbench] listing tasks from {TERMINALBENCH_HF_API} ...")
    meta = _get_json(TERMINALBENCH_HF_API)
    seen: set[str] = set()
    for sib in meta.get("siblings", []):
        rfn: str = sib.get("rfilename", "")
        parts = rfn.split("/")
        if len(parts) >= 2 and parts[1] == "instruction.md":
            seen.add(parts[0])
    return sorted(seen)


def _download_terminalbench_instruction(task_name: str) -> str:
    """Download instruction.md for a single task, return its text content."""
    url = f"{TERMINALBENCH_HF_REPO}/resolve/main/{task_name}/instruction.md"
    return _get_text(url)


def _extract_description(md_text: str, max_chars: int = 500) -> str:
    """Extract a human-readable task description from instruction.md markdown.

    Strategy: use the first non-empty, non-heading line after stripping markup,
    then truncate to *max_chars*.
    """
    lines = [line.strip() for line in md_text.splitlines() if line.strip()]
    # Skip heading lines (start with #) and metadata-like lines
    for line in lines:
        if line.startswith("#"):
            continue
        # Skip typical metadata keys
        if re.match(r"^(Author|Date|Version|Status|Category|Tags?|Difficulty|Depends|Requires?|Container)\s*:", line, re.IGNORECASE):
            continue
        # Found the first real content line
        return line[:max_chars]
    # Fallback: first non-trivial line
    return lines[0][:max_chars] if lines else ""


def _infer_category(task_name: str) -> str:
    """Infer a category tag from the task directory name."""
    _lower = task_name.lower()
    for prefix, cat in [
        ("git-", "git"),
        ("fix-", "debugging"),
        ("build-", "build"),
        ("compile-", "build"),
        ("crack-", "security"),
        ("dna-", "bioinformatics"),
        ("protein-", "bioinformatics"),
        ("llm-", "ml"),
        ("torch-", "ml"),
        ("pytorch-", "ml"),
        ("train-", "ml"),
        ("model-", "ml"),
        ("mcmc-", "statistics"),
        ("polyglot-", "languages"),
        ("sqlite-", "databases"),
        ("db-", "databases"),
        ("regex-", "parsing"),
        ("filter-", "parsing"),
        ("break-", "parsing"),
        ("video-", "multimedia"),
        ("code-from-", "multimedia"),
        ("circuit-", "engineering"),
        ("qemu-", "systems"),
        ("nginx-", "systems"),
        ("configure-", "systems"),
        ("kv-store-", "systems"),
    ]:
        if _lower.startswith(prefix):
            return cat
    return "general"


def prepare_terminalbench(out_dir: Path) -> int:
    """Download and convert Terminal-Bench 2.0 tasks to adapter-compatible JSONL.

    Returns number of tasks prepared.
    """
    out_dir.mkdir(parents=True, exist_ok=True)
    task_names = _list_terminalbench_tasks()
    print(f"[terminalbench] {len(task_names)} tasks found")

    family = DEFAULT_TERMINALBENCH_FAMILY
    out_path = out_dir / f"{family}.jsonl"
    count = 0
    with out_path.open("w", encoding="utf-8") as fh:
        for name in task_names:
            try:
                md = _download_terminalbench_instruction(name)
            except Exception as exc:
                print(f"  WARNING: failed to download {name}/instruction.md: {exc}", file=sys.stderr)
                continue
            description = _extract_description(md)
            category = _infer_category(name)
            record = {
                "name": name,
                "description": description,
                "category": category,
            }
            fh.write(json.dumps(record, ensure_ascii=False) + "\n")
            count += 1
            print(f"  [{count:3d}] {name} -> {category} | {description[:80]}...")

    print(f"[terminalbench] wrote {count} tasks to {out_path}")
    return count


# ---------------------------------------------------------------------------
# tau2-bench
# ---------------------------------------------------------------------------


def _download_tau2_tasks(domain: str) -> list[dict[str, Any]]:
    """Download tasks.json for *domain*, return the full list."""
    url = f"{TAU2BENCH_RAW}/data/tau2/domains/{domain}/tasks.json"
    print(f"[tau2bench] downloading {url} ...")
    return _get_json(url)


def _extract_user_from_task(task: dict[str, Any]) -> str:
    """Extract the 'user' field for the adapter from a tau2-bench task dict.

    Priority: user_scenario.instructions.reason_for_call, then purpose.
    """
    us = task.get("user_scenario", {})
    instr = us.get("instructions", {})
    reason = instr.get("reason_for_call", "")
    if reason:
        return reason
    purpose = task.get("description", {}).get("purpose", "")
    return purpose or ""


def _rewrite_tau2_task(task: dict[str, Any], domain: str) -> dict[str, Any]:
    """Convert a raw tau2-bench task dict into adapter-compatible JSONL record."""
    return {
        "id": task.get("id", ""),
        "user": _extract_user_from_task(task),
        "_env": domain,
        # Preserve evaluation criteria for official scorer
        "evaluation_criteria": task.get("evaluation_criteria"),
    }


def prepare_tau2bench(out_dir: Path) -> int:
    """Download and convert tau2-bench tasks to adapter-compatible JSONL.

    Returns number of tasks prepared.
    """
    out_dir.mkdir(parents=True, exist_ok=True)
    total = 0
    for domain in TAU2_DOMAINS:
        try:
            raw_tasks = _download_tau2_tasks(domain)
        except Exception as exc:
            print(f"  ERROR: failed to download {domain}/tasks.json: {exc}", file=sys.stderr)
            continue
        out_path = out_dir / f"{domain}.jsonl"
        count = 0
        with out_path.open("w", encoding="utf-8") as fh:
            for task in raw_tasks:
                record = _rewrite_tau2_task(task, domain)
                fh.write(json.dumps(record, ensure_ascii=False) + "\n")
                count += 1
        print(f"[tau2bench] wrote {count} tasks ({domain}) -> {out_path}")
        total += count
    return total


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Download and prepare Terminal-Bench 2.0 / tau2-bench benchmark data"
    )
    parser.add_argument(
        "--all",
        action="store_true",
        help="Prepare both benchmarks",
    )
    parser.add_argument(
        "--terminalbench",
        action="store_true",
        help="Prepare Terminal-Bench 2.0 data",
    )
    parser.add_argument(
        "--tau2bench",
        action="store_true",
        help="Prepare tau2-bench data",
    )
    parser.add_argument(
        "--out-dir",
        default="eval/benchmark_data",
        help="Output directory (default: eval/benchmark_data)",
    )
    parser.add_argument(
        "--terminalbench-out",
        default="",
        help="Terminal-Bench output subdirectory (default: --out-dir/terminalbench)",
    )
    parser.add_argument(
        "--tau2bench-out",
        default="",
        help="tau2-bench output subdirectory (default: --out-dir/tau2bench)",
    )
    args = parser.parse_args(argv)

    if not args.all and not args.terminalbench and not args.tau2bench:
        parser.print_help()
        print("\nERROR: specify at least one of --all, --terminalbench, --tau2bench", file=sys.stderr)
        return 1

    # Use proxy if set in env
    proxy = os.environ.get("HTTP_PROXY", os.environ.get("http_proxy", ""))
    if proxy:
        os.environ.setdefault("HTTP_PROXY", proxy)
        os.environ.setdefault("HTTPS_PROXY", proxy.replace("http://", "https://"))

    do_terminalbench = args.all or args.terminalbench
    do_tau2bench = args.all or args.tau2bench

    out_root = Path(args.out_dir)
    tb_out = Path(args.terminalbench_out) if args.terminalbench_out else (out_root / "terminalbench")
    tau_out = Path(args.tau2bench_out) if args.tau2bench_out else (out_root / "tau2bench")

    ok = 0
    if do_terminalbench:
        try:
            n = prepare_terminalbench(tb_out)
            if n > 0:
                print(f"\n[terminalbench] ✓ {n} tasks ready")
                print(f"  Set: $env:TERMINALBENCH_DATA_DIR = '{tb_out.resolve()}'")
                ok += 1
            else:
                print("[terminalbench] ✗ 0 tasks downloaded — check network/proxy", file=sys.stderr)
        except Exception as exc:
            print(f"[terminalbench] ✗ failed: {exc}", file=sys.stderr)

    if do_tau2bench:
        try:
            n = prepare_tau2bench(tau_out)
            if n > 0:
                print(f"\n[tau2bench] ✓ {n} tasks ready")
                print(f"  Set: $env:TAU2_DATA_DIR = '{tau_out.resolve()}'")
                ok += 1
            else:
                print("[tau2bench] ✗ 0 tasks downloaded — check network/proxy", file=sys.stderr)
        except Exception as exc:
            print(f"[tau2bench] ✗ failed: {exc}", file=sys.stderr)

    return 0 if ok > 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
