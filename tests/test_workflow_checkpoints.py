from __future__ import annotations

import json
import sqlite3
from pathlib import Path

import pytest

from orchestrator.workflows import (
    CheckpointConflictError,
    SQLiteWorkflowStore,
    WorkerResult,
    WorkflowRun,
)


def _run(output: str) -> WorkflowRun:
    return WorkflowRun(
        id="workflow",
        workers={"worker": WorkerResult.completed("worker", "default", output)},
        state="completed",
    )


def test_checkpoint_revision_rejects_stale_writers_without_losing_new_state(tmp_path: Path) -> None:
    database = tmp_path / "checkpoint.sqlite"
    first = SQLiteWorkflowStore(database)
    second = SQLiteWorkflowStore(database)
    try:
        revision = first.save(_run("first"), detail="first write")
        assert revision == 1
        snapshot = second.load_checkpoint("workflow")
        assert snapshot is not None
        assert snapshot.revision == 1

        assert first.save(_run("second"), expected_revision=snapshot.revision, detail="second write") == 2
        with pytest.raises(CheckpointConflictError):
            second.save(_run("stale"), expected_revision=snapshot.revision, detail="stale write")

        latest = first.load_checkpoint("workflow")
        assert latest is not None
        assert latest.revision == 2
        assert latest.run.workers["worker"].output == "second"
        assert [event[3] for event in first.events("workflow")] == ["first write", "second write"]
    finally:
        first.close()
        second.close()


def test_checkpoint_schema_migrates_existing_snapshot_table(tmp_path: Path) -> None:
    database = tmp_path / "legacy-checkpoint.sqlite"
    legacy_run = _run("legacy")
    connection = sqlite3.connect(database)
    try:
        connection.execute(
            "CREATE TABLE workflow_checkpoints (workflow_id TEXT PRIMARY KEY, state_json TEXT NOT NULL, updated_at TEXT NOT NULL)"
        )
        connection.execute(
            "INSERT INTO workflow_checkpoints(workflow_id, state_json, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)",
            (legacy_run.id, json.dumps(legacy_run.to_dict())),
        )
        connection.commit()
    finally:
        connection.close()

    store = SQLiteWorkflowStore(database)
    try:
        snapshot = store.load_checkpoint("workflow")
        assert snapshot is not None
        assert snapshot.revision == 0
        assert store.save(_run("migrated"), expected_revision=0, detail="migrated write") == 1
    finally:
        store.close()


def test_checkpoint_revision_and_events_survive_reopen(tmp_path: Path) -> None:
    database = tmp_path / "reopen-checkpoint.sqlite"
    store = SQLiteWorkflowStore(database)
    assert store.save(_run("durable"), detail="durable write") == 1
    store.close()

    reopened = SQLiteWorkflowStore(database)
    try:
        snapshot = reopened.load_checkpoint("workflow")
        assert snapshot is not None
        assert snapshot.revision == 1
        assert snapshot.run.workers["worker"].output == "durable"
        assert reopened.events("workflow")[0][3] == "durable write"
    finally:
        reopened.close()
