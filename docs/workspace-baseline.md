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

Preparation reuses the worktree package's hardened Git arguments, bounded
output and lease IDs. It creates a locked detached worktree without checkout,
initializes its index with `read-tree`, then copies current files through rooted
I/O. Checkout filters do not run. The new private directories reuse the local
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
ignore rules remain active. Implicit lazy fetch is disabled in the shared Git
environment, and baseline inspection rejects partial/promisor metadata before
native reads. Dependency preparation requires its own approved stage.

Limits remain 8 MiB per admitted file, 256 MiB total, 20,000 source files and
20,000 exclusions, bounded Git stdout and metadata traversal. Exceeding a limit
refuses admission rather than silently dropping files. The CLI's read-only
`/worktree baseline` command remains available without preparing a copy.

Windows pins named roots, existing metadata directories and read-only files
through native Git calls, then revalidates after acquiring pins. Public tests
check directory/file replacement, aliases, filters and forbidden lazy fetch.
These pins do not freeze the absence of optional metadata: concurrent creation
of a previously missing alternate/include-related entry remains an unverified
boundary. Post-operation checks can refuse the result but cannot undo a native
read. Complete namespace/race admission remains **BLOCKED**; no execution is
attached to this preparation slice. New product preparation on other platforms
fails closed until their native Git pin contract is verified. Linux baseline,
legacy callers and source CI are retained.

[Issue #4](https://github.com/huadeng408/CodeOps-Agent/issues/4) stays open.
The current implementation and browser receipt prove their named preparation
checks; they do not establish the full race matrix, real coding tasks, complete
Go/Python Trace or the milestone. See
[preparation evidence](evidence/workspace-preparation-2026-10-10.md) and
[the original baseline evidence](evidence/workspace-baseline-2026-10-10.md).
