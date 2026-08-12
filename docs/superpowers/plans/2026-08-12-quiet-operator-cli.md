# Quiet Operator CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a calm, deterministic scrolling CLI whose real Windows terminal output satisfies the approved Quiet Operator V0.1 specification.

**Architecture:** Keep `App`, `InputBuffer`, and orchestrator behavior intact. Add injected terminal capabilities and a semantic, mutex-protected renderer that owns text safety, cell-width wrapping, one ephemeral tool row, and compact status layout; migrate only presentation call sites.

**Tech Stack:** Go 1.25, `golang.org/x/term`, `github.com/mattn/go-runewidth`, standard ANSI sequences, existing Go/Python test suites.

---

## File Map

- Create `internal/cli/capabilities.go`: environment and TTY capability detection.
- Create `internal/cli/capabilities_test.go`: deterministic environment/capability tests.
- Create `internal/cli/text.go`: ANSI-aware cell measurement, wrapping, sanitization, and truncation.
- Create `internal/cli/text_test.go`: CJK, ANSI, control-character, and narrow-width tests.
- Create `internal/cli/theme.go`: semantic ANSI styles and Unicode/ASCII symbols.
- Replace `internal/cli/renderer.go`: semantic output API plus compatibility helpers.
- Replace `internal/cli/renderer_test.go`: renderer lifecycle and deterministic transcript tests.
- Modify `internal/cli/statusline.go`: width-aware prioritized status fields.
- Create `internal/cli/statusline_test.go`: wide/narrow/error-priority tests.
- Modify `internal/cli/app.go`: bootstrap, tool, permission, diff, error, and interruption rendering.
- Modify `internal/cli/ask.go`: semantic question rendering.
- Modify `internal/cli/app_permission_test.go`: integration assertions for permission and AskUser grammar.
- Create `internal/cli/renderer_integration_test.go`: real OS pipe transcript and cursor-control assertions.
- Create `cmd/cli-preview/main.go`: deterministic real-renderer states for Windows visual acceptance.
- Modify `go.mod` and `go.sum`: add `go-runewidth` only.
- Modify `docs/PROGRESS-2026-08-12.md` and `D:/Obsidian/code-autogrowth/私人/localcode/PROGRESS-2026-08-12.md`: exact implementation and release evidence.

### Task 1: Terminal capabilities

- [ ] Add failing table tests in `internal/cli/capabilities_test.go` for `NO_COLOR`, `CLICOLOR=0`, `FORCE_COLOR`, `CODE_AGENT_ASCII=1`, non-TTY safety, and width fallback.
- [ ] Run `go test ./internal/cli -run TestDetectTerminalCapabilities -count=1`; expect compile failure because `detectTerminalCapabilities` does not exist.
- [ ] Implement `TerminalCapabilities`, `capabilityEnv`, and `detectTerminalCapabilities(out io.Writer, env capabilityEnv)` in `internal/cli/capabilities.go`. Cursor updates must require `term.IsTerminal(fd)`; `FORCE_COLOR` changes color only; fallback is non-interactive ASCII width 80.
- [ ] Run the focused test and `go test ./internal/cli -count=1`; expect PASS.

### Task 2: Safe terminal text

- [ ] Add failing tests in `internal/cli/text_test.go` proving `cellWidth("中A") == 3`, ANSI sequences have zero width, C0/CSI input is neutralized, long tokens hard-wrap, and every wrapped line stays within the requested cell width.
- [ ] Run `go test ./internal/cli -run 'Test(CellWidth|SanitizeTerminalText|WrapCells)' -count=1`; expect compile failure for missing helpers.
- [ ] Add `github.com/mattn/go-runewidth` and implement `cellWidth`, `stripANSI`, `sanitizeTerminalText`, `wrapCells`, `truncateCells`, and `padCells` in `internal/cli/text.go`. Preserve newline and tab semantics while stripping executable terminal control sequences from content.
- [ ] Run focused and package tests; expect PASS with no warnings.

### Task 3: Semantic renderer lifecycle

- [ ] Replace obsolete panel-shape assertions with failing behavior tests in `internal/cli/renderer_test.go` for compact bootstrap, unboxed assistant streaming, non-TTY tool transition audit rows, interactive success replacement, failure detail expansion, active-row commit before assistant text, ASCII symbols, error/interruption, permission, question, and diff output.
- [ ] Run `go test ./internal/cli -run 'TestStreamRenderer' -count=1`; expect failures against the old boxed renderer.
- [ ] Add semantic `Theme`, `ToolEvent`, `PermissionView`, `QuestionView`, `DiffSummary`, and `BootstrapView` values. Implement `NewStreamRendererWithCapabilities`, `PrintBootstrap`, `StartAssistant`, `AppendAssistantText`, `EndAssistant`, `ToolStarted`, `ToolCompleted`, `PrintPermission`, `PrintQuestion`, `PrintDiff`, `PrintStatus`, `PrintError`, and `PrintInterrupted`.
- [ ] Retain `PrintLine`, `PrintBlock`, `PrintAssistant`, `StartAssistantPanel`, and `EndAssistantPanel` as thin migration helpers, but remove panel borders and corrupted glyphs.
- [ ] Run focused and package tests; expect PASS.

### Task 4: Width-aware status

- [ ] Add failing tests in `internal/cli/statusline_test.go` for error-first ordering, full 100-column output, removal of hints/counts before token/cost fields, and a readable minimal 30-column status.
- [ ] Run `go test ./internal/cli -run TestStatusLine -count=1`; expect failures because `FormatWidth` is missing.
- [ ] Implement `StatusView` and `FormatWidth(snapshot, mode, width)` in `internal/cli/statusline.go`; build atomic fields in priority order and never slice a field mid-token. Keep `Format` as an 80-column compatibility wrapper.
- [ ] Run focused and package tests; expect PASS.

### Task 5: Migrate App presentation

- [ ] Add failing integration assertions to `app_permission_test.go` and focused app tests for exact `Permission required`, `[1] Allow once`, denial-default text, neutral `Question`, compact bootstrap, semantic tool completion, `Changes`, and durable `Interrupted` output.
- [ ] Run `go test ./internal/cli -run 'Test(RenderBootstrap|HandleToolCall|HandleAskUserRequest|HandleInterrupt|HandleOrchestratorEvent|Diff)' -count=1`; expect the new assertions to fail.
- [ ] Modify `app.go` to call the semantic renderer for bootstrap, permission, tool lifecycle, diff, errors, and interruptions. Modify `ask.go` to pass structured options to `PrintQuestion`. Do not change approval policy, tool execution, protocol, or answer normalization.
- [ ] Run focused tests, then `go test ./internal/cli -count=1`; expect PASS.

### Task 6: Explicit integration and preview executable

- [ ] Add failing `renderer_integration_test.go` tests using `os.Pipe` to prove plain output is deterministic and contains neither CSI cursor controls nor corrupted box-drawing bytes across bootstrap, stream, tool, permission, diff, and interruption states.
- [ ] Run `go test ./internal/cli -run TestRendererPipeTranscript -count=1`; expect failure until all semantic paths sanitize and flush correctly.
- [ ] Add `cmd/cli-preview/main.go`, which invokes the real `StreamRenderer` with named scenarios (`all`, `success`, `failure`, `permission`, `narrow`, `ascii`) and no HTML/CSS substitute.
- [ ] Run `go run ./cmd/cli-preview --scenario all`; expect all approved states in a linear transcript. Run `go test ./internal/cli -count=1`; expect PASS.

### Task 7: Repository verification and real Windows evidence

- [ ] Run `gofmt` on changed Go files, then `go test ./internal/cli -count=1` and `go test ./...`; expect PASS.
- [ ] Run the repository's documented full Python suite and frozen benchmark receipt/report gates without regenerating inputs or changing thresholds; record exact commands, pass/fail counts, and any external-service skips.
- [ ] Build `go build -o .tmp/code-agent-v0.1.exe ./cmd/agent` and `go build -o .tmp/cli-preview-v0.1.exe ./cmd/cli-preview`; expect exit 0.
- [ ] Run the real preview executable in Windows Terminal/PowerShell at normal and narrow widths with color and `CODE_AGENT_ASCII=1`; capture bootstrap, success, failure, permission, narrow, and ASCII screenshots outside Git.
- [ ] Run `git diff --check`, `git diff --cached --check`, staged-added-line credential scanning, and inspect `git status --short`; expect no whitespace errors, secrets, or forbidden exploration files staged.

### Task 8: Documentation, review, and V0.1 publication

- [ ] Update repository and Obsidian progress documents with `DESIGNED`, `IMPLEMENTED`, `VERIFIED`, and `BLOCKED` distinctions; include exact commit, commands, screenshot paths, benchmark evidence, remaining four-workstream limitations, and rollback boundary.
- [ ] Request independent code/release review and resolve every Critical or Important finding with a fresh RED/GREEN test.
- [ ] Re-run every release gate from Task 7 after review fixes.
- [ ] Commit only the intended V0.1 files with the required co-author trailer. Do not add the five `eval/swebench_work` exploration scripts or three root `phoenix-v0.1-playwright-mcp*` artifacts.
- [ ] Create annotated tag `V0.1`, push `main` and `V0.1` without force, then verify `git ls-remote origin refs/heads/main refs/tags/V0.1 refs/tags/V0.1^{}` matches local objects.

## Plan Self-Review

- Spec coverage: all approved sections map to Tasks 1-8; protocol and four-workstream changes remain non-goals.
- Placeholder scan: no TODO, TBD, “similar to”, or unbounded error-handling step remains.
- Type consistency: capability, view, renderer, and status names are defined before their App migration use.
- Release honesty: browser mockups are excluded; screenshots must come from the compiled Go renderer; benchmark inputs and thresholds stay frozen.
