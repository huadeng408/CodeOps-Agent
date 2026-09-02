from __future__ import annotations

import copy
import json
import sqlite3
from contextlib import closing
from pathlib import Path

import pytest

from eval.harness.skill_selection_checkpoint import SkillSelectionCheckpoint


def _contract() -> dict[str, object]:
    return {
        "schema_version": 1,
        "run_id": "skill-selection-resume-test",
        "source_pin": {"git_sha": "a" * 40, "dirty_hash": "b" * 64},
        "dataset_sha256": "c" * 64,
        "catalog_sha256": "d" * 64,
        "model": "locked-model",
        "endpoint": {"scheme": "http", "host": "127.0.0.1", "port": "8080"},
        "prompt_schema_sha256": "e" * 64,
        "budget": {"max_output_tokens_per_case": 32, "calls": 4},
    }


def test_checkpoint_commits_gold_free_result_with_payload_integrity(
    tmp_path: Path,
) -> None:
    path = tmp_path / "checkpoint.sqlite3"
    result = {
        "case_id": "case-001",
        "selected_skill": "debug",
        "correct": True,
        "failure_message": "",
    }
    store = SkillSelectionCheckpoint(path)
    try:
        store.initialize(_contract())
        store.save_result("case-001", result)

        assert store.completed_case_ids() == frozenset({"case-001"})
        assert store.load_ordered_results(["case-001"]) == [result]
    finally:
        store.close()

    with closing(sqlite3.connect(path)) as connection:
        assert connection.execute("PRAGMA journal_mode").fetchone()[0] == "wal"
        assert connection.execute("PRAGMA synchronous").fetchone()[0] == 2
        payload_json = connection.execute(
            "SELECT payload_json FROM results WHERE case_id = ?", ("case-001",)
        ).fetchone()[0]
        persisted = "\n".join(
            str(value)
            for row in connection.execute(
                "SELECT case_id, payload_json, payload_sha256 FROM results"
            )
            for value in row
        )

    assert "Never store this user prompt" not in persisted
    assert "expected_skill" not in persisted
    assert "gold" not in persisted.lower()
    assert json.loads(payload_json) == result


@pytest.mark.parametrize(
    ("field", "replacement"),
    [
        ("source_pin", {"git_sha": "f" * 40, "dirty_hash": "b" * 64}),
        ("dataset_sha256", "f" * 64),
        ("catalog_sha256", "f" * 64),
        ("model", "different-model"),
        (
            "endpoint",
            {"scheme": "http", "host": "127.0.0.1", "port": "9090"},
        ),
        ("prompt_schema_sha256", "f" * 64),
        ("budget", {"max_output_tokens_per_case": 16, "calls": 4}),
    ],
)
def test_resume_rejects_any_changed_contract_pin(
    tmp_path: Path,
    field: str,
    replacement: object,
) -> None:
    path = tmp_path / "checkpoint.sqlite3"
    initial = _contract()
    store = SkillSelectionCheckpoint(path)
    expected_hash = store.initialize(initial)
    store.close()

    resumed = SkillSelectionCheckpoint(path)
    try:
        assert resumed.validate_contract(initial) == expected_hash
        changed = copy.deepcopy(initial)
        changed[field] = replacement
        with pytest.raises(ValueError, match="contract"):
            resumed.validate_contract(changed)
    finally:
        resumed.close()


@pytest.mark.parametrize("forbidden_key", ["prompt", "expected_skill", "gold", "qrels"])
def test_checkpoint_rejects_gold_or_prompt_fields(
    tmp_path: Path,
    forbidden_key: str,
) -> None:
    store = SkillSelectionCheckpoint(tmp_path / "checkpoint.sqlite3")
    try:
        store.initialize(_contract())
        with pytest.raises(ValueError, match="forbidden"):
            store.save_result(
                "case-001",
                {
                    "case_id": "case-001",
                    "selected_skill": "debug",
                    forbidden_key: "must-not-persist",
                },
            )
        assert store.completed_case_ids() == frozenset()
    finally:
        store.close()


def test_checkpoint_rejects_result_payload_tampering(tmp_path: Path) -> None:
    path = tmp_path / "checkpoint.sqlite3"
    store = SkillSelectionCheckpoint(path)
    store.initialize(_contract())
    store.save_result(
        "case-001",
        {"case_id": "case-001", "selected_skill": "debug", "correct": True},
    )
    store.close()

    with closing(sqlite3.connect(path)) as connection, connection:
        connection.execute(
            "UPDATE results SET payload_json = ? WHERE case_id = ?",
            (
                json.dumps(
                    {
                        "case_id": "case-001",
                        "selected_skill": "inspect",
                        "correct": False,
                    }
                ),
                "case-001",
            ),
        )

    resumed = SkillSelectionCheckpoint(path)
    try:
        with pytest.raises(ValueError, match="checksum mismatch"):
            resumed.load_ordered_results(["case-001"])
    finally:
        resumed.close()


def test_checkpoint_rejects_overwriting_a_completed_case(tmp_path: Path) -> None:
    store = SkillSelectionCheckpoint(tmp_path / "checkpoint.sqlite3")
    try:
        store.initialize(_contract())
        store.save_result(
            "case-001",
            {"case_id": "case-001", "selected_skill": "debug", "correct": True},
        )
        with pytest.raises(ValueError, match="already completed"):
            store.save_result(
                "case-001",
                {
                    "case_id": "case-001",
                    "selected_skill": "inspect",
                    "correct": False,
                },
            )
        assert store.load_ordered_results(["case-001"])[0]["selected_skill"] == (
            "debug"
        )
    finally:
        store.close()
