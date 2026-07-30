from __future__ import annotations

import sqlite3
import sys
from dataclasses import dataclass
from pathlib import Path


CODEX_DB = Path(r"C:\Users\ieeep\.codex\state_5.sqlite")
EXPECTED_CWD_SUFFIX = r"d:\vscode\localcode"


@dataclass(frozen=True)
class RecoveryItem:
    thread_id: str
    source: Path
    target: Path


ITEMS = (
    RecoveryItem(
        "019fab95-5593-7370-b218-808e12500831",
        Path(r"C:\Users\ieeep\.codex\archived_sessions\rollout-2026-07-29T09-55-18-019fab95-5593-7370-b218-808e12500831.jsonl"),
        Path(r"C:\Users\ieeep\.codex\sessions\2026\07\29\rollout-2026-07-29T09-55-18-019fab95-5593-7370-b218-808e12500831.jsonl"),
    ),
    RecoveryItem(
        "019eab14-5594-7171-a399-0dd74525ab8c",
        Path(r"C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-09T14-31-42-019eab14-5594-7171-a399-0dd74525ab8c.jsonl"),
        Path(r"C:\Users\ieeep\.codex\sessions\2026\06\09\rollout-2026-06-09T14-31-42-019eab14-5594-7171-a399-0dd74525ab8c.jsonl"),
    ),
    RecoveryItem(
        "019e8871-1e27-7a73-8ce0-c74b219a4fab",
        Path(r"C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-02T21-06-20-019e8871-1e27-7a73-8ce0-c74b219a4fab.jsonl"),
        Path(r"C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-06-20-019e8871-1e27-7a73-8ce0-c74b219a4fab.jsonl"),
    ),
    RecoveryItem(
        "019e886f-f8b3-7863-9a62-5e7b7e3fb091",
        Path(r"C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-02T21-05-05-019e886f-f8b3-7863-9a62-5e7b7e3fb091.jsonl"),
        Path(r"C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-05-05-019e886f-f8b3-7863-9a62-5e7b7e3fb091.jsonl"),
    ),
    RecoveryItem(
        "019e451b-36de-7803-8172-aa29d2e438b3",
        Path(r"C:\Users\ieeep\.codex\archived_sessions\rollout-2026-05-20T19-17-51-019e451b-36de-7803-8172-aa29d2e438b3.jsonl"),
        Path(r"C:\Users\ieeep\.codex\sessions\2026\05\20\rollout-2026-05-20T19-17-51-019e451b-36de-7803-8172-aa29d2e438b3.jsonl"),
    ),
    RecoveryItem(
        "019e4080-0c58-7f30-bc74-9d70a3b32589",
        Path(r"C:\Users\ieeep\.codex\archived_sessions\rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl"),
        Path(r"C:\Users\ieeep\.codex\sessions\2026\05\19\rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl"),
    ),
)


def normalized_cwd(value: str) -> str:
    normalized = value.lower().replace("/", "\\")
    if normalized.startswith("\\\\?\\"):
        normalized = normalized[4:]
    return normalized


def verify_jsonl_identity(item: RecoveryItem) -> None:
    first_line = item.source.open("r", encoding="utf-8").readline()
    expected_markers = (
        '"type":"session_meta"',
        item.thread_id,
        'localcode',
    )
    missing = [marker for marker in expected_markers if marker not in first_line]
    if missing:
        raise RuntimeError(
            f"Rollout metadata mismatch for {item.thread_id}; missing {missing}"
        )


def main() -> int:
    if not CODEX_DB.is_file():
        raise RuntimeError(f"Codex database not found: {CODEX_DB}")

    moved: list[RecoveryItem] = []
    committed = False
    connection = sqlite3.connect(CODEX_DB, timeout=5.0)
    connection.row_factory = sqlite3.Row

    try:
        quick_check = connection.execute("PRAGMA quick_check").fetchone()[0]
        if quick_check != "ok":
            raise RuntimeError(f"Pre-mutation quick_check failed: {quick_check}")

        connection.execute("BEGIN IMMEDIATE")

        query_marks = ",".join("?" for _ in ITEMS)
        ids = tuple(item.thread_id for item in ITEMS)
        rows = connection.execute(
            f"SELECT id, archived, cwd, rollout_path FROM threads "
            f"WHERE id IN ({query_marks}) ORDER BY id",
            ids,
        ).fetchall()

        if len(rows) != len(ITEMS):
            raise RuntimeError(f"Expected 6 target rows, found {len(rows)}")

        expected_sources = {item.thread_id: str(item.source) for item in ITEMS}
        for row in rows:
            if row["archived"] != 1:
                raise RuntimeError(f"Thread is not archived: {row['id']}")
            if normalized_cwd(row["cwd"]) != EXPECTED_CWD_SUFFIX:
                raise RuntimeError(f"Unexpected cwd for {row['id']}: {row['cwd']}")
            if row["rollout_path"].lower() != expected_sources[row["id"]].lower():
                raise RuntimeError(
                    f"Unexpected rollout path for {row['id']}: {row['rollout_path']}"
                )

        for item in ITEMS:
            if not item.source.is_file():
                raise RuntimeError(f"Archived rollout missing: {item.source}")
            if item.target.exists():
                raise RuntimeError(f"Active rollout already exists: {item.target}")
            verify_jsonl_identity(item)

        for item in ITEMS:
            item.target.parent.mkdir(parents=True, exist_ok=True)
            item.source.replace(item.target)
            moved.append(item)

        for item in ITEMS:
            cursor = connection.execute(
                "UPDATE threads "
                "SET archived = 0, archived_at = NULL, rollout_path = ? "
                "WHERE id = ? AND archived = 1",
                (str(item.target), item.thread_id),
            )
            if cursor.rowcount != 1:
                raise RuntimeError(f"SQLite update did not affect one row: {item.thread_id}")

        active_rows = connection.execute(
            f"SELECT id, archived, rollout_path FROM threads "
            f"WHERE id IN ({query_marks}) ORDER BY id",
            ids,
        ).fetchall()

        if len(active_rows) != len(ITEMS):
            raise RuntimeError("Post-update target-row count changed")

        expected_targets = {item.thread_id: str(item.target) for item in ITEMS}
        for row in active_rows:
            if row["archived"] != 0:
                raise RuntimeError(f"Thread remains archived: {row['id']}")
            if row["rollout_path"].lower() != expected_targets[row["id"]].lower():
                raise RuntimeError(f"Updated rollout path mismatch: {row['id']}")
            if not Path(row["rollout_path"]).is_file():
                raise RuntimeError(f"Updated rollout path is missing: {row['rollout_path']}")

        connection.commit()
        committed = True

        quick_check = connection.execute("PRAGMA quick_check").fetchone()[0]
        if quick_check != "ok":
            raise RuntimeError(f"Post-commit quick_check failed: {quick_check}")

        print("RECOVERY_OK: 6 archived threads restored")
        for item in ITEMS:
            print(f"{item.thread_id} -> {item.target}")
        return 0
    except Exception:
        if not committed:
            connection.rollback()
            reverse_failures: list[str] = []
            for item in reversed(moved):
                try:
                    if item.target.exists() and not item.source.exists():
                        item.source.parent.mkdir(parents=True, exist_ok=True)
                        item.target.replace(item.source)
                except Exception as error:
                    reverse_failures.append(f"{item.thread_id}: {error}")

            if reverse_failures:
                print("ROLLBACK_FILE_FAILURES:", file=sys.stderr)
                for failure in reverse_failures:
                    print(failure, file=sys.stderr)
        else:
            print(
                "POST_COMMIT_FAILURE: keep files in active paths and use the backup rollback procedure",
                file=sys.stderr,
            )
        raise
    finally:
        connection.close()


if __name__ == "__main__":
    raise SystemExit(main())
