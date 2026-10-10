# Working-copy baseline and Task Workspace preparation

The local-core browser supports owner-scoped preparation from one
host-approved repository. The selected A layout shows a compact right card
with the repository, baseline, opaque isolation ID and preparation result;
file metadata expands on demand. Preparation creates a copy, not an executing
Agent. Provider, sandbox, budget and execution admission remain separate.

Configure `CODE_AGENT_REPOSITORY_ROOT` and `CODE_AGENT_TASK_WORKSPACE_ROOT`
together, or set `harness.repository_root` and `harness.task_workspace_root`
in the server configuration. Both must be absolute, existing local paths;
storage must be outside the repository and cannot be its ancestor. Browser
paths do not grant repository permissions. Approval uses the persisted local
identity, including first setup and restart, rather than a fixed user ID.
Full-stack preparation remains unavailable until its repository grants are wired.

`GET /api/v1/sessions/:id/task-workspace` reads the owner-scoped projection.
`POST /api/v1/sessions/:id/task-workspace/prepare` accepts `expectedSeq` and
`requestId`; roots and shell commands are never accepted by this endpoint.
The existing Session Ledger stores a CAS preparation intent, retained lease,
baseline and outcome. Repeated requests reuse the same result. Other requests
cannot overwrite a retained workspace. An interrupted intent is `unknown`,
failed or changed copies are `blocked`, and expired leases remain retained.
None of these paths starts execution or silently deletes data.

Go captures permitted working-copy files with `os.Root`, including dirty,
new, empty and deleted states. The inventory combines HEAD with tracked and
allowed untracked paths; empty and missing files differ. It records existence,
permissions, SHA-256 and the original index digest, with no file bodies in the
Ledger or HTTP metadata. Two matching captures are required. Source is
rechecked while copying and afterwards; the original working files and index
are not rewritten. Git registration adds its own administration metadata.

Preparation reads Git through a captured metadata inventory and an `os.Root`
read-only filesystem. It reuses go-git reference/index/tree/delta codecs and
Go regexp's linear matching for Git ignore rules; no native Git process, hook, include or network request runs
on this path. Go creates the locked detached worktree registration and HEAD
index through rooted exclusive writes, then copies current files. The new private directories reuse the local
identity ACL policy. Recovery checks the recorded files, modes, absence states,
inventory, Git link/backlink, commit, lock and prepared index digest.

Known credentials, local provider/auth configuration and runtime/generated
paths are excluded. Recognizable key/private-key content refuses preparation.
This is not a universal secret detector and does not sanitize shared Git
history. Keep credentials outside source; later sandbox/tool admission must
also enforce metadata and workspace access.

The baseline refuses unsafe relative paths, Windows devices/ADS/UNC and
drive-relative roots, symlink/reparse aliases, unapproved linked/common/alternate
metadata and config includes. External `core.excludesFile` is disabled; repository
ignore rules remain active, including repository `core.ignorecase`, an enabled
`config.worktree` override, nested rules, negation, escapes and `**`. UTF-8
wildcards use Git's byte semantics. Global excludes remain outside this contract.
Baseline inspection rejects partial/promisor metadata; missing objects fail
closed. Legacy native Git callers retain the shared environment's lazy-fetch
refusal. Dependency preparation requires its own approved stage.

Limits remain 8 MiB per admitted file, 256 MiB total, 20,000 source files and
20,000 exclusions. Git/source inventories each allow at most 100,000 entries
and 8 MiB of path names. Non-pack metadata files allow 16 MiB; pack indexes
allow 64 MiB in aggregate. Loose headers allow 128 bytes, required objects and
delta outputs 8 MiB each, delta depth 64, and aggregate decode reservations
64 MiB per reader. Required objects are checksum-verified; unrelated historical
blobs are not inflated. Version 2/3/4 index paths are preflighted before
decoding, including reconstructed version 4 names, within 8 MiB total.
Ignore rules allow 4 KiB each, 8 MiB of pattern text and 20,000 rules across
the capture; immutable scopes share parent rules rather than copy them.
Unsupported patterns or exceeded limits refuse admission without partial
results. The current codec accepts SHA-1 repositories; SHA-256 repositories
are explicitly refused until a compatible bounded codec is added. Go 1.26+
is required by the pinned go-git version. The CLI's read-only
`/worktree baseline` command remains available without preparing a copy.

Windows pins named roots, existing metadata directories and read-only files
while capturing metadata and copying, then revalidates after acquiring pins. Public tests
check directory/file replacement, aliases, filters and forbidden lazy fetch.
The captured Git namespace excludes entries added afterwards; absolute paths,
chroot escapes and filesystem writes are refused by the reader. Existing-file
pins remain necessary, and source/copy races still require revalidation. This
slice removes the native reader that could see newly inserted optional metadata;
it does not grant execution. New product preparation on other platforms fails
closed until their metadata pin contract is verified. Linux baseline,
legacy callers and source CI are retained.

[Issue #4](https://github.com/huadeng408/CodeOps-Agent/issues/4) stays open.
The current implementation and browser receipt prove their named preparation
checks; they do not establish the full race matrix, real coding tasks, complete
Go/Python Trace or the milestone. See
[rooted Git evidence](evidence/workspace-rooted-git-2026-10-10.md),
[preparation evidence](evidence/workspace-preparation-2026-10-10.md) and
[the original baseline evidence](evidence/workspace-baseline-2026-10-10.md).
