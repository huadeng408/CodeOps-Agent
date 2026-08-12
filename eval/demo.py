"""Deterministic offline vertical slice for the real evaluation Harness.

This module is intentionally synthetic. It proves that the local Harness can
execute one instance and finalize its canonical artifact tree; it does not
measure model, retrieval, multimodal, or production observability quality.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import sys
from pathlib import Path
from typing import Sequence

from eval.adapter import EvalInstance, EvalResult
from eval.harness.artifacts import RunArtifacts
from eval.harness.pin_contract import system_prompt_pin
from eval.harness.runner import HarnessRun, SCORER_RAW_OUTPUT_KEY


DEFAULT_RUN_ID = "offline-harness-demo"
DEMO_INSTANCE_ID = "demo/echo-1"
DEMO_EXPECTED_ANSWER = "offline-harness-demo-ok"


class DeterministicDemoAdapter:
    """Return a fixed local result without calling a model or network service."""

    def solve_instance(
        self,
        instance: EvalInstance,
        working_dir: str,
        **kwargs: object,
    ) -> EvalResult:
        del working_dir, kwargs
        return EvalResult(
            instance_id=instance.instance_id,
            answer=DEMO_EXPECTED_ANSWER,
            cost=0.0,
            tokens_in=0,
            tokens_out=0,
        )


def synthetic_demo_scorer(
    result: EvalResult,
    instance: EvalInstance,
    workspace: Path,
) -> dict[str, object]:
    """Score the fixed answer and retain the synthetic raw scorer payload."""
    del workspace
    passed = result.instance_id == instance.instance_id and result.answer == DEMO_EXPECTED_ANSWER
    raw_payload = {
        "demo_passed": passed,
        "instance_id": instance.instance_id,
        "scorer_kind": "synthetic-demo",
    }
    return {
        "demo_passed": passed,
        "scorer_kind": "synthetic-demo",
        SCORER_RAW_OUTPUT_KEY: {
            "demo-output.json": json.dumps(raw_payload, indent=2, sort_keys=True) + "\n"
        },
    }


def _git_output(*args: str) -> str:
    try:
        completed = subprocess.run(
            ["git", *args],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            check=False,
        )
    except OSError:
        return ""
    return completed.stdout if completed.returncode == 0 else ""


def _git_head() -> str:
    return _git_output("rev-parse", "HEAD").strip() or "unknown"


def _tracked_dirty_hash() -> str:
    diff = _git_output("diff", "--no-ext-diff", "--binary")
    if not diff:
        return "0" * 64
    return hashlib.sha256(diff.encode("utf-8")).hexdigest()


def run_demo(output_dir: Path, run_id: str) -> dict[str, object]:
    """Run one synthetic instance and return the verified CLI result."""
    if not run_id or any(char in run_id for char in ("/", "\\")):
        raise ValueError("run-id must be a non-empty directory name")

    run_root = output_dir / run_id
    if run_root.exists() and any(run_root.iterdir()):
        raise FileExistsError(f"refusing to append to non-empty run directory: {run_root}")

    artifacts = RunArtifacts(run_id, output_dir)
    harness = HarnessRun(
        run_id=run_id,
        artifacts=artifacts,
        adapter=DeterministicDemoAdapter(),
        scorer=synthetic_demo_scorer,
        network_allowed=False,
        config={
            "benchmark": "offline-harness-demo",
            "dirty_hash": _tracked_dirty_hash(),
            "git_sha": _git_head(),
            "mode": "demo",
            "model": "deterministic-local-demo",
            "model_revision": "builtin-v1",
            "prompt_hash": system_prompt_pin(),
            "seed": 0,
            "synthetic": True,
            "trace_capabilities": [],
        },
    )
    outcome = harness.run(
        [
            EvalInstance(
                instance_id=DEMO_INSTANCE_ID,
                task_description="Return the deterministic offline demo response.",
                metadata={"synthetic": True, "mode": "demo"},
            )
        ]
    )
    checksum_problems = artifacts.verify_checksums()
    summary = outcome["summary"]
    status = "ok" if summary["completed"] == 1 and not checksum_problems else "failed"
    return {
        "status": status,
        "run_id": run_id,
        "artifact_dir": str(artifacts.root.resolve()),
        "summary": summary,
        "checksum_problems": checksum_problems,
    }


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=Path("eval_results") / "demo",
        help="Parent directory for the run artifact directory.",
    )
    parser.add_argument("--run-id", default=DEFAULT_RUN_ID)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        result = run_demo(args.output_dir, args.run_id)
    except Exception as exc:  # noqa: BLE001 - CLI reports a concise failure
        print(f"offline Harness demo failed: {exc}", file=sys.stderr)
        return 1
    print(json.dumps(result, indent=2, ensure_ascii=False))
    return 0 if result["status"] == "ok" else 1


if __name__ == "__main__":
    raise SystemExit(main())
