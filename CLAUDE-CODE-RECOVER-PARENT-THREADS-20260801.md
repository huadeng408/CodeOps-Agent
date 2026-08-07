# Codex Parent Conversation Recovery Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to execute this plan task-by-task. Also use `superpowers:systematic-debugging` before diagnosing any mismatch and `superpowers:verification-before-completion` before reporting success. Steps use checkbox (`- [ ]`) syntax for tracking. Do not parallelize mutation steps.

**Goal:** Restore the eight ordinary Codex parent conversations for `D:\vscode\localcode` to the sidebar without changing the 26 delegated rows whose `source` is a subagent JSON object (`source LIKE '{"subagent":%'`) or any conversation JSONL content.

**Architecture:** Codex stores conversation metadata in `C:\Users\ieeep\.codex\state_5.sqlite` and conversation bodies in rollout JSONL files. The database currently contains eight ordinary `source=vscode` parent rows and 26 delegated rows whose `source` begins with `{"subagent":`. Only the parent rows belong in the normal sidebar. The recovery uses a full backup, a real-content provider pilot, then a guarded parent-only archive/path restoration. CC Switch is stopped during all writes so its possible Codex configuration integration cannot race the repair.

**Tech Stack:** Windows PowerShell, SQLite at `D:\anaconda\Library\bin\sqlite3.exe`, Codex Desktop local storage, Codex/CC Switch UI acceptance.

---

## Operator Contract

Read this document completely before running it. Run from `D:\vscode\localcode` in the new Claude Code window. This handoff is for recovery only; do not edit project source code or commit anything.

Hard safety rules:

- Do not run either older recovery document directly: `CLAUDE-CODE-RECOVER-CONVERSATIONS.md` and `CLAUDE-CODE-MIGRATE-LEGACY-PROVIDER.md` use the 2026-07-29 baseline and are stale for the current 34-row database.
- Ask the user to close Codex Desktop, Claude Code, and CC Switch before every database/file mutation. Never terminate them automatically.
- Do not modify or delete any row where `source LIKE '{"subagent":%'`. Those 26 rows are valid delegated histories and are intentionally excluded from the sidebar parent set.
- Do not edit `.codex-global-state.json`, `session_index.jsonl`, Codex logs, project-Agent SQLite, timestamps, titles, previews, IDs, or JSONL contents.
- Do not overwrite an existing backup directory. Do not use recursive deletion commands.
- Do not expose or print API keys from `config.toml` or CC Switch storage.
- If any precondition differs from the values below, stop and report the actual values. Do not improvise a broader migration.

## Verified Current Evidence

Read-only checks on 2026-08-01 found:

| Layer | Current evidence |
|---|---|
| Codex index | `PRAGMA quick_check = ok`; 34 rows whose cwd resolves to `D:\vscode\localcode` |
| Parent rows | 8 rows with `source=vscode`; 2 active/custom, 1 active/OpenAI, 5 archived/custom |
| Delegated rows | 26 rows with `source LIKE '{"subagent":%'`; leave all unchanged |
| Rollout bodies | All checked parent histories are directly readable; the long parent is `019fab98-710c-7093-ac19-b7c253c900c8` |
| Codex config | `C:\Users\ieeep\.codex\config.toml` currently has `model_provider = "custom"` |
| CC Switch | Version `3.19.0`, process `cc-switch.exe`, proxy `127.0.0.1:15721`; it must be stopped during mutation |

The previous pilot `019fab95-5593-7370-b218-808e12500831` is an almost empty parent containing one user message and no assistant reply. It is not a useful provider pilot. Use the real long parent `019fab98-710c-7093-ac19-b7c253c900c8` for the pilot.

## Exact Parent Set

Only these eight IDs may be changed:

```text
019facf3-998b-79f2-a253-1b6596fa6952  current recovery task; already active/custom
019fab98-710c-7093-ac19-b7c253c900c8  long parent; active/OpenAI; provider pilot
019fab95-5593-7370-b218-808e12500831  archived/custom; one user message only
019eab14-5594-7171-a399-0dd74525ab8c  active/custom; README task
019e8871-1e27-7a73-8ce0-c74b219a4fab  archived/custom
019e886f-f8b3-7863-9a62-5e7b7e3fb091  archived/custom
019e451b-36de-7803-8172-aa29d2e438b3  archived/custom
019e4080-0c58-7f30-bc74-9d70a3b32589  archived/custom
```

The five archived rollout moves are:

| ID | Source | Destination |
|---|---|---|
| `019fab95-5593-7370-b218-808e12500831` | `C:\Users\ieeep\.codex\archived_sessions\rollout-2026-07-29T09-55-18-019fab95-5593-7370-b218-808e12500831.jsonl` | `C:\Users\ieeep\.codex\sessions\2026\07\29\rollout-2026-07-29T09-55-18-019fab95-5593-7370-b218-808e12500831.jsonl` |
| `019e8871-1e27-7a73-8ce0-c74b219a4fab` | `C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-02T21-06-20-019e8871-1e27-7a73-8ce0-c74b219a4fab.jsonl` | `C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-06-20-019e8871-1e27-7a73-8ce0-c74b219a4fab.jsonl` |
| `019e886f-f8b3-7863-9a62-5e7b7e3fb091` | `C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-02T21-05-05-019e886f-f8b3-7863-9a62-5e7b7e3fb091.jsonl` | `C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-05-05-019e886f-f8b3-7863-9a62-5e7b7e3fb091.jsonl` |
| `019e451b-36de-7803-8172-aa29d2e438b3` | `C:\Users\ieeep\.codex\archived_sessions\rollout-2026-05-20T19-17-51-019e451b-36de-7803-8172-aa29d2e438b3.jsonl` | `C:\Users\ieeep\.codex\sessions\2026\05\20\rollout-2026-05-20T19-17-51-019e451b-36de-7803-8172-aa29d2e438b3.jsonl` |
| `019e4080-0c58-7f30-bc74-9d70a3b32589` | `C:\Users\ieeep\.codex\archived_sessions\rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl` | `C:\Users\ieeep\.codex\sessions\2026\05\19\rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl` |

---

### Task 1: Stop Writers and Capture the Read-Only Baseline

**Files:** Read `C:\Users\ieeep\.codex\state_5.sqlite`, `C:\Users\ieeep\.codex\.codex-global-state.json`, and `C:\Users\ieeep\.codex\config.toml`; modify none.

- [ ] **Step 1: Confirm Codex, Claude Code, and CC Switch are closed**

Ask the user to close all three applications, then run:

```powershell
$writers = @(Get-CimInstance Win32_Process | Where-Object {
    $_.Name -match '^(codex|codex-code-mode-host|claude|cc-switch)\.exe$'
})
if ($writers.Count -gt 0) {
    $writers | Select-Object Name, ProcessId, ParentProcessId, CommandLine | Format-List
    throw 'A Codex/Claude/CC Switch writer is still running. Stop and ask the user to close it.'
}
'Writer check: clear'
```

Expected: `Writer check: clear`. Do not kill a listed process.

- [ ] **Step 2: Verify the current database shape and provider distribution**

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'
$sql = @'
PRAGMA quick_check;
SELECT CASE WHEN source='vscode' THEN 'vscode' WHEN source LIKE '{"subagent":%' THEN 'subagent-json' ELSE 'other' END AS source_kind,
       archived, model_provider, COUNT(*) AS n
FROM threads
WHERE lower(cwd) LIKE '%localcode%'
GROUP BY source_kind, archived, model_provider
ORDER BY source_kind, archived, model_provider;
SELECT id, source, archived, model_provider, rollout_path
FROM threads
WHERE lower(cwd) LIKE '%localcode%'
ORDER BY created_at;
'@
$sql | & $sqlite -readonly -header -column $db
if ($LASTEXITCODE -ne 0) { throw 'Baseline query failed.' }
```

Expected parent classification: 8 `source=vscode` rows; expected total is 34 rows with 26 `source LIKE '{"subagent":%'` rows. If the parent ID set or source counts differ, stop.

- [ ] **Step 3: Check for a queued follow-up without editing global state**

```powershell
$state = 'C:\Users\ieeep\.codex\.codex-global-state.json'
Select-String -LiteralPath $state -Pattern 'queued-follow-ups','019fab98-710c-7093-ac19-b7c253c900c8' |
    Select-Object LineNumber, Line
```

If the output shows a queued follow-up for the long parent, ask the user to cancel or finish it through the UI before proceeding. Never delete the queue entry by hand.

### Task 2: Create an Independent Backup

**Files:** Create a new directory under `D:\vscode\recovery-backups`; copy databases, config, global state, and all indexed rollout files. Do not overwrite an existing backup.

- [ ] **Step 1: Create a timestamped backup directory**

```powershell
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$backup = "D:\vscode\recovery-backups\localcode-parent-recovery-$stamp"
if (Test-Path -LiteralPath $backup) { throw "Backup already exists: $backup" }
New-Item -ItemType Directory -Path $backup -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $backup 'rollouts') -Force | Out-Null
$backup
```

Record the printed path in the final report.

- [ ] **Step 2: Copy the complete current state**

```powershell
$codexRoot = 'C:\Users\ieeep\.codex'
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = Join-Path $codexRoot 'state_5.sqlite'
$paths = @('state_5.sqlite','state_5.sqlite-wal','state_5.sqlite-shm','config.toml','.codex-global-state.json')
foreach ($name in $paths) {
    $source = Join-Path $codexRoot $name
    if (Test-Path -LiteralPath $source) { Copy-Item -LiteralPath $source -Destination $backup }
}
$rollouts = @(& $sqlite -readonly -noheader $db "SELECT rollout_path FROM threads WHERE lower(cwd) LIKE '%localcode%' ORDER BY id;")
if ($LASTEXITCODE -ne 0 -or $rollouts.Count -ne 34) { throw "Expected 34 indexed localcode rollout paths, got $($rollouts.Count)." }
foreach ($path in $rollouts) {
    $normalized = $path -replace '^\\\\\?\\',''
    if (-not (Test-Path -LiteralPath $normalized)) { throw "Missing indexed rollout: $normalized" }
    Copy-Item -LiteralPath $normalized -Destination (Join-Path $backup 'rollouts')
}
Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $backup 'config.toml'), (Join-Path $backup '.codex-global-state.json') |
    Export-Csv -LiteralPath (Join-Path $backup 'config-global-state-sha256.csv') -NoTypeInformation
Get-ChildItem -LiteralPath $backup -Recurse -File | Measure-Object -Property Length -Sum
```

Expected: the backup contains the SQLite state, config/global-state snapshots, and 34 rollout files. Keep this directory for rollback.

- [ ] **Step 3: Verify the backup database**

```powershell
$backupDb = Join-Path $backup 'state_5.sqlite'
& $sqlite -readonly $backupDb 'PRAGMA quick_check;'
if ($LASTEXITCODE -ne 0) { throw 'Backup quick_check failed.' }
```

Expected: `ok`.

### Task 3: Pilot the Provider Fix on the Real Long Parent

**Files:** Modify only `threads.model_provider` for `019fab98-710c-7093-ac19-b7c253c900c8`.

- [ ] **Step 1: Recheck the pilot precondition**

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'
& $sqlite -readonly -header -column $db "SELECT id, source, archived, model_provider, rollout_path FROM threads WHERE id='019fab98-710c-7093-ac19-b7c253c900c8';"
```

Expected: exactly one `source=vscode` row with `archived=0`, `model_provider=OpenAI`, and an existing rollout path.

- [ ] **Step 2: Update exactly one provider field**

```powershell
$sql = @'
.bail on
PRAGMA busy_timeout=5000;
BEGIN IMMEDIATE;
UPDATE threads
SET model_provider='custom'
WHERE id='019fab98-710c-7093-ac19-b7c253c900c8'
  AND source='vscode'
  AND archived=0
  AND model_provider='OpenAI';
SELECT changes() AS changed_rows;
COMMIT;
PRAGMA quick_check;
'@
$sql | & $sqlite -header -column $db
if ($LASTEXITCODE -ne 0) { throw 'Pilot provider update failed.' }
```

Expected: `changed_rows = 1`, then `ok`.

- [ ] **Step 3: Reopen Codex with CC Switch still stopped and obtain the pilot checkpoint**

Ask the user to start Codex, open the `localcode` project, and confirm that the long parent titled approximately `自研harness-多模态rag，评测集，可观测性` is visible and its prior turns load. Record `PILOT_VISIBLE` only after explicit user confirmation. Ask the user to close Codex again and rerun Task 1 Step 1 before continuing.

- [ ] **Step 4: Roll back the pilot if it is not visible**

With all writers closed, run:

```powershell
$sql = @'
.bail on
PRAGMA busy_timeout=5000;
BEGIN IMMEDIATE;
UPDATE threads
SET model_provider='OpenAI'
WHERE id='019fab98-710c-7093-ac19-b7c253c900c8'
  AND source='vscode'
  AND archived=0
  AND model_provider='custom';
SELECT changes() AS changed_rows;
COMMIT;
PRAGMA quick_check;
'@
$sql | & $sqlite -header -column $db
if ($LASTEXITCODE -ne 0) { throw 'Pilot rollback failed.' }
```

Expected: `changed_rows = 1`, `ok`. Report `BLOCKED_PILOT_NOT_VISIBLE`; do not restore archived rows.

### Task 4: Restore the Five Archived Parent Rollouts and Metadata

**Files:** Move only the five exact rollout files in the table above; update only their `threads.rollout_path`, `archived`, and `archived_at` columns. Leave all rows where `source LIKE '{"subagent":%'` and all other columns unchanged.

- [ ] **Step 1: Verify source/destination preconditions**

```powershell
$moves = @(
  @{Id='019fab95-5593-7370-b218-808e12500831'; Source='C:\Users\ieeep\.codex\archived_sessions\rollout-2026-07-29T09-55-18-019fab95-5593-7370-b218-808e12500831.jsonl'; Target='C:\Users\ieeep\.codex\sessions\2026\07\29\rollout-2026-07-29T09-55-18-019fab95-5593-7370-b218-808e12500831.jsonl'}
  @{Id='019e8871-1e27-7a73-8ce0-c74b219a4fab'; Source='C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-02T21-06-20-019e8871-1e27-7a73-8ce0-c74b219a4fab.jsonl'; Target='C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-06-20-019e8871-1e27-7a73-8ce0-c74b219a4fab.jsonl'}
  @{Id='019e886f-f8b3-7863-9a62-5e7b7e3fb091'; Source='C:\Users\ieeep\.codex\archived_sessions\rollout-2026-06-02T21-05-05-019e886f-f8b3-7863-9a62-5e7b7e3fb091.jsonl'; Target='C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-05-05-019e886f-f8b3-7863-9a62-5e7b7e3fb091.jsonl'}
  @{Id='019e451b-36de-7803-8172-aa29d2e438b3'; Source='C:\Users\ieeep\.codex\archived_sessions\rollout-2026-05-20T19-17-51-019e451b-36de-7803-8172-aa29d2e438b3.jsonl'; Target='C:\Users\ieeep\.codex\sessions\2026\05\20\rollout-2026-05-20T19-17-51-019e451b-36de-7803-8172-aa29d2e438b3.jsonl'}
  @{Id='019e4080-0c58-7f30-bc74-9d70a3b32589'; Source='C:\Users\ieeep\.codex\archived_sessions\rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl'; Target='C:\Users\ieeep\.codex\sessions\2026\05\19\rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl'}
)
$bad = @($moves | Where-Object {
    -not (Test-Path -LiteralPath $_.Source) -or
    (Test-Path -LiteralPath $_.Target) -or
    -not (Test-Path -LiteralPath (Split-Path -Parent $_.Target))
})
if ($bad.Count -ne 0) { $bad | Format-Table; throw 'Rollout move precondition failed.' }
```

If the final destination string in the last mapping does not exactly match the source filename, stop and correct it from the SQLite row before proceeding. The intended last filename is `rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl`.

- [ ] **Step 2: Move the five files without overwriting**

```powershell
$moved = @()
try {
    foreach ($item in $moves) {
        Move-Item -LiteralPath $item.Source -Destination $item.Target -ErrorAction Stop
        $moved += $item
    }
} catch {
    foreach ($item in ($moved | Select-Object -Reverse)) {
        Move-Item -LiteralPath $item.Target -Destination $item.Source -ErrorAction SilentlyContinue
    }
    throw
}
```

If this step fails, verify all five sources are restored and stop. Do not update SQLite.

- [ ] **Step 3: Update exactly five parent rows in one transaction**

```powershell
$sql = @'
.bail on
PRAGMA busy_timeout=5000;
BEGIN IMMEDIATE;
UPDATE threads
SET archived=0, archived_at=NULL,
    rollout_path=CASE id
      WHEN '019fab95-5593-7370-b218-808e12500831' THEN 'C:\Users\ieeep\.codex\sessions\2026\07\29\rollout-2026-07-29T09-55-18-019fab95-5593-7370-b218-808e12500831.jsonl'
      WHEN '019e8871-1e27-7a73-8ce0-c74b219a4fab' THEN 'C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-06-20-019e8871-1e27-7a73-8ce0-c74b219a4fab.jsonl'
      WHEN '019e886f-f8b3-7863-9a62-5e7b7e3fb091' THEN 'C:\Users\ieeep\.codex\sessions\2026\06\02\rollout-2026-06-02T21-05-05-019e886f-f8b3-7863-9a62-5e7b7e3fb091.jsonl'
      WHEN '019e451b-36de-7803-8172-aa29d2e438b3' THEN 'C:\Users\ieeep\.codex\sessions\2026\05\20\rollout-2026-05-20T19-17-51-019e451b-36de-7803-8172-aa29d2e438b3.jsonl'
      WHEN '019e4080-0c58-7f30-bc74-9d70a3b32589' THEN 'C:\Users\ieeep\.codex\sessions\2026\05\19\rollout-2026-05-19T21-49-54-019e4080-0c58-7f30-bc74-9d70a3b32589.jsonl'
    END
WHERE source='vscode' AND archived=1 AND id IN (
  '019fab95-5593-7370-b218-808e12500831',
  '019e8871-1e27-7a73-8ce0-c74b219a4fab',
  '019e886f-f8b3-7863-9a62-5e7b7e3fb091',
  '019e451b-36de-7803-8172-aa29d2e438b3',
  '019e4080-0c58-7f30-bc74-9d70a3b32589'
);
SELECT changes() AS changed_rows;
COMMIT;
PRAGMA quick_check;
'@
$sql | & $sqlite -header -column $db
if ($LASTEXITCODE -ne 0) { throw 'Parent metadata update failed.' }
```

Expected: `changed_rows = 5`, then `ok`.

### Task 5: Final Verification and CC Switch A/B Check

**Files:** Read-only verification after the mutation; no further database edits.

- [ ] **Step 1: Verify parent and subagent counts**

```powershell
$sql = @'
PRAGMA quick_check;
SELECT CASE WHEN source='vscode' THEN 'vscode' WHEN source LIKE '{"subagent":%' THEN 'subagent-json' ELSE 'other' END AS source_kind,
       COUNT(*) AS total,
       SUM(CASE WHEN archived=0 THEN 1 ELSE 0 END) AS active,
       SUM(CASE WHEN archived=1 THEN 1 ELSE 0 END) AS archived,
       SUM(CASE WHEN model_provider='custom' THEN 1 ELSE 0 END) AS custom,
       SUM(CASE WHEN model_provider='OpenAI' THEN 1 ELSE 0 END) AS OpenAI
FROM threads
WHERE lower(cwd) LIKE '%localcode%'
GROUP BY source_kind;
'@
$sql | & $sqlite -readonly -header -column $db
```

Expected:

```text
quick_check: ok
source=vscode: total=8, active=8, archived=0, custom=8, OpenAI=0
source=subagent-json: total=26, active=26, archived=0, custom=26, OpenAI=0
```

- [ ] **Step 2: Verify all 34 indexed rollout files exist**

```powershell
$paths = @(& $sqlite -readonly -noheader $db "SELECT rollout_path FROM threads WHERE lower(cwd) LIKE '%localcode%';")
$existing = @($paths | Where-Object { Test-Path -LiteralPath (($_ -replace '^\\\\\?\\','')) })
[pscustomobject]@{ Indexed=$paths.Count; Existing=$existing.Count } | Format-List
if ($paths.Count -ne 34 -or $existing.Count -ne 34) { throw 'Rollout verification is not 34/34.' }
```

- [ ] **Step 3: Verify the config file did not change during recovery**

Run:

```powershell
$hashFile = Join-Path $backup 'config-global-state-sha256.csv'
$expected = Import-Csv -LiteralPath $hashFile | Where-Object { $_.Path -eq (Join-Path $backup 'config.toml') } | Select-Object -ExpandProperty Hash
$actual = (Get-FileHash -Algorithm SHA256 -LiteralPath 'C:\Users\ieeep\.codex\config.toml').Hash
if ($expected -ne $actual) { throw 'CONFIG_CHANGED_DURING_RECOVERY' }
'Config SHA-256 unchanged'
```

A mismatch means a writer changed configuration during the operation; report `CONFIG_CHANGED_DURING_RECOVERY` and do not claim success.

- [ ] **Step 4: User UI acceptance with CC Switch still stopped**

Ask the user to open Codex and confirm these eight parent IDs are visible/readable. The empty `019fab95…` task is expected to contain only its original user message. Record the user's explicit confirmation as `PARENT_UI_ACCEPTED`.

- [ ] **Step 5: Optional CC Switch isolation check after UI acceptance**

Close Codex, record the parent counts above, then start CC Switch only. Do not switch profiles and do not start Claude Code. After one minute, rerun the read-only SQLite query. If any `archived`, `model_provider`, or `rollout_path` value changes, stop CC Switch and report `CC_SWITCH_WRITES_OR_TRIGGERS_METADATA_CHANGE`; use ProcMon to capture whether `cc-switch.exe` opened `state_5.sqlite`. If nothing changes, report `CC_SWITCH_NOT_REPRODUCED_AS_WRITER`.

## Rollback Boundary

Use rollback only if SQLite integrity fails, a transaction changes an unexpected number of rows, a rollout move cannot be reconciled, or the user requests restoration. Keep Codex, Claude Code, and CC Switch closed. Restore `state_5.sqlite`, `state_5.sqlite-wal`, and `state_5.sqlite-shm` together from the timestamped backup. Restore moved rollout files from the backup copy if required. Do not restore `.codex-global-state.json` or `config.toml` unless the user explicitly requests it and the hash comparison proves they were changed by this run.

## Final Report

Report exactly:

```text
Status: SUCCESS | BLOCKED_PILOT_NOT_VISIBLE | CONFIG_CHANGED_DURING_RECOVERY | CC_SWITCH_WRITES_OR_TRIGGERS_METADATA_CHANGE | ROLLED_BACK
Backup path: <actual timestamped path>
Pilot changed rows: <literal SQLite output>
Pilot UI: VISIBLE | NOT_VISIBLE | NOT_RUN
Parent rows changed: <literal SQLite output>
Codex quick_check: <literal output>
Parent totals: total / active / archived / custom / OpenAI
Subagent totals: total / active / archived / custom / OpenAI
Rollouts: indexed / existing
Config hash unchanged: YES | NO
CC Switch A/B result: NOT_RUN | NOT_REPRODUCED_AS_WRITER | WRITES_OR_TRIGGERS_CHANGE
User UI acceptance: CONFIRMED | NOT_CONFIRMED
Git commit: none
```

Do not report `SUCCESS` unless the pilot is visible, exactly five archived parent rows were restored, the database and rollout checks pass, the config hash is unchanged, and the user confirms all eight parent conversations are accessible.
