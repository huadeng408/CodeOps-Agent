# External Configuration

Public-key trust roots used by the page-Qrels release gate are operator-owned
configuration, not source data. Create a JSON file outside the repository
that follows [`page-qrels-trust-root.schema.json`](page-qrels-trust-root.schema.json).
Set `CODE_AGENT_PAGE_QRELS_TRUST_ROOT` in the release process to the file path.
The release function loads that operator-owned file itself and records its
SHA-256 plus the configured key IDs in the release manifest. It intentionally
does not accept a per-call signer mapping, so a caller cannot self-authorize a
new key.

The file contains Ed25519 public keys only. Keep private keys and API
credentials in the local secret manager; do not copy them into this repository,
logs, receipts, or test fixtures. Multiple key IDs may coexist during a
rotation, while every role must retain at least one configured key.
