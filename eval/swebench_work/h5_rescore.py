"""Defect-J validation: re-score the existing real H5 patch through our own fix.

This deliberately reuses the patch the agent already produced in
``eval_results/h5-smoke-20260810-123749`` instead of re-running the agent, so it
spends no model tokens and isolates exactly one variable: whether official
scoring can now reach a verdict.

It exercises the real code path (``_run_official_scoring`` -> namespace
resolution -> WSL -> swebench), not a hand-rolled invocation, so a pass is
evidence about the shipped fix rather than about this script.

No model is called and no key is read anywhere in this path.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from eval.benchmarks.swebench import (  # noqa: E402
    _read_official_resolution,
    _resolve_namespace,
    _run_official_scoring,
)

SOURCE_ARTIFACT = (
    REPO_ROOT
    / "eval_results/h5-smoke-20260810-123749"
    / "swebench-deepseek-v4-pro-bfad8522/predictions.jsonl"
)
INSTANCE_ID = "astropy__astropy-12907"
MODEL_NAME = "deepseek-v4-pro"
RUN_ID = "h5-rescore-namespace-v1"
WORKDIR = REPO_ROOT / "eval/swebench_work/h5_rescore"


def main() -> int:
    WORKDIR.mkdir(parents=True, exist_ok=True)

    with SOURCE_ARTIFACT.open(encoding="utf-8") as fh:
        rows = [json.loads(line) for line in fh if line.strip()]
    if len(rows) != 1:
        print(f"FAIL: expected 1 source row, got {len(rows)}")
        return 2
    patch = rows[0].get("model_patch") or ""
    if not patch.strip():
        print("FAIL: source artifact has an empty patch; nothing to score")
        return 2

    print(f"source patch bytes: {len(patch)}")
    print(f"resolved namespace: {_resolve_namespace()!r}")

    preds = WORKDIR / "predictions.jsonl"
    with preds.open("w", encoding="utf-8") as fh:
        fh.write(
            json.dumps(
                {
                    "instance_id": INSTANCE_ID,
                    "model_name_or_path": MODEL_NAME,
                    "model_patch": patch,
                },
                ensure_ascii=False,
            )
            + "\n"
        )

    print(f"scoring run_id={RUN_ID} ...", flush=True)
    ok, detail = _run_official_scoring(
        str(preds),
        str(WORKDIR),
        run_id=RUN_ID,
        timeout=3600,
    )
    print(f"scorer ok={ok}")
    print(f"scorer detail: {detail[-1500:]}")

    resolved = _read_official_resolution(
        instance_id=INSTANCE_ID, run_id=RUN_ID, model_name=MODEL_NAME,
    )
    print(f"official resolution: {resolved!r}")

    if resolved is True:
        print("VERDICT: RESOLVED - official scorer reached a real verdict")
        return 0
    if resolved is False:
        print("VERDICT: NOT RESOLVED - official verdict exists and is negative")
        return 0
    print("VERDICT: NO VERDICT - scoring still cannot produce evidence")
    return 3


if __name__ == "__main__":
    sys.exit(main())
