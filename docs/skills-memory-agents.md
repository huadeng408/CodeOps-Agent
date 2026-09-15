# Skills, Memory And Independent Agents

This document describes the local interfaces. Current acceptance status and
run evidence belong to [GOAL.md](GOAL.md), not to this contract.

## Skills

Discovery reads YAML frontmatter only. Instruction bodies and attached text
resources are loaded on demand by `Skill`; scripts are not executed by this
tool. Directory packages use `<root>/<name>/SKILL.md`; flat `<root>/<name>.md`
packages are also accepted.

Root precedence, from highest to lowest, is project `.dsh/skills`, project
`.agents/skills`, legacy project `.agent/skills`, configured directories,
user `.dsh/skills`, user `.agents/skills`, user `.agent/skills`, bundled.
Equal-ranked roots retain their configured order. Failed refreshes retain
the last-good catalog and mark its snapshot incomplete.

Frontmatter includes `name`, `description`, `tools` or `allowed-tools`,
`whenToUse`, `disable-model-invocation`, and `user-invocable`. Model and user
invocation policies are checked separately. Metadata is not permission:
actual effects still require Harness authorization.

`Skill({"name":"inspect"})` loads instructions;
`Skill({"name":"inspect","resource":"references/checks.md"})` reads an
attached resource, capped at 256 KiB. The Go catalog anchors discovery,
lazy bodies and resource-directory resolution at the configured root with
`os.Root`; descendant symlink/junction escapes fail closed. Python's
standalone discovery checks resolved containment before reading metadata
and again for lazy reads. Production resource access remains enforced by Go.

## Memory

All new durable facts use the append-only Session Ledger. `LedgerMemory`
projects completed trajectories and typed experiences; a separate mutable
memory database is not introduced. Source event identity, hash chain,
active branch and checksums are validated before promotion and recall.
An earlier completed turn remains eligible while its source facts remain
active; rewinds, compaction or deletion that remove its sources invalidate it.

Post-task reflection uses `ReflectMemory` with tools disabled. Candidates
must cite admitted source events. Missing providers, unsafe fields and
unknown evidence fail closed, preserving the task's original outcome.
Durable proposals can be adopted after restart without repeating the model
request. Experience identities use `(owner, kind, key)`; revisions are CAS
updates in an owner-scoped Ledger stream. Kinds are `profile`, `preferences`,
`entities`, `events`, `cases`, and `patterns`.

`RecallMemory` accepts `query`, `max_tokens`, optional `kind`, and optional
`detail` (`abstract`, `overview`, `full`). It returns at most five entries
within the estimated token budget, including provenance. The default budget
is 1200 tokens; the maximum is 8000. Retrieval is lexical and token counts
are estimates, not measured provider savings.

`ManageMemory` supports `list`, `read`, `remember`, `forget`, and `retain`.
Mutations use `expected_revision`; TTL is bounded to one year. `forget`
adds a tombstone and automatic reflection cannot resurrect it. Explicit
`remember` can replace it using the current revision. Expired entries are
excluded from recall; neither TTL nor forgetting erases historical Ledger
evidence.

The CLI `/memory add|list|find|show|delete` interface is a one-way adapter to
Ledger memory. `/memory import` explicitly imports validated legacy Markdown
without writing back to it. Construction does not silently import legacy
files. CLI actor namespaces are distinct from web-user memory namespaces.

## Independent Agents

The local `agent.v2` contract has four primary objects:

| Object | Contract |
| --- | --- |
| `AgentCard` | Capability ID, description, capabilities, local address, transports, authentication requirements |
| `AgentMessage` | Message ID, role, parts containing text, a pinned file, or `data_json` |
| `AgentTask` | Parent/child Session IDs, status, revision, messages, pending human approvals and artifacts |
| `AgentArtifact` | Delivery ID, name, parts and checksum; file/patch delivery does not apply changes to the parent |

Cards advertise `harness://agents/<kind>` over protobuf/gRPC with an
authenticated Session owner. These are local addresses, not remotely
reachable A2A endpoints. This contract does not claim A2A/OAuth compatibility.

Each task has a separate durable child Session, actor binding, history,
checkpoint, plan, budget and run queue. A child receives only explicit
assignment materials. Parent history, provider transcript, Memory context
and tool approvals are not implicitly copied. Request-local Python runners
may share a provider transport; context independence does not require a
dedicated OS process per child. Memory recall must be explicitly requested
or authorized by the Harness.

`deep`, `explore`, `plan`, `review`, and `security` are read-only capability
profiles. `general` and `background` receive write tools and a retained
managed worktree. An optional `allowed_tools` subset narrows the profile;
it cannot widen permissions. Task communication tools remain available.
Child Shell execution requires a configured sandbox. All tools are still
authorized and audited by Go, even when the model has an allow-list.

The main agent discovers Cards with `AgentTask({"action":"cards"})` and
assigns a child with a `SpawnAgent` tool payload such as:

```json
{
  "kind": "review",
  "title": "Review the parser",
  "objective": "Check parser edge cases and deliver findings",
  "parallel": true,
  "allowed_tools": ["Read", "Glob", "Grep", "Skill"],
  "message": {
    "parts": [
      {"file": {"path": "src/parser.go"}},
      {"data_json": "{\"focus\":\"error handling\"}"}
    ]
  }
}
```

Paths are relative to the supplying workspace, confined, size-bounded and
SHA-256 verified. Message files are pinned under `.agent/materials` and
artifact files under `.agent/artifacts`. File parts are capped at 4 MiB;
each message/artifact has at most 16 parts. A task accepts at most 16
artifacts, including automatic reports; concurrent additions use Ledger
CAS and identical request replays do not consume capacity.

Task status is `submitted`, `working`, `input_required`, `completed`,
`failed`, or `canceled`. `parallel=true` returns without awaiting completion;
`AgentTask` supports `list`, `get`, `wait`, `message`, and `cancel`. A wait
returns the current projection after at most 30 seconds, or earlier on
completion, required input or a pending human tool approval. Child `AskUser`
or `input_required` requests parent input. A parent `message` starts another
turn in that same child Session, preserving only the child's own history.

Children deliver through `PublishArtifact({"artifact": ...})`. Task and
artifact mutation IDs are stable across a retry lineage and distinct across
normal runs. Pinned-file mutation or a conflicting replay is rejected.
Failed external-effect receipts remain failures on replay; only read-only
or Ledger-idempotent operations may retry under the same call ID. A nonzero
Shell/Git/MCP exit code is not proof that no state changed.
Restart recovery resumes durable assignments and queued messages; parent
deletion cancels child tasks. Required human decisions remain pending;
models cannot approve their own or another agent's tools. The CLI human
interface is `/agents list` and `/agents approve|deny <task-id> <run-id>
<tool-call-id>`.

For changes to these contracts, run the corresponding Skills, Memory,
Session, server and CLI tests, then the opt-in process integration tests.
Protocol edits also require regenerated Go/Python protobuf outputs. Report
fixture process evidence separately from provider-backed runtime evidence.
