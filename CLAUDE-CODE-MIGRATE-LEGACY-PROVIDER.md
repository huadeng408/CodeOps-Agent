# Codex Legacy Provider Migration Execution Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to execute this plan task-by-task. Also use `superpowers:systematic-debugging` for any unexpected state and `superpowers:verification-before-completion` before reporting success. Steps use checkbox (`- [ ]`) syntax for tracking. Do not parallelize mutation steps.

**Goal:** Make the seven recovered `D:\vscode\localcode` conversations visible in the current Codex Desktop sidebar by migrating only their obsolete `model_provider` metadata from `OpenAI` to the currently configured provider `custom`.

**Architecture:** Preserve the successful first-stage recovery: all eight localcode threads remain active and all rollout JSONL files remain untouched. Prove the provider-filter hypothesis with one recent thread that already has a correct project assignment. Continue with the remaining six rows only after the user confirms that the pilot thread appears in the sidebar. Every mutation happens while Codex Desktop is fully stopped and after a new independent backup.

**Tech Stack:** Windows PowerShell, SQLite (`D:\anaconda\Library\bin\sqlite3.exe`), Codex Desktop local storage.

---

## Operator Contract

Read the complete document before executing commands. Work from `D:\vscode\localcode` in a new VSCode Claude Code window.

The user authorizes changes only to the `model_provider` column of the seven exact thread IDs listed in this document. Do not repeat the first-stage archive recovery.

Hard rules:

- Do not move, copy, rename, truncate, or rewrite rollout JSONL files.
- Do not modify `archived`, `archived_at`, `rollout_path`, `cwd`, timestamps, recency, title, preview, model, or conversation content.
- Do not modify `C:\Users\ieeep\.codex\.codex-global-state.json` or `session_index.jsonl`.
- Do not edit `C:\Users\ieeep\.codex\config.toml` or add provider aliases during this run.
- Do not kill Codex automatically. If any Codex process is running before an offline step, ask the user to close Codex and wait.
- Do not proceed from the pilot to the batch until the user explicitly confirms that the pilot thread is visible in the sidebar.
- Do not run the batch after a failed pilot.
- Never use recursive deletion or database rebuild commands.
- Keep all pre-existing repository changes. Do not commit recovery artifacts.

## Verified Starting Evidence

The following was verified read-only after the first-stage recovery:

| Layer | Verified result |
|---|---|
| Codex SQLite | `PRAGMA quick_check = ok` |
| Localcode thread state | 8 total, 8 active, 0 archived |
| Rollout storage | 8 indexed paths, 8 files present |
| Direct task reads | All 7 missing tasks readable through the official Codex thread reader |
| Backup | `D:\vscode\recovery-backups\localcode-codex-20260729-before-recovery` exists |
| Sidebar/list service | Only 3 tasks returned globally |
| Provider correlation | The 3 listed tasks are exactly the 3 active `custom` rows; all 7 missing localcode tasks use `OpenAI` |

Current localcode provider distribution must be:

```text
custom: 1
OpenAI: 7
```

The current Codex configuration selects:

```toml
model_provider = "custom"
```

## Exact Migration Set

Pilot thread, migrated first and alone:

| Thread ID | Current provider | Required provider | Reason for pilot selection |
|---|---|---|---|
| `019fab95-5593-7370-b218-808e12500831` | `OpenAI` | `custom` | Recent task; rollout exists; already has the correct `localcode` project assignment; currently absent from sidebar |

Batch threads, migrated only after successful pilot acceptance:

| Thread ID | Recognizable title | Current provider | Required provider |
|---|---|---|---|
| `019fab98-710c-7093-ac19-b7c253c900c8` | `改用 MinerU 和 OCR 处理 PDF` | `OpenAI` | `custom` |
| `019eab14-5594-7171-a399-0dd74525ab8c` | `重写 README 概述功能特点` | `OpenAI` | `custom` |
| `019e8871-1e27-7a73-8ce0-c74b219a4fab` | `将agent上下文窗口设为256k` | `OpenAI` | `custom` |
| `019e886f-f8b3-7863-9a62-5e7b7e3fb091` | `设置上下文窗口为256k` | `OpenAI` | `custom` |
| `019e451b-36de-7803-8172-aa29d2e438b3` | `按设计方案实现代码落库` | `OpenAI` | `custom` |
| `019e4080-0c58-7f30-bc74-9d70a3b32589` | `搭建项目骨架` | `OpenAI` | `custom` |

The existing current conversation `019facf3-998b-79f2-a253-1b6596fa6952` is already `custom` and must not be updated.

---

### Task 1: Reproduce and Verify the Provider-Filter Baseline

**Files:**

- Read: `C:\Users\ieeep\.codex\state_5.sqlite`
- Read: `C:\Users\ieeep\.codex\config.toml`
- Modify: none

- [ ] **Step 1: Verify the current Codex provider without exposing credentials**

Run:

```powershell
rg -n "^(model_provider|model)\s*=|^\[model_providers\." 'C:\Users\ieeep\.codex\config.toml'
```

Expected first provider line:

```text
model_provider = "custom"
```

Do not print authentication files or API keys.

- [ ] **Step 2: Verify SQLite integrity and the exact localcode state**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'

& $sqlite -readonly -header -column $db `
  'PRAGMA quick_check;' `
  "SELECT COUNT(*) AS localcode_threads,
          SUM(CASE WHEN archived=0 THEN 1 ELSE 0 END) AS active,
          SUM(CASE WHEN archived=1 THEN 1 ELSE 0 END) AS archived,
          SUM(CASE WHEN model_provider='OpenAI' THEN 1 ELSE 0 END) AS legacy_provider,
          SUM(CASE WHEN model_provider='custom' THEN 1 ELSE 0 END) AS current_provider
     FROM threads
    WHERE lower(cwd) LIKE '%localcode%';"

if ($LASTEXITCODE -ne 0) { throw 'Baseline SQLite query failed.' }
```

Expected:

```text
quick_check: ok
localcode_threads: 8
active: 8
archived: 0
legacy_provider: 7
current_provider: 1
```

If any value differs, stop without mutation and report the actual values.

- [ ] **Step 3: Verify the seven exact target rows**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'

& $sqlite -readonly -header -column $db @"
SELECT id, archived, model_provider, model, rollout_path
FROM threads
WHERE id IN (
  '019fab95-5593-7370-b218-808e12500831',
  '019fab98-710c-7093-ac19-b7c253c900c8',
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

Expected: exactly 7 rows; every row has `archived=0`, `model_provider=OpenAI`, and a rollout path under `C:\Users\ieeep\.codex\sessions`.

- [ ] **Step 4: Verify every indexed localcode rollout still exists**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'
$paths = & $sqlite -readonly -noheader $db `
  "SELECT rollout_path FROM threads WHERE lower(cwd) LIKE '%localcode%' ORDER BY id;"

$existing = @($paths | Where-Object { Test-Path -LiteralPath $_ })
[pscustomobject]@{ Indexed = $paths.Count; Existing = $existing.Count } | Format-List

if ($paths.Count -ne 8 -or $existing.Count -ne 8) {
    throw 'Rollout baseline is not 8/8. Stop without mutation.'
}
```

Expected: `Indexed=8`, `Existing=8`.

---

### Task 2: Stop Codex and Create a Second Independent Backup

**Files:**

- Create: `D:\vscode\recovery-backups\localcode-codex-provider-migration-20260729\`
- Copy from: `C:\Users\ieeep\.codex\state_5.sqlite`
- Copy from: `C:\Users\ieeep\.codex\state_5.sqlite-wal`
- Copy from: `C:\Users\ieeep\.codex\state_5.sqlite-shm`
- Copy from: `C:\Users\ieeep\.codex\config.toml`

- [ ] **Step 1: Ask the user to close Codex Desktop, then verify no Codex process remains**

Run only after the user says Codex is closed:

```powershell
$codexProcesses = @(Get-Process -ErrorAction SilentlyContinue | Where-Object {
    $_.ProcessName -like '*Codex*' -or
    ($_.Path -and $_.Path -like '*OpenAI.Codex*')
})

if ($codexProcesses.Count -gt 0) {
    $codexProcesses | Select-Object ProcessName, Id, Path | Format-Table -AutoSize
    throw 'Codex is still running. Do not terminate it automatically.'
}

'Codex offline check: clear'
```

Expected: `Codex offline check: clear`.

- [ ] **Step 2: Create the backup without overwriting an existing directory**

Run:

```powershell
$backup = 'D:\vscode\recovery-backups\localcode-codex-provider-migration-20260729'
$codexRoot = 'C:\Users\ieeep\.codex'

if (Test-Path -LiteralPath $backup) {
    throw "Backup already exists; do not overwrite it: $backup"
}

New-Item -ItemType Directory -Path $backup -Force | Out-Null

foreach ($name in @('state_5.sqlite', 'state_5.sqlite-wal', 'state_5.sqlite-shm', 'config.toml')) {
    $source = Join-Path $codexRoot $name
    if (Test-Path -LiteralPath $source) {
        Copy-Item -LiteralPath $source -Destination $backup
    }
}

$hashRows = Get-ChildItem -LiteralPath $backup -File | Sort-Object Name | ForEach-Object {
    $hash = Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256
    [pscustomobject]@{ Name=$_.Name; Length=$_.Length; SHA256=$hash.Hash }
}
$hashRows | Export-Csv -LiteralPath (Join-Path $backup 'sha256-manifest.csv') -NoTypeInformation -Encoding UTF8
$hashRows | Format-Table -AutoSize
```

Expected: `state_5.sqlite` and `config.toml` are backed up; WAL/SHM are included when present; every copied file has a SHA-256 value.

- [ ] **Step 3: Verify the backup database independently**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$backupDb = 'D:\vscode\recovery-backups\localcode-codex-provider-migration-20260729\state_5.sqlite'

& $sqlite -readonly $backupDb 'PRAGMA quick_check;'
if ($LASTEXITCODE -ne 0) { throw 'Backup database verification failed.' }
```

Expected: `ok`.

---

### Task 3: Run the One-Thread Pilot Migration

**Files:**

- Modify: `C:\Users\ieeep\.codex\state_5.sqlite`
- Modify columns: `threads.model_provider` for one exact ID only

- [ ] **Step 1: Recheck the pilot row immediately before mutation**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'

& $sqlite -readonly -header -column $db `
  "SELECT id, archived, model_provider, rollout_path
     FROM threads
    WHERE id='019fab95-5593-7370-b218-808e12500831';"
```

Expected: one row with `archived=0`, `model_provider=OpenAI`, and an existing active rollout path.

- [ ] **Step 2: Change only the pilot provider**

Run exactly once:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'

$sql = @'
.bail on
PRAGMA busy_timeout=5000;
BEGIN IMMEDIATE;
UPDATE threads
SET model_provider='custom'
WHERE id='019fab95-5593-7370-b218-808e12500831'
  AND archived=0
  AND model_provider='OpenAI';
SELECT changes() AS changed_rows;
COMMIT;
PRAGMA quick_check;
'@

$output = $sql | & $sqlite -header -column $db
$output
if ($LASTEXITCODE -ne 0) { throw 'Pilot provider migration failed.' }
```

Expected: `changed_rows=1`, followed by `ok`.

- [ ] **Step 3: Verify only one localcode row changed provider**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'

& $sqlite -readonly -header -column $db `
  'PRAGMA quick_check;' `
  "SELECT COUNT(*) AS localcode_threads,
          SUM(CASE WHEN archived=0 THEN 1 ELSE 0 END) AS active,
          SUM(CASE WHEN archived=1 THEN 1 ELSE 0 END) AS archived,
          SUM(CASE WHEN model_provider='OpenAI' THEN 1 ELSE 0 END) AS legacy_provider,
          SUM(CASE WHEN model_provider='custom' THEN 1 ELSE 0 END) AS current_provider
     FROM threads
    WHERE lower(cwd) LIKE '%localcode%';" `
  "SELECT id, model_provider
     FROM threads
    WHERE id='019fab95-5593-7370-b218-808e12500831';"
```

Expected:

```text
quick_check: ok
localcode_threads: 8
active: 8
archived: 0
legacy_provider: 6
current_provider: 2
pilot model_provider: custom
```

---

### Task 4: Pause for Pilot UI Acceptance

**Files:**

- Read through Codex UI: pilot task
- Modify: none beyond normal Codex startup/index loading

- [ ] **Step 1: Ask the user to launch Codex Desktop normally**

Tell the user the offline pilot migration passed and ask them to start Codex. Do not launch it silently.

- [ ] **Step 2: Ask the user to check the localcode sidebar for the pilot**

The pilot is:

```text
Thread ID: 019fab95-5593-7370-b218-808e12500831
Recognizable title: --基于D:\Obsidian\code-autogrowth\私人\localcode\HANDOFF-2026-0…
```

Stop and wait for an explicit user answer.

- [ ] **Step 3A: If the pilot appears, record success and continue only after Codex is closed again**

Record:

```text
PILOT_VISIBLE
```

Ask the user to close Codex again. Re-run the process check from Task 2 Step 1. Continue to Task 5 only when the offline check is clear.

- [ ] **Step 3B: If the pilot does not appear, roll back the pilot and stop**

Ask the user to close Codex, verify it is offline, then run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'

$sql = @'
.bail on
PRAGMA busy_timeout=5000;
BEGIN IMMEDIATE;
UPDATE threads
SET model_provider='OpenAI'
WHERE id='019fab95-5593-7370-b218-808e12500831'
  AND archived=0
  AND model_provider='custom';
SELECT changes() AS changed_rows;
COMMIT;
PRAGMA quick_check;
'@

$output = $sql | & $sqlite -header -column $db
$output
if ($LASTEXITCODE -ne 0) { throw 'Pilot rollback failed.' }
```

Expected: `changed_rows=1`, `quick_check=ok`, and provider distribution returns to `OpenAI=7`, `custom=1` for localcode.

Report `BLOCKED_PROVIDER_HYPOTHESIS_REJECTED`. Do not execute Task 5 and do not edit global state or timestamps.

---

### Task 5: Migrate the Remaining Six Threads After a Successful Pilot

**Files:**

- Modify: `C:\Users\ieeep\.codex\state_5.sqlite`
- Modify columns: `threads.model_provider` for six exact IDs only

- [ ] **Step 1: Verify the exact batch precondition while Codex is offline**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'

& $sqlite -readonly -header -column $db @"
SELECT COUNT(*) AS eligible_batch_rows
FROM threads
WHERE archived=0
  AND model_provider='OpenAI'
  AND id IN (
    '019fab98-710c-7093-ac19-b7c253c900c8',
    '019eab14-5594-7171-a399-0dd74525ab8c',
    '019e8871-1e27-7a73-8ce0-c74b219a4fab',
    '019e886f-f8b3-7863-9a62-5e7b7e3fb091',
    '019e451b-36de-7803-8172-aa29d2e438b3',
    '019e4080-0c58-7f30-bc74-9d70a3b32589'
  );
"@
```

Expected: `eligible_batch_rows=6`. If not exactly 6, stop without running the update.

- [ ] **Step 2: Migrate the six exact rows**

Run exactly once:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'

$sql = @'
.bail on
PRAGMA busy_timeout=5000;
BEGIN IMMEDIATE;
UPDATE threads
SET model_provider='custom'
WHERE archived=0
  AND model_provider='OpenAI'
  AND id IN (
    '019fab98-710c-7093-ac19-b7c253c900c8',
    '019eab14-5594-7171-a399-0dd74525ab8c',
    '019e8871-1e27-7a73-8ce0-c74b219a4fab',
    '019e886f-f8b3-7863-9a62-5e7b7e3fb091',
    '019e451b-36de-7803-8172-aa29d2e438b3',
    '019e4080-0c58-7f30-bc74-9d70a3b32589'
  );
SELECT changes() AS changed_rows;
COMMIT;
PRAGMA quick_check;
'@

$output = $sql | & $sqlite -header -column $db
$output
if ($LASTEXITCODE -ne 0) { throw 'Batch provider migration failed.' }
```

Expected: `changed_rows=6`, followed by `ok`.

- [ ] **Step 3: Verify final database and rollout state**

Run:

```powershell
$sqlite = 'D:\anaconda\Library\bin\sqlite3.exe'
$db = 'C:\Users\ieeep\.codex\state_5.sqlite'

& $sqlite -readonly -header -column $db `
  'PRAGMA quick_check;' `
  "SELECT COUNT(*) AS localcode_threads,
          SUM(CASE WHEN archived=0 THEN 1 ELSE 0 END) AS active,
          SUM(CASE WHEN archived=1 THEN 1 ELSE 0 END) AS archived,
          SUM(CASE WHEN model_provider='OpenAI' THEN 1 ELSE 0 END) AS legacy_provider,
          SUM(CASE WHEN model_provider='custom' THEN 1 ELSE 0 END) AS current_provider
     FROM threads
    WHERE lower(cwd) LIKE '%localcode%';"

$paths = & $sqlite -readonly -noheader $db `
  "SELECT rollout_path FROM threads WHERE lower(cwd) LIKE '%localcode%' ORDER BY id;"
$existing = @($paths | Where-Object { Test-Path -LiteralPath $_ })
[pscustomobject]@{ Indexed=$paths.Count; Existing=$existing.Count } | Format-List
```

Expected:

```text
quick_check: ok
localcode_threads: 8
active: 8
archived: 0
legacy_provider: 0
current_provider: 8
Indexed: 8
Existing: 8
```

- [ ] **Step 4: Ask the user to reopen Codex and verify all eight localcode tasks**

Expected sidebar/readable set:

```text
019facf3-998b-79f2-a253-1b6596fa6952
019fab98-710c-7093-ac19-b7c253c900c8
019fab95-5593-7370-b218-808e12500831
019eab14-5594-7171-a399-0dd74525ab8c
019e8871-1e27-7a73-8ce0-c74b219a4fab
019e886f-f8b3-7863-9a62-5e7b7e3fb091
019e451b-36de-7803-8172-aa29d2e438b3
019e4080-0c58-7f30-bc74-9d70a3b32589
```

Success requires that all eight appear or can be opened from the localcode task group and their prior turns are readable.

If the pilot remains visible but one or more older batch tasks do not appear, report the exact missing IDs as `PARTIAL_PROJECT_ASSIGNMENT_INDEX`. Do not roll back provider metadata and do not edit `.codex-global-state.json`; the next safe action is official direct navigation/rebinding by thread ID.

---

## Emergency Database Rollback

Use this only if SQLite integrity fails, the update affects an unexpected number of rows, or the user explicitly requests restoration of the pre-migration database. Codex must be closed.

Restore the complete second-stage database set from:

```text
D:\vscode\recovery-backups\localcode-codex-provider-migration-20260729
```

Copy `state_5.sqlite`, `state_5.sqlite-wal`, and `state_5.sqlite-shm` back together. Do not mix files from different backup generations. Re-run `PRAGMA quick_check` and verify `OpenAI=7`, `custom=1` for localcode before reopening Codex.

Do not restore or alter rollout JSONL files because this plan never changes them.

---

## Final Report

Report command evidence in this structure:

```text
Status: SUCCESS, PARTIAL_PROJECT_ASSIGNMENT_INDEX, BLOCKED_PROVIDER_HYPOTHESIS_REJECTED, or ROLLED_BACK
Second-stage backup path: D:\vscode\recovery-backups\localcode-codex-provider-migration-20260729
Pilot changed rows: record the literal SQLite output
Pilot sidebar acceptance: VISIBLE or NOT_VISIBLE
Batch changed rows: record the literal SQLite output, or NOT_RUN
Codex quick_check: record the literal PRAGMA output
Localcode total / active / archived: record all three SELECT values
Localcode OpenAI / custom: record both SELECT values
Indexed rollout files present: record the verified count out of 8
UI verified IDs: list each verified thread ID
UI missing IDs: list each missing thread ID, or NONE
Git commit: none
```

Do not report `SUCCESS` unless the pilot checkpoint passed, the batch affected exactly six rows, database and file verification passed, and the user confirmed all eight tasks are accessible.
