# Quiet Operator CLI Design

**Status:** Approved  
**Date:** 2026-08-12  
**Release target:** V0.1  
**Scope:** `internal/cli` presentation and interaction ergonomics

## 1. Purpose

Modernize the existing Go CLI so long agent sessions remain calm, legible, and
auditable. The result should feel comparable to current coding-agent CLIs while
preserving this repository's scrolling transcript, raw-mode input, harness
boundaries, and low dependency footprint.

The selected visual direction is **Quiet Operator**: restrained decoration,
clear semantic hierarchy, compact successful tool activity, prominent
exceptions and approvals, and stable behavior when color or Unicode is not
available.

## 2. Decisions

- Keep the existing scrolling conversation model. Do not build a full-screen
  TUI for V0.1.
- Keep `App`, `InputBuffer`, the orchestrator protocol, session persistence,
  permissions, and tool execution behavior intact.
- Evolve the renderer around semantic events instead of styling arbitrary
  strings at call sites.
- Collapse successful tool calls to one result line. Expand failures,
  permission requests, and important change summaries.
- Use no new rendering framework. In particular, do not add Bubble Tea,
  Lip Gloss, Ink, or Ratatui for this release.
- Treat non-TTY output as an audit log: no cursor movement, erasure, spinners,
  or animation.
- Preserve meaning without color and without Unicode.

## 3. Research Basis

The design was informed by public behavior and source-level inspection of
modern coding-agent CLIs, especially OpenAI Codex CLI and Gemini CLI, plus the
public Claude Code interaction model. The useful patterns are architectural:

- separate composer, approval, execution, diff, and status models;
- render tool lifecycle states explicitly;
- make approvals visually distinct from ordinary transcript content;
- preserve transcript correctness across wrapping and terminal resize;
- test interactive and non-interactive output separately;
- keep color secondary to textual status markers.

Codex's full-screen Ratatui implementation is not copied because it conflicts
with the chosen incremental architecture. External source code is research
evidence, not a dependency or implementation shortcut.

## 4. Experience Contract

### 4.1 Bootstrap

The first screen contains a compact session header:

```text
code-agent v0.1                     main | D:\vscode\localcode
chat | deepseek-v4-pro | /help
```

TTY output may use subtle bold/dim styling and a Unicode separator. Plain
output uses ASCII only. Detailed configuration remains available through
commands rather than occupying the first viewport.

### 4.2 User Composer

The idle prompt is visually stable and uses a single semantic prompt marker:

```text
> Ask, fix, or /command
```

In capable terminals the marker may use an accent treatment. The marker stays
ASCII so pasted transcripts and fallback output remain identical.
The composer keeps current history, cursor movement, completion, and interrupt
behavior. This release does not add multi-line editing or a fixed bottom pane.

### 4.3 Assistant Output

Streaming assistant text begins under a short assistant marker or a thin left
rail. It is not enclosed in a box. Deltas append without repainting prior
transcript content. Closing a response emits a newline and the compact status
line, not a full-width decorative separator.

### 4.4 Tool Activity

Tool progress is represented as a semantic lifecycle:

```text
* Test internal/rag | running
[ok] Test internal/rag | 18 passed | 1.4s
```

TTY mode may update the running row in place when no assistant stream is
currently writing. A successful completion replaces the active row with a
single summary. A failed completion is retained and followed by bounded error
detail:

```text
[fail] Test internal/rag | exit 1 | 2.1s
  FAIL TestSpanClosesOnError
  expected span.End(), got active span
  internal/rag/trace_test.go:84
```

When safe in-place replacement cannot be guaranteed, the renderer appends a
new completion row instead. Transcript correctness has priority over animation.
Non-TTY mode always appends one record for each state transition.

Tool summaries are derived from structured fields already available on the
event or result. The UI must not infer safety, success, or file changes from
color or human-facing prose.

### 4.5 Permissions

Permission requests use the strongest visual hierarchy in the CLI. They show:

- the tool;
- why approval is required;
- the relevant command, target, or bounded parameter summary;
- explicit choices: Allow once, Allow for session when supported, and Deny;
- denial as the default for empty or unrecognized input.

Example:

```text
Permission required
Shell changes repository state

  git commit -m "fix: close rag spans"

[1] Allow once  [2] Allow session  [Esc] Deny
```

The visual redesign does not broaden permission policy or introduce a global
allow-all option.

### 4.6 AskUser

AskUser prompts use the same question/choice grammar as permissions but a
neutral accent. Options keep their number, label, description, and bounded
preview. Existing free-form and multi-select behavior remains unchanged.

### 4.7 Diff and Change Summaries

`/diff` leads with a compact file and line summary when those facts are
available:

```text
Changes | 4 files | +82 -17
M internal/cli/renderer.go
A internal/cli/theme.go
```

Relevant existing diff content remains available below the summary. V0.1 does
not add syntax highlighting or an interactive fold browser. Long content uses
the shared wrapping and truncation rules and must announce truncation.

### 4.8 Status Line

The post-turn status line is ordered by operational importance:

1. mode and active error state;
2. context/token usage;
3. cost;
4. tool and turn counts;
5. keyboard or command hint.

Fields are removed from the right as width decreases. At narrow widths the
status may wrap into two lines, but no field may be cut mid-token. The status
line never relies on negative letter spacing or viewport-scaled font behavior.

## 5. Renderer Architecture

### 5.1 Terminal Capabilities

Introduce a small immutable capability value created with the renderer:

```go
type TerminalCapabilities struct {
    Interactive bool
    Color       bool
    Unicode     bool
    Width       int
}
```

Capabilities may be injected in tests. Production detection follows these
rules:

- `NO_COLOR` or `CLICOLOR=0` disables color;
- `FORCE_COLOR` enables color but never enables cursor movement by itself;
- cursor updates require an interactive terminal writer;
- width comes from the terminal when available, with a conservative fallback;
- Unicode defaults to enabled only for known UTF-8 terminals and can be
  disabled with `CODE_AGENT_ASCII=1`;
- non-TTY output remains plain even if color is forced, except that ANSI color
  may be emitted when the user explicitly sets `FORCE_COLOR`.

`CODE_AGENT_ASCII=1` affects symbols and borders only; it does not change user
or model content.

### 5.2 Theme

Use a semantic theme rather than raw ANSI constants at call sites:

- `Accent`: user marker and commands;
- `Success`: completed work;
- `Pending`: running work and approval attention;
- `Danger`: failures and denials;
- `Muted`: metadata;
- `Strong`: labels and primary text.

The default palette uses standard ANSI colors for broad terminal support.
Color is never the only status signal: `[ok]`, `[fail]`, `Permission required`,
and equivalent text remain present.

### 5.3 Semantic Render Methods

The renderer exposes focused methods for these concepts:

- bootstrap/session header;
- user turn and composer prompt;
- assistant stream start, append, and end;
- tool started and tool completed;
- approval request;
- neutral question/choices;
- section or detail block;
- diff summary;
- status and error.

Existing `PrintLine` and `PrintBlock` may remain as compatibility helpers while
call sites migrate, but new behavior must not encode state in arbitrary title
strings.

### 5.4 Interactive Row Updates

The renderer tracks at most one active ephemeral row for V0.1. It may erase and
replace only that row, while holding the existing renderer mutex. Starting an
assistant stream or printing a durable block first commits the active row to
the transcript. This prevents tool progress and model deltas from corrupting
each other.

Parallel tool events are printed as durable start/completion rows unless a
single active row can be identified unambiguously. V0.1 does not implement an
in-place multi-row activity manager.

## 6. Width, Wrapping, and Text Safety

- Wrapping uses terminal cell width, not rune count, so CJK and wide glyphs do
  not break alignment.
- ANSI escape sequences do not count toward visible width.
- At widths below 60 columns, metadata is shortened before primary content.
- At extremely narrow widths, borders disappear and sections become labeled
  text blocks.
- Long unbroken tokens are hard-wrapped without resizing surrounding layout.
- User/model text is treated as content. Control characters other than
  permitted newlines and tabs are escaped or stripped so output cannot inject
  cursor movement into the renderer.
- All former corrupted box-drawing literals are removed. Unicode literals are
  valid UTF-8 and have ASCII equivalents.

## 7. Error Handling

- Rendering failures must not terminate the agent session when a plain-text
  fallback can be emitted.
- Terminal capability detection failure falls back to non-interactive ASCII at
  the default width.
- Tool failures expose exit code, truncation, and a bounded error/output
  excerpt when present.
- Interrupted turns produce a durable interruption line, clear any ephemeral
  row, and return a clean composer prompt.
- Orchestrator disconnect/restart states use pending/success/error semantics
  and remain visible in scrollback.

## 8. Accessibility and Automation

- `NO_COLOR`, `CLICOLOR`, and `FORCE_COLOR` retain their established meaning.
- Plain output contains no cursor-control sequences.
- Status is expressed in words and symbols, not color alone.
- Non-interactive input/output remains deterministic for scripts and tests.
- No animation is required to understand current state.
- V0.1 does not claim screen-reader certification; it does require a clean
  linear text representation suitable for assistive terminals and logs.

## 9. Test Design

### 9.1 Unit Tests

Use table-driven tests for:

- wide, narrow, and extremely narrow widths;
- color on/off;
- Unicode and ASCII symbols;
- interactive and non-interactive output;
- ANSI-aware and terminal-cell-aware wrapping;
- control-character sanitization;
- status-line field priority;
- assistant stream interleaving with an active tool row;
- tool success collapse and failure expansion;
- permission and AskUser option rendering;
- diff summary and announced truncation;
- interrupt cleanup.

### 9.2 Integration Tests

Add a real pseudo-terminal integration test where the supported test platform
allows it. It must verify:

- a running tool row can be replaced without corrupting adjacent text;
- assistant streaming commits any active ephemeral row first;
- Ctrl+C leaves a durable interruption message and a valid next prompt;
- resize or injected width changes preserve readable wrapping.

Windows Terminal and PowerShell behavior must also be exercised manually with
the built CLI because a Unix-style pseudo-terminal alone cannot validate the
target environment.

### 9.3 Visual Acceptance

Capture evidence from the real CLI for:

- bootstrap and idle composer;
- successful tool lifecycle;
- failed tool lifecycle;
- permission request;
- a narrow terminal;
- `NO_COLOR` or ASCII fallback.

The evidence must come from a running build, not an HTML mockup. Screenshots
are release evidence and remain outside the repository unless a later decision
explicitly adds curated documentation assets.

## 10. Release Gates

Before V0.1 is committed and tagged:

1. Run focused CLI unit and integration tests.
2. Run `go test ./...`.
3. Run the repository's full Python test suite.
4. Re-run the frozen benchmark receipt/report gates without regenerating or
   weakening evidence.
5. Run `git diff --check` and `git diff --cached --check`.
6. Scan staged added lines for credentials and machine-specific paths.
7. Inspect the real CLI in Windows Terminal/PowerShell and capture the required
   states.
8. Obtain an independent code/release review with no unresolved Critical or
   Important findings.
9. Update repository and Obsidian progress records with exact evidence and
   remaining limitations.
10. Commit, create annotated tag `V0.1`, push `main` and the tag without force,
    and verify the remote refs.

## 11. Non-Goals

V0.1 does not include:

- a full-screen TUI or alternate-screen buffer;
- mouse interaction;
- a fixed bottom composer;
- Markdown or source syntax highlighting;
- an interactive transcript/tool fold browser;
- a plugin theme system;
- multi-line composer editing;
- changes to harness evaluation, RAG retrieval, telemetry contracts, or
  orchestrator protocol behavior beyond what is necessary to present existing
  events correctly.

## 12. Acceptance Criteria

The design is implemented when all of the following are true:

- no corrupted box-drawing text is emitted by CLI sources or tests;
- the first viewport is compact and immediately usable;
- assistant output is readable without a decorative panel;
- successful tools occupy one durable result row;
- failed tools and approvals expose the information needed to act safely;
- colorless and ASCII modes preserve the same meaning;
- CJK, long tokens, and narrow widths do not break alignment or overlap text;
- non-TTY output is deterministic and free of cursor-control sequences;
- existing CLI behavior and the full repository test suites pass;
- real Windows terminal evidence matches the approved Quiet Operator design;
- release documentation states that V0.1 is a runnable demo and gate baseline,
  not a claim that the four production workstreams are complete.
