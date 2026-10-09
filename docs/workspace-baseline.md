# Working-copy baseline inspection

`/worktree baseline` inspects the CLI's configured repository without invoking
a model or preparing an isolated checkout. It prints the repository identity,
HEAD, baseline checksum and admitted/excluded counts. It prints no file
contents. This is the first implementation slice of
[Issue #4](https://github.com/huadeng408/CodeOps-Agent/issues/4), not completed
Task Workspace preparation.

The existing Go worktree manager uses `os.Root` to read allowed working-copy
files, including uncommitted changes and new files reported by Git. The HEAD
inventory also preserves staged and unstaged deletions as missing entries.
An empty regular file has existence=true and the SHA-256 of empty content;
a deleted file has existence=false and no content checksum. The original
index bytes are hashed, not rewritten. Two matching captures are required.

The baseline contains metadata: paths, existence, size, permissions and
SHA-256. It includes neither file bodies nor an independent mutable balance,
Session store or checkpoint. Preparing and persisting a Task Workspace remains
the responsibility of subsequent Go/Ledger integration.

Known credential locations, key/container files and runtime/generated
directories are excluded and reported. Directory exclusions are aggregated
before source-file counting. Repository skills remain eligible except local
authentication/provider configuration. This path policy is not proof that
arbitrary source files contain no secrets; a copy/admission content audit is
still required before enabling task preparation.

Unsafe relative paths, Windows device/stream/UNC/drive-relative roots, aliases,
symlinks and reparse points are refused. Git metadata must currently be an
in-repository directory. Linked/common/alternate metadata require an explicit
Harness-approved relation and are blocked in this slice. Nested metadata
aliases and configuration includes also refuse inspection.

Git calls reuse the repository's hardened arguments and scrubbed environment,
with fixed metadata and working roots. External `core.excludesFile` is
disabled; repository `.gitignore` and
`.git/info/exclude` remain active. This intentionally prevents machine-local
ignore configuration from changing the admitted inventory.
Git stdout is bounded to 8 MiB; admitted files and exclusion records each
have a 20,000-entry ceiling. File
reads are limited to 8 MiB each and 256 MiB total per capture. Metadata checks
use bounded directory batches, at most 100,000 entries/8 MiB of path names,
and bounded config/index reads. Exceeding a limit refuses the inspection;
it does not silently omit allowed source files.

These controls and checks do not establish an atomic filesystem snapshot or
complete race-resistant task preparation. Go must still freeze/recheck the
baseline while creating the checkout, retain failures, persist lease/intent
and outcomes through Ledger CAS, and verify recovery through the real product.
The current baseline command changes neither source files nor the index;
normal CLI startup still creates its own configured Session/runtime files.
Keep those runtime paths outside the source inventory or ignored by Git.

Public regression boundaries: `worktree.Manager.CaptureBaseline` and the CLI
`App.Run` command. Narrow tests use real Git repositories, SQLite CLI startup,
Windows junctions, oversized synthetic index inventories and process isolation;
they make zero model calls. The three product UI previews use sample data and
remain outside `frontend/` pending the user's choice. #4 stays open until its
copy, permission, lease, restart and browser criteria have fresh evidence.
