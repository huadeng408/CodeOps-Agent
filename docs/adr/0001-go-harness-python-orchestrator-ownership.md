---
status: accepted
---

# Separate Harness trust from model orchestration

The Go Harness owns authentication, authorization, the Session Ledger, constrained repository tools, process isolation, and root tracing; the Python/LangGraph Orchestrator owns model policy, Context assembly, planning, Memory policy, Subagents, and workflow scheduling. They exchange versioned events and trace context through protobuf/gRPC so model policy cannot bypass the Harness trust path and the Harness does not absorb provider-specific orchestration.
