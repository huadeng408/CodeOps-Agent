# CodeOps-Agent Goal

执行状态：`ACTIVE`；验收状态：`BLOCKED`（整体）。本文件保存当前目标和验收边界；
运行记录按任务保存，只有新鲜、绑定源码的证据才能改变对应验收状态。

## 当前交付目标

正式规格：[Windows 真实代码任务闭环，milestone 1](https://github.com/huadeng408/CodeOps-Agent/issues/1)，
设计状态：`DESIGNED`。先完成本机单用户 Windows 产品，Linux 源码 CI 和官方评测保留。
18 张实施任务已发布，执行入口见[正式任务导航](refactor-tickets.md)；发布任务不代表实现完成。

- 一键启动基础代码 Agent，无 MySQL/Redis/MinIO 时仍能鉴权、查看历史和能力状态；
  RAG、对象存储、远程索引按能力启用。缺执行前提时副作用保持 `BLOCKED`。
- Go 管理隔离 Task Workspace，以允许的当前工作副本为基线，包含未提交改动和
  新源文件，排除凭据和生成目录。保留原仓库与 index 的已有内容。
- 展示 Code Change Proposal 和测试结果，用户确认后由 Go 核对原仓库基线再应用；
  并发变化、部分执行和未知结果保留提案并对账，不能盲重试或静默覆盖。
- 固定版本任务镜像；由 Go 在受控准备阶段从批准的源获取依赖，执行默认离线。
- 初批真实 API 验收批次上限为 100,000,000 tokens（累计输入＋输出），替代
  原人民币 100 元限额；初期一次只运行一个代码任务。重试、反思和子调用计入
  同一批次，缓存输入按 provider 契约计入一次，重启不重置预算。
  usage 无法确认时保留预留并阻断；费用独立如实记录，未知费用不写为零。
- 两个公开仓库：Go 修复、Python 功能各一个，每个至少 10 轮有意义的真实浏览器
  对话。另验取消、进程故障恢复、旧会话重开、重启后空新会话记忆召回和并发冲突。
- 真实反思由 Go 校验并通过 Ledger/CAS 写回；默认同用户、同仓库跨会话检索，
  跨仓库需显式选择或晋升。完整 Go/Python/tool trace 必须从真实后端读回。

首批任务输入、模型、预算、镜像、源码和场景分母在运行前冻结；失败、预算受阻、
缺前提及部分结果全部保留。此 milestone 不代替下列完整产品和发布门槛。

## 当前缺口与状态

清理与规格拆解属于工程工作，不是新一轮 runtime 验证。当前凭据、沙箱和后端
运行条件应在执行准备时检查；旧进程“缺凭据”的记录不能当作本机当前事实。
批准的凭据来源仅用于安全配置运行进程，不进入任务副本、日志、trace 或提交。

### 已完成的实施切片

- **Issue #2 基础启动与历史**：`VERIFIED`，源码 `42ef664a`。
  本机 core 和旧全栈各 8/8 真实浏览器检查，当前提交的 Go 跨进程检查 7/7；
  已验证身份/吊销持久化、owner/ticket、私有 ACL、明确阻断与能力状态。
  收据、完整分母、SHA-256 和 CI 见[本项证据](evidence/local-core-2026-10-09.md)。
  只覆盖此任务；真实模型、预算、代码任务、Trace 和全部后续门禁保持原状态。

- **Issue #3 token 批次**：已按用户要求改为累计 100,000,000 输入＋输出 tokens，
  正式规格和任务已同步。Go Ledger 计量模块及显式 required 模式的调用接入为 `IMPLEMENTED`，支持共享批次、
  CAS 预留/结算、单任务、未知用量及重启；gateway 聚合并发固定为 1，崩溃后的
  未结算预留继续阻断。前台、反思、压缩、Worker 和新 Python 进程已做小规模
  真实调用探针，最终轮 5 次调用/8 项检查通过，另一 Go 进程恢复同一批次；
  五轮共 25 次调用累计结算 75,975 tokens，不重置批次，费用保持 unknown。
  右侧清单接入只读账本额度和具体缺失条件。完整产品默认接入、重试/对账、任务槽
  生命周期、生产入口恢复和完整 Trace 仍为 `BLOCKED`，不能用探针关闭 #3。
  接入源码 `626561fc`，最终 runtime 源码 `bbdb5e17`；浏览器额度/鉴权/重启
  9/9 检查通过，未执行真实代码任务。接口与剩余接入见[token 批次说明](token-budget.md)，
  命令、退出码、完整分母、源码绑定、失败与 SHA-256 见[接入证据](evidence/token-gateway-2026-10-10.md)。
  原计量模块 `db11083b` 的[历史证据](evidence/token-budget-2026-10-09.md)保留。

- **Issue #4 工作副本基线**：只读基线检查及公开 CLI `/worktree baseline`
  切片为 `IMPLEMENTED`，源码 `612daf54`。纳入允许的 dirty/new/empty/deleted
  状态，保留原 index，排除已知凭据/生成路径，限制库存输出并拒绝未批准的
  Git metadata、include 和 aliases。实际构建的 CLI 两进程检查 7/7、退出 0，
  使用隔离 fixture 仓库且不调用模型。复制、独立存储、lease/Ledger、产品准备
  入口、浏览器和完整 Trace 仍 `BLOCKED`；三 UI 预览等用户选择后才改前端。
  [基线说明](workspace-baseline.md)与[本项证据](evidence/workspace-baseline-2026-10-10.md)
  保存完整范围、分母、源码绑定、失败与 SHA-256，不能据此关闭 #4。

| 范围 | 状态及完成条件 |
| --- | --- |
| 首批 Windows 产品 | `DESIGNED`：以正式规格的 AC01–AC09 和批准后的任务拆解交付 |
| 真实代码任务/反思/召回 | `BLOCKED`：缺当前构建的完整浏览器、模型写回、空新会话召回收据 |
| 200 轮与 compaction | `BLOCKED`：至少 200 assistant-turn，记录 event-count、压缩和完整分母 |
| 浏览器连接恢复 | `BLOCKED`：完整 ticket/live/reconnect 矩阵及草稿、幂等、恢复证据 |
| CLI/HTTP 与 Git 恢复 | `BLOCKED`：生产入口完整故障矩阵；管理器双进程集成不替代此项 |
| Context/Memory 质量 | `BLOCKED`：语义检索质量、来源边界及同任务同模型同预算 token 对照 |
| Workflow/Skills | `BLOCKED`：真实 8 Worker/200 长任务/30 故障及 40 Skills/1,000 案例 |
| 多模态 RAG 与评测数据 | `BLOCKED`：真实视觉/独立索引、qrels 污染审计与逐条独立复核；缺外部 page-Qrels trust root 时拒绝发布 |
| Trace/scorer/发布 | `BLOCKED`：成功任务完整 trace 后端读回、官方 scorer 和全部 source-bound 发布收据 |

已有 Loop Registry、P0/P1/P2、Ledger/checkpoint、managed-worktree 和 Memory
接口作为实现接缝复用；代码、定向测试或 fixture 只证明其相应范围。
历史成功和失败均保留在[归档索引](archive/INDEX.md)，不自动继承为当前验证。

## 长期能力验收

- **可扩展 Agent Loop**：主循环与模型/工具解耦，提供稳定插件扩展点；以同等规模
  的真实模块接入工时记录证明接入周期由 2 天级降到数小时级。
- **多 Agent/Workflow**：LangGraph checkpoint 为 Go Ledger 投影，支持并行、
  流水线和故障隔离；固定 8 Worker 执行 200 个长任务并注入 30 次真实进程故障。
- **Context/Memory**：Ledger 持久化计划、工具调用、文件 diff 与执行结果；按
  P0 目录概览、P1 节点摘要、P2 原始内容三级按需加载。任务结束生成有来源的
  反思摘要，写回按任务检索的长期记忆；token 降幅采用固定任务、模型和预算对照。
- **Skill System**：注册、发现、按需加载、模型自主选用与用户显式调用；至少
  40 个可运行 Skill，在锁定的 1,000 案例上评测选型准确率。
- **真实评测集**：SWE-bench、Terminal-Bench 等走统一固定预算和交付契约，调用
  官方 scorer，保留输入 pin、失败案例、完整分母、trace 和 receipt。

## 发布门槛

每项能力同时具备源码、定向测试、集成测试和真实运行时 E2E 证据。夹具、mock、
合成 receipt、开发 smoke 和单独 scorer connectivity 只能证明其命名范围，不能替代真实 E2E。
完整执行契约与七条 receipt lane 见 [release-gate.md](release-gate.md)。

| 指标 | 最低结果 | 计分口径 |
| --- | ---: | --- |
| 模块接入耗时 | `数小时级` | 同等规模模块，从开始到测试/E2E 接通；基线为 2 天级（门禁 >=16h 对 <=8h） |
| 进程故障恢复 | `>=98.5%` | 8 Worker、200 个长任务、30 次进程故障；恢复任务数 / 全部任务数 |
| 输入 Token 降幅 | `>=60%` | 同一固定任务、同一模型和预算的前后对照，质量不下降 |
| Skill 规模与准确率 | `>=40` 且 `>=948/1000` | 锁定案例、Skill 版本和 checksum |
| SWE 子集通过数 | `>=18/20` | 相对 8/20 基线；固定预算、官方 scorer、保留所有失败实例 |

真实 receipt 记录当前或门禁允许的父 Git SHA、源码/数据/model/image pin、预算、
run/trace ID、命令和退出码、完整分母、产物路径和 SHA-256。缺任何必要证据就保持
`BLOCKED`。任何数据、正确率、恢复率或已验证安全基线回退都拒绝发布。
完整运行树留在本地或外部不可变存储并默认忽略；只有已脱敏、已审核、实际用于
发布结论的精简 receipt/manifest 才晋升到 `data/eval/`，保留外部原始证据及 checksum。

普通 push/PR CI 检查 Go、Python、前端源码；发布工作流只手动执行相同的 fail-closed
发布检查，缺收据时返回 `BLOCKED`/退出 3。源码 CI 绿色不代表发布合格。

## 架构与迁移契约

- Go Harness 独占鉴权、授权、Session Ledger、受约束的文件/Shell/Git/MCP、
  沙箱、工作区及 symlink/reparse 校验、进程/PTY 生命周期和根 Trace。
- Python/LangGraph 负责模型策略、Context、计划、Memory、Subagent、Worker/Workflow
  与评测编排；双方只通过版本化 protobuf/gRPC、事件 schema 和 TraceContext 交互。
- Session Ledger 是唯一可变会话事实源；Surface、checkpoint、Memory 索引、fork、
  rewind、compaction 和工作流状态是投影。事件包含连续 seq、唯一 ID、前序与自身
  checksum；事务 CAS 成功后才能推进内存投影。
- 旧 SQLite snapshot 只读导入，原消息逐字保留；单个 `legacy/import` 事件绑定源
  checksum，重复恢复幂等，源变化 fail-closed。旧表不写回，不推测旧 tool-call。
- 新实现接在现有深层模块接口后；旧调用方经单向 Adapter 进入新路径，一次只有
  一个事实写入者或执行者。全部旧调用方有新鲜真实 E2E 和失败恢复证据后才删除旧路径。
- Loop、Context、Workflow、MCP、PTY、Memory 和 Session 替换都需保持旧行为契约，
  新接口定向回归、相关完整测试和 source-bound runtime 证据；回滚保留事件与数据。

## 不可变执行与评测约束

- 权限不明、沙箱/trust root/pin 缺失、越界或副作用未知时 fail-closed，保留可恢复
  `BLOCKED`/`unknown` 和人工对账。模型、插件及 Python 不获得本地副作用权限。
- PDF 统一 MinerU 显式 OCR；Tika 仅处理非 PDF Office。optional 服务保持兼容与数据。
- BeeAPI/OpenAI relay 所有进程和 shard 聚合并发为 1–10，HTTP 429 有界退避；
  不可恢复的传输失败保持 `BLOCKED`，不能计成模型失败。
- `AI_REVIEWED`、`DISPUTED`、`HUMAN_REVIEWED` 分离，只有逐条真实人工复核产生后者。
- 评测输入、prompt 和 receipt 不含答案、gold/qrels、参考 patch 或修复提示；失败分母保留。
- 凭据、原始运行树、数据库和本机配置不进入提交；归档保留历史原文与 checksum。

## 当前执行入口

按 `AGENTS.md` → `AGENT.md` → 本文 → 目标任务源码/测试启动。需要设计边界时读取
`docs/DESIGN-MAP.md` 和一个任务卡；地图及 state 只作导航/规划投影。发布后的正式任务、
依赖和负责人以 GitHub Issues 为准，本地 `.scratch/` 保存链接、草稿及工作笔记。

迭代：冻结基线和分母 → 定向失败回归 → 最小改动 → 相关完整测试 → 真实验收 →
比对 receipt → 精确 staged paths 与不输出匹配值的秘密扫描 → 提交/推送并核对远端。
任务结束报告路径、命令/退出码、artifact/checksum、四态及 blocker；只有完整验收才结束 goal。

追溯旧运行时再打开[2026-10-09 原始日志](archive/receipts/GOAL-log-2026-10-09.md)
（本地归档）或[原始 Git 版本](https://github.com/huadeng408/CodeOps-Agent/blob/1c82efd982842187c0b3ec8333cf817514f2dc91/docs/GOAL.md)。
本轮清理结论见[审计报告](repository-cleanup-2026-10-09.md)。
