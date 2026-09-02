package skills

// goalSkills is the stable built-in catalog exposed to the model and CLI.
// Prompts stay short so discovery remains cheap while every entry remains
// directly runnable through the existing Skill executor.
func goalSkills() []Skill {
	return []Skill{
		{Name: "inspect", Description: "inspect repository structure and relevant files", Prompt: "Inspect the repository structure and identify the files relevant to the request.", Tools: []string{"Read", "Glob", "Grep"}},
		{Name: "task-decomposition", Description: "split a complex request into testable work units", Prompt: "Split the request into bounded work units with explicit dependencies, verification, and acceptance evidence.", Tools: []string{"Read", "Write", "Grep"}},
		{Name: "architecture", Description: "map boundaries and dependencies before a change", Prompt: "Map the affected module boundaries, dependencies, and invariants before proposing a change.", Tools: []string{"Read", "Grep"}},
		{Name: "api-design", Description: "design a stable API contract", Prompt: "Design a small, explicit API contract with inputs, outputs, errors, and compatibility notes.", Tools: []string{"Read", "Write", "Grep"}},
		{Name: "data-model", Description: "review persistence schemas and domain models", Prompt: "Review the domain model and persistence schema for missing invariants and migration risks.", Tools: []string{"Read", "Grep", "Git"}},
		{Name: "dependency-audit", Description: "audit dependencies and lockfile provenance", Prompt: "Audit dependencies, versions, licenses, and lockfile provenance for the requested change.", Tools: []string{"Read", "Grep", "Git"}},
		{Name: "debug", Description: "diagnose a reproducible failure", Prompt: "Reproduce the failure, isolate its cause, and propose the smallest verified fix.", Tools: []string{"Read", "Grep", "Bash"}},
		{Name: "performance", Description: "profile a slow path and identify bottlenecks", Prompt: "Measure the slow path, identify the dominant bottleneck, and preserve a before/after baseline.", Tools: []string{"Read", "Bash", "Grep"}},
		{Name: "concurrency", Description: "review concurrent execution and cancellation", Prompt: "Review concurrent execution, cancellation, backpressure, and shared-state safety.", Tools: []string{"Read", "Grep", "Bash"}},
		{Name: "implementation", Description: "implement a bounded repository change", Prompt: "Implement the requested bounded change while preserving existing interfaces and tests.", Tools: []string{"Read", "Write", "Bash"}},
		{Name: "refactor", Description: "refactor without changing observable behavior", Prompt: "Refactor the targeted code for clearer boundaries without changing observable behavior.", Tools: []string{"Read", "Write", "Grep"}},
		{Name: "migration", Description: "plan and execute a backward-compatible migration", Prompt: "Plan and execute a backward-compatible migration with rollback and data-integrity checks.", Tools: []string{"Read", "Write", "Bash", "Git"}},
		{Name: "error-handling", Description: "harden errors and failure semantics", Prompt: "Review error paths and make failures explicit, bounded, actionable, and observable.", Tools: []string{"Read", "Write", "Grep"}},
		{Name: "serialization", Description: "validate structured serialization contracts", Prompt: "Validate serialization schemas, versioning, line endings, and malformed-input behavior.", Tools: []string{"Read", "Grep", "Bash"}},
		{Name: "parser", Description: "review parser boundaries and provenance", Prompt: "Review parser selection, provenance, malformed input handling, and output fidelity.", Tools: []string{"Read", "Grep", "Bash"}},
		{Name: "cli", Description: "improve a command-line workflow", Prompt: "Improve the CLI workflow with explicit options, errors, exit codes, and scripted behavior.", Tools: []string{"Read", "Write", "Bash"}},
		{Name: "tests", Description: "add focused behavior tests", Prompt: "Add focused behavior tests for the requested change, including the relevant edge cases.", Tools: []string{"Read", "Write", "Bash"}},
		{Name: "integration-test", Description: "build an integration test across module boundaries", Prompt: "Build an integration test that exercises the real boundary between the affected modules.", Tools: []string{"Read", "Write", "Bash"}},
		{Name: "regression", Description: "capture a regression with a durable test", Prompt: "Capture the reported regression in a deterministic test before changing implementation code.", Tools: []string{"Read", "Write", "Grep"}},
		{Name: "fuzz", Description: "probe malformed and adversarial inputs", Prompt: "Probe malformed, boundary, and adversarial inputs and preserve any discovered counterexample.", Tools: []string{"Read", "Bash", "Grep"}},
		{Name: "benchmark", Description: "run a fixed-budget performance benchmark", Prompt: "Run a fixed-budget benchmark and report denominator, environment, baseline, and variance.", Tools: []string{"Read", "Bash"}},
		{Name: "lint", Description: "run and fix repository lint checks", Prompt: "Run the repository lint checks, fix only relevant violations, and preserve existing behavior.", Tools: []string{"Read", "Bash", "Write"}},
		{Name: "type-check", Description: "run static type and compile checks", Prompt: "Run static type checks and compilation checks for the affected languages and packages.", Tools: []string{"Read", "Bash"}},
		{Name: "release", Description: "prepare a reproducible release candidate", Prompt: "Prepare a release candidate with pinned inputs, checksums, tests, and a concise change summary.", Tools: []string{"Read", "Git", "Bash"}},
		{Name: "ci", Description: "review continuous integration coverage", Prompt: "Review CI workflows for reproducibility, failure visibility, secret hygiene, and required gates.", Tools: []string{"Read", "Grep", "Git"}},
		{Name: "docker", Description: "diagnose and validate Docker execution", Prompt: "Validate Docker configuration, health checks, resource limits, and safe lifecycle behavior.", Tools: []string{"Read", "Bash", "Grep"}},
		{Name: "wsl", Description: "diagnose Windows and WSL2 integration", Prompt: "Validate Windows/WSL2 path, process, networking, and filesystem boundary behavior.", Tools: []string{"Read", "Bash", "Grep"}},
		{Name: "sandbox", Description: "review sandbox policy and isolation", Prompt: "Review sandbox permissions, resource limits, path boundaries, and fail-closed behavior.", Tools: []string{"Read", "Grep", "Bash"}},
		{Name: "mcp", Description: "review MCP server lifecycle and tool contracts", Prompt: "Review MCP discovery, lifecycle, schemas, timeouts, and untrusted tool output handling.", Tools: []string{"Read", "Grep", "Bash"}},
		{Name: "observability", Description: "add auditable metrics and logs", Prompt: "Add or review observability with stable fields, privacy boundaries, and actionable failure signals.", Tools: []string{"Read", "Write", "Grep"}},
		{Name: "trace", Description: "validate OpenTelemetry trace topology", Prompt: "Validate trace propagation, span parentage, required attributes, and export failure behavior.", Tools: []string{"Read", "Bash", "Grep"}},
		{Name: "security", Description: "inspect security boundaries and secrets", Prompt: "Inspect trust boundaries, authorization, secret handling, injection risks, and fail-closed paths.", Tools: []string{"Read", "Grep", "Bash"}},
		{Name: "secrets", Description: "scan changes without exposing credentials", Prompt: "Scan the exact change set for credential patterns without printing matched values.", Tools: []string{"Git", "Grep", "Bash"}},
		{Name: "incident", Description: "triage a production-like incident", Prompt: "Triage the incident from logs and traces, preserve evidence, and identify a bounded mitigation.", Tools: []string{"Read", "Grep", "Bash"}},
		{Name: "rollback", Description: "plan a reversible rollback", Prompt: "Plan a reversible rollback that preserves data, evidence, and a clear recovery path.", Tools: []string{"Read", "Git", "Bash"}},
		{Name: "git-history", Description: "inspect history and change provenance", Prompt: "Inspect Git history and provenance to distinguish current evidence from stale or aspirational claims.", Tools: []string{"Git", "Read", "Grep"}},
		{Name: "documentation", Description: "write concise repository documentation", Prompt: "Write concise documentation that matches verified behavior and labels blocked or synthetic paths honestly.", Tools: []string{"Read", "Write", "Grep"}},
		{Name: "review", Description: "review changes for correctness and risk", Prompt: "Review the current change set and report concrete correctness, security, and regression risks first.", Tools: []string{"Read", "Git", "Grep"}},
		{Name: "commit", Description: "draft a conventional commit message from the current diff", Prompt: "Given the current diff, draft one conventional-commit message with a concise subject and body.", Tools: []string{"Git"}},
		{Name: "init", Description: "bootstrap project instructions and baseline structure", Prompt: "Initialize the repository skeleton and explain the active structure.", Tools: []string{"Read", "Glob", "Grep"}},
	}
}
