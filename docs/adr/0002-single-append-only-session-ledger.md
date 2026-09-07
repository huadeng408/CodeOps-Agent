---
status: accepted
---

# Keep one append-only Session Ledger

Every new Session has one append-only Session Ledger as its sole mutable source of truth; Surface, Checkpoint, fork, rewind, and compaction state are projections from that ledger. Legacy snapshots remain read-only and may be imported once with a source checksum, because dual writes or destructive restore would create competing truths and make recovery and audit results non-deterministic.
