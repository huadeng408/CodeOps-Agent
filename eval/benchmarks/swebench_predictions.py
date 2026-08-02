"""SWE-bench Verified predictions and sampling (plan Task 8.2).

The adapter only transforms EvalResult into the official SWE-bench
predictions schema and provides deterministic stratified sampling. Scoring
happens ONLY through the official Docker harness (swebench.harness) — the
adapter never modifies tests or the scorer.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any


# Official SWE-bench predictions schema (swebench.harness.run_evaluation).
OFFICIAL_SCHEMA_FIELDS = ("instance_id", "model_patch", "model_name_or_path")


def to_official_prediction(instance_id: str, model_patch: str, model_name: str) -> dict[str, str]:
    """Convert one EvalResult into the official predictions record."""
    return {
        "instance_id": instance_id,
        "model_patch": model_patch or "",
        "model_name_or_path": model_name,
    }


def write_predictions(records: list[dict[str, str]], path: str | Path) -> Path:
    """Write official predictions.jsonl (one record per line)."""
    out = Path(path)
    out.parent.mkdir(parents=True, exist_ok=True)
    with out.open("w", encoding="utf-8") as handle:
        for record in records:
            for field in OFFICIAL_SCHEMA_FIELDS:
                if field not in record:
                    raise ValueError(f"prediction missing official field {field}")
            handle.write(json.dumps(record, ensure_ascii=False) + "\n")
    return out


def stratified_sample(instance_ids: list[str], limit: int, seed_repo: str = "") -> list[str]:
    """Deterministic stratified sample across repos.

    Repos are ordered by name; within each repo, instances are taken in
    their original order. This yields a reproducible '1 -> fixed 10 ->
    approved 100' ramp: call with limit=1/10/100.
    """
    if limit <= 0:
        return []
    by_repo: dict[str, list[str]] = {}
    for instance_id in instance_ids:
        repo = _repo_of(instance_id)
        by_repo.setdefault(repo, []).append(instance_id)
    ordered_repos = sorted(by_repo)
    selected: list[str] = []
    while len(selected) < limit:
        progressed = False
        for repo in ordered_repos:
            if len(selected) >= limit:
                break
            candidates = by_repo[repo]
            if candidates:
                selected.append(candidates.pop(0))
                progressed = True
        if not progressed:
            break
    return selected


def _repo_of(instance_id: str) -> str:
    # SWE-bench ids: "org__repo-issue" or "org/repo__issue".
    if "__" in instance_id:
        return instance_id.split("__")[0].replace("/", "-")
    return "unknown"


def official_harness_command(predictions_path: str, run_id: str, max_workers: int = 4) -> list[str]:
    """The ONLY sanctioned scoring path: the official SWE-bench Docker harness."""
    return [
        "python",
        "-m",
        "swebench.harness.run_evaluation",
        "--dataset_name",
        "princeton-nlp/SWE-bench_Verified",
        "--predictions_path",
        predictions_path,
        "--max_workers",
        str(max_workers),
        "--run_id",
        run_id,
    ]
