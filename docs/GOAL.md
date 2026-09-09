# CodeOps-Agent Goal

执行状态：`ACTIVE`；验收状态：`BLOCKED`（2026-09-07 当前快照）

## 当前验证快照（2026-09-10）

本轮修复了 checkpoint 恢复后的续跑状态缺口：`session/rewind` 现在由
`reduceSessionView` 投影为 `paused`，因此即使恢复前会话是 `done` 或仍处于运行态，
恢复后也能通过既有 continuation seam 继续；新增回归测试覆盖
`done -> restore -> paused -> continue` 完整链路。定向 Go 测试、全量 Go 测试、
`go vet ./...`、前端生产构建和 Python 全量测试均通过（`2251 passed, 15 skipped,
3 warnings`）。远端 `release-gate` job 按项目决策保持禁用，因为它依赖仓库外部
provider/scorer receipt；本地验证仍是当前可执行门禁。

本快照仍不能把真实浏览器 200 turns、后端重启/重连 receipt、竞态检查或外部 scorer
指标标为 `VERIFIED`；缺少这些新鲜证据时，整体验收继续保持 `BLOCKED`。

## 历史验证快照（2026-09-07）

本轮代码变更基线为 `5033625e`。P0/P1/P2 的代码路径已完成定向验证：Python
上下文、记忆、工作流、工具和 Agent Loop 全量测试 `2234 passed, 16 skipped`；Go
全量测试 `go test ./... -count=1` 通过；本轮新增 streaming sandbox 作业路径的
定向 `internal/tools`、`internal/jobs`、`internal/sandbox` 测试及全量 Go 回归均通过。生产
Go/Python 跨进程 E2E 的 Agent Loop、Extension、Background Job、Session
Control（含进程恢复）、Legacy Session Import、Skill Manifest 和 Tool Spill
共 `8 passed`。Session Control 恢复测试同时修复了一个由进程终止前在途事件
追加造成的计数竞态，并继续校验 hash chain、连续序列和 surface projection。

本轮将 `JobStart` 接入可选的 `sandbox.StreamingRunner`：Docker/WSL2 后台作业
沿用受限 workspace、网络和资源边界，并保留增量 stdout/stderr、stdin、取消与回收；
仅在配置了同步 sandbox 而没有 streaming 能力时继续 fail-closed。模型流式响应现在
同时记录 provider model identity、response ID 和 fingerprint，避免 receipt 与实际
模型漂移脱节。当前机器仍未提供
可用 Docker daemon 或 WSL2 内 Docker，因此该路径只有契约和本地回归证据，不能标为
真实 Docker/WSL2 E2E。

本快照补齐了真实多进程恢复基准：8 个 Worker 执行 200 个任务，按固定计划注入
30 次进程终止；30/30 次故障均已应用，恢复 `200/200`，成功率 `1.0`，收据状态为
`VERIFIED`，并通过 SHA-256 校验。运行 ID 为
`canonical-20260907-071234`，收据保存在被忽略的 `.tmp/fault-injection/` 下，
不进入版本控制。

本快照再次通过仓库统一 helper 隐藏启动 Docker Desktop，并执行
`Wait-DockerDaemonReady -TimeoutSeconds 60`；探测在 60 秒后超时，`Ubuntu-24.04`
与 `docker-desktop` 仍为 stopped。该结果是新鲜的环境阻塞证据，未启动或清理任何
容器、卷或业务数据。

使用用户提供的 provider 配置完成了一次最小真实 Anthropic-compatible 流式 smoke，
结果为 `OK`，流序列包含 `text -> usage -> done`，响应侧返回了 `reported_model` 和
`response_id`。provider 未返回不可变 revision，因此该结果仍标记为
`identity_verified=false`，只作为 `non_release_dev_smoke`，不能替代跨进程 Harness
E2E 或发布级评测；脱敏 receipt 保存在被忽略的
`.runtime/e2e/provider-live-stream-smoke.json`。

在本快照中又完成了一次显式 opt-in 的真实 provider 跨进程 smoke：生产 Go Agent
通过 gRPC 到达独立 Python Orchestrator，再调用 Anthropic-compatible endpoint，
返回 `REAL_PROVIDER_ROUTE_OK`，并在独立 route receipt 中记录 `reported_model`、
`response_id`、Go/Python 进程边界和退出码。该结果为 `SMOKE_PASS` 且保留
`non_release_dev_smoke=true`；provider 未返回不可变 revision，故
`identity_verified=false`，不能替代发布级 provider、Docker/WSL2 或官方 scorer 证据。

以下发布指标仍保持 `BLOCKED`，不能由上述 deterministic provider、fixture 或开发
smoke 替代：发布级真实外部 provider 跨进程证据、Docker/WSL2 受限执行、Phoenix live trace，
以及官方 SWE-bench/Terminal-Bench scorer。Docker daemon 不可用时，相关 E2E 必须记录为
`BLOCKED`。

当前 HEAD 又完成两条 provider 评测。上下文 Token 对照运行
`context-token-current-20260907`，两臂任务结果均通过，输入 Token 降幅为
`0.9644355046`；provider 未返回稳定 fingerprint，按契约状态为 `BLOCKED`。
Skill 选择运行 `skill-selection-current-20260907`，固定 40 个 Skill、1,000 个案例，
结果为 `573/1000`（`0.573`）；`363` 个案例触发 provider integrity 失败，且
provider fingerprint 缺失，因此状态为 `BLOCKED`。两份完整 receipt 与 checkpoint
均保存在被忽略的 `.tmp/eval-results/` 下，未进入版本控制。

本轮修复了 Anthropic adapter 的工具路由边界：同步与流式请求现在都会把
OpenAI 风格的 `tool_choice=function` 转换为 Anthropic `tool_choice.type=tool`，
并将 `parallel_tool_calls=false` 转换为 `disable_parallel_tool_use=true`；新增
回归测试覆盖两条请求路径。使用代理重跑官方 1,000 案例时 endpoint 持续
`read operation timed out`，未产生可采信的新 receipt，因此 Skill 准确率和发布状态
仍沿用上一个有完整证据的 `573/1000`、`BLOCKED` 快照。

本轮又修复了增量事件读取的 P0 完整性旁路：Go `EventsAfter` 与 Python
SQLite/Redis/MySQL `events_after` 现在会在返回后缀前校验完整 hash chain，早期
payload 被篡改时统一 fail closed；新增篡改前缀回归测试，Python 全量为
`2228 passed, 16 skipped`，Go 全量通过。

本轮补齐 P1 checkpoint 失败语义：ConversationRunner 仅在显式未配置
checkpointer 时返回空恢复状态；SQLite 损坏、校验或类型异常不再被吞掉而误判为
首次运行，避免已有工具副作用被重复执行。新增损坏 checkpoint 契约测试，Python
全量为 `2229 passed, 16 skipped`，Go 全量通过。

本轮补齐 P0 tool-call 收据 ID 契约：provider 未返回 `tool_call.id` 时，编排层按
session、turn、调用序号、工具名和规范化参数生成稳定摘要 ID，替代进程地址生成的
不可恢复 ID；同一请求跨对象/重试保持一致。现在还会在副作用执行前拒绝同一轮中
重复的非空 provider ID，避免多个工具结果写入同一收据键；新增回归测试，Python
全量为 `2231 passed, 16 skipped`，Go 全量通过。

本轮继续补齐 P0 持久化 fail-closed 入口契约：事件存储在会话开始时不可用时，
ConversationRunner 在首次模型调用前直接结束，并返回 `context_persistence_unavailable` /
`context persistence unavailable`，保证模型和工具副作用均不会发生；新增回归测试覆盖
模型调用次数为零。

本轮补齐 P1 checkpoint 写入失败语义：已配置的 LangGraph checkpoint 写入异常会被记录为
`checkpoint_persistence_unavailable`，并在下一次模型或工具轮次前停止；仅“未配置 checkpoint”
继续保留轻量兼容路径。批量工具结果处理中如果 checkpoint 写入失败，也会停止后续结果
投影，避免进入下一轮模型调用。新增失败注入测试，确保写入失败不会继续调用模型或投影。

本轮修复 P2 Python EventLog 增量读取的全局序列游标缺口：SQLite、Redis 和 MySQL
后端现在按事件自身的 `sequence > cursor` 过滤，而不是按当前 session 结果列表下标切片；
跨 session 事件交错时不会漏读后缀，新增回归测试并保持完整 hash-chain 校验。

本次继续复核 provider 评测配置。首次低并发尝试因仓库 `.env.local` 的
`LLM_PROVIDER=openai` 优先于临时 Anthropic 配置，实际误路由到 DeepSeek
兼容 endpoint，完整分母结果为 `0/1000` 且类别为 `provider_call`，不计入准确率。
修正 provider 优先级后，单并发 Anthropic 运行的前 `5` 个案例均真实返回并正确选中
Skill，`reported_model` 为请求模型；provider 仍未返回稳定 fingerprint。并发 `4`
运行在约 `70` 秒仅完成 `12` 个案例，显示当前 relay 吞吐不足以在本机合理时限内完成
正式分母；该运行已中止，未产生可采信的完整 receipt。发布结论继续沿用上一个完整
证据 `573/1000`、`BLOCKED`，不得把部分运行或网络失败当作模型准确率。

本项目的验收标准来自当前任务的 active Goal。历史设计地图、进展日志、
外部记忆和旧计划只用于追溯，不是启动入口或执行权威。

## 目标

把 `CodeOps-Agent` 持续改造成面向代码仓库的多智能体开发协作工具：

- Go Harness 负责鉴权、受约束的 Shell/Git/文件系统/MCP 工具调用和会话持久化。
- Python/LangGraph 编排层负责上下文、计划、记忆、子 Agent 与 Worker 编排。
- SQLite checkpoint、事件溯源、Docker/WSL2 沙箱和 OpenTelemetry 让长任务可恢复、可约束、可审计。
- Redis、MySQL、SQLite、Skills 和统一评测接口作为可替换的持久化、扩展和评测组件。

技术边界为 Python、LangGraph、Go、MCP、Redis、MySQL、SQLite、Skills、
Docker/WSL2 与 OpenTelemetry。新增组件必须进入同一条可恢复、可约束、可审计的
任务闭环，不能成为绕过 Harness、checkpoint、sandbox 或 trace 的旁路。

## 能力验收

- **可扩展 Agent Loop**：主循环与模型/工具解耦，提供稳定插件扩展点；以同等规模
  的真实模块接入工时记录证明接入周期由 2 天级降到数小时级。
- **多 Agent/Workflow**：SQLite checkpoint 编排子 Agent 与 Worker，支持并行、
  流水线和故障隔离；固定 8 Worker 执行 200 个长任务并注入 30 次真实进程故障。
- **Context/Memory**：事件溯源持久化计划、工具调用、文件 diff 与执行结果；按
  P0 目录摘要、P1 节点摘要、P3 原始内容三级按需加载，任务结束生成反思摘要并
  写回可检索、可演化的长期记忆。Token 降幅必须用同任务、同模型、同预算对照。
- **Skill System**：支持注册、发现、按需加载、模型自主选用与用户显式调用；至少
  接入 40 个可运行 Skill，并在锁定的 1,000 案例上评测选型准确率。
- **真实评测集**：SWE-bench、Terminal-Bench 等走统一固定预算和交付契约，调用
  官方 scorer，保留完整输入 pin、失败案例、分母、trace 和 receipt。

## 发布门槛

每项能力都必须同时有源码、定向测试、集成测试和真实运行时 E2E 证据。夹具、
mock、合成 receipt 和开发 smoke 只能标为相应范围，不能替代真实 E2E。

| 指标 | 最低结果 | 计分口径 |
| --- | ---: | --- |
| 模块接入耗时 | `数小时级` | 同等规模模块，从开始到测试/E2E 接通；基线为 2 天级 |
| 进程故障恢复 | `>=98.5%` | 8 Worker、200 个长任务、30 次进程故障；恢复任务数 / 全部任务数 |
| 输入 Token 降幅 | `>=60%` | 同一固定任务、同一模型和预算的前后对照 |
| Skill 规模与准确率 | `>=40` 且 `>=948/1000` | 锁定案例、Skill 版本和 checksum |
| SWE 子集通过数 | `>=18/20` | 相对 8/20 基线；固定预算、官方 scorer、保留所有失败实例 |

真实 receipt 至少记录当前 Git SHA、源码/数据/model pin、预算、run/trace ID、
退出码、分母、产物路径和 SHA-256。任何数据或已验证基线回退都拒绝发布。
`eval_results/` 等完整运行树留在本地或外部不可变存储并默认忽略；只有实际用于
发布结论、已去除敏感信息并完成审核的精简 receipt/manifest 才晋升到
`data/eval/`。晋升件必须保留全部失败分母、外部原始证据引用及其 SHA-256，
不得用清理运行目录的理由删除已发布证据。

## 不可变契约

- PDF 入口统一走 MinerU 显式 OCR；Tika 只用于非 PDF Office 文档。
- BeeAPI/OpenAI relay 的聚合并发上限为 10，HTTP 429 必须有有限退避，
  无法恢复时保持 `BLOCKED`，不得把传输失败计成模型失败。
- `AI_REVIEWED`、`DISPUTED` 与 `HUMAN_REVIEWED` 语义严格分离；只有真实
  真人逐条复核才能产生 `HUMAN_REVIEWED`。
- 评测 prompt、输入和 receipt 不得泄露答案、gold/qrels、patch 或修复提示；
  失败案例和分母始终保留。

## DeepSeek Harness 迁移原则

`CodeOps-Agent` 是唯一改造目标仓库。保留 Go Harness 与 Python Orchestrator 的
职责划分，把 DeepSeek Harness 作为可执行行为规范和测试参考；不复制其
TypeScript package 拓扑，也不以整体语言翻译替代模块设计。本路线覆盖既定
46 项能力清单中除第 23、45、46 项外的 43 项。

迁移以深层模块的稳定接口为边界：

- Go Harness 独占鉴权、权限判定、Session 事件持久化、surface projection、
  Shell/Git/文件系统/MCP 调用、沙箱、进程与 PTY 生命周期以及根 Trace。
- Python Orchestrator 独占 LangGraph Agent Loop、上下文策略、计划、长期记忆、
  子 Agent、Worker/Workflow 和评测编排。
- Go/Python 之间只通过版本化 protobuf/gRPC、事件 schema 和 TraceContext 交互；
  Python 不旁路 Harness 写 Session 主账本，Go 不承载模型编排策略。

每个阶段都采用同一替换协议：

1. 先冻结旧路径的可观察行为并写失败的契约测试。
2. 在新接口后实现深层模块，旧调用方只通过单向兼容 Adapter 进入新路径。
3. 新数据只写新实现；禁止新旧双写和两个可变事实来源。
4. 运行定向测试、完整 Go/Python 测试和真实跨进程 E2E，并生成可审计 receipt。
5. 对比上一份已验证基线；任何数据、正确率、恢复率或安全属性下降都阻止切换。
6. E2E 证明新路径覆盖全部旧调用方且具备失败恢复后，才删除该阶段旧实现；
   兼容 Adapter 仅保留到其调用方完成迁移，不得继续承载独立业务逻辑。

Session 存储采用一次性并行迁移，而不是双写：新会话只写 append-only 事件日志；
旧 SQLite Session snapshot 保持只读。首次恢复旧会话时写入带规范化快照 SHA-256
的单个 `legacy/import` 事件，后续状态只从事件日志演进。导入必须逐字保留旧消息，
不得推测或伪造旧 assistant tool-call；重复恢复必须幂等且校验源 snapshot 未变化。
事件日志是 surface、checkpoint、fork、rewind 和 compaction 的唯一事实来源。

## 分阶段改造计划

| 阶段 | 新深层模块 | 本阶段删除门槛 |
| --- | --- | --- |
| 1 | Go Session Event Log、hash chain、surface projection、旧 snapshot 只读导入 | 新会话和已导入会话均只从事件日志恢复；跨进程重启 E2E 通过后删除 snapshot 写路径 |
| 2 | 插件化 Agent Loop、生命周期事件、正式 LangGraph 图 | 所有模型轮次和工具轮次经新循环及 checkpoint E2E 后删除旧 ReAct 主循环 |
| 3 | Token 感知事务压缩、工具结果预修剪、溢出恢复、手动 `/compact` | 正常压力与强制溢出 E2E 均保持 tool-call/result 配对后删除字符截断式压缩 |
| 4 | Worker/Subagent 调度、SQLite checkpoint、并行/流水线、故障隔离 | 8 Worker、200 长任务、30 次真实进程故障达到 `>=98.5%` 后删除旧调度路径 |
| 5 | Hooks、命令、Skills、插件包和 MCP 生命周期扩展点 | 兼容命令及 MCP 会话 E2E 通过、Skill 选型不下降后删除散落注册路径 |
| 6 | P0/P1/P3 Context、语义摘要、反思、检索和可演化长期记忆 | 同任务同模型输入 Token 降幅 `>=60%` 且质量不下降后删除旧上下文拼接与记忆路径 |
| 7 | 统一权限/沙箱、后台 PTY/Job、取消、输出 spill 与审计 | Windows、WSL2、Docker 的真实进程与拒绝路径 E2E 通过后删除旁路执行器 |
| 8 | Session fork/rewind、统一 Trace、评测和发布门禁 | 43 项能力全部有新鲜 E2E receipt 且最终指标达标后删除剩余兼容 Adapter |

### 阶段 1 验收契约

- 每个事件包含不可变 `session_id`、连续 `seq`、唯一 `event_id`、版本、类型、时间、
  JSON payload、前序 checksum 和自身 checksum；SQLite 事务以期望 `seq` 做并发保护。
- surface 由事件日志纯投影得到，支持追加与范围替换；日志型事件不可进入模型历史，
  被替换节点仍保留在原始日志中，重放结果必须确定。
- 现有 `session.Manager` 公共调用形状作为兼容 Adapter 保留，但状态写入必须进入事件
  接口；持久化失败时内存 projection 不得先行推进。
- 旧 `sessions` 表在导入前后逐字节保持不变。相同 snapshot 只产生一次
  `legacy/import`；checksum 不一致时 fail closed，不能静默重新导入。
- 真实 E2E 必须启动生产 Go 入口写入新会话、终止进程、再启动独立进程恢复，核对
  连续序号、hash chain、surface、计划、权限、undo 和 worktree 状态；另一路从旧
  snapshot 恢复并证明只读导入、幂等以及后续事件可继续追加。
- 阶段 1 的 receipt 必须记录 Git SHA、数据库 schema/version、测试命令、进程退出码、
  两次进程 ID、Session ID、事件数、最终 checksum 和产物 SHA-256，且不得包含密钥。

## 迭代循环

1. 记录当前基线和数据分母。
2. 为下一个可测缺口写失败测试，做一个边界清晰的改动。
3. 运行定向测试、完整 Python/Go 测试和相关真实 runtime E2E。
4. 对照上一份 receipt，确认指标不下降并保留失败证据。
5. 对精确 staged paths 做不输出值的密钥扫描和 diff 审核。
6. 有意义且验证通过的改动自动提交到 `main`、推送 `origin/main`，并核对远端 SHA。

## 当前边界

本快照仍不能把目标指标或完整 Go Harness/LangGraph/Redis/MySQL/MCP 闭环称为
`VERIFIED`，除非仓库中有新鲜的端到端 receipt。人工评审批次在 verdict 对账完成
前不进入版本控制；外部 page-Qrels trust root 由运行环境配置，缺失时发布路径
保持 fail-closed。运行产物、密钥、缓存和机器专属路径不属于仓库源文件。
在上述指标均有新鲜真实运行时 E2E receipt 且不低于既有基线前，验收状态保持
`BLOCKED`；这不暂停迭代执行。
