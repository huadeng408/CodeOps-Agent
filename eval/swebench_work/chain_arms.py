"""Run arm B as soon as arm A finishes, and only then.

Sequential by design. Two arms in parallel contend for Docker and for a 16GB
host's memory, and a scoring container evicted under pressure produces an
infrastructure failure that looks exactly like an agent failure — the failure
mode this whole experiment exists to stop conflating.

An earlier shell version of this used ``pgrep -f`` to wait for arm A, which
cannot see Windows process command lines from MSYS: it matched nothing, concluded
arm A had exited, and started arm B immediately. It happened to be right that
time, for the wrong reason. This waits on the run's own artifacts instead, which
is the same evidence a human would check.
"""

from __future__ import annotations

import json
import subprocess
import sys
import time
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

EXPERIMENT_DIR = REPO_ROOT / "eval_results" / "harness-uplift-20260810"
POLL_SECONDS = 30
#: A run that has not written a prediction in this long has stalled, not finished.
STALL_SECONDS = 45 * 60


def _latest_run(arm_dir: Path) -> Path | None:
    runs = [p for p in arm_dir.glob("*") if (p / "predictions.jsonl").is_file()]
    return max(runs, key=lambda p: p.stat().st_mtime) if runs else None


def _scored_count(run_dir: Path) -> int:
    path = run_dir / "predictions.jsonl"
    try:
        return sum(1 for line in path.read_text(encoding="utf-8").splitlines() if line.strip())
    except OSError:
        return 0


def _is_complete(run_dir: Path, expected: int) -> bool:
    """Whether the run wrote its summary, which it only does at the very end."""
    summary = run_dir / "summary.json"
    if not summary.is_file():
        return False
    try:
        data = json.loads(summary.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return False
    return int(data.get("total", 0)) >= expected


def wait_for_arm_a(expected: int = 20) -> bool:
    arm_dir = EXPERIMENT_DIR / "arm-a-baseline"
    last_progress = time.time()
    last_count = -1
    while True:
        run_dir = _latest_run(arm_dir) if arm_dir.is_dir() else None
        if run_dir is not None:
            if _is_complete(run_dir, expected):
                print(f"[chain] arm A complete: {run_dir.name}", flush=True)
                return True
            count = _scored_count(run_dir)
            if count != last_count:
                print(f"[chain] arm A at {count}/{expected}", flush=True)
                last_count = count
                last_progress = time.time()
        if time.time() - last_progress > STALL_SECONDS:
            print("[chain] arm A stalled; NOT starting arm B", flush=True)
            return False
        time.sleep(POLL_SECONDS)


def main() -> int:
    if not wait_for_arm_a():
        return 1
    log = REPO_ROOT / "eval_results" / "arm-b-v3.log"
    print(f"[chain] starting arm B, log -> {log}", flush=True)
    with log.open("w", encoding="utf-8") as handle:
        completed = subprocess.run(
            [sys.executable, "-u", "eval/swebench_work/run_arm.py", "optimized"],
            cwd=str(REPO_ROOT),
            stdout=handle,
            stderr=subprocess.STDOUT,
        )
    print(f"[chain] arm B exited rc={completed.returncode}", flush=True)
    return completed.returncode


if __name__ == "__main__":
    sys.exit(main())
