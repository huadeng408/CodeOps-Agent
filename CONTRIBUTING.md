# Contributing to CodeOps-Agent

Thanks for helping improve CodeOps-Agent. Contributions should preserve the
trust boundary between the Go Harness and the Python/LangGraph orchestrator.

## Before opening a pull request

1. Read `AGENTS.md` and the relevant design or goal document.
2. Keep credentials, local databases, runtime output, and machine-specific
   paths out of commits.
3. Run the narrow tests for the changed package, then the relevant full suite.
4. Run `git diff --check` and review the exact staged paths.
5. Describe runtime limitations and external dependencies honestly.

## Change boundaries

- Go owns authentication, authorization, the Session Ledger, constrained tools,
  sandboxing, process lifecycle, and the trust boundary.
- Python owns model policy, context, planning, memory, subagents, and workflow
  orchestration. It must not bypass the Go Harness for local side effects.
- Protocol changes start in `proto/codeagent/orchestrator.proto` and regenerate
  both language bindings with `make proto` or `scripts/generate-proto.ps1`.

## Pull requests

Keep pull requests focused. Include the user-visible behavior, tests run, and
any evidence that requires external services. Do not include secrets or copied
provider responses. By submitting a contribution, you agree that it is offered
under the Apache-2.0 license in this repository.
