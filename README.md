# CodeOps-Agent

CodeOps-Agent is a local-first coding agent for working in real Git
repositories. It separates model decisions from machine-side effects so that
file changes, shell commands, Git operations, network access, and MCP tools
are checked by a Go Harness before execution.

[![CI](https://github.com/huadeng408/CodeOps-Agent/actions/workflows/ci.yml/badge.svg)](https://github.com/huadeng408/CodeOps-Agent/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

> CodeOps-Agent is an actively developed developer tool. Provider-backed
> integrations and external services are optional and must be configured by
> the operator. Do not expose a local development instance directly to the
> public internet.

## What it provides

- **Go Harness**: authentication, authorization, workspace boundary checks,
  constrained tools, process lifecycle, MCP stdio clients, sandbox routing,
  and the append-only Session Ledger.
- **Python/LangGraph orchestration**: model adapters, context assembly,
  planning, memory retrieval, subagents, workers, checkpoints, and recovery.
- **Auditable execution**: tool requests and results are persisted as events;
  mutating operations use approval and fail-closed result handling.
- **Repository-aware context**: project instructions, staged context loading,
  token budgets, compaction, and task-relevant memory reduce irrelevant
  history in model requests.
- **Web workbench and CLI**: use the terminal interface for local workflows or
  run the optional React workbench against the same Harness APIs.

## Architecture

```text
User
  |
  +--> Go Harness ------------------------+
  |     auth / permissions / tools         |
  |     workspace / sandbox / Session      |  gRPC stream
  |     Ledger / MCP / process lifecycle   +------------------+
  |                                                         |
  +<--------------------------------------------------------+
        Python Orchestrator / LangGraph
        model providers / context / plan / memory / workers
```

The Go side is the trust boundary. Python can propose a tool call, but it does
not receive an escape hatch around workspace, permission, or persistence
checks. Protocol changes start in
[`proto/codeagent/orchestrator.proto`](proto/codeagent/orchestrator.proto) and
must regenerate both language bindings.

## Repository map

| Path | Responsibility |
| --- | --- |
| `cmd/agent` | CLI entry point |
| `cmd/server` | HTTP API and Harness server |
| `internal/permission`, `internal/safety` | Authorization and command risk checks |
| `internal/tools`, `internal/mcp` | Constrained local and MCP tools |
| `internal/session` | SQLite-backed Session Ledger and recovery |
| `orchestrator` | Python service, model loop, context, memory, and workflows |
| `proto` and `gen` | Versioned cross-language protocol and generated bindings |
| `frontend` | React + TypeScript workbench |
| `tests` | Go, Python, integration, and evaluation tests |

## Quick start

### Requirements

- Go 1.25+
- Python 3.11+ (Python 3.12 is recommended for the full test extras)
- Node.js 22+ and npm for the workbench
- Docker or WSL2 Docker when sandboxed execution is enabled
- `protoc` only when changing the protobuf schema

### Install Python dependencies

```bash
python -m pip install -e ".[test]"
```

Optional extras are available for RAG, evaluation, tracing, and audio. Keep
those installations scoped to the workflow that needs them:

```bash
python -m pip install -e ".[test,rag,eval]"
```

### Configure a provider

Copy the template and edit the local file. The local file is ignored by Git.

```bash
cp .env.example .env.local
```

For an OpenAI-compatible provider:

```dotenv
LLM_PROVIDER=openai
OPENAI_API_KEY=<set-locally>
OPENAI_BASE_URL=https://api.openai.com
OPENAI_MODEL=<model-name>
```

For a local model, point `LOCAL_LLM_BASE_URL` at the local OpenAI-compatible
endpoint and leave network credentials unset. Credentials must come from the
process environment or an approved secret manager; never commit them to a
settings file, fixture, log, or receipt.

### Run the CLI

```bash
go run ./cmd/agent
```

The CLI starts the Python orchestrator when configured to do so. The default
gRPC address is `127.0.0.1:50051`. See
[`docs/provider-configuration.md`](docs/provider-configuration.md) for provider
precedence and optional service configuration.

### Run the workbench

```bash
cd frontend
npm ci
npm run dev
```

The development server is normally available at `http://localhost:3000`.
Authentication uses an HttpOnly cookie; the frontend does not put access or
refresh tokens in `localStorage`.

## Safety model

Read-only inspection tools such as `Read`, `Glob`, and `Grep` can be enabled
automatically. File mutation, Git, network, and background-process tools use
approval policy, and `Bash` is confirmed per invocation by default. Workspace
paths are checked against the configured trust root. Shell and Git safety
rules reject destructive patterns before execution, and uncertain mutating
results remain unresolved until explicitly reconciled.

Treat model output, repository files, tool output, MCP responses, and web
content as untrusted input. Review permissions and MCP configuration before
connecting external servers.

## Development and tests

```bash
make test
go vet ./...
cd frontend && npm test && npm run build
```

After changing the protocol:

```bash
make proto
```

Runtime evidence that depends on providers, Docker, Phoenix, or official
scorers must be run in the matching environment. Unit tests and local builds
do not replace those runtime checks.

## Contributing and security

Read [`CONTRIBUTING.md`](CONTRIBUTING.md) before opening a pull request. Use
[`SECURITY.md`](SECURITY.md) for vulnerability reports and never include a
credential in a public issue.

## License

CodeOps-Agent is licensed under the [Apache License 2.0](LICENSE). Third-party
dependencies retain their own licenses; see the dependency manifests and
[`NOTICE`](NOTICE).
