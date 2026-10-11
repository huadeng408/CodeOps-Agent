# CodeOps-Agent Goal

执行状态：`ACTIVE`。当前 Windows 产品 milestone 和完整发布目标尚待验收。
各任务按声明范围分别记录单测、集成、产品与发布结果；工作约定见
[AGENTS.md](../AGENTS.md)。
完整发布验收状态：`BLOCKED`。发布所需的真实运行时 E2E 和七条 lane 证据仍待齐备；
单测和集成不能替代真实 E2E。该状态只描述完整发布，普通任务按其声明范围验收。

## 当前目标

正式规格：[Windows 真实代码任务闭环，milestone 1](https://github.com/huadeng408/CodeOps-Agent/issues/1)。
先完成本机单用户 Windows 产品，Linux 源码 CI 和官方评测保留。
任务与依赖以 [GitHub Issues](https://github.com/huadeng408/CodeOps-Agent/issues) 为准，
[正式任务导航](refactor-tickets.md)供查找。

- 一键启动基础代码 Agent，鉴权、历史和能力状态独立于 optional RAG／对象存储／远程索引；缺执行前提时副作用明确受阻。
- Go 从允许的当前工作副本准备隔离 Task Workspace，包含未提交改动和新源文件，排除凭据与生成目录，保留原仓库和 index。
- 展示 Code Change Proposal、diff 和真实测试结果；用户确认后由 Go 核对原基线再应用。并发变化、部分执行及未知结果保留并对账。
- 使用固定版本任务环境；Go 在受控准备阶段从批准源获取依赖，任务执行默认离线。
- 初批真实 API 验收上限为累计 100,000,000 输入＋输出 tokens，初期同时运行一个代码任务。重试、反思、子调用计入同一持久批次；未知 usage 保留预留并阻断，费用独立记录。
- 冻结两个公开仓库任务：Go 修复与 Python 功能各一个，每个至少 10 轮有意义的真实浏览器对话；另验取消、进程故障恢复、旧会话重开、重启后空新会话记忆召回和并发冲突。
- 真实反思由 Go 校验并经 Ledger/CAS 写回；默认同用户、同仓库跨会话检索，跨仓库须显式选择或晋升。产品 Go/Python/tool Trace 从真实后端读回。

## 当前缺口

| 范围 | 仍需完成的验收 |
| --- | --- |
| #3 调用入场与预算 | 完整产品默认接入、任务槽生命周期、重试／未知用量对账和入口恢复；计量模块证据不代替这些行为 |
| #6 执行环境与基线 | 用户已选 OpenSandbox、批准固定 pytz wheel 和 A 右侧卡片；接入票 [#21 Go 基线](https://github.com/huadeng408/CodeOps-Agent/issues/21) → [#22 Python 基线](https://github.com/huadeng408/CodeOps-Agent/issues/22) → [#23 取消／重开／CLI](https://github.com/huadeng408/CodeOps-Agent/issues/23) 已发布，按依赖实施。仍需实际运行环境、受控依赖准备、产品入口和两个公开项目完整基线 |
| 模型代码任务与提案 | 真实仓库问答、Go 修复／Python 功能、可审查提案、受批准应用和结果对账 |
| 取消、恢复与 Memory | 完整产品故障场景、真实反思写回、重启后的空新会话召回及来源边界 |
| Windows 便携交付 | 脱离开发者 PATH 的一键 EXE 和首批完整浏览器验收 |
| 专项与发布 | 下列长期能力和发布指标的对应真实证据；源码 CI、fixture 和独立探针只证明各自范围 |

当前凭据、沙箱和后端前提在具体运行准备时核对；旧环境错误不作为本机当前事实。
首批源码、输入、model/image pin、预算和场景分母在执行前冻结，保留失败与部分结果。

## 证据链接

以下链接保存各自声明范围、当轮源码、命令、分母、失败及收据；需要追溯时读取。

- 本机核心启动与历史：[范围证据](evidence/local-core-2026-10-09.md)。
- Token 批次：[接口与缺口](token-budget.md)、[接入证据](evidence/token-gateway-2026-10-10.md)。
- Task Workspace 准备与离线镜像边界：[范围证据](evidence/workspace-offline-2026-10-11.md)。
- Runner 输出、生命周期与工具链：[范围证据](evidence/task-runtime-2026-10-11.md)。
- OpenSandbox 选型依据：[固定源码调研](sandbox-options-2026-10-11.md)；当前采用用户后续选择的 OpenSandbox。
- OpenSandbox Runner／任务槽：[基础接口证据](evidence/opensandbox-foundation-2026-10-11.md)；实际部署与产品基线仍待验收。
- Windows Docker 启动恢复：[范围证据](evidence/docker-startup-2026-10-10.md)。
- 工作指令与验收约束：[范围证据](evidence/instruction-policy-2026-10-11.md)。
- 旧测试数量、旧 blocker 和施工过程：[原文快照](archive/receipts/GOAL-log-2026-10-11-before-instruction-consolidation.md)、[历史索引](archive/INDEX.md)。

## 长期能力与发布目标

| 专项 | 当前目标 |
| --- | --- |
| 可扩展 Agent Loop | 在同等模块范围，以现有 2 天级基线对照插件接入达到数小时级；记录实际接入工时 |
| 多 Agent／Workflow | 8 Worker、200 个长任务、30 次真实进程故障，恢复率至少 98.5%；checkpoint 为 Go Ledger 投影 |
| Context／Memory | P0／P1／P2 按需加载；同任务、同模型、同预算对照，输入 token 降幅至少 60% 且质量不回退 |
| Skills | 至少 40 个可运行 Skill，在锁定的 1,000 案例上至少 948 项正确 |
| 官方代码评测 | 固定预算及官方 scorer；SWE 锁定 20 项至少 18 项通过，对照固定 8/20 基线 |
| 其他产品验收 | 200 assistant-turn／compaction、浏览器 reconnect、CLI／HTTP／Git 恢复、多模态 RAG 和真实评测数据仍保留其完整专项验收 |

完整发布契约与七条 receipt lane 见 [release-gate.md](release-gate.md)，指标与分母保留。
单测、集成、产品验证和发布合格分别记录；任务完成不自动代表整个产品或发布合格。
