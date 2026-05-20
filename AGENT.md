# Project Instructions

- Keep the Go harness and Python orchestrator boundaries separate.
- Prefer small, reviewable changes that follow the design document.
- Keep Go code `go test ./...` clean.
- Keep Python modules importable from the repository root.
- Keep `proto/codeagent/orchestrator.proto` and the implementation tree in sync.
- Avoid adding dependencies unless a module boundary already needs them.
