"""SWE-bench predictions schema and sampling tests (plan Task 8.2)."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.benchmarks.swebench_predictions import (
    OFFICIAL_SCHEMA_FIELDS,
    official_harness_command,
    stratified_sample,
    to_official_prediction,
    write_predictions,
)


def test_official_schema_fields_frozen() -> None:
    assert OFFICIAL_SCHEMA_FIELDS == ("instance_id", "model_patch", "model_name_or_path")


def test_to_official_prediction() -> None:
    record = to_official_prediction("flask__flask-5014", "+def f(): pass", "deepseek-v4-pro")
    assert record["instance_id"] == "flask__flask-5014"
    assert record["model_patch"] == "+def f(): pass"
    assert record["model_name_or_path"] == "deepseek-v4-pro"


def test_write_predictions_official_format(tmp_path: Path) -> None:
    records = [
        to_official_prediction("flask__flask-5014", "patch-1", "model"),
        to_official_prediction("django__django-1000", "patch-2", "model"),
    ]
    path = write_predictions(records, tmp_path / "predictions.jsonl")
    lines = path.read_text(encoding="utf-8").splitlines()
    assert len(lines) == 2
    first = json.loads(lines[0])
    assert set(first.keys()) == set(OFFICIAL_SCHEMA_FIELDS)


def test_write_predictions_rejects_missing_field(tmp_path: Path) -> None:
    with pytest.raises(ValueError) as exc:
        write_predictions([{"instance_id": "x", "model_patch": "p"}], tmp_path / "bad.jsonl")
    assert "model_name_or_path" in str(exc.value)


def test_stratified_sample_ramp() -> None:
    ids = [
        "flask__flask-1",
        "flask__flask-2",
        "django__django-1",
        "django__django-2",
        "requests__requests-1",
    ]
    one = stratified_sample(ids, 1)
    assert len(one) == 1 and one[0] == "django__django-1"  # first repo alphabetically
    ten = stratified_sample(ids, 10)
    assert len(ten) == len(ids)  # capped at available
    assert set(ten) == set(ids)


def test_stratified_sample_deterministic() -> None:
    ids = ["flask__flask-1", "flask__flask-2", "django__django-1"]
    assert stratified_sample(ids, 2) == stratified_sample(ids, 2)


def test_stratified_sample_spreads_repos() -> None:
    ids = ["flask__flask-1", "flask__flask-2", "django__django-1", "django__django-2"]
    sample = stratified_sample(ids, 2)
    repos = {_repo_of_stub(i) for i in sample}
    assert len(repos) == 2  # one from each repo, not both from one


def _repo_of_stub(instance_id: str) -> str:
    return instance_id.split("__")[0]


def test_official_harness_command() -> None:
    cmd = official_harness_command("predictions.jsonl", "run-1", max_workers=2)
    assert cmd[0] == "python"
    assert "swebench.harness.run_evaluation" in cmd
    assert "princeton-nlp/SWE-bench_Verified" in cmd
    assert "--max_workers" in cmd
