## Project Instructions

- Keep the Go harness and Python orchestrator boundaries separate.
- Prefer small, reviewable changes that follow the design document.
- Keep Go code `go test ./...` clean.
- Keep Python modules importable from the repository root.
- Keep `proto/codeagent/orchestrator.proto` and the implementation tree in sync.
- Avoid adding dependencies unless a module boundary already needs them.

## Waiting and retry discipline

- **Waiting is not token-free.** Every wait/poll tool call starts another model
  turn over the current conversation context, even when the result is empty or
  the input is cached. A long context therefore makes repeated polling very
  expensive without producing progress.
- Call `functions.wait` only after `functions.exec` has returned the exact text
  `Script running with cell ID <id>`, and copy that exact `<id>`. Never invent,
  prefix, increment, guess, or reuse an ID; a completed `functions.exec` call
  has no cell to wait on.
- If `functions.wait` reports that a cell is missing, closed, completed, or not
  found, mark that cell unusable and stop immediately. Do not retry it once,
  and do not substitute a fabricated ID.
- A `functions.wait` response beginning with `Script completed` or
  `Script failed` closes the cell even when its payload contains several
  parallel command results. Consume that payload directly; never call wait on
  the same ID to "confirm" completion or request omitted output.
- Once a cell has returned a terminal result, remove its ID from working
  context. Do not mention it as a candidate for any later wait; use a fresh
  synchronous command or the collaboration namespace for subsequent status.
- Wait for subagents with `collaboration.wait_agent` or inspect them with
  `collaboration.list_agents`; never route subagent waiting through
  `functions.wait`.
- Before calling any wait-like tool, verify both its namespace and argument
  schema. `functions.wait` requires `cell_id`; `collaboration.wait_agent`
  requires only `timeout_ms`. If the intended namespace is uncertain, use
  `collaboration.list_agents` instead of guessing.
- Use one bounded, meaningful wait instead of frequent short polls. While work
  runs, do independent useful work; otherwise wait once and check once only
  after the bounded interval or a state-change notification.
- Every waiting loop must name its exit condition: process completion, new
  output, agent state change, user input, or a documented blocker. If a poll
  returns no state change, do not poll again unless the previously declared
  interval or event condition has occurred.
- After any other identical tool/argument error occurs twice, stop retrying,
  reread the tool contract, identify the root cause, and switch tools before a
  third attempt.
- **Subagent work disables `functions.wait` by default.** When the intended
  state change is a subagent result, use only `collaboration.list_agents` or
  `collaboration.wait_agent`. Do not select a wait tool by name similarity.
  `functions.wait` is allowed only when the immediately preceding `exec`
  returned an exact live cell ID and the commentary update already named that
  process cell's exit condition. If either fact is absent, treat
  `functions.wait` as unavailable for the task.
- A repeated wrong-namespace wait is a decision-path failure, not a transient
  tool failure. After the first occurrence, write down the intended namespace
  and schema before the next wait-like call; after the second, disable that
  tool for the rest of the task and record the recurrence in the progress log.
- If the active runtime nevertheless routes another subagent-status check to
  `functions.wait`, stop all wait/poll calls for the turn. Use an immediate
  agent listing at a natural checkpoint or continue independent work; never
  test the disabled path with placeholder IDs such as `bad`, `wrong`, or
  `disabled`.
- **2026-08-13 recurrence:** cell `660` timed out and was closed, but was then
  incorrectly passed to `functions.wait` twice. After the first `cell not
  found`, the written stop rule was acknowledged but not enforced. Treat this
  as a controller decision failure: once any cell is terminal, add it to a
  per-turn denylist before the next tool selection. After one missing-cell
  response, disable `functions.wait` for the rest of the turn; do not rely on
  narration alone to prevent a second call.
- **2026-08-13 second recurrence:** cell `771` returned `Script completed`, but
  was incorrectly passed to `functions.wait` again while attempting to collect
  output that had already been captured from its redirected log. This confirms
  that a prose-only denylist is insufficient. After any terminal wait result,
  set `functions.wait` unavailable for the entire remaining turn, even for
  other cells; use redirected logs plus process exit codes for terminal jobs,
  and the collaboration namespace for agents. Never call wait to obtain a
  second copy of output already available in a log or terminal result.
- **2026-08-13 third recurrence:** while waiting for an independent reviewer,
  `functions.wait` was repeatedly called without its required `cell_id`. Every
  call failed immediately, yet the same invalid schema was retried many times
  with no state change. This is a hard circuit-breaker event: after the first
  wait schema or namespace error, mark that exact tool unavailable for the rest
  of the turn and perform the next productive action immediately. Agent status
  may only use the collaboration namespace. Do not issue a second wait-like
  call until a valid live target identifier has been obtained from the matching
  namespace; repeated identical validation errors are never transient.

- **2026-08-14 fourth recurrence:** a review-status check incorrectly invoked
  `functions.wait` with a non-live cell id. The resulting `cell not found`
  error is a controller failure, not a transient condition. After the first
  such error, mark `functions.wait` permanently unavailable for the rest of
  the turn, record the incident before resuming work, and use only
  `collaboration.wait_agent` or `collaboration.list_agents` for review and
  subagent state.

- **2026-08-14 fifth recurrence:** an attempted baseline check called
  `functions.wait` with the placeholder id `dummy`, before any live `exec`
  cell existed. This is forbidden even as a tool-routing probe. After this
  error, `functions.wait` was disabled for the rest of the turn; all remaining
  work must use synchronous commands or explicitly returned terminal output.
  Never use a placeholder, guessed, copied, or synthetic cell id.

- **2026-08-14 sixth recurrence:** despite the fifth recurrence's turn-wide
  disablement, a later `functions.wait` was invoked for a long-running Go
  test cell. It consumed 124 seconds and timed out without a usable result.
  This confirms that even a real cell id is not authority to override the
  circuit breaker. After a wait-tool incident, move long commands to a fresh
  turn with output redirected to a log and a bounded native process timeout;
  do not invoke `functions.wait` again under any condition in that turn.

## Skill 优先原则

- **前端 UI/视觉设计优先调用 `frontend-design` skill**，避免千篇一律的 AI 风格界面。
- **代码架构、后端、系统设计优先走 `superpowers` 工作流**（brainstorming → writing-plans → executing-plans → code-review），不跳过规划直接写代码。
- **已有 skill 能覆盖的任务用 skill**，不从头手写 prompt。
- **变更代码前先读相关文件**，保持风格和命名一致。
- **用中文回复**，代码和注释保持项目原有语言。

## 通用规则

- **诚实评测，严禁作弊**：跑 benchmark 或评测时，Agent 只能获得与真实场景一致的输入（如问题描述和代码仓库），不得在 Prompt 中夹带答案、定位提示、修复方向等任何形式的泄题。评测的目的不是「跑通」，是「真实验收能力」——通过作弊手段让数字好看等于自欺欺人，是对面试官和自己的不尊重。
- 涉及多文件修改时，先用 EnterPlanMode 出方案，用户确认后再写代码。
- 能用专用工具（Read/Glob/Grep/Edit/Write）就不用 Shell 命令。
- 提交前跑 `git diff --stat` 确认改动范围。
- **有意义的进展必须同步写入 `D:\Obsidian\code-autogrowth\私人\localcode`**，用中文，含架构决策、阶段性成果、真实验收、数据状态变化、踩坑和未完成项。
- 进展记录用 `PROGRESS-YYYY-MM-DD.md`（或更新当天已有文档），写明分支/提交、事实证据、执行过的验证、数据快照、未完成项、回滚边界。
- 提交或交接前检查上一份 Obsidian 记录到当前 HEAD 的提交，补录未沉淀的进展。不能写 Obsidian 时先在仓库 `docs/` 生成同名待同步文档，获得权限后补同步。
- 严格区分 `DESIGNED`、`IMPLEMENTED`、`VERIFIED`、`BLOCKED`：有规格或测试代码不等于实现或验收通过。
- **需要模型调用测试时**，从 `D:\Obsidian\code-autogrowth\项目进展\api-key.md` 读取 DeepSeek 官方 key，模型用 `deepseek-v4-pro`；key 只用于本地测试，不硬编码、不提交。
- **阶段性任务完成后可自动 `git commit` 并 `git push`**，提交信息概括本轮要点，结尾附 `Co-Authored-By: Claude <noreply@anthropic.com>`。
- **需要 Docker 时可直接隐藏启动 Docker Desktop**，无需确认；不含删除容器、volume、索引或业务数据。
- **本地代理不通或卡顿时用 Clash for Windows**（默认 `127.0.0.1:7890` HTTP 代理）：
  - Windows 侧：`$env:HTTP_PROXY='http://127.0.0.1:7890'; $env:HTTPS_PROXY='http://127.0.0.1:7890'`
  - WSL 侧：`export http_proxy=http://<Windows主机IP>:7890 https_proxy=http://<Windows主机IP>:7890`（主机 IP 用 `ip route show default | awk '{print $3}'` 取）
- **超过 15 分钟的工作（下载大文件、Docker 构建、pip install）先确认走代理是否更快**。优先在代理可达的环境下载（如 WSL 直连而非容器内），再把文件传入 Docker build context。

## RAG continuation memory

- **四目标工作的唯一权威执行地图是 `docs/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md`。** 处理自研 Harness、多模态 RAG、评测集或可观测性前必须完整读取，按其中的 Phase 依赖、artifact/trace 契约、状态口径、任务卡和发布门禁执行，不得另建冲突路线或越过前置门禁。
- Record every meaningful RAG milestone in both `docs/PROGRESS-YYYY-MM-DD.md` and `D:\Obsidian\code-autogrowth\私人\localcode\PROGRESS-YYYY-MM-DD.md` before handoff or push.
- State `DESIGNED`, `IMPLEMENTED`, `VERIFIED`, and `BLOCKED` precisely, including commands, current MySQL/ES/MinIO counts, unfinished plans, and rollback boundaries.
- All PDF entry points use MinerU in explicit OCR mode. Tika is limited to non-PDF office documents such as DOCX, PPTX, and XLSX.
- BeeAPI/OpenAI relay concurrency is capped at 10 in-flight requests across all local processes and shards combined. Prefer a lower value for retry-heavy review runs. Never bypass the CLI limit by launching shards whose aggregate concurrency exceeds 10; HTTP 429 responses must be retried with backoff or left fail-closed.
- GPT-5.6 Sol 复核必须标记为 `AI_REVIEWED` 或 `DISPUTED`，不得生成真人 `reviewer_hash` 或冒充真人复核；只有真实人工参与后才可标记 `HUMAN_REVIEWED`。
- Do not claim the multimodal corpus complete until the design map's data, qrels, index, visual bake-off, observability, and explicit integration gates pass.

## Mandatory phase-end status report

At every natural phase boundary, handoff, blocked stop, or final answer, report
the four design-map workstreams in Chinese. This is mandatory even when no code
changed in the phase:

1. 自研 Harness
2. 多模态 RAG
3. 评测集
4. 可观测性

The report must contain all of the following:

- a percentage bar for each workstream, for example `自研 Harness 55% [#####-----]`;
- the items that most need work next, ordered by impact;
- the thing the agent is least certain about right now;
- the largest likely omission in the current understanding.

Percentages are evidence-weighted coverage estimates, not completion claims.
Count only requirements with fresh, reproducible verification evidence. Do not
increase a percentage merely because code, a plan, a label, a synthetic fixture,
or a preflight exists. State the source of material uncertainty and revise the
estimate downward when an acceptance gate is missing or invalidated.

## 2026-08-14 continuation state

- Branch: `main`; latest pushed commit at the time of this note: `cec5e0cd`.
- Verified current-head receipts include a real MinerU OCR PDF to RAG E2E,
  Terminal-Bench official failure (model payload failure, not infrastructure),
  DocVQA page-level qualification, and Phoenix O3 multi-instance trace evidence.
- Current evidence-weighted estimates: Harness 90%, multimodal RAG 84%,
  evaluation set 74%, observability 89%. These are not completion claims.
- Highest-impact gaps: license-clear document-native element+bbox qrels with
  genuine human review; unified Phoenix joins for official Terminal-Bench/tau2
  runs; long-running metrics, persistence, and actionable alerts; a new clean
  holdout and a passing official benchmark sample.
- Preserve failed receipts and do not tune fixed public tasks until they pass;
  do not convert AI review into human review or labels into acceptance evidence.
