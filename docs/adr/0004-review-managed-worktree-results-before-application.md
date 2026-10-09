---
status: accepted
---

# Review managed-worktree results before application

By default, repository coding tasks execute in Go-managed isolated worktrees seeded from the permission-checked current working copy, including allowed uncommitted changes and new files while excluding credentials and generated directories. The task produces a Code Change Proposal for operator approval; Go applies it only after validating the workspace and starting file states, records intent and outcome in the Session Ledger, and retains conflicting or uncertain results for reconciliation. Direct editing was considered for familiarity, but isolation was chosen so cancellation and recovery can preserve unrelated user work; partial-failure behavior must be verified before claiming safe recovery.
