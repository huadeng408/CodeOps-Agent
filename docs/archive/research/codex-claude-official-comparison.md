# Codex 与 Claude Code：官方可观察能力比较

> 研究日期：2026-09-07
> 范围：仅使用 OpenAI Codex 当前官方手册/官方页面与 Anthropic Claude Code 官方文档。
> 用途：为 `localcode` Agent Harness 的生产级上下文、记忆、子 agent、会话恢复和安全边界设计提供可追溯的产品行为基线。

## 先读结论

Codex 和 Claude Code 都把“上下文治理”拆成了几层，而不是把完整聊天记录无限追加给模型：持久项目指导、按需加载的工作流/工具、独立的子 agent 上下文、会话级压缩，以及跨会话记忆。两者都把权限审批与执行沙箱视为不同边界，并提供生命周期 hooks。

对 `localcode` 最有价值的可复刻模式如下：

1. **上下文分层**：稳定约束不应依赖聊天记录；项目规则、路径规则、技能描述、工具 schema、最近会话和记忆分别治理。
2. **压缩是有状态操作**：要保存结构化摘要、最近文件/证据、未完成任务和约束，而不是简单截断字符串；用户应能主动压缩，也应能在接近上限时自动压缩。
3. **子 agent 使用独立上下文**：主会话只接收结果摘要和可追溯 receipt，避免把探索日志、测试输出和大文件读取污染主窗口。
4. **记忆不是强制规则**：记忆可以帮助召回，但必须与项目规则分离、可关闭、可检查、可清理；必须保留来源和置信度。
5. **工具 schema 应延迟加载**：大量 MCP 工具的完整 schema 会占用上下文，应先列出工具，再按需搜索/加载。
6. **安全边界必须独立于模型判断**：工具权限、工作区、网络域、审批、沙箱、工作树和 workspace trust 需要在执行边界强制落实。
7. **会话可观察且可恢复**：session/thread 应有稳定 ID、事件流、resume/fork/rewind/compact 操作及幂等行为；UI 应展示状态和失败，而不是只显示最终文本。

官方文档没有承诺完整的内部实现：两家都没有在这些页面中公开模型提示词、完整 compaction 算法、记忆召回排序、向量/图存储 schema、调度器公平性、绝对并发上限或“长对话永不遗忘”的保证。因此，下文把**官方明确行为**与**公开文档未说明**分开，不能把产品行为反推为源码实现。

## 证据边界与术语

- **Codex**：使用当前官方手册快照 `C:\Users\ieeep\AppData\Local\Temp\openai-docs-cache\codex-manual.md`（生成时间 2026-09-05）及手册中指向的官方 OpenAI/ChatGPT 文档。手册行号是本次快照的定位辅助；线上页面标题和 URL 是主要引用。
- **Claude Code**：使用 `code.claude.com/docs/en/` 的官方文档页面。这里只写文档明确描述的可观察行为，不做 Anthropic 私有实现或源码推断。
- **“未说明”**：在公开官方页面没有找到保证或接口契约，不等于产品一定没有该能力。
- **“可观察”**：用户可以通过 CLI、应用、配置、事件、UI 或受支持的命令观察到的行为；不代表内部算法已公开。

## 能力比较矩阵

| # | 生产能力 | Codex 官方可观察行为 | Claude Code 官方可观察行为 | 对 localcode 的落地含义 | 证据 |
|---:|---|---|---|---|---|
| 1 | 项目级持久指导 | `AGENTS.md` 是自动加载的开放格式项目指导，可放构建、测试、架构和工作约束；可在全局、仓库和更深子目录分层。 | `CLAUDE.md`/`CLAUDE.local.md` 支持 managed、user、project、local 多个范围；项目文件通过版本控制共享。 | 建立 `AGENTS.md`/项目规则的解析、来源、优先级和 receipt；不要把必须执行的策略只放进 prompt。 | [C1][A1] |
| 2 | 路径/模块化规则 | 手册建议用更具体的 `AGENTS.md` 约束子目录，减少每轮注入；具体加载细节以手册为准。 | `.claude/rules/` 支持递归发现和 `paths` frontmatter；匹配文件被读取时才加载路径规则。 | 规则应支持 path scope、依赖和按需注入，并记录“为何本轮加载”。 | [C1][A2] |
| 3 | 上下文预算可视化 | CLI `/status` 可查看 session/context 使用情况；手册建议限制 prompt、AGENTS 和 MCP 数量，避免上下文浪费。 | `/context` 和官方 context-window 页面展示启动内容、文件读取、MCP、skills、hooks、子 agent 和 compact 后的上下文占用。 | 需要 context ledger：每个 block 有 token 估计、来源、优先级、是否可丢弃和 compact 后恢复策略。 | [C1][A3] |
| 4 | 手动压缩 | `/compact` 把当前聊天压缩成更简洁的摘要，释放上下文空间。 | `/compact` 生成结构化会话摘要；官方模拟说明会保留关键请求、文件、错误、待办，并重新读取近期修改文件。 | 设计显式 `compact` API/事件；输出应含 facts、constraints、decisions、files、errors、pending、receipts。 | [C2][A3] |
| 5 | 自动压缩 | Codex 官方手册明确说聊天会自动 compact，也允许手动 `/compact`；具体触发阈值和算法未说明。 | 文档描述 compact 作为长会话治理能力，但没有承诺固定阈值或摘要无损。 | 自动 compact 只能作为保底；必须暴露触发原因、前后摘要版本和可审计差异。 | [C2][A3] |
| 6 | 会话恢复 | `/resume` 恢复已保存聊天；app-server 有 `thread/resume`，会用稳定 thread id 继续后续 turn。 | `/resume` 返回早前 conversation；`/clear` 开新任务但保留项目记忆，`/rewind` 可回退代码和会话状态。 | session store 要区分 transcript、ledger、working tree、memory；恢复必须校验版本/权限/工具配置。 | [C2][A4] |
| 7 | 分支/试验 | `/fork` 复制本地聊天而保留原 transcript；app-server `thread/fork` 支持指定历史点和 ephemeral in-memory fork。 | `/branch`/`/fork` 可分支当前会话；大任务可用 `/batch` 为独立单元建 worktree。 | 需要 immutable parent pointer、fork point、隔离 event stream 和合并/丢弃策略。 | [C2][A4] |
| 8 | 跨会话记忆开关 | Codex local memories 与必需项目指导分离；可用 `/memories` 控制本聊天读取/生成记忆，默认可关闭，配置有 `generate_memories`、`use_memories` 等。 | Claude Code 每个 session 从新的 context window 开始；`CLAUDE.md` 与 auto memory 共同跨 session 加载。 | 记忆必须有 per-user/per-project opt-in、读取和写入开关、审计、删除/导出和 secret redaction。 | [C3][A1] |
| 9 | 记忆存储边界 | Codex 记忆存于 `~/.codex/memories/`，包含 summaries、durable entries、recent inputs 和 evidence；官方称为生成状态，不建议手工当主控制面。 | auto memory 在 `~/.claude/projects/<project>/memory/`；`MEMORY.md` 是索引，topic 文件存细节；同一 repo 的 worktree/subdirectory 共享该目录，跨机器/cloud 不共享。 | 采用明确的 memory namespace（user/project/session/subagent），并把 source event、更新时间、TTL、敏感级别一并存储。 | [C3][A5] |
| 10 | 记忆启动预算 | Codex公开说明记忆可注入未来 session，但未承诺固定行数、字节数或召回排序。 | Claude Code 每次启动最多加载 `MEMORY.md` 前 200 行或 25KB；超出内容应移到 topic 文件。 | 为 memory index 设硬 token/byte budget；详情按需检索，禁止把全量长期记忆塞进启动 prompt。 | [C3][A5] |
| 11 | 记忆可靠性边界 | Codex 明确说记忆是 helpful recall，不是必须适用的规则；规则应放 `AGENTS.md`/项目文档。 | Claude 文档明确说 `CLAUDE.md` 和 auto memory 是 context，不是 enforced configuration；必须阻断的动作应使用 PreToolUse hook。 | 生产策略、审批、数据隔离不进入可被模型忽略的 memory；策略放 policy engine/hook。 | [C3][A5] |
| 12 | 子 agent 独立上下文 | Codex subagent 有自己的 agent thread 和模型/tool work；主 thread 汇总结果而不是引入全部中间日志，支持并行 exploration/test/triage。 | subagent 从新 context window 开始，不继承主会话 transcript 或主 auto memory；只返回最终文本和少量 metadata。 | 每个 worker 必须有独立 context ledger、预算、取消、超时、父子关系和结构化 summary/receipt。 | [C6][A6] |
| 13 | 子 agent 触发 | Codex 可由明确请求或适用的 `AGENTS.md`/skill 指令触发；`/agent` 可查看/切换 agent thread。 | Claude 根据 subagent description 决定委派，也支持显式调用；`/tasks` 查看后台工作。 | 支持 explicit、policy-driven、planner-driven 三种触发，但记录触发原因和是否用户授权。 | [C6][A6] |
| 14 | 子 agent 模型路由 | Codex 子 agent 默认继承父 model/reasoning，可在 config 或 custom agent 中配置；官方建议按任务在速度/成本/推理间取舍。 | 可在 subagent 定义中设 `model`，也可用 `CLAUDE_CODE_SUBAGENT_MODEL`（及 force 变量）统一路由。 | worker profile 要显式记录 model、reasoning、budget、fallback 和 provider，禁止静默换模。 | [C6][A6] |
| 15 | 子 agent 工具/权限继承 | Codex 子 agent 继承当前 sandbox/approval policy；可在 custom agent 中限制能力。 | 子 agent 继承主会话权限但可用工具被过滤；可配置 `tools`、`permissionMode`、hooks 和 deny `Agent(name)`。 | 把“继承父权限”设为默认，再用 allowlist 降权；每次 tool call 带 worker/session identity。 | [C6][A6] |
| 16 | 子 agent 文件隔离 | Codex 官方把 worktree 作为并行写入的隔离选择，并警告共享文件写入会冲突。 | `isolation: worktree` 让 subagent 的 Bash/PowerShell 在自己的 worktree；文档描述了防止命令落回主 checkout 的检查。 | 实现 isolated workspace lease、路径校验、清理/保留、patch/merge receipt；绝不能仅靠 prompt 要求“不要改同一文件”。 | [C7][A6] |
| 17 | DAG/团队协作 | Codex 官方文档公开 subagent workflow、并行收集和 agent thread，但没有承诺完整 DAG 调度语义或固定并发上限。 | Claude Agent Teams 支持 lead/teammate、共享任务、消息、计划审批和显示模式；文档提示团队有额外 token/协调开销。 | 把 DAG、依赖、join、失败重试、取消、预算和 message delivery 变成显式状态机；不要把一次并行 spawn 等同于生产调度器。 | [C6][A7] |
| 18 | MCP 工具上下文 | Codex 把 MCP 作为外部上下文/工具连接，并提醒每个服务器会增加消息上下文和使用量；可在 `/mcp` 查看。 | Claude MCP 支持 HTTP/SSE/stdio/WebSocket；默认可延迟工具 schema，通过 tool search 按需加载，`ENABLE_TOOL_SEARCH` 可调整策略。 | MCP registry 要有 server health、auth/trust、schema cache、按需加载、每轮 token budget 和失败降级。 | [C1][A8] |
| 19 | MCP 信任/审批 | Codex 对 MCP/app 工具可按工具注解和 approval mode 审批；非 managed hooks/MCP 需信任流程。 | 项目 `.mcp.json` server 在 workspace trust 前保持 pending/需要批准；文档警告外部内容带 prompt-injection 风险。 | 新服务器/域名/写工具必须走 approval；把外部内容当不可信数据，工具结果不能升级权限。 | [C8][A8] |
| 20 | Skills 按需加载 | Codex skill 初始只注入 name/description，选中后加载完整 `SKILL.md`；可通过 plugin 分发。 | Claude skill listing 只提供描述，完整 body 在调用时加载；可通过 `disable-model-invocation`、skill overrides 控制隐式调用。 | 建立 progressive disclosure：catalog → selected skill → references/scripts，并统计每层 token 成本。 | [C9][A9] |
| 21 | Hooks 生命周期 | Codex 支持 `SessionStart/End`、`UserPromptSubmit`、`PreToolUse`、`PostToolUse`、`PreCompact/PostCompact`、`SubagentStart/Stop`、`Stop` 等；非 managed hook 需要按 hash review/trust。 | Claude 支持 `SessionStart`、`PreToolUse`、`PostToolUse`、`PermissionRequest`、`Notification` 等；hook 输入为 JSON，退出码可阻断 PreToolUse。 | hook runner 应支持 matcher、超时、异步、structured additional context、deny、trust/hash、审计和 fail-closed。 | [C10][A10] |
| 22 | 权限与审批 | Codex 明确区分 sandbox（可访问边界）与 approval（何时暂停）；有 read-only/workspace/danger-full-access profiles、on-request/never 等策略及 auto-review。 | Claude permissions 控制工具/文件/域；sandbox 对 Bash 及子进程做 OS 级限制；deny/ask/allow 和 managed precedence 可组合。 | policy engine 必须在工具执行前独立于模型判断，审计 approved/denied/timeout/retry；高风险动作默认 fail-closed。 | [C11][A11] |
| 23 | 网络与文件沙箱 | Codex native Windows、macOS、Linux/WSL 均有平台沙箱说明；workspace roots/网络策略可配置。 | Claude sandbox 原生支持 macOS/Linux/WSL2，Windows 文档要求 WSL2；`failIfUnavailable` 可将缺依赖变为硬失败。 | Windows harness 要提供真正的隔离实现或明确 degraded mode；不能把“检测到沙箱”当成执行隔离证据。 | [C11][A11] |
| 24 | 长任务与暂停恢复 | Codex Goal mode (`/goal`) 有目标、约束、验证、暂停/恢复；后台 subagents、scheduled/long-running work 和桌面进度可观察。 | Claude 支持 `/background` 分离整个 session、后台 agents、`/tasks`、web/remote control；命令文档提供 `/resume`、`/rewind`、`/compact`。 | 需要 durable goal、heartbeat、pause/cancel/resume、idle recovery 和 human input gate；目标不能自动扩大权限。 | [C12][A4] |
| 25 | UI/会话可见性 | Codex desktop/CLI/IDE 显示 agent threads、进度、审批、context/status；app-server 提供 thread/turn/item 事件流。 | Claude terminal/IDE/web/remote-control 显示后台任务、context、审批、diff 和 worktree；`/context` 能解释窗口组成。 | 前端应消费结构化 event stream，展示 worker、token、审批、重连、错误和持久化结果，而不是只轮询最终答案。 | [C6][C13][A3] |
| 26 | 结构化执行协议 | Codex app-server 文档公开 JSON-RPC `thread/start/resume/fork`、`turn/*`、`item/*`、`thread/compact/start` 等事件/方法。 | Claude 公开 `stream-json`/headless 事件、hooks JSON input 和 Agent SDK/Remote Control，但本比较不把其内部协议等同于 Codex app-server。 | 设计版本化 session/event API；命令行、WebSocket、浏览器控制台都消费同一事件模型。 | [C13][A8][A10] |
| 27 | 可观测性与成本 | Codex 手册列出 token、turn、tool/MCP、approval、hooks、multi-agent、memory 等指标类别。 | Claude 文档公开 `/context`、`/usage`、`/tasks`、skill-doctor 等可观察命令，但未承诺统一生产 metrics schema。 | 记录 token input/cache/output、latency、tool cost、memory hits、compaction、worker outcome、approval outcome，并按 tenant/session 隔离。 | [C3][C6][A9] |
| 28 | 自动记忆生成安全 | Codex 文档说明生成记忆会跳过 active/short-lived sessions、redact secrets，并可因外部 context 设置禁用。 | Claude auto memory 会写入机器本地目录，文件不随 session transcript retention sweep 删除；可用配置关闭。 | 记忆写入需要敏感信息扫描、来源 receipt、人工查看/删除和 retention policy；不能把自动生成当已验证事实。 | [C3][A5] |
| 29 | 多租户/跨机器语义 | Codex local memory 与 host/Codex home 绑定；ChatGPT Work 使用账户/工作区记忆，不等同于 local store。 | Claude project auto memory 在本机 repo/worktree 间共享，不跨 machine/cloud；cloud session 使用 clone/不同设置边界。 | 生产 harness 必须明确 tenant、workspace、host、repo、session 的 key；禁止把本机记忆误当共享知识库。 | [C3][A5] |
| 30 | 官方未说明的关键保证 | 未公开固定 context window/compact 阈值、召回算法、完整 subagent scheduler、绝对并发上限、摘要无损率和端到端不遗忘 SLA。 | 未公开 auto memory 的语义检索/排序实现、固定 compact 质量、调度公平性、绝对并发上限和跨 provider 一致性。 | 这些必须在 localcode 自己的 contract tests/benchmark 中定义；对外报告真实测量值和环境，不借用产品文档当 SLA。 | [C2][C3][C6][A3][A5][A6] |

## 重点领域的设计解读

### 1. 上下文管理：从 transcript 转为 Context Ledger

官方行为共同指向一个分层 ledger，而非单一字符串：

```text
system/policy
  -> durable project guidance (AGENTS.md / CLAUDE.md)
  -> scoped rules (path/module)
  -> skill catalog and selected skill body
  -> MCP catalog and selected tool schemas
  -> session goal, constraints, decisions
  -> recent events/tool evidence
  -> compact summary and unresolved work
  -> optional memory hits
```

建议 localcode 为每个 context block 保存：`block_id`、`kind`、`source`、`scope`、`token_estimate`、`priority`、`provenance`、`expires_at`、`restorable`、`redaction_state`。compact 时只删除可重建的低优先级块；关键约束、决策、工具配对和安全策略要有保留断言。恢复时用 event cursor 和 summary version 重放，而不是依赖模型“记得”。

### 2. 记忆管理：区分规则、事实、偏好和证据

两家官方文档都不把记忆当成强制配置。建议 localcode 使用四类对象：

- **Rule**：必须执行的策略，存 policy/AGENTS/配置，由 hook 或 executor 强制。
- **Fact**：经验证的项目事实，带 source event、commit、时间和置信度。
- **Preference**：用户偏好，按 user/tenant 隔离，可覆盖默认呈现方式但不能放宽安全策略。
- **Episode**：一次任务的摘要和 receipt，用于恢复，不直接当长期知识。

记忆召回结果应带 `memory_id`、原文摘要、来源、更新时间、scope、confidence、命中原因和是否过期；召回失败、冲突或低置信度时应显式告诉 orchestrator，不应静默覆盖当前用户约束。

### 3. 子 agent：以 receipt 作为边界

主 agent 不应接收子 agent 的完整过程日志。每个 worker 至少返回：

```json
{
  "worker_id": "w-123",
  "parent_session_id": "s-456",
  "status": "succeeded",
  "summary": "...",
  "facts": [{"claim": "...", "source": "...", "confidence": 0.92}],
  "artifacts": [{"path": "...", "sha256": "..."}],
  "tests": [{"command": "...", "exit_code": 0}],
  "tokens": {"input": 0, "output": 0},
  "started_at": "...",
  "finished_at": "..."
}
```

worker 的原始 tool events 仍应持久化到隔离 ledger，供审计和 UI 查看，但默认只把摘要、引用和失败原因注入父上下文。DAG join 必须等依赖满足并对重复消息幂等；超时、取消和重试不能产生重复 side effect。

### 4. 安全：权限、沙箱、trust、hook 四道边界

建议执行顺序：

1. 根据 tenant/project/session policy 做工具和资源 allow/deny 匹配。
2. 检查 workspace trust、MCP server trust、hook hash trust。
3. 在 OS/container sandbox 中执行文件/网络命令。
4. 记录审批、实际执行边界、退出码和 receipt；策略不可用时 fail-closed。

模型提出“允许”不能替代这四步。Codex 官方文档明确说 auto-review 不会扩展 sandbox；Claude 官方文档也明确区分 permission 与 Bash sandbox。localcode 应保持相同边界。

## 不应从官方文档推断的事项

以下问题在公开页面没有足够证据，必须由 localcode 自己实现并测量：

1. compact 摘要对每一种事实类型的召回率和无损率。
2. 200+ turns、重启、断线和多次 compact 后的长期记忆准确率。
3. 记忆的向量/图/倒排索引结构、embedding 模型和召回排序。
4. subagent 的精确最大深度、绝对并发数、队列公平性和资源隔离强度。
5. provider 切换时 tool-call pairing、消息顺序和 token accounting 是否完全一致。
6. MCP server schema cache 的持久化、失效、污染和跨 session 复用细节。
7. UI 事件在浏览器刷新、WebSocket 重连、服务重启后的 exactly-once/at-least-once 语义。
8. 自动 memory 生成是否覆盖所有会话类型、所有外部工具和所有敏感信息模式。
9. 生产 SLA、成功率、P95/P99、成本上限或“永不遗忘”保证。
10. 任何未在官方页面承诺的内部 prompt、私有 API 或源码行为。

## 对 localcode 验收的建议

官方能力只能提供行为基线，不能替代本项目验收。建议把以下 receipt 纳入浏览器 Acceptance Console，并从 UI 点击一路验证到后端：

- context budget 面板：展示每类 block 的 token、来源、优先级、compact 前后差异。
- memory 面板：创建/检索/禁用/删除 memory，展示 scope、source event、confidence 和 redaction 结果。
- worker 面板：启动、取消、重试、join DAG，查看独立 transcript、worktree、预算和结构化 receipt。
- session 面板：暂停、恢复、fork、rewind、compact、重连；刷新浏览器后状态仍一致。
- MCP/skill 面板：server health、trust、按需 schema、skill catalog、调用 token 成本和失败降级。
- security 面板：审批、deny、sandbox boundary、workspace trust、hook run/deny/timeout。
- long-dialog gate：200 turns、超过模型窗口两倍的 raw history、4 次 compact、2 次重启，检查关键约束、普通事实、tool pairing 和 token reduction。

## 官方来源索引

### OpenAI / Codex

- **[C1] Codex Manual / Best practices**：`https://developers.openai.com/codex/codex-manual.md`；本地快照中的 “Strong first use: Context and prompts”“AGENTS.md”“Use MCPs”“Turn repeatable work into skills” 小节（约 1790-1980 行）。
- **[C2] Codex Manual / Developer commands**：`https://learn.chatgpt.com/docs/developer-commands?surface=cli`；`/compact`、`/resume`、`/fork`、`/status`、`/agent`。
- **[C3] Codex Manual / Memories**：`https://learn.chatgpt.com/docs/customization/memories`；本地快照 “Memories” 小节（约 25471 行起）。
- **[C4] Codex Manual / Authentication and sessions**：`https://learn.chatgpt.com/docs/auth`；本地快照 “Authentication and sessions” 小节（约 12900 行起）。
- **[C5] Codex app-server**：`https://developers.openai.com/codex/app-server`；本地手册 “Start (or resume) a thread”“thread/fork”“thread/compact/start” 小节（约 30790-31440 行）。
- **[C6] Codex Manual / Subagents**：`https://learn.chatgpt.com/docs/agent-configuration/subagents`；本地快照 “Multi-agent operations” 小节（约 2022-2160 行）。
- **[C7] Codex Manual / Long-running work and worktrees**：`https://learn.chatgpt.com/docs/long-running-work`、`https://learn.chatgpt.com/docs/environments/git-worktrees`。
- **[C8] Codex Manual / MCP**：`https://learn.chatgpt.com/docs/extend/mcp`；本地快照 “Model Context Protocol” 小节（约 25581 行起）。
- **[C9] Codex Manual / Skills and plugins**：`https://learn.chatgpt.com/docs/build-skills`、`https://developers.openai.com/plugins/build/skills`；本地快照 “Build skills” 小节（约 21899-22239 行）。
- **[C10] Codex Manual / Hooks**：`https://learn.chatgpt.com/docs/hooks`；本地快照 “Hooks” 小节（约 23366-24481 行）。
- **[C11] Codex Manual / Permissions and sandbox**：`https://learn.chatgpt.com/docs/permissions`、`https://learn.chatgpt.com/docs/sandboxing`；本地快照约 10185-11210 行。
- **[C12] Codex Manual / Long-running work**：`https://learn.chatgpt.com/docs/long-running-work`；目标、暂停/恢复、同 chat steering 和并行 worktree。
- **[C13] Codex app-server protocol**：`https://developers.openai.com/codex/app-server`；JSON-RPC `thread/*`、`turn/*`、`item/*`、compact 和事件流章节。

### Anthropic / Claude Code

- **[A1] How Claude remembers your project**：`https://code.claude.com/docs/en/memory`；`CLAUDE.md`、auto memory、scope、前 200 行/25KB、非强制配置边界。
- **[A2] Memory / path-specific rules**：`https://code.claude.com/docs/en/memory#path-specific-rules`；`.claude/rules/` 的 `paths` frontmatter 和按需加载。
- **[A3] Explore the context window**：`https://code.claude.com/docs/en/context-window`；启动内容、deferred MCP、skills、hooks、subagent 独立 context、compact 后恢复。
- **[A4] Commands**：`https://code.claude.com/docs/en/commands`；`/context`、`/compact`、`/resume`、`/branch`、`/fork`、`/rewind`、`/background`、`/tasks`。
- **[A5] Subagents**：`https://code.claude.com/docs/en/sub-agents`；scope、model、tools、permission、worktree isolation、persistent memory、hooks。
- **[A6] Subagents / persistent memory**：`https://code.claude.com/docs/en/sub-agents#enable-persistent-memory`；`user/project/local` memory scope、`MEMORY.md` 预算、自动启用读写工具。
- **[A7] Agent teams**：`https://code.claude.com/docs/en/agent-teams`；lead/teammate、消息、任务协作、计划审批、额外 token/协调开销。
- **[A8] Connect Claude Code to tools via MCP**：`https://code.claude.com/docs/en/mcp`；HTTP/SSE/stdio/WebSocket、project approval、trust、health、tool search。
- **[A9] Skills**：`https://code.claude.com/docs/en/skills`；skill listing budget、按需 body、disable-model-invocation、skill overrides、skill doctor/evals。
- **[A10] Hooks guide**：`https://code.claude.com/docs/en/hooks-guide`；事件、matcher、JSON stdin、退出码、additionalContext、异步 hook。
- **[A11] Sandboxing and permissions**：`https://code.claude.com/docs/en/sandboxing`、`https://code.claude.com/docs/en/permissions`；Bash OS 隔离、network/filesystem boundary、allow/ask/deny、managed precedence、workspace trust。
- **[A12] Claude Code on the web / remote control**：`https://code.claude.com/docs/en/claude-code-on-the-web`、`https://code.claude.com/docs/en/remote-control`；云 session、远程继续和本机/云设置边界。具体可用性依账号和版本而定。

## 结论的使用规则

本文件只作为官方行为基线和设计输入。localcode 的生产级结论必须来自本项目自己的测试、浏览器 E2E、真实 provider receipt、故障注入和成本/延迟测量；不得把上面的产品文档能力直接写成 localcode 已经具备的能力，也不得把官方未说明的内部机制写成事实。
