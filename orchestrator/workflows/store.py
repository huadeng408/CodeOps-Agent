from __future__ import annotations

import json
import sqlite3
import threading
from pathlib import Path

from .models import WorkflowRun


class SQLiteWorkflowStore:
    """Durable checkpoints and append-only transition events for workflows."""

    def __init__(self, path: str | Path) -> None:
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._connection = sqlite3.connect(self.path, timeout=30.0)
        self._lock = threading.Lock()
        with self._connection:
            self._connection.execute("PRAGMA busy_timeout = 30000")
            self._connection.execute("PRAGMA journal_mode = WAL")
            self._connection.execute("PRAGMA synchronous = NORMAL")
            self._connection.executescript(
                """
                CREATE TABLE IF NOT EXISTS workflow_checkpoints (
                  workflow_id TEXT PRIMARY KEY,
                  state_json TEXT NOT NULL,
                  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
                );
                CREATE TABLE IF NOT EXISTS workflow_events (
                  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
                  workflow_id TEXT NOT NULL,
                  worker_id TEXT NOT NULL,
                  state TEXT NOT NULL,
                  detail TEXT NOT NULL DEFAULT '',
                  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
                );
                CREATE INDEX IF NOT EXISTS workflow_events_workflow_sequence
                ON workflow_events(workflow_id, sequence);
                """
            )

    def save(self, run: WorkflowRun, worker_id: str = "", detail: str = "") -> None:
        payload = json.dumps(run.to_dict(), ensure_ascii=False, separators=(",", ":"))
        worker_state = run.workers.get(worker_id).state.value if worker_id in run.workers else run.state
        with self._lock, self._connection:
            self._connection.execute(
                """
                INSERT INTO workflow_checkpoints(workflow_id, state_json, updated_at)
                VALUES (?, ?, CURRENT_TIMESTAMP)
                ON CONFLICT(workflow_id) DO UPDATE SET state_json=excluded.state_json, updated_at=CURRENT_TIMESTAMP
                """,
                (run.id, payload),
            )
            self._connection.execute(
                "INSERT INTO workflow_events(workflow_id, worker_id, state, detail) VALUES (?, ?, ?, ?)",
                (run.id, worker_id, worker_state, detail),
            )

    def load(self, workflow_id: str) -> WorkflowRun | None:
        with self._lock:
            row = self._connection.execute(
                "SELECT state_json FROM workflow_checkpoints WHERE workflow_id = ?", (workflow_id,)
            ).fetchone()
        if row is None:
            return None
        decoded = json.loads(str(row[0]))
        if not isinstance(decoded, dict):
            raise ValueError(f"invalid workflow checkpoint for {workflow_id}")  # noqa: TRY004 - malformed persisted data
        return WorkflowRun.from_dict(decoded)

    def events(self, workflow_id: str) -> list[tuple[int, str, str, str]]:
        with self._lock:
            rows = self._connection.execute(
                "SELECT sequence, worker_id, state, detail FROM workflow_events WHERE workflow_id = ? ORDER BY sequence",
                (workflow_id,),
            ).fetchall()
        return [(int(row[0]), str(row[1]), str(row[2]), str(row[3])) for row in rows]

    def backup_to(self, target: str | Path) -> Path:
        """Create a consistent SQLite snapshot, including committed WAL pages."""
        destination_path = Path(target)
        destination_path.parent.mkdir(parents=True, exist_ok=True)
        if destination_path.exists():
            destination_path.unlink()
        with self._lock:
            destination = sqlite3.connect(destination_path)
            try:
                self._connection.backup(destination)
                destination.execute("PRAGMA journal_mode = DELETE")
                destination.commit()
            finally:
                destination.close()
        return destination_path

    def close(self) -> None:
        with self._lock:
            self._connection.close()
