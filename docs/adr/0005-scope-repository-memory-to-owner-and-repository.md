---
status: accepted
---

# Scope repository Memory to its owner and repository

Default task retrieval uses Memory from the same user and repository across Sessions, with source authorization enforced by Go. Cross-repository use of general experience requires an explicit user selection or promotion rather than automatic inclusion of all personal history. User-wide retrieval was considered for convenience, but repository scoping was chosen to limit irrelevant context and unintended disclosure; reopening an old Session and recalling Memory into an empty new Session must be tested separately.
