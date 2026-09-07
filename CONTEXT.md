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

**Session Ledger**:
The append-only source of truth for a Session. Earlier facts remain available
for audit even when the current view changes.
_Avoid_: event cache, history table

**Surface**:
The ordered projection of Session facts currently visible to a model or user.
A Surface may replace a range without erasing it from the Session Ledger.
_Avoid_: full history, transcript

**Context**:
The bounded information assembled for one agent step from the current Surface,
repository evidence, instructions, retrieved Memory, and tool definitions.
_Avoid_: prompt, full repository

**Memory**:
Durable, attributable knowledge that can be retrieved across agent steps or
Sessions. A transcript is evidence for Memory, not Memory by itself.
_Avoid_: chat history, cache

**Checkpoint**:
A durable recovery anchor for a known Session state. Restoring a Checkpoint
changes the active Surface without deleting later Session Ledger facts.
_Avoid_: backup, destructive rollback

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
