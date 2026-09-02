"""Durable, gold-free checkpoints for Skill selection evaluation."""

from __future__ import annotations

import hashlib
import json
import sqlite3
from collections.abc import Iterable, Mapping
from pathlib import Path
from typing import Any

_FORBIDDEN_RESULT_KEYS = frozenset({"prompt", "expected_skill", "gold", "qrels"})


def _canonical_json(value: Mapping[str, Any]) -> str:
    return json.dumps(
        value,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    )


def _sha256_text(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def _reject_forbidden_result_fields(value: Any) -> None:
    if isinstance(value, Mapping):
        for key, nested in value.items():
            if str(key).lower() in _FORBIDDEN_RESULT_KEYS:
                raise ValueError(f"checkpoint result contains forbidden field: {key}")
            _reject_forbidden_result_fields(nested)
    elif isinstance(value, (list, tuple)):
        for nested in value:
            _reject_forbidden_result_fields(nested)


class SkillSelectionCheckpoint:
    """Persist completed case results behind a small SQLite interface."""

    def __init__(self, path: str | Path) -> None:
        self.path = Path(path).resolve()
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._connection = sqlite3.connect(self.path, timeout=30.0)
        self._connection.row_factory = sqlite3.Row
        self._connection.execute("PRAGMA busy_timeout=30000")
        self._writes_enabled = False

    def _enable_durable_writes(self) -> None:
        if self._writes_enabled:
            return
        mode = self._connection.execute("PRAGMA journal_mode=WAL").fetchone()
        if mode is None or str(mode[0]).lower() != "wal":
            raise RuntimeError("checkpoint journal could not enter WAL mode")
        self._connection.execute("PRAGMA synchronous=FULL")
        self._writes_enabled = True

    def initialize(self, contract: Mapping[str, Any]) -> str:
        self._enable_durable_writes()
        self._connection.executescript(
            """
            CREATE TABLE IF NOT EXISTS metadata (
                key TEXT PRIMARY KEY,
                value_json TEXT NOT NULL,
                value_sha256 TEXT NOT NULL
            );
            CREATE TABLE IF NOT EXISTS results (
                case_id TEXT PRIMARY KEY,
                payload_json TEXT NOT NULL,
                payload_sha256 TEXT NOT NULL
            );
            """
        )
        payload = _canonical_json(contract)
        digest = _sha256_text(payload)
        try:
            with self._connection:
                self._connection.execute(
                    """
                    INSERT INTO metadata(key, value_json, value_sha256)
                    VALUES ('contract', ?, ?)
                    """,
                    (payload, digest),
                )
        except sqlite3.IntegrityError as exc:
            raise ValueError("checkpoint contract is already initialized") from exc
        return digest

    def completed_case_ids(self) -> frozenset[str]:
        rows = self._connection.execute("SELECT case_id FROM results").fetchall()
        return frozenset(str(row["case_id"]) for row in rows)

    def validate_contract(self, contract: Mapping[str, Any]) -> str:
        try:
            row = self._connection.execute(
                """
                SELECT value_json, value_sha256
                FROM metadata
                WHERE key = 'contract'
                """
            ).fetchone()
        except sqlite3.Error as exc:
            raise ValueError("checkpoint contract is missing") from exc
        if row is None:
            raise ValueError("checkpoint contract is missing")
        stored_payload = str(row["value_json"])
        stored_digest = str(row["value_sha256"])
        if _sha256_text(stored_payload) != stored_digest:
            raise ValueError("checkpoint contract checksum mismatch")
        requested_payload = _canonical_json(contract)
        if requested_payload != stored_payload:
            raise ValueError("checkpoint contract does not match this evaluation")
        return stored_digest

    def save_result(self, case_id: str, result: Mapping[str, Any]) -> None:
        if not case_id or result.get("case_id") != case_id:
            raise ValueError("checkpoint result case_id must match its row")
        _reject_forbidden_result_fields(result)
        payload = _canonical_json(result)
        digest = _sha256_text(payload)
        self._enable_durable_writes()
        try:
            with self._connection:
                self._connection.execute(
                    """
                    INSERT INTO results(case_id, payload_json, payload_sha256)
                    VALUES (?, ?, ?)
                    """,
                    (case_id, payload, digest),
                )
        except sqlite3.IntegrityError as exc:
            raise ValueError(
                f"checkpoint case is already completed: {case_id}"
            ) from exc

    def load_ordered_results(self, case_ids: Iterable[str]) -> list[dict[str, Any]]:
        rows = {
            str(row["case_id"]): row
            for row in self._connection.execute(
                "SELECT case_id, payload_json, payload_sha256 FROM results"
            )
        }
        results: list[dict[str, Any]] = []
        for case_id in case_ids:
            row = rows.get(case_id)
            if row is None:
                raise ValueError(f"checkpoint result is missing: {case_id}")
            payload = str(row["payload_json"])
            if _sha256_text(payload) != str(row["payload_sha256"]):
                raise ValueError(f"checkpoint result checksum mismatch: {case_id}")
            result = json.loads(payload)
            if not isinstance(result, dict) or result.get("case_id") != case_id:
                raise ValueError(f"checkpoint result payload is invalid: {case_id}")
            results.append(result)
        return results

    def finalize(self) -> None:
        """Checkpoint WAL pages and remove volatile sidecars before checksumming."""

        checkpoint = self._connection.execute(
            "PRAGMA wal_checkpoint(TRUNCATE)"
        ).fetchone()
        if checkpoint is None or int(checkpoint[0]) != 0:
            raise RuntimeError("checkpoint WAL could not be finalized")
        mode = self._connection.execute("PRAGMA journal_mode=DELETE").fetchone()
        if mode is None or str(mode[0]).lower() != "delete":
            raise RuntimeError("checkpoint journal could not be made stable")

    def close(self) -> None:
        self._connection.close()


__all__ = ["SkillSelectionCheckpoint"]
