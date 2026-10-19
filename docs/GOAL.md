# CodeOps-Agent Goal

执行状态：`ACTIVE`。Windows milestone 1 的首批范围已按 #19 验收；完整发布目标继续推进。
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
- 初批真实 API 使用服务商已设置的 100,000,000-token 硬上限，初期同时运行一个代码任务。重试、反思、子调用计入同一持久批次；本地保留已确认用量、未知 usage 和原预留，计量缺口不阻塞新的独立调用，费用独立记录。
- 冻结两个公开仓库任务：Go 修复与 Python 功能各一个，每个至少 10 轮有意义的真实浏览器对话；另验取消、进程故障恢复、旧会话重开、重启后空新会话记忆召回和并发冲突。
- 真实反思由 Go 校验并经 Ledger/CAS 写回；默认同用户、同仓库跨会话检索，跨仓库须显式选择或晋升。产品 Go/Python/tool Trace 从真实后端读回。

## 当前缺口

| 范围 | 仍需完成的验收 |
| --- | --- |
| Memory 与故障专项 | 完整长期故障、长对话与恢复矩阵；#19 的五类首批产品场景已按原范围验收 |
| 可选 Cloudflare #20 | 按用户要求暂缓；实际 Free 账户、部署目标及条件能力仍待独立验收，不作为 Windows 首批 milestone 的门禁 |
| 专项与发布 | 下列长期能力和发布指标的对应真实证据；源码 CI、fixture 和独立探针只证明各自范围 |

当前凭据、沙箱和后端前提在具体运行准备时核对；旧环境错误不作为本机当前事实。
首批源码、输入、model/image pin、预算和场景分母在执行前冻结，保留失败与部分结果。

## 证据链接

以下链接保存各自声明范围、当轮源码、命令、分母、失败及收据；需要追溯时读取。

- 本机核心启动与历史：[范围证据](evidence/local-core-2026-10-09.md)。
- Token 批次：[接口与缺口](token-budget.md)、[接入证据](evidence/token-gateway-2026-10-10.md)、[默认边界验证](evidence/model-default-admission-2026-10-12.md)。
- 本机模型工作区准入：[范围证据](evidence/model-local-workspace-2026-10-12.md)。
- 代码任务显式结束：[HTTP／CLI 结束边界与右侧入口证据](evidence/model-task-finish-2026-10-12.md)。
- 模型占用恢复：[真实进程退出、Linux namespace 与生产 HTTP 边界证据](evidence/model-scope-recovery-2026-10-12.md)。
- 可结算有界重试：[Go／Python 计量、退避和 Ledger 拒绝边界证据](evidence/model-accounted-retry-2026-10-12.md)。
- 新消息准入：[草稿保留、幂等与真实运行／人工零用量对账记录](evidence/model-message-preflight-2026-10-12.md)。
- 默认模型读取：[反思期限、只读 Git 上下文、真实读取链与未结算反思](evidence/model-runtime-read-2026-10-12.md)。
- 发送入口恢复：[具体拒绝原因、草稿与同请求恢复的前端范围证据](evidence/model-entry-recovery-2026-10-13.md)。
- 歧义模型用量：[重复计费字段拒绝、完整预留与后续派发阻断证据](evidence/model-ambiguous-usage-2026-10-13.md)。
- 重试等待字段：[重复 Retry-After 停止自动重试与已结算用量保留证据](evidence/model-retry-after-fields-2026-10-13.md)。
- 重试字段数量：[split-date／空字段拒绝及上轮覆盖缺口的纠正证据](evidence/model-retry-after-arity-2026-10-13.md)。
- 真实 Go 提案与预算续行：[受控产品演示、完整失败分母、冻结输入与原仓库保护证据](evidence/model-proposal-budget-2026-10-13.md)。
- #7 隔离提案范围收口：[原验收映射、当前指纹与生产 HTTP／分组 OpenSandbox 回归](evidence/proposal-closeout-2026-10-14.md)。
- #8 单文件用户决策：[Go 授权／独占写入、重放／unknown 与实际浏览器确认／拒绝证据](evidence/proposal-application-2026-10-14.md)。
- #9 文件生命周期：[四类文件修改、逐项 Ledger／部分失败及实际产品批准证据](evidence/file-lifecycle-2026-10-15.md)。
- #10 应用中断与并发保护：[逐项核验、完整中断分母、生产浏览器与新基线再次批准证据](evidence/application-recovery-2026-10-15.md)。
- #11 仓库 MCP：[固定只读能力、Go 授权／停用边界及真实模型与浏览器证据](evidence/mcp-product-2026-10-15.md)。
- #12 代码任务取消：[Go fence、资源归属、provider/tool/approval 实际取消与结果留存证据](evidence/task-cancellation-2026-10-15.md)。
- 模型 scope 并发释放：[持久释放窗口复现、精确撤销及 Go 回归](evidence/model-scope-release-2026-10-15.md)。
- #13 任务故障与重开：[真实任务故障、Go/Python 重启、同一旧 Session 续行及严格拒绝证据](evidence/task-recovery-2026-10-15.md)；[源码 CI 异步取消回归修正](evidence/task-recovery-ci-2026-10-15.md)。
- #14 Memory：[同用户同仓库、来源完整性与旧身份的源码验证](evidence/memory-scope-2026-10-15.md)；[真实反思写回、进程重启后空新会话召回与跨仓库拒绝](evidence/memory-product-2026-10-15.md)。
- #15 产品 Trace：[真实浏览器代码任务、Go/Python/provider/tool/反思整树后端读回与拒绝边界](evidence/product-trace-2026-10-15.md)。
- 待审批工具取消：[源码与失败留存](evidence/pending-tool-cancellation-2026-10-18.md)、[真实批准／取消与完整 Trace 专项](evidence/pending-tool-cancel-product-2026-10-18.md)已按各自范围验证；该专项不计入正常开发轮次。
- OpenSandbox 长命令与停止核验：[源码与十二次尝试的未知执行](evidence/opensandbox-execution-deadline-2026-10-18.md)、[真实长命令及同账号浏览器结束恢复](evidence/opensandbox-deadline-product-2026-10-18.md)；原失败、#17 与发布缺口分别保留。
- Go 新原任务：[真实修改、项目测试、批准应用及同任务 Memory](evidence/go-browser-task-memory-2026-10-19.md)已按范围验证；该轮 Trace 父链缺口及原不完整结果保留，后续完整任务见下方 v5 收口。
- 失败轮次反思父链：[公开 Runner 接缝复现与最小上下文修复](evidence/failed-reflection-trace-2026-10-19.md)、[真实失败／完成两轮专项](evidence/failed-reflection-runtime-2026-10-19.md)已按范围验证；专项未计入正常开发轮次，原任务的完整 Trace 缺口仍保留。
- 新 Go 正常任务与停止后恢复：[三次上游失败、完整计量分母、实际结束及旧 Session 重开](evidence/go-browser-task-outage-2026-10-19.md)；新代码任务未完成，未知用量及原预留保留。
- 待补反思的恢复父链：[终态引用、SQLite 重开与公开 Runner 回归](evidence/memory-recovery-trace-2026-10-19.md)；源码验证与新的真实模型恢复结果分别验收。
- Go 正常任务收口：[v5 真实浏览器任务、最终回归、两文件批准应用、同任务 Memory 与全部轮次的完整 v2 Trace](evidence/go-browser-task-trace-v5-2026-10-19.md)；旧失败、拒绝和 legacy FAIL 保留，#19 与发布继续独立验收。
- 多轮 Trace 验证器：[完整轮次、父链、调用分母及部分读回留存的源码／集成证据](evidence/product-task-trace-source-2026-10-15.md)；验证器与两个公开任务的 runtime 证据分别验收。
- 受管代码任务工具额度：[两次真实提前停止、有限额度修复及公开协议／当前源码 CI 证据](evidence/harness-tool-allowance-2026-10-16.md)；真实十轮任务继续独立验收。
- Windows 文件编辑换行：[真实 Read/Edit 缺口、精确匹配保护、源码回归及新构建证据](evidence/windows-edit-crlf-2026-10-16.md)；该修复与真实多轮任务结果分别绑定。
- 并发工具入账：[五并发 CAS 缺口、Runner 协调、源码回归及新构建证据](evidence/parallel-tool-ledger-2026-10-16.md)；#17/#18 十轮任务继续独立验收。
- 修复后的真实 Go 尝试：[两轮完成、参数错误、浏览器恢复与完整失败分母](evidence/browser-task-iteration-2026-10-16.md)；同时确认 Python 提案入口的当前缺口。
- Python 冻结提案：[离线依赖绑定、同一 Runner／应用边界、受控 runtime 与新便携包](evidence/python-proposal-2026-10-16.md)；该实现与正式多轮任务分别验证。
- 已知工具结果后的重复命令：[Python 尝试的完整失败分母、Runner 修复、旧任务显式结束及新冻结输入](evidence/repeated-command-2026-10-16.md)。
- Python 真实多轮任务：[模型修改、项目测试、实际提案批准应用和原副本保护](evidence/python-browser-task-2026-10-16.md)；[后续同任务反思／v2 Trace 对账](evidence/trace-integrity-v2-2026-10-16.md)，完整失败和冻结场景分母保留。
- Go 早期多轮任务：[十轮完成、完整失败分母、项目测试、实际提案应用及未通过的 Memory／Trace 范围](evidence/go-browser-task-2026-10-16.md)；该轮失败保留，后续 v5 完整收口独立绑定。
- 取消后的 Memory 与 provider 分类：[终态投影、未知副作用拒绝和固定公开错误的源码验证](evidence/memory-cancellation-classification-2026-10-16.md)；[同会话取消后真实反思写回及跨进程 fixture 修正](evidence/memory-cancellation-product-2026-10-16.md)。源码／产品分类与正式多轮任务分别验收。
- #19 早期对账：[五类恢复场景、文件／兼容矩阵、AC01–AC09 和当轮缺口](evidence/milestone-reconciliation-2026-10-16.md)；当时未通过的结论保留，当前状态见下方首批验收结论。
- #19 v5 早期补验：[旧 Session 重开、空新会话的原经验检索、完整失败与边界／物料核对](evidence/milestone-v5-recovery-2026-10-19.md)；该轮最终模型答复失败保留，后续矩阵和许可核验见下方首批验收结论。
- Windows 随包材料：[四个 Python 包的固定许可材料、拒绝边界、完整回归与实际离线组装](evidence/windows-python-notices-2026-10-19.md)；许可组装与其后产品／整体物料验收分别记录，完整发布范围保留。
- #19 首批验收通过：[五类恢复、完成的空新会话 Memory 答复及故障 Trace 缺口](evidence/milestone-v5-matrix-2026-10-19.md)、[文件／兼容、full-stack、许可包重开和 AC01–AC09](evidence/milestone-v5-compatibility-2026-10-19.md)；旧失败、unknown、父规格和完整发布范围保留。
- #20 可选静态预览：[独立构建、无 API 的本机浏览器及普通登录 fixture 回归](evidence/cloudflare-static-preview-2026-10-19.md)；账户／Free 计划、线上部署与其他条件能力分别待验收，不改变首批 milestone 结论。
- #24 浏览器重连：[双标签页漏事件复现、当前页游标修复、浏览器 UI 与真实 Go 本机兼容回归](evidence/browser-reconnect-2026-10-19.md)；完整长对话、真实 Go 双标签页故障与发布分别验收。
- #25 真实产品重连：[v7 便携包、双标签页断线期间的真实模型／工具结果、当前游标补齐与 Go/Python 空闲重启](evidence/browser-reconnect-product-2026-10-19.md)已按六项声明范围验收；两次用户请求、六次实际调用及完整失败／计量分母保留，完整长对话和发布仍独立推进。
- Trace 读回的 unknown 边界：[已停止调用允许采集、最终结算门禁及完整回归](evidence/trace-readback-unknown-2026-10-16.md)；原严格 Trace 结果保留，采集与验收状态分别记录。
- 工作区续租竞争：[正常 Ledger 并发写入、有限重读及安全拒绝回归](evidence/task-lease-contention-2026-10-16.md)；原十轮任务的 Memory／Trace 缺口保留。
- 沙箱 Git 元数据：[自包含 HEAD 快照、凭据／旧 lease 边界及真实浏览器四项只读 Git 验证](evidence/portable-task-git-2026-10-16.md)；原任务失败及完整 Trace／Memory 缺口分别保留。
- 恢复执行的 Trace 采集：[全部实际尝试读回、原严格拒绝与真实后端完整树证据](evidence/recovered-trace-readback-2026-10-16.md)。
- 已批准的 Trace 完整性 v2：[原构建／新分析器独立绑定、三批真实后端读回及 Python #18 范围对账](evidence/trace-integrity-v2-2026-10-16.md)；Python 32 条同任务经验与完整 Trace 已核对，Go 的真实 unknown／缺失仍保留，父规格与发布分别验收。
- #16 Windows 便携包：[迁移目录／空 PATH、真实登录问答、组件 pin 与所属进程回收证据](evidence/portable-windows-2026-10-15.md)。
- Task Workspace 准备与离线镜像边界：[范围证据](evidence/workspace-offline-2026-10-11.md)。
- Runner 输出、生命周期与工具链：[范围证据](evidence/task-runtime-2026-10-11.md)。
- OpenSandbox 选型依据：[固定源码调研](sandbox-options-2026-10-11.md)；当前采用用户后续选择的 OpenSandbox。
- OpenSandbox Runner／任务槽：[基础接口证据](evidence/opensandbox-foundation-2026-10-11.md)。
- OpenSandbox 本地构建镜像：[身份校验证据](evidence/opensandbox-image-pins-2026-10-12.md)；后续 Docker 归组与执行核对见[分组范围证据](evidence/docker-grouping-2026-10-13.md)。
- OpenSandbox Go 产品基线：[实际部署、浏览器、故障／重开和 Trace 证据](evidence/opensandbox-product-2026-10-12.md)。
- OpenSandbox Python 产品基线：[固定依赖准备、离线完整测试、保护／重开和 Trace 证据](evidence/opensandbox-python-2026-10-12.md)。
- OpenSandbox 取消、故障恢复与 CLI：[范围证据、完整分母和不可变收据](evidence/opensandbox-lifecycle-2026-10-12.md)。
- Agent 取消终态：[Go 竞态修复证据](evidence/agent-cancellation-2026-10-12.md)；完整 runtime 取消仍按对应票验收。
- Windows Docker 启动恢复：[范围证据](evidence/docker-startup-2026-10-10.md)。
- 工作指令与验收约束：[范围证据](evidence/instruction-policy-2026-10-11.md)。
- 指令、导航与门禁清理：[八项修改及验证范围](evidence/policy-cleanup-2026-10-14.md)；真实仓库问答与重试缺口见[当轮证据](evidence/repository-qa-2026-10-14.md)。
- 仓库只读缓存与错误分类：[外部私有缓存、完整运行分母及当前问答／重试缺口](evidence/repository-spill-2026-10-14.md)。
- 模型失败后的续行恢复：[Go 准入 head 前移、公开 gRPC 回归与 provider 契约边界](evidence/model-continuation-2026-10-14.md)。
- 请求长度与完整计费上界：[可选 Go 总量 profile、旧限制保留与源码验证](evidence/model-total-limit-2026-10-14.md)。
- 真实仓库两轮只读问答：[第一问完成、第二问失败、八项场景分母与同批次计量](evidence/repository-qa-product-2026-10-14.md)。
- 工具轮次上限：[具体失败分类、无工具收尾与缓存展示的公开协议回归](evidence/model-round-limit-2026-10-14.md)。
- 真实问答续行：[原问题、409 冲突、获准续行的完整失败分母与同批次保护证据](evidence/repository-qa-continuation-2026-10-14.md)。
- 模型传输失败分类：[Go timeout／unknown 预留、失败投影及 Python 协议接缝证据](evidence/model-gateway-classification-2026-10-14.md)。
- 真实失败分类与恢复：[工具轮次／timeout、启动草稿恢复、代理续行与全部失败分母](evidence/repository-qa-classified-2026-10-14.md)。
- 有界续行恢复：[累计游标、同 run 额度、待执行请求重放与公开协议回归](evidence/continuation-rounds-2026-10-14.md)。
- 有界续行的真实仓库验证：[实际游标推进、第一问质量缺口、第二问失败与完整调用分母](evidence/continuation-product-2026-10-14.md)。
- 续行入场与工作区续租：[入队前拒绝、当前游标、活跃反思占用及同一保留工作区的 Go／HTTP 回归](evidence/continuation-admission-2026-10-14.md)。
- 准入恢复后的真实浏览器验证：[两问回答、过期续租、独立补问失败与任务结束恢复的完整分母](evidence/continuation-recovery-product-2026-10-14.md)。
- Go 控制的模型期限：[可选请求 profile、RPC 重试上界与取消／unknown 的协议范围证据](evidence/model-deadline-2026-10-14.md)。
- 基础票按范围收口：[预算同步、#3 五项验收映射和有限 #5 问答](evidence/foundation-closeout-2026-10-14.md)。
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
