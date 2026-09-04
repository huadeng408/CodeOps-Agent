from __future__ import annotations

import json
import sqlite3
import threading
import time
import uuid
from dataclasses import dataclass
from pathlib import Path

from .models import WorkflowRun


@dataclass(frozen=True, slots=True)
class WorkerLease:
    workflow_id: str
    worker_id: str
    owner_id: str
    token: str
    acquired_at: float
    heartbeat_at: float
    expires_at: float


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
                CREATE TABLE IF NOT EXISTS workflow_leases (
                  workflow_id TEXT NOT NULL,
                  worker_id TEXT NOT NULL,
                  owner_id TEXT NOT NULL,
                  lease_token TEXT NOT NULL,
                  acquired_at REAL NOT NULL,
                  heartbeat_at REAL NOT NULL,
                  expires_at REAL NOT NULL,
                  PRIMARY KEY(workflow_id, worker_id)
                );
                CREATE INDEX IF NOT EXISTS workflow_leases_expiry
                ON workflow_leases(expires_at);
                """
            )

    def acquire_lease(
        self,
        workflow_id: str,
        worker_id: str,
        owner_id: str,
        ttl_seconds: float,
        *,
        now: float | None = None,
    ) -> WorkerLease | None:
        """Atomically claim a worker while no unexpired owner holds it."""
        self._validate_lease_args(workflow_id, worker_id, owner_id, ttl_seconds)
        acquired_at = time.time() if now is None else float(now)
        expires_at = acquired_at + float(ttl_seconds)
        token = uuid.uuid4().hex
        with self._lock, self._connection:
            cursor = self._connection.execute(
                """
                INSERT INTO workflow_leases(
                  workflow_id, worker_id, owner_id, lease_token,
                  acquired_at, heartbeat_at, expires_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(workflow_id, worker_id) DO UPDATE SET
                  owner_id=excluded.owner_id,
                  lease_token=excluded.lease_token,
                  acquired_at=excluded.acquired_at,
                  heartbeat_at=excluded.heartbeat_at,
                  expires_at=excluded.expires_at
                WHERE workflow_leases.expires_at <= excluded.acquired_at
                """,
                (workflow_id, worker_id, owner_id, token, acquired_at, acquired_at, expires_at),
            )
            if cursor.rowcount != 1:
                return None
        return WorkerLease(workflow_id, worker_id, owner_id, token, acquired_at, acquired_at, expires_at)

    def renew_lease(
        self,
        workflow_id: str,
        worker_id: str,
        owner_id: str,
        token: str,
        ttl_seconds: float,
        *,
        now: float | None = None,
    ) -> bool:
        self._validate_lease_args(workflow_id, worker_id, owner_id, ttl_seconds)
        heartbeat_at = time.time() if now is None else float(now)
        expires_at = heartbeat_at + float(ttl_seconds)
        with self._lock, self._connection:
            cursor = self._connection.execute(
                """
                UPDATE workflow_leases
                   SET heartbeat_at = ?, expires_at = ?
                 WHERE workflow_id = ? AND worker_id = ?
                   AND owner_id = ? AND lease_token = ?
                   AND expires_at > ?
                """,
                (heartbeat_at, expires_at, workflow_id, worker_id, owner_id, token, heartbeat_at),
            )
        return cursor.rowcount == 1

    def release_lease(self, workflow_id: str, worker_id: str, owner_id: str, token: str) -> bool:
        if not workflow_id.strip() or not worker_id.strip() or not owner_id.strip() or not token.strip():
            raise ValueError("workflow, worker, owner, and lease token must not be empty")
        with self._lock, self._connection:
            cursor = self._connection.execute(
                """
                DELETE FROM workflow_leases
                 WHERE workflow_id = ? AND worker_id = ? AND owner_id = ? AND lease_token = ?
                """,
                (workflow_id, worker_id, owner_id, token),
            )
        return cursor.rowcount == 1

    def get_lease(self, workflow_id: str, worker_id: str, *, now: float | None = None) -> WorkerLease | None:
        check_at = time.time() if now is None else float(now)
        with self._lock:
            row = self._connection.execute(
                """
                SELECT workflow_id, worker_id, owner_id, lease_token,
                       acquired_at, heartbeat_at, expires_at
                  FROM workflow_leases
                 WHERE workflow_id = ? AND worker_id = ? AND expires_at > ?
                """,
                (workflow_id, worker_id, check_at),
            ).fetchone()
        if row is None:
            return None
        return WorkerLease(
            workflow_id=str(row[0]),
            worker_id=str(row[1]),
            owner_id=str(row[2]),
            token=str(row[3]),
            acquired_at=float(row[4]),
            heartbeat_at=float(row[5]),
            expires_at=float(row[6]),
        )

    def reap_expired_leases(self, *, now: float | None = None) -> int:
        check_at = time.time() if now is None else float(now)
        with self._lock, self._connection:
            cursor = self._connection.execute("DELETE FROM workflow_leases WHERE expires_at <= ?", (check_at,))
        return cursor.rowcount

    @staticmethod
    def _validate_lease_args(workflow_id: str, worker_id: str, owner_id: str, ttl_seconds: float) -> None:
        if not workflow_id.strip() or not worker_id.strip() or not owner_id.strip():
            raise ValueError("workflow, worker, and owner must not be empty")
        if float(ttl_seconds) <= 0:
            raise ValueError("lease ttl must be positive")

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

    def record_event(self, workflow_id: str, worker_id: str, state: str, detail: str) -> None:
        """Append an audit event without replacing a newer checkpoint."""
        if not workflow_id.strip():
            raise ValueError("workflow id must not be empty")
        with self._lock, self._connection:
            self._connection.execute(
                "INSERT INTO workflow_events(workflow_id, worker_id, state, detail) VALUES (?, ?, ?, ?)",
                (workflow_id, worker_id, state, detail),
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
