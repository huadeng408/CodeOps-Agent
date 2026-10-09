# CodeOps-Agent 开源复用改造路径

日期：2026-10-09。状态：`DESIGNED`。
正式规格已发布为 [GitHub Issue #1](https://github.com/huadeng408/CodeOps-Agent/issues/1)，
分流标签为 `ready-for-agent`；本路径保留设计依据与实施顺序。
用户批准的 18 张纵向任务已发布为 #2–#19，32 条原生阻塞关系及本地投影已建立，
见[正式任务导航](refactor-tickets.md)。任务实现状态仍为 `DESIGNED`。

本路径承接 [125 项复用清单](reuse-landscape.md) 与
[可筛选清单](reuse-landscape.csv)。第一里程碑是 Windows 本机代码产品的真实任务闭环；
完整可靠性、编排、评测与发布目标继续以 [GOAL](GOAL.md) 为验收权威。
设计选择已确定，生产源码改造和 runtime 验收尚未开始。

## 已确定的产品边界

| 编号 | 选择 | 决策记录 |
| --- | --- | --- |
| D01 | 先交付真实代码任务闭环，再推进完整可靠性与官方评测 | 本文里程碑 |
| D02 | Windows 完整产品优先；Linux 保留源码 CI 和官方评测 | 本文验收 |
| D03 | GitHub 保存正式规格与任务，本地 Markdown 保存草稿与工作笔记 | [任务配置](agents/issue-tracker.md) |
| D04 | 基础代码 Agent 独立启动，RAG、对象存储、索引按需启用 | [ADR-0003](adr/0003-start-coding-without-optional-rag-services.md) |
| D05 | 隔离工作树修改与测试，预览后由 Go 应用结果 | [ADR-0004](adr/0004-review-managed-worktree-results-before-application.md) |
| D06 | 首批真实 API 验收单批最多 100,000,000 输入＋输出 tokens，同时执行 1 个代码任务 | 本文预算（2026-10-09 用户修订） |
| D07 | 任务继承权限允许的当前工作副本，保留未提交改动 | [ADR-0004](adr/0004-review-managed-worktree-results-before-application.md) |
| D08 | 固定任务镜像，受控准备依赖，执行阶段默认离线 | 本文执行环境 |
| D09 | Go 修复任务、Python 功能任务各 1 个，每个至少 10 轮浏览器对话 | 本文验收 |
| D10 | 本机单用户、回环监听，保持完整鉴权和身份持久化 | [ADR-0003](adr/0003-start-coding-without-optional-rag-services.md) |
| D11 | 同用户、同仓库跨会话召回；跨仓库使用须显式选择或晋升 | [ADR-0005](adr/0005-scope-repository-memory-to-owner-and-repository.md) |

产品术语见 [CONTEXT](../CONTEXT.md)。架构与信任边界继续遵循
[AGENT](../AGENT.md)、[ADR-0001](adr/0001-go-harness-python-orchestrator-ownership.md)
和 [ADR-0002](adr/0002-single-append-only-session-ledger.md)。

## 现有源码决定的起点

- [HTTP 入口](../cmd/server/main.go) 在打开 SQLite Ledger 前初始化 MySQL/Redis；
  [桌面脚本](../scripts/start-interview.ps1) 启动服务栈。独立启动需要拆分能力初始化、
  身份存储及路由装配，单纯跳过数据库连接不足以完成改造。
- [CLI](../internal/cli/app.go) 已有本地 SQLite 会话接缝；
  [Go SessionRunner](../internal/session/runner.go) 和工具执行边界可复用。
- [Memory Ledger](../internal/memory/ledger.go)、
  [反思演进](../internal/memory/evolution.go) 已有轨迹、提案、CAS 和投影重建路径；
  首批沿用这些实现，向量索引后续按对照结果选择。
- [HeadlessDriver](../eval/driver_headless.py) 直接运行 Python 与本地工具；
  [独立 Terminal-Bench Agent](../eval/swebench_work/deepseek_tb_agent.py) 另有执行链。
  产品验收及后续正式评分必须明确经过 Go Harness 的实际产品入口。

这些源码接缝证明实现存在；真实能力仍需下面的新鲜验证。

## 第一里程碑的执行切片

下表按改造领域说明接缝与回退，不是最终工单边界。正式工单按 `to-tickets` 拆成
可独立演示的纵向闭环，批准后的工单以 GitHub 为准。调用方范围包括
HTTP、CLI、continuation、child/workflow 与 Hook/MCP；仅按本片涉及范围迁移和测试。

| 顺序 | 改造与复用 | 完成条件 | 回退方式 |
| --- | --- | --- | --- |
| 0：冻结基线 | 固定产品源码、两份公开任务输入、仓库 commit、模型与环境；准备真实产品验收入口 | 输入不含参考答案或补丁；原始失败、版本与完整分母可追溯；费用、沙箱和 trace 前置条件明确 | 保留原始输入与失败记录 |
| 1：安全修改与恢复 | 复用 Go 文件原子写入、managed worktree、RestoreCodeChanges；补文件存在状态与根边界；Hook 参数用 argv/JSON 而非 shell 插值 | 覆盖空文件与不存在、create/delete/rename、链接与路径竞态、用户并发改动、部分应用崩溃；CLI/HTTP 应用结果均经 Go 授权与 Ledger | 禁用新应用入口，保留隔离结果和账本事件，未知副作用进入对账 |
| 2：独立产品启动 | 从现有 server/CLI 分离本机 core profile；复用现有 Go 鉴权、SQLite、进程管理；服务能力明确启用 | MySQL/Redis/MinIO 未运行时仍可鉴权、选择仓库、查看历史和能力状态；执行条件缺失时给出明确 BLOCKED；旧全栈配置仍可用 | 路由回既有 profile，保持历史数据与 owner 映射 |
| 3：任务执行环境 | 复用现有 Docker/WSL Runner，制作固定版本的 Go/Python/Node 任务镜像；受控准备依赖 | 编译与测试能真实运行；目录、网络、超时、环境清理与取消仍由 Go 强制；工具链与镜像 pin 可核对 | 切回已批准镜像；保存工作树，拒绝不确定的执行 |
| 4：模型与工具接入 | 在现有 LLMClient/Registry/Go MCP Manager 接缝使用官方 SDK；先复用现有计量与检索，按收益引入 ripgrep/解析器 | 模型、工具、超时、取消、畸形输入和 provider 错误覆盖原调用方；P0/P1/P2 按需加载且原始内容经 Go Read；成本可核验 | 保留单向兼容 Adapter，切换唯一活跃实现 |
| 5：反思、召回与 trace | 沿用 Ledger 反思提案和 Experience CAS；复用 OTel；验证旧会话与空新会话两种重启行为 | 真实模型反思写回、来源可核对、重启后按任务召回；成功代码任务的 Go/Python/tool trace 可在后端 readback | 保留账本与提案，禁用有问题的投影或检索 Adapter |
| 6：界面与便携交付 | 沿用已选界面方向与 React；复用 Markdown/diff 部件，打包已有 Go/Python，移除启动器机器路径 | 能查看工具、diff、权限、取消与恢复；Windows 一键启动不依赖开发者 PATH；前端 tests/build 和真实浏览器验收通过 | 回退资源和启动路由，保留 Session 与工作树 |

对应的安全写入与恢复条件是启用该类原仓库写入前的前置条件。身份与旧数据兼容通过后，
才把新启动路径设为默认。界面和打包可在接口稳定后并行准备，runtime 验收等主链路齐备后执行。
前端调整遵循仓库的截图预览、用户选择及浏览器检查流程。

## 首批复用范围

下列是清单中的接入候选与已有部件；实际引入前固定兼容版本、许可证和调用方。

| 清单 ID | 接入方式 |
| --- | --- |
| H07/H08/H09/H19 | 优先使用 os.Root、现有原子写入与恢复模块、Go argv/JSON 和 Runner；保持现有更严格的 no-symlink/reparse 策略 |
| H21 | 借用语言镜像配方，保留本仓沙箱参数、trust root 与镜像批准流程 |
| O01/O04/O05 | 官方 provider SDK 通过现有 client 适配；现有 router、TokenBudget 与 Go 协调门控继续复用 |
| H16 | 官方 Go MCP SDK 仅替换协议与传输，Go 保留鉴权、权限、工作区与审计 |
| H11/O11/O12 | ripgrep、结构摘要、model-aware tokenizer 按实际收益接入；检索与摘要始终保留来源和 revision |
| H39/P22 | 现有 OTel SDK 与必要 Collector 组件；trace 收据包含真实 backend readback |
| P03/P10 | Markdown 渲染与只读 diff 部件，沿用现有 React Surface |

每类保留一个活跃执行或写入实现。新增外部库优先经过现有接口；已有 helper 和标准库能
满足验收时直接复用。Node/Rust 的协议、测试和算法可以借鉴，运行主体继续保持 Go/Python。

## 执行环境、数据与预算

- 基础界面启动与有副作用的执行就绪分别判断。任务镜像、trust root、权限或工作区
  pin 缺失时，执行明确 BLOCKED，不以宿主无约束执行代替。
- Go 从权限允许的当前工作副本构造任务基线；应用提案前检查文件存在状态、内容、
  路径和相关仓库状态。冲突保留结果；部分应用的 intent/outcome 与 unknown 由 Ledger 记录。
- 凭据、生成目录和未授权路径不进入任务副本。依赖获取在批准的准备阶段执行，
  固定来源与版本，任务执行默认网络关闭；Docker 操作使用现有 readiness helper。
- 首批真实 API 验收单批上限为 100,000,000 tokens（累计输入＋输出），同时执行 1 个代码任务；按 2026-10-09 用户要求替代原人民币 100 元限额。
  先确认调用用量上界并预留，再按实际 usage 结算，包含重试、反思和子 Agent；
  usage 无法可靠确认时保留预留并进入 unknown/BLOCKED。价格独立记录，未知费用
  不写为零；该 token 限额不代表人民币费用上限。
- BeeAPI/OpenAI relay 的跨进程/shard 合计并发继续保持 1–10，429 使用有界退避。
  达到预算或无法恢复时保存完整结果，不以已通过子集替代分母。
- 本机身份、owner/session/ticket 与旧用户映射保持 Go 强制。旧快照只读导入，
  使用源 checksum 和单向 Adapter；迁移与回退都保留原数据。
- Memory 默认同用户、同仓库，先复用现有词法检索与 Ledger 反思；向量检索、语义质量
  与 token 对照进入后续阶段。Python checkpoint、索引和界面缓存继续作为投影。

## 第一里程碑的真实验收

核心分母为两份公开仓库任务：一个 Go 修复任务、一个 Python 功能任务。
执行前由 Agent 只读选取并固定仓库、commit、任务输入与正常项目测试；不注入参考修复。

- 每个任务至少 10 轮有实质内容的浏览器对话，合计至少 20 轮；前端连接真实 Go/Python/provider。
- 验证真实文件修改、项目测试、diff 预览、授权应用，以及用户已有改动的保留。
- 分别验证取消、任务故障恢复、Go/Python 重启后的旧会话恢复、空新会话 Memory 召回、
  原仓库并发改动冲突拒绝。规格拆分时为每个场景列明预期结果与完整分母。
- 成功代码任务核验完整 Go/Python/tool trace 与后端 readback；故障时保留原 trace
  缺失或 partial/unknown，不用重建的漂亮轨迹覆盖失败证据。
- 每份 receipt 记录源码 SHA、模型/数据/镜像 pin、命令与退出码、完整分母、
  run/trace ID、artifact 和 SHA-256；预期故障、真实失败、预算受阻分别记录。
- 定向回归、相关完整 Go/Python 测试、diff 检查、frontend tests/build 与真实 E2E
  逐级执行；fixture 或独立第三方 Agent 的结果保留其原分类。

第一里程碑通过后可认定这条产品链路具备对应证据；整体验收和生产发布仍由 GOAL 与
[release gate](release-gate.md) 判断。200 轮和官方 scorer 的缺口保持原状态。

## 第一里程碑之后

| 阶段 | 继续推进的工作 | 进入或完成门槛 |
| --- | --- | --- |
| 可靠性 | 200 轮、compaction、完整 ticket/live/reconnect、CLI/HTTP crash 与 Git restore 矩阵 | 当前源码、真实产品入口、完整失败分母与恢复证据 |
| 语义 Context/Memory | 结构检索、向量 Adapter、任务相关性、来源校验、同任务 token 对照 | 同模型/预算/初始仓库的双臂结果支持收益，唯一权威写入不变 |
| 编排与扩展 | 真正可执行代码的 child/Worker、40 Skills、8 Worker/200 长任务/30 故障 | 子任务真实执行与回收，lease/checkpoint/CAS/预算及 trace 证据完整 |
| 官方评测与发布 | 本产品的 Terminal-Bench、SWE-bench 和其他官方 scorer；SBOM、许可证与发布工件 | 当前源码绑定的正式结果满足 GOAL 门槛，release gate 通过 |
| 有条件的重服务迁移 | 向量/全文、对象存储、缓存、调度器等候选替代 | 实测瓶颈、对照收益和运维成本支持替换；备份、恢复和单向迁移验证通过 |

后续技术选择在其事实前置条件具备后决定。完整候选仍保留在复用清单中，每批按
可验收的产品行为选择对应组件，保持小改动和可回退。

## Matt 技能的执行顺序与当前交付

1. `setup-matt-pocock-skills`：已写入配置，提交为 `80d9d62d`。
2. `grill-with-docs`：11 项用户选择、词汇与 ADR 已记录，用户继续进入规格化。
3. `to-spec`：正式规格已发布为 Issue #1，本地保留链接与投影，正文与标签 readback 通过。
4. `to-tickets`：按切片拆分依赖和验收，每张任务标出源码、调用方、测试、迁移与回退。
5. 实施：按前沿任务执行；先最小复现，再最小改造、真实验收和审查后提交推送。

本轮无生产源码改造、无真实 API 或 E2E 运行。尚待执行前核实的事实包括两份具体
任务输入、provider 有效性与费用计量、批准的任务镜像、隔离环境中的 Windows 打包能力。
这些事实在切片 0/3 核实，缺新鲜证据时保留对应 BLOCKED。
