# code-agent

Local code agent prototype built around a Go harness and a Python orchestrator.

## Architecture

- Go harness: CLI loop, permission checks, hooks, sessions, tools, undo, worktrees, MCP lifecycle, metrics.
- Python orchestrator: LLM calls, prompt/context assembly, planning/todos, skills, memory, compaction, recovery, sub-agent coordination.
- gRPC stream: typed boundary between the harness that executes actions and the orchestrator that decides what to ask for.

## Quick start

```bash
# Run tests
go test ./...
pytest -q

# Start CLI
go run ./cmd/agent
```

The CLI auto-starts the Python orchestrator by default using:

```bash
python -m orchestrator.server
```

Configuration is loaded from `.agent/settings.json` and `.agent/settings.local.json`.

## Useful slash commands

- `/help` — show command help.
- `/plan` — toggle planning mode.
- `/compact` — compact the current session history.
- `/resume [session-id]` — resume persisted session state.
- `/sessions [limit]` — list recent saved sessions.
- `/memory ...` — manage durable markdown memories.
- `/tasks` — show current todos.
- `/undo` — revert the last recorded file change.
- `/diff` — show current git diff summary.
- `/worktree <list|create|switch|cleanup>` — manage local worktrees.
- `/review`, `/security-review`, `/init` — invoke built-in skills.

## Implemented tools

- `Read`: text with line numbers/ranges, basic image data URI output, PDF metadata placeholder.
- `Write`: create/overwrite workspace files with undo records.
- `Edit`: exact replacement, uniqueness checks, optional `replace_all`.
- `Bash`: shell execution with timeout, persisted working directory, safety analysis.
- `Glob`: workspace glob matching with `**` support.
- `Grep`: regex search with content/files/count modes, context, only-match, glob filter, head limit.
- `Git`: safe git subcommand wrapper with destructive argument blocking.
- `WebFetch`: HTTP fetch with size limits.
- `WebSearch`: DuckDuckGo-compatible JSON search endpoint.

## Generated protobufs

After editing [proto/codeagent/orchestrator.proto](proto/codeagent/orchestrator.proto), regenerate both language bindings:

```bash
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  proto/codeagent/orchestrator.proto
mv proto/codeagent/orchestrator.pb.go gen/codeagentpb/orchestrator.pb.go
mv proto/codeagent/orchestrator_grpc.pb.go gen/codeagentpb/orchestrator_grpc.pb.go
python -m grpc_tools.protoc -Iproto --python_out=. --grpc_python_out=. proto/codeagent/orchestrator.proto
```

## Safety model

The LLM can request tools, but the Go harness owns execution. Permissions combine default levels, allow/deny rules, session approvals, hooks, and command/git safety analysis. Dangerous shell and git operations are blocked before execution.
