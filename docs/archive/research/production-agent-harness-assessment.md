# CodeOps-Agent 生产级 Agent Harness 评估

> 评估日期：2026-09-07
> 目标仓库：`D:\vscode\localcode`，当前基线：以工作树 `docs/GOAL.md` 为验收权威。本文件已归档，不是启动入口。
> 研究对象：本仓库、DeepSeek Harness `cd5ef8148158c3a752a658978873241fdf8e2bbc`、官方 Codex/Claude Code 文档、官方 OpenViking `volcengine/OpenViking@a843ab6bf220b2b3bc82321576d623d1c55c6598`。

## 结论

当前项目是一个有真实基础的开发中 Harness，不是可以直接承诺生产级 SLA 的产品。它已经拥有 Go Harness/Python Orchestrator、事件溯源 Session、LangGraph checkpoint、Skills、MCP、工具权限、Worktree、沙箱适配器和多种 Context/Memory 后端；但四个新 HTTP/UI 路径仍需要收敛到 canonical `internal/session.SQLiteEventLog`，并且 Docker/WSL2、真实外部 provider、Phoenix live trace、官方 benchmark scorer 尚缺新鲜发布证据。

推荐路线是保留 Go/Python ownership split：Go 负责鉴权、授权、Session Ledger、工具执行、沙箱、进程和 root trace；Python 负责 Agent Loop、Context、计划、Memory、Subagent、Worker 和 Workflow。OpenViking 只做 clean-room 行为参考，不复制 AGPLv3 源码；事实源是 append-only ledger/filesystem，向量索引只能是可重建的 derived index。

## 证据边界

| 标签 | 含义 |
| --- | --- |
| `implemented` | 在当前源码中找到实现，仍需对应 runtime E2E 才能升级为发布能力。 |
| `verified` | 有当前源码、定向测试、集成测试和真实运行时 receipt。 |
| `research` | 来自第一方文档或源码的可观察行为，不代表内部实现。 |
| `blocked` | 缺少真实依赖、官方 scorer、外部 provider 或其他发布门槛；不能用 mock/fixture 替代。 |

当前 `docs/GOAL.md` 的整体验收仍是 `BLOCKED`。现有单测、确定性 provider、开发 smoke 和历史 receipt 只能证明各自范围，不能证明长对话不遗忘、生产沙箱或官方 benchmark 成功率。

## 参考实现对比

| 领域 | CodeOps-Agent 当前证据 | Codex/Claude Code 第一方行为 | DeepSeek Harness 源码参考 | OpenViking 源码参考 | 差距判断 |
| --- | --- | --- | --- | --- | --- |
| 项目规则 | `internal/config/agentmd.go` 已解析规则 | `AGENTS.md`/`CLAUDE.md` 分层、路径规则按需加载 | `packages/CLAUDE.md` 和 workspace loader | 不承担项目规则 | 已有基础，缺来源/优先级 receipt |
| Session | `internal/session/eventlog.go` 有 hash chain、surface、fork、rewind；HTTP/UI 通过 owner-scoped ledger API 读写 | `thread/resume/fork/compact` 可观察 | `storage-*`、session snapshot/invariant | session commit/archive/queue | Context Ledger 与跨模块恢复仍需补强 |
| Context | `orchestrator/context/*` 有预算、compaction、git diff、memory | `/context`、自动/手动 compact、独立 subagent context | snapshot compaction fixture | `ContextType` + L0/L1/L2 | 缺统一 Context Ledger 和恢复断言 |
| Memory | SQLite/Redis/MySQL long-term memory、checksum、TTL | local memory 与强制规则分离 | durable storage、snapshot | registry、policy、isolation、derived vector | 缺 namespace/ACL/冲突与召回轨迹统一模型 |
| Subagent | Python subagent/workflow/process executor | 独立 context、权限继承/降权、worktree | workflow worker thread、独立 package | peer/user memory isolation | 缺 durable DAG lease/join/idempotency |
| Tool/MCP | Go 工具、MCP、权限和 sandbox 适配器 | deferred schema、trust、PreToolUse hook | tool package/loader、process invariants | service/transport 解耦 | 缺统一 trust/hash/预算/降级状态机 |
| UI | 新 React workbench 初版 | Codex app-server item/thread events；Claude `/tasks`/`/context` | web-app/headless bundle | Studio/trace 可观察 | 必须从 session/event API 驱动并做真实浏览器验收 |
| 生产证据 | Go/Python 测试和部分 E2E | 官方产品行为而非 SLA | 大量 invariant/e2e 测试 | commit/reindex/failure tests | Docker、provider、官方 scorer 仍 blocked |

## P0/P1/P2 改造矩阵

优先级含义：`P0` 阻断数据正确性、安全或恢复；`P1` 影响生产可用性、成本或可观察性；`P2` 增强体验、扩展性或运营效率。共 30 项，其中 23 项直接属于 Context、Memory 或 Subagent。

| # | 优先级 | 改造点 | 当前问题/证据 | 目标接口与验收 |
| ---: | :---: | --- | --- | --- |
| 1 | P0 | 单一 Session Ledger | HTTP 新事件模型 `session_event_projections` 与 canonical `session_events` 并存 | 所有新写入经 `SQLiteEventLog`；schema 检查证明无第二可写事实源 |
| 2 | P0 | 事件并发 CAS | 读后写在多进程下会竞争 | `expected_seq` 原子拒绝旧写；hash chain 连续；跨进程 E2E |
| 3 | P0 | Owner 强制参数 | `ownerID ...uint` 可省略，存在绕过可能 | 所有外部入口使用非零 `OwnerID`；未授权统一 404/403；单测+浏览器验证 |
| 4 | P0 | Checkpoint 非破坏恢复 | 旧路径删除后缀会破坏审计 | restore 只追加 `session/rewind` 和 state projection；原始事件不变 |
| 5 | P0 | WebSocket ticket | JWT query 会进入日志和代理 | HTTP 一次性 ticket，WS 只接收短期 ticket/header；过期、重放、跨 owner 拒绝 |
| 6 | P0 | Replay/live 去重 | replay 与 live 竞态可能重复或漏事件 | `seq/event_id` 游标去重，分页尾游标可恢复；浏览器断线重连验证 |
| 7 | P0 | 慢消费者 fail-closed | 当前队列满时静默丢事件 | 关闭连接并返回最后 cursor；客户端从 cursor replay，不伪造成功 |
| 8 | P0 | Context Ledger | Context block 来源、预算和可恢复性散落 | block 记录 `id/kind/source/tokens/priority/restorable/checksum`；每轮 receipt |
| 9 | P0 | Tool-call/result 配对 | compact/恢复若截断配对会重放副作用 | 压缩边界必须落在完整 turn；孤立 call/result fail closed；200 turn E2E |
| 10 | P0 | Context 持久化 fail-closed | ledger/checkpoint 不可用时不能继续副作用 | 首次模型/工具调用前返回明确错误；零副作用测试 |
| 11 | P0 | Memory namespace/ACL | long-term memory 需区分 user/project/session/subagent | namespace key 和 ACL 在存储、检索、删除三处强制；跨租户浏览器验证 |
| 12 | P0 | Memory 敏感信息过滤 | 自动记忆不能写 secret、token、凭据 | 写入前 redaction/pattern scan；来源 event/checksum；拒绝路径有 receipt |
| 13 | P0 | Memory 冲突与过期 | 旧事实可能静默覆盖当前约束 | `revision/source/expires_at/confidence`；冲突返回给 orchestrator，不静默覆盖 |
| 14 | P0 | Subagent 独立 Context | 父会话若接收全部探索日志会污染窗口 | 子 agent 独立 ledger；父只收结构化 summary/facts/artifacts/receipt |
| 15 | P0 | Subagent 权限降级 | 子 agent 继承权限但缺显式 allowlist/deny | parent policy 继承后只能收窄；每次 tool call 携 worker identity |
| 16 | P0 | Subagent workspace lease | 并行写入共享 checkout 会互相覆盖 | worktree lease、路径校验、释放/保留策略和 merge receipt |
| 17 | P0 | Worker durable lease | 进程终止可能重复执行同一副作用 | SQLite/CAS lease、attempt、heartbeat、fencing token；故障恢复 E2E |
| 18 | P1 | Token-aware retention | 字符数 compactor 不能表达 token 成本 | 按 token budget、优先级和可重建性裁剪；保留约束/决策/证据 |
| 19 | P1 | L0/L1/L2 Context | 全量文件/历史注入浪费窗口 | L0 摘要、L1 概览、L2 原文按需加载；每次加载记录层级和 token |
| 20 | P1 | Context retrieval trajectory | 召回结果缺可解释路径 | 记录 query、namespace、候选、过滤、最终命中和 checksum |
| 21 | P1 | Context conflict detector | 项目规则、Memory、用户新指令可能冲突 | 优先级矩阵 + conflict event；高风险冲突暂停请求人工确认 |
| 22 | P1 | Memory extraction coordinator | policy、registry、compressor、store 耦合 | 一个深模块编排候选、预过滤、去重、写入、失败重试 |
| 23 | P1 | Session commit coordinator | summary/archive/memory/index 写入缺统一恢复协议 | 两阶段 commit；持久 queue、done/failed marker；重启可继续 |
| 24 | P1 | Subagent DAG scheduler | 当前 workflow 可运行但调度语义不完整 | 显式依赖、join、取消、超时、重试、预算、公平性和幂等消息 |
| 25 | P1 | Parent-child result contract | 结果文本缺 facts/artifacts/test denominator | 固定 receipt schema；父上下文只注入摘要和引用 |
| 26 | P1 | Deferred tool schema | MCP schema 全量进入窗口 | catalog → selected schema → invocation；按轮 token 预算和 cache |
| 27 | P1 | Hook/trust registry | 外部 MCP/hook 的信任和 hash 未统一 | server/hook manifest、审批、hash review、超时、fail-closed |
| 28 | P1 | UI session/event API | UI 直接拼 REST/WS，无法展示持久化状态 | 版本化 event API；session、turn、item、worker、approval、cursor 统一 |
| 29 | P2 | Memory lifecycle console | 记忆不可见、不可导出/删除/审计 | 浏览器查看来源、scope、TTL、confidence，逐条删除需确认 |
| 30 | P2 | Benchmark/release gate | receipt 分散，外部 scorer/环境阻塞 | 固定 pin/budget/denominator/trace/artifact SHA；缺证据自动 `BLOCKED` |

## Context/Memory/Subagent 目标设计

### Context Ledger

```text
policy/rules
  -> scoped project guidance
  -> skill and MCP catalog
  -> selected skill/tool schema
  -> goal/constraints/decisions
  -> active Surface
  -> compact summary and unresolved work
  -> scoped Memory hits
  -> current tool evidence
```

每个 block 都要有来源、scope、token estimate、priority、TTL、redaction state、checksum 和重建方式。compact 是追加事件，不删除 ledger；自动 compact 只能是保底，必须暴露触发原因和前后版本。`tool/call` 与 `tool/result`、审批、文件 diff 和 checkpoint 必须成对保留。

### Memory

采用 clean-room 的 `viking://` 思路，但不复制 AGPL 源码：统一 `Resource/Memory/Skill` URI；目录摘要使用 L0/L1，原始证据使用 L2；FS/ledger 是 source of truth，向量索引只存 URI/vector/metadata。Memory 分为 `Rule`、`Fact`、`Preference`、`Episode`，其中 Rule 不能来自可被模型忽略的自动记忆。每条召回返回 `memory_id/content/source/revision/scope/confidence/expiry/hit_reason`。

官方 OpenViking 源码中值得复刻的行为包括：`session.commit()` 的同步 archive + 异步 extraction、持久 `session_commit` queue、失败不写 `.done`、memory type registry、peer/self isolation、tenant-aware ACL/filter 和可重建 vector index。官方仓库 `volcengine/OpenViking` 为 AGPLv3，网络服务修改也有源代码提供义务；当前实现只采用行为契约与测试，不复制实现。

### Subagent

子 agent 拥有独立 Context Ledger、预算、取消信号、权限上下文和可选 worktree。父会话只接收：

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
  "attempt": 1
}
```

DAG join 必须在依赖满足后以 `worker_id + attempt` 幂等；超时、取消、失败和重试都要持久化。共享 checkout 不属于隔离策略，必须使用 worktree lease 或只读模式。

## 浏览器验收矩阵

浏览器验收不是截图演示，而是从真实前端点击到真实后端持久化的证据。前置条件：启动生产 Go 入口、React Vite 前端和可用的 SQLite ledger；若 MySQL/Redis/ES/provider 不可用，必须在页面显示 degraded/blocked，不得伪造成功。

| 场景 | 浏览器动作 | 后端断言 | 证据 |
| --- | --- | --- | --- |
| 登录/注册 | 填表、点击、刷新 | JWT 仅用于 header；未授权路由拒绝 | screenshot + network + response status |
| 新建 Session | 点击 `New`，填写标题/目标 | ledger 追加 state；列表读取同一 session | event seq/checksum + screenshot |
| 发消息 | 输入并发送 | `user/message` 追加，返回 event id/seq | REST response + WS event + SQLite query |
| 实时事件 | 打开 WS，触发工具/消息 | ticket owner 校验、cursor 连续、无重复 | WS frames + cursor |
| 刷新恢复 | 刷新页面/重新选择 Session | surface 从 ledger 重放，不依赖内存 | event count/surface screenshot |
| 断线重连 | 关闭网络或 WS，再恢复 | 从最后 cursor replay；慢消费者不静默丢失 | close code + replay frames |
| Checkpoint | 选择事件创建 checkpoint | checkpoint 关联 canonical event hash | response + ledger query |
| Restore | 点击恢复 | 只追加 rewind/state，历史行数不减少 | before/after event count + surface |
| 越权 | 第二用户访问 ID/WS | 404/403，无事件泄露 | response body + no WS frames |
| 长对话 | 生成 200 turns，强制 compact，刷新/重启 | tool pairing、约束、最近事实和 memory 可恢复 | receipt + browser transcript |
| Memory | 会话结束后查看记忆 | source/scope/TTL/confidence 可见；secret 被拒绝 | UI + store row + rejection receipt |
| Subagent | 点击/触发 bounded worker | 独立 context、权限和 worktree，父只收 receipt | worker timeline + child ledger |
| 错误状态 | 停止 provider/ledger 后点击 | 页面显示明确 blocked/error，不显示成功 | HTTP/WS error + screenshot |

验收脚本必须使用真实浏览器；Playwright 只负责点击、输入、刷新、断线和截图，数据库/日志查询作为后端证据，不能以 DOM mock 代替。

## 分阶段实施与删除门槛

1. **阶段 1：Ledger 与架构基线。** 先完成四个 Bug 的 canonical adapter、owner、WS ticket/replay，保留旧 snapshot 只读导入。跨进程恢复和浏览器闭环通过后，才删 snapshot 写路径。
2. **阶段 2：Agent Loop/Context。** Context Ledger、token retention、compact 事件和恢复断言通过后，才删字符截断路径。
3. **阶段 3：Memory。** namespace/ACL、L0/L1/L2、extraction queue、冲突/TTL 和检索轨迹通过后，才删旧 memory 拼接路径。
4. **阶段 4：Subagent/Worker。** durable lease、DAG、worktree、receipt 和真实故障恢复达标后，才删旧调度路径。
5. **阶段 5：UI/发布门禁。** 浏览器逐按钮、200-turn、真实 provider/Docker/WSL2 和官方 scorer 均有新鲜 receipt 后，才删除兼容 adapter。

旧设计、地图、材料和仿冒仓库的删除不属于上述自动删除门槛。需要先向用户提交精确路径、文件数、大小、哈希和用途，再在获得明确确认后执行。

## 当前阻塞

- `configs/server.yaml` 仍包含开发默认凭据表达式，生产启动必须要求外部 secret。
- WebSocket ticket、一次性消费、session/owner 绑定和过期拒绝已有实现与定向测试；仍需补齐真实生产部署下的 header-only 传输与代理日志审计。
- `frontend/node_modules` 属于本地依赖并保持未跟踪；当前 task panel 已展示会话状态、运行 lineage、worker 标识、ledger 记忆事件数与 checkpoint；独立的 worker/memory 时间线与筛选面板仍是后续项。
- `GET /api/v1/sessions/:id/recovery-manifest` 是只读 ledger 派生接口，返回最新 seq/hash、事件与 checkpoint 数、rewind/continuation 计数及最近恢复 marker；前端可导出该清单，但不得把它当第二可写事实源。
- 全局 CORS 为 `*`，不符合生产租户策略。
- Docker daemon/WSL2、Phoenix live trace、真实外部 provider 发布证据和官方 SWE-bench/Terminal-Bench scorer 尚未齐全。
- 用户给定的 `D:\vscode\OpenViking` 不是官方仓库，包含扫描 `.env` 并向 `httpbin.org/post` 外传的 `backdoor.sh`；在用户确认前不删除。
