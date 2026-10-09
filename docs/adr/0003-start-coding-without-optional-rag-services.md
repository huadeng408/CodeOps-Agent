---
status: accepted
---

# Start repository coding without optional RAG services

The first independent CodeOps-Agent browser product will be a single-user local application listening on loopback, with Go-managed persistent identity, authentication, and the canonical SQLite Session Ledger; RAG, object storage, remote indexes, and their service dependencies are enabled explicitly for the capabilities that need them. The existing HTTP bootstrap couples repository coding to the MySQL/Redis/MinIO stack, so separating capability startup reduces the dependencies needed for the Windows product while preserving the service-backed deployment through adapters. Missing execution prerequisites leave the affected operation `BLOCKED`, while the operator can still open the interface and inspect history; shared-server product support and runtime verification require separate acceptance.
