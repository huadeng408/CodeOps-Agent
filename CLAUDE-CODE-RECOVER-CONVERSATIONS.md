# Codex Conversation Recovery Execution Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to execute this plan task-by-task. Also use `superpowers:systematic-debugging` before diagnosing any mismatch and `superpowers:verification-before-completion` before reporting success. Steps use checkbox (`- [ ]`) syntax for tracking. Do not parallelize mutation steps.

**Goal:** Restore the six archived Codex Desktop conversations for `D:\vscode\localcode`, preserve the two active conversations, and make all eight records internally consistent and recoverable without losing any raw conversation data.

**Architecture:** Treat Codex's SQLite index and rollout JSONL files as one recovery unit. First prove the known baseline, then create an immutable backup outside the Git repository. Only while Codex Desktop is fully stopped, move the six archived rollout files to their canonical active paths and update the six matching SQLite rows in one guarded recovery script. Do not rebuild the database, rewrite conversation content, or modify unrelated threads.

**Tech Stack:** Windows PowerShell, SQLite (`D:\anaconda\Library\bin\sqlite3.exe`), Python 3 standard library (`sqlite3`, `pathlib`), Codex Desktop local storage.

---

## Operator Contract

Read this entire document before running commands. Work from `D:\vscode\localcode` in a new VSCode Claude Code window.

The user has authorized recovery of the six exact archived Codex threads listed below. This authorization does not extend to deleting other sessions, resetting Codex, rebuilding `state_5.sqlite`, editing conversation contents, terminating Codex automatically, or modifying Git history.

Hard safety rules:

- Codex Desktop must be fully closed before any backup or mutation. If a Codex process is running, stop and ask the user to close it. Do not kill it automatically.
- Perform baseline checks before writes. If the observed state differs from the expected state, stop and report the difference.
- Keep backups outside the repository at `D:\vscode\recovery-backups\localcode-codex-20260729-before-recovery`.
- Never delete `state_5.sqlite`, `session_index.jsonl`, an active rollout, or an archived rollout.
- Never run recursive deletion commands.
- Do not modify `.codex-global-state.json`, `session_index.jsonl`, `logs_2.sqlite`, or the project Agent database during recovery.
- Do not update `created_at`, `updated_at`, `recency_at`, titles, previews, IDs, or conversation JSONL content.
- Do not commit recovery artifacts or unrelated working-tree changes.
- If a mutation step fails, stop. Use the backup and the rollback section; do not improvise a second repair.

## Known Evidence

The following facts were verified read-only on 2026-07-29 before this handoff:

| Store | Verified state |
|---|---|
| `C:\Users\ieeep\.codex\state_5.sqlite` | `PRAGMA quick_check = ok`; 8 rows whose `cwd` contains `localcode`; 2 active and 6 archived |
| Codex rollout files | All 8 indexed rollout paths exist |
| Active hidden task | `019fab98-710c-7093-ac19-b7c253c900c8`, title `改用 MinerU 和 OCR 处理 PDF`, readable through the Codex thread API |
| Oldest archived task | `019e4080-0c58-7f30-bc74-9d70a3b32589`, title `搭建项目骨架`, readable through the Codex thread API |
| `D:\vscode\localcode\.agent\sessions\sessions.sqlite` | `PRAGMA quick_check = ok`; 743 project-Agent sessions; 1928 messages; 0 invalid JSON payloads |

The project-Agent SQLite database is a separate store. Back it up for safety, but do not alter it as part of this recovery.

## Exact Recovery Set

Only these six rows and files may be changed:

| Thread ID | Current archived rollout | Required active rollout |
|---|---|---|
| `019fab95-5593-7370-b218-808e12500831` | `C:\Users\ieeep\.codex\archived_sessions\rollout-2026-07-29T09-55-18-019fab95-5593-7370-b218-808e12500831.jsonl` | `C:\Users\ieeep\.codex\sessions\2026\07\29\rollout-2026-07-29T09-55-18-019fab95-5593-7370-b218-808e12500831.jsonl` |
| `019eab14-5594-7171-a399-0dd74525ab8c` | `C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-09T14-31-42-019eab14-5594-7171-a399-0dd74525ab8c.jsonl` | `C:\Users\ieeep\.codex\sessions\2026\06\09\rollout-2026-06-09T14-31-42-019eab14-5594-7171-a399-0dd74525ab8c.jsonl` |
| `019e8871-1e27-7a73-8ce0-c74b219a4fab` | `C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-02T21-06-20-019e8871-1e27-7a73-8ce0-c74b219a4fab.jsonl` | `C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-06-20-019e8871-1e27-7a73-8ce0-c74b219a4fab.jsonl` |
| `019e886f-f8b3-7863-9a62-5e7b7e3fb091` | `C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-02T21-05-05-019e886f-f8b3-7863-9a62-5e7b7e3fb091.jsonl` | `C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-05-05-019e886f-f8b3-7863-9a62-5e7b7e3fb091.jsonl` |
| `019e451b-36de-7803-8172-aa29d2e438b3` | `C:\Users\ieeep\.codex\archived_sessions\rollout-2026-05-20T19-17-51-019e451b-36de-7803-8172-aa29d2e438b3.jsonl` | `C:\Users\ieeep\.codex\sessions\2026\05\20\rollout-2026-05-20T19-17-51-019e451b-36de-7803-8172-aa29d2e438b3.jsonl` |
| `019e4080-0c58-7f30-bc74-9d70a3b32589` | `C:\Users\ieeep\.codex\archived_sessions\rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl` | `C:\Users\ieeep\.codex\sessions\2026\05\19\rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl` |

---

### Task 1: Confirm Offline State and Read-Only Baseline

**Files:**

- Read: `C:\Users\ieeep\.codex\state_5.sqlite`
- Read: `C:\Users\ieeep\.codex\archived_sessions\*.jsonl`
- Read: `D:\vscode\localcode\.agent\sessions\sessions.sqlite`
- Modify: none

- [ ] **Step 1: Confirm Codex Desktop is not running**

Run in PowerShell:

```powershell
$codexProcesses = @(Get-Process -ErrorAction SilentlyContinue | Where-Object {
    $_.ProcessName -like '*Codex*' -or
    ($_.Path -and $_.Path -like '*OpenAI.Codex*')
})

if ($codexProcesses.Count -gt 0) {
    $codexProcesses | Select-Object ProcessName, Id, Path | Format-Table -AutoSize
    throw 'Codex Desktop is running. Ask the user to close it, then rerun this check.'
}

'Codex Desktop process check: clear'
```

Expected: `Codex Desktop process check: clear`. If any process is listed, stop here.

- [ ] **Step 2: Verify both SQLite databases without changing them**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$codexDb = 'C:\Users\ieeep\.codex\state_5.sqlite'
$agentDb = 'D:\vscode\localcode\.agent\sessions\sessions.sqlite'

& $sqlite -readonly -header -column $codexDb `
  'PRAGMA quick_check;' `
  "SELECT COUNT(*) AS localcode_threads,
          SUM(CASE WHEN archived=0 THEN 1 ELSE 0 END) AS active,
          SUM(CASE WHEN archived=1 THEN 1 ELSE 0 END) AS archived
     FROM threads
    WHERE lower(cwd) LIKE '%localcode%';"

if ($LASTEXITCODE -ne 0) { throw 'Codex database verification failed.' }

& $sqlite -readonly -header -column $agentDb `
  'PRAGMA quick_check;' `
  "SELECT COUNT(*) AS sessions,
          SUM(json_array_length(payload, '$.messages')) AS messages,
          SUM(CASE WHEN NOT json_valid(payload) THEN 1 ELSE 0 END) AS invalid_payloads
     FROM sessions;"

if ($LASTEXITCODE -ne 0) { throw 'Project Agent database verification failed.' }
```

Expected:

```text
Codex quick_check: ok
localcode_threads: 8
active: 2
archived: 6
Project Agent quick_check: ok
sessions: 743
messages: 1928
invalid_payloads: 0
```

If any value differs, do not mutate anything. Record the actual output and stop.

- [ ] **Step 3: Verify the exact six rows are archived**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$codexDb = 'C:\Users\ieeep\.codex\state_5.sqlite'

& $sqlite -readonly -header -column $codexDb @"
SELECT id, archived, cwd, rollout_path
FROM threads
WHERE id IN (
  '019fab95-5593-7370-b218-808e12500831',
  '019eab14-5594-7171-a399-0dd74525ab8c',
  '019e8871-1e27-7a73-8ce0-c74b219a4fab',
  '019e886f-f8b3-7863-9a62-5e7b7e3fb091',
  '019e451b-36de-7803-8172-aa29d2e438b3',
  '019e4080-0c58-7f30-bc74-9d70a3b32589'
)
ORDER BY id;
"@

if ($LASTEXITCODE -ne 0) { throw 'Target-row query failed.' }
```

Expected: exactly six rows; every `archived` value is `1`; every `rollout_path` is under `C:\Users\ieeep\.codex\archived_sessions`; every `cwd` resolves to `D:\vscode\localcode` (the stored form may include the Windows `\\?\` prefix).

- [ ] **Step 4: Verify every source exists and every destination is absent**

Run:

```powershell
$mappings = @(
    [pscustomobject]@{ Id='019fab95-5593-7370-b218-808e12500831'; Source='C:\Users\ieeep\.codex\archived_sessions\rollout-2026-07-29T09-55-18-019fab95-5593-7370-b218-808e12500831.jsonl'; Target='C:\Users\ieeep\.codex\sessions\2026\07\29\rollout-2026-07-29T09-55-18-019fab95-5593-7370-b218-808e12500831.jsonl' },
    [pscustomobject]@{ Id='019eab14-5594-7171-a399-0dd74525ab8c'; Source='C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-09T14-31-42-019eab14-5594-7171-a399-0dd74525ab8c.jsonl'; Target='C:\Users\ieeep\.codex\sessions\2026\06\09\rollout-2026-06-09T14-31-42-019eab14-5594-7171-a399-0dd74525ab8c.jsonl' },
    [pscustomobject]@{ Id='019e8871-1e27-7a73-8ce0-c74b219a4fab'; Source='C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-02T21-06-20-019e8871-1e27-7a73-8ce0-c74b219a4fab.jsonl'; Target='C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-06-20-019e8871-1e27-7a73-8ce0-c74b219a4fab.jsonl' },
    [pscustomobject]@{ Id='019e886f-f8b3-7863-9a62-5e7b7e3fb091'; Source='C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-02T21-05-05-019e886f-f8b3-7863-9a62-5e7b7e3fb091.jsonl'; Target='C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-05-05-019e886f-f8b3-7863-9a62-5e7b7e3fb091.jsonl' },
    [pscustomobject]@{ Id='019e451b-36de-7803-8172-aa29d2e438b3'; Source='C:\Users\ieeep\.codex\archived_sessions\rollout-2026-05-20T19-17-51-019e451b-36de-7803-8172-aa29d2e438b3.jsonl'; Target='C:\Users\ieeep\.codex\sessions\2026\05\20\rollout-2026-05-20T19-17-51-019e451b-36de-7803-8172-aa29d2e438b3.jsonl' },
    [pscustomobject]@{ Id='019e4080-0c58-7f30-bc74-9d70a3b32589'; Source='C:\Users\ieeep\.codex\archived_sessions\rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl'; Target='C:\Users\ieeep\.codex\sessions\2026\05\19\rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl' }
)

$mappings | ForEach-Object {
    [pscustomobject]@{
        Id = $_.Id
        SourceExists = Test-Path -LiteralPath $_.Source
        TargetExists = Test-Path -LiteralPath $_.Target
    }
} | Format-Table -AutoSize

$bad = @($mappings | Where-Object {
    -not (Test-Path -LiteralPath $_.Source) -or
    (Test-Path -LiteralPath $_.Target)
})

if ($bad.Count -ne 0) {
    $bad | Format-Table -AutoSize
    throw 'Source/target precondition failed. Stop without mutation.'
}
```

Expected: six rows with `SourceExists=True` and `TargetExists=False`.

---

### Task 2: Create a Complete Recovery Backup

**Files:**

- Create: `D:\vscode\recovery-backups\localcode-codex-20260729-before-recovery\`
- Copy from: `C:\Users\ieeep\.codex\state_5.sqlite*`
- Copy from: the eight current-directory rollout JSONL files
- Copy from: `C:\Users\ieeep\.codex\session_index.jsonl`
- Copy from: `C:\Users\ieeep\.codex\.codex-global-state.json`
- Copy from: `D:\vscode\localcode\.agent\sessions\sessions.sqlite`

- [ ] **Step 1: Create a new, non-overlapping backup directory**

Run:

```powershell
$backupRoot = 'D:\vscode\recovery-backups\localcode-codex-20260729-before-recovery'

if (Test-Path -LiteralPath $backupRoot) {
    throw "Backup directory already exists: $backupRoot. Do not overwrite it."
}

New-Item -ItemType Directory -Path $backupRoot -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $backupRoot 'codex-state') -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $backupRoot 'rollouts') -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $backupRoot 'project-agent') -Force | Out-Null

Get-Item -LiteralPath $backupRoot | Format-List FullName, CreationTime
```

Expected: the exact backup directory is created outside `D:\vscode\localcode`.

- [ ] **Step 2: Copy database state, index state, all eight localcode rollouts, and the project-Agent database**

Run:

```powershell
$backupRoot = 'D:\vscode\recovery-backups\localcode-codex-20260729-before-recovery'
$codexRoot = 'C:\Users\ieeep\.codex'
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$codexDb = Join-Path $codexRoot 'state_5.sqlite'

foreach ($name in @('state_5.sqlite', 'state_5.sqlite-wal', 'state_5.sqlite-shm', 'session_index.jsonl', '.codex-global-state.json')) {
    $source = Join-Path $codexRoot $name
    if (Test-Path -LiteralPath $source) {
        Copy-Item -LiteralPath $source -Destination (Join-Path $backupRoot 'codex-state')
    }
}

Copy-Item -LiteralPath 'D:\vscode\localcode\.agent\sessions\sessions.sqlite' `
  -Destination (Join-Path $backupRoot 'project-agent\sessions.sqlite')

$rolloutPaths = & $sqlite -readonly -noheader $codexDb `
  "SELECT rollout_path FROM threads WHERE lower(cwd) LIKE '%localcode%' ORDER BY id;"

if ($LASTEXITCODE -ne 0 -or $rolloutPaths.Count -ne 8) {
    throw "Expected 8 rollout paths, got $($rolloutPaths.Count)."
}

foreach ($path in $rolloutPaths) {
    if (-not (Test-Path -LiteralPath $path)) {
        throw "Indexed rollout is missing before backup: $path"
    }
    Copy-Item -LiteralPath $path -Destination (Join-Path $backupRoot 'rollouts')
}

Get-ChildItem -LiteralPath $backupRoot -Recurse -File |
  Sort-Object FullName |
  Select-Object FullName, Length | Format-Table -AutoSize
```

Expected: a state backup, 8 rollout JSONL files, and the separate project-Agent database are present.

- [ ] **Step 3: Generate and verify a SHA-256 manifest**

Run:

```powershell
$backupRoot = 'D:\vscode\recovery-backups\localcode-codex-20260729-before-recovery'
$manifest = Join-Path $backupRoot 'sha256-manifest.csv'

$hashRows = Get-ChildItem -LiteralPath $backupRoot -Recurse -File |
  Where-Object { $_.FullName -ne $manifest } |
  Sort-Object FullName |
  ForEach-Object {
      $hash = Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256
      [pscustomobject]@{
          Path = $_.FullName
          Length = $_.Length
          SHA256 = $hash.Hash
      }
  }

$hashRows | Export-Csv -LiteralPath $manifest -NoTypeInformation -Encoding UTF8

$manifestRows = @(Import-Csv -LiteralPath $manifest)
$rolloutBackups = @(Get-ChildItem -LiteralPath (Join-Path $backupRoot 'rollouts') -File -Filter '*.jsonl')

if ($manifestRows.Count -lt 11) { throw "Backup manifest is unexpectedly small: $($manifestRows.Count) rows." }
if ($rolloutBackups.Count -ne 8) { throw "Expected 8 rollout backups, got $($rolloutBackups.Count)." }

[pscustomobject]@{
    ManifestRows = $manifestRows.Count
    RolloutBackups = $rolloutBackups.Count
    BackupBytes = (Get-ChildItem -LiteralPath $backupRoot -Recurse -File | Measure-Object Length -Sum).Sum
} | Format-List
```

Expected: `RolloutBackups: 8`, at least 11 manifest rows, and a nonzero backup size.

---

### Task 3: Build and Review the Guarded Recovery Script

**Files:**

- Create: `D:\vscode\localcode\tmp\recover_codex_threads.py`
- Modify during execution: `C:\Users\ieeep\.codex\state_5.sqlite`
- Move during execution: the six exact archived JSONL files in the recovery table

- [ ] **Step 1: Create the recovery script with exactly this content**

Use the file editing tool to create `D:\vscode\localcode\tmp\recover_codex_threads.py`:

```python
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
```

- [ ] **Step 2: Review the script against the recovery table**

Run:

```powershell
Get-Content -Raw 'D:\vscode\localcode\tmp\recover_codex_threads.py'
```

Manually verify all of the following before execution:

- `ITEMS` contains exactly six entries.
- Every ID, source, and target exactly matches the recovery table in this document.
- The script only updates `archived`, `archived_at`, and `rollout_path`.
- The script does not delete files, touch unrelated IDs, or alter timestamps/content.
- The exception path rolls back SQLite and reverses completed file moves.

- [ ] **Step 3: Compile the script without executing recovery**

Run:

```powershell
python -m py_compile 'D:\vscode\localcode\tmp\recover_codex_threads.py'
if ($LASTEXITCODE -ne 0) { throw 'Recovery script does not compile.' }
'Recovery script compile check: ok'
```

Expected: `Recovery script compile check: ok`. This may create only a Python bytecode cache under `tmp`; it must not change Codex state.

---

### Task 4: Execute the Single Recovery Mutation

**Files:**

- Execute: `D:\vscode\localcode\tmp\recover_codex_threads.py`
- Modify: `C:\Users\ieeep\.codex\state_5.sqlite`
- Move: six exact rollout JSONL files

- [ ] **Step 1: Repeat the Codex process check immediately before mutation**

Run the exact process check from Task 1 Step 1 again.

Expected: no Codex process. If Codex has restarted, stop and ask the user to close it.

- [ ] **Step 2: Execute the recovery script exactly once**

Run:

```powershell
python 'D:\vscode\localcode\tmp\recover_codex_threads.py'
if ($LASTEXITCODE -ne 0) { throw 'Recovery script failed. Do not rerun it.' }
```

Expected first line:

```text
RECOVERY_OK: 6 archived threads restored
```

Expected following output: six `thread-id -> active rollout path` lines.

If the command fails, do not rerun it. Capture the complete error, keep Codex closed, and proceed to the failure assessment in the Rollback section.

---

### Task 5: Verify Storage Consistency Before Reopening Codex

**Files:**

- Read: `C:\Users\ieeep\.codex\state_5.sqlite`
- Read: eight active rollout JSONL files
- Read: backup manifest
- Modify: none

- [ ] **Step 1: Verify SQLite integrity and final state**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$codexDb = 'C:\Users\ieeep\.codex\state_5.sqlite'

& $sqlite -readonly -header -column $codexDb `
  'PRAGMA quick_check;' `
  "SELECT COUNT(*) AS localcode_threads,
          SUM(CASE WHEN archived=0 THEN 1 ELSE 0 END) AS active,
          SUM(CASE WHEN archived=1 THEN 1 ELSE 0 END) AS archived
     FROM threads
    WHERE lower(cwd) LIKE '%localcode%';" `
  "SELECT id, archived, rollout_path
     FROM threads
    WHERE id IN (
      '019fab95-5593-7370-b218-808e12500831',
      '019eab14-5594-7171-a399-0dd74525ab8c',
      '019e8871-1e27-7a73-8ce0-c74b219a4fab',
      '019e886f-f8b3-7863-9a62-5e7b7e3fb091',
      '019e451b-36de-7803-8172-aa29d2e438b3',
      '019e4080-0c58-7f30-bc74-9d70a3b32589'
    )
    ORDER BY id;"

if ($LASTEXITCODE -ne 0) { throw 'Final SQLite verification failed.' }
```

Expected:

- `quick_check = ok`
- `localcode_threads = 8`
- `active = 8`
- `archived = 0`
- Six target rows, each with `archived = 0`
- Each target `rollout_path` is under `C:\Users\ieeep\.codex\sessions\YYYY\MM\DD`

- [ ] **Step 2: Verify all eight indexed rollouts exist and identities match**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$codexDb = 'C:\Users\ieeep\.codex\state_5.sqlite'
$rows = & $sqlite -readonly -separator '|' -noheader $codexDb `
  "SELECT id, rollout_path FROM threads WHERE lower(cwd) LIKE '%localcode%' ORDER BY id;"

if ($LASTEXITCODE -ne 0 -or $rows.Count -ne 8) {
    throw "Expected 8 indexed localcode rollouts, got $($rows.Count)."
}

$failures = @()
foreach ($row in $rows) {
    $parts = $row -split '\|', 2
    $id = $parts[0]
    $path = $parts[1]
    if (-not (Test-Path -LiteralPath $path)) {
        $failures += "Missing: $id -> $path"
        continue
    }
    $firstLine = Get-Content -LiteralPath $path -TotalCount 1
    if ($firstLine -notmatch [regex]::Escape($id) -or $firstLine -notmatch 'session_meta') {
        $failures += "Metadata mismatch: $id -> $path"
    }
}

if ($failures.Count -gt 0) {
    $failures | ForEach-Object { Write-Error $_ }
    throw 'Indexed rollout verification failed.'
}

'Indexed rollout verification: 8/8 present with matching metadata'
```

Expected: `Indexed rollout verification: 8/8 present with matching metadata`.

- [ ] **Step 3: Verify the separate project-Agent database remained unchanged**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$agentDb = 'D:\vscode\localcode\.agent\sessions\sessions.sqlite'

& $sqlite -readonly -header -column $agentDb `
  'PRAGMA quick_check;' `
  "SELECT COUNT(*) AS sessions,
          SUM(json_array_length(payload, '$.messages')) AS messages,
          SUM(CASE WHEN NOT json_valid(payload) THEN 1 ELSE 0 END) AS invalid_payloads
     FROM sessions;"

if ($LASTEXITCODE -ne 0) { throw 'Project Agent database post-check failed.' }
```

Expected: `ok`, 743 sessions, 1928 messages, 0 invalid payloads.

---

### Task 6: Reopen Codex and Perform User-Visible Acceptance

**Files:**

- Read through Codex UI: restored conversations
- Modify: none beyond normal Codex startup/index loading

- [ ] **Step 1: Ask the user to start Codex Desktop normally**

Do not start it silently and do not kill/restart it yourself. Tell the user that offline verification passed and ask them to launch Codex Desktop.

- [ ] **Step 2: Verify the current directory task list**

In Codex Desktop, open `D:\vscode\localcode` and verify these tasks are accessible:

| Thread ID | Expected recognizable title/content |
|---|---|
| `019facf3-998b-79f2-a253-1b6596fa6952` | Current recovery-plan conversation |
| `019fab98-710c-7093-ac19-b7c253c900c8` | `改用 MinerU 和 OCR 处理 PDF` |
| `019fab95-5593-7370-b218-808e12500831` | Handoff-based localcode continuation task |
| `019eab14-5594-7171-a399-0dd74525ab8c` | README rewrite task |
| `019e8871-1e27-7a73-8ce0-c74b219a4fab` | `你是谁` conversation |
| `019e886f-f8b3-7863-9a62-5e7b7e3fb091` | `你是谁` conversation |
| `019e451b-36de-7803-8172-aa29d2e438b3` | Implementing `设计方案.md` |
| `019e4080-0c58-7f30-bc74-9d70a3b32589` | Project skeleton / `搭建项目骨架` |

Acceptance requires that all eight can be opened and their existing turns are readable. It is acceptable for old tasks to remain lower in recency order; do not rewrite timestamps merely to move them to the top.

- [ ] **Step 3: Handle a UI-only index miss conservatively**

If offline verification shows 8 active rows and 8 valid rollouts but one or more tasks still do not appear in the list:

1. Do not edit SQLite again.
2. Do not change recency timestamps.
3. Record the missing IDs and the Codex app version.
4. Attempt direct navigation/opening by exact thread ID through any official Codex task control available in that environment.
5. If direct navigation is unavailable, report that raw data and SQLite state are recovered but the remaining issue is UI indexing. Stop for user direction.

---

## Rollback Procedure

Use rollback only if Task 4 fails or Task 5 reports a storage inconsistency. Keep Codex Desktop closed.

The recovery script attempts to roll back its SQLite transaction and reverse any completed file moves automatically. First rerun the read-only checks from Task 1 Steps 2-4 to determine the actual state. Do not rerun the recovery script.

If the database no longer passes `PRAGMA quick_check` or target rows are inconsistent, restore from the exact backup only after reporting the observed state to the user:

```powershell
$backupRoot = 'D:\vscode\recovery-backups\localcode-codex-20260729-before-recovery'
$codexRoot = 'C:\Users\ieeep\.codex'

foreach ($name in @('state_5.sqlite', 'state_5.sqlite-wal', 'state_5.sqlite-shm')) {
    $backup = Join-Path (Join-Path $backupRoot 'codex-state') $name
    if (Test-Path -LiteralPath $backup) {
        Copy-Item -LiteralPath $backup -Destination (Join-Path $codexRoot $name) -Force
    }
}
```

After restoring the database files, restore only missing archived source rollouts from `D:\vscode\recovery-backups\localcode-codex-20260729-before-recovery\rollouts`. Do not remove any active rollout until its identity and backup hash have been checked and the user has approved that exact removal.

Re-run `PRAGMA quick_check`, verify 8 indexed rows and 8 indexed files, then report the rollback result. Do not attempt another recovery in the same run.

---

## Final Report Format

Report concise evidence, not confidence:

```text
Recovery status: SUCCESS | PARTIAL | ROLLED BACK | BLOCKED
Backup: D:\vscode\recovery-backups\localcode-codex-20260729-before-recovery
Codex DB quick_check: fill this field with the literal PRAGMA output
Current-directory threads: fill this field with the literal SELECT count
Active / archived: fill both fields with the literal SELECT counts
Indexed rollout files present: fill this field with the verified count out of 8
Project-Agent DB: fill these fields with quick_check, sessions, messages, and invalid_payloads outputs
UI acceptance: list every verified ID and separately list any missing ID
Files changed outside Codex storage: D:\vscode\localcode\tmp\recover_codex_threads.py only
Git changes: no commit; preserve all pre-existing user changes
```

Do not claim success unless the database check, file check, and user-visible acceptance all pass. If the UI check is unavailable, report `PARTIAL`, even when storage recovery is complete.
