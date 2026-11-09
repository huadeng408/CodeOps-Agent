# CodeOps-Agent

CodeOps-Agent is an Agent Harness for durable, constrained, and auditable work on
software repositories. This glossary names the concepts shared by its runtime,
orchestration, and operator surfaces.

## Language

**Agent Harness**:
The environment that lets an agent inspect a repository, use constrained tools,
delegate work, recover long tasks, and leave auditable evidence.
_Avoid_: chatbot, wrapper, demo agent

**Session**:
A durable stream of user intent, agent decisions, tool activity, and lifecycle
changes for one coherent outcome.
_Avoid_: chat record, request

**Run**:
One durable execution of a request within a Session. A Run can have multiple
lease attempts; retries remain linked to the original request lineage.
_Avoid_: model call, worker attempt

**Run Projection**:
A derived view of Run lifecycle and retry lineage from the complete Session
Ledger. It is read-only and does not replace or cache the Ledger's facts.
_Avoid_: run record, mutable status store

**Session Ledger**:
The append-only source of truth for a Session. Earlier facts remain available
for audit even when the current view changes.
_Avoid_: event cache, history table

**Surface**:
The ordered projection of Session facts currently visible to a model or user.
A Surface may replace a range without erasing it from the Session Ledger.
_Avoid_: full history, transcript

**Standing Constraint**:
An explicit user rule, prohibition, or revision in a Session that remains binding across Surface replacements until superseded.
_Avoid_: hint, soft preference, ephemeral instruction

**Context**:
The bounded information assembled for one agent step from the current Surface,
repository evidence, instructions, retrieved Memory, and tool definitions.
_Avoid_: prompt, full repository

**Memory**:
Durable, attributable knowledge that can be retrieved across agent steps or
Sessions. A transcript is evidence for Memory, not Memory by itself.
_Avoid_: chat history, cache

**Memory Namespace**:
The isolation scope in which a Memory can be discovered and recalled. It does
not grant access by itself; authorization remains a Harness responsibility.
_Avoid_: folder, tenant permission

**Memory Detail**:
The declared level of detail of one Memory: abstract, overview, or full. It is
a retrieval contract, not a claim that one record was generated from another.
_Avoid_: priority, confidence

**Memory Provenance**:
The source URI, source checksum, and optional Session association that make a
Memory attributable to evidence in the Session Ledger or another approved
source.
_Avoid_: copied transcript, unverified citation

**Frozen Memory View**:
A fixed, attributable selection of prior Memory available to an evaluation
Session. New Session facts remain auditable but do not expand that selection.
_Avoid_: live shared memory

**Checkpoint**:
A durable recovery anchor for a known Session state. Restoring a Checkpoint
changes the active Surface without deleting later Session Ledger facts.
_Avoid_: backup, destructive rollback

**Task Workspace**:
An isolated working copy used for a repository coding task, separate from the
user's working copy and representing the task's starting state and changes.
_Avoid_: scratch folder, Session

**Code Change Proposal**:
A reviewable set of repository changes produced by a coding task relative to
its starting state, awaiting a decision about application to the user's workspace.
_Avoid_: applied change, completed task

**Continuation**:
The act of resuming an interrupted or paused Session from a verified Checkpoint
while preserving every prior Session Ledger fact for recovery and audit.
_Avoid_: retry from scratch, mutable resume flag

**Subagent**:
A child agent assigned a bounded outcome with its own Context, lifecycle,
budget, and result that returns to a parent agent.
_Avoid_: thread pool task, Worker

**Worker**:
A lease-holding executor of scheduled work. A Worker can run deterministic or
agentic work and is not necessarily a Subagent.
_Avoid_: Subagent, process

**Receipt**:
An immutable evidence manifest that identifies what ran, under which pins and
budget, with its complete denominator, result, trace references, and checksums.
_Avoid_: log excerpt, synthetic report

**Verification Receipt**:
A Receipt for a declared engineering verification scope and its observed result.
It does not establish completion of a product task or acceptance of a release.
_Avoid_: Session state, Checkpoint, product acceptance
