# 产品 Trace 完整性 v2

工程状态 `IMPLEMENTED`。用户已批准将 Trace 链路完整性与实际执行结果分别验收；原功能、项目测试、Memory、计量和发布要求保持。源码固定点 `00f1000b5d06316c01ef0f9ab170c8463fb4e527` 加本轮四项源码／测试修改。

## 已批准的实现范围

默认 `product-v1` 及其原测试继续保留。显式选择 `product-integrity-v2` 时，新收据使用 `schema_version=2`、`scope=product-trace-integrity`；同时保存完整性 `report` 和同一全量 span 按旧规则计算的 `legacy_report`。原始旧 v1 收据、输入、SHA、FAIL 和完整分母不改写。

| 边界 | v2 行为 |
| --- | --- |
| canonical run／实际尝试 | Session 全部 canonical run、连续且唯一的 native lease 尝试与实际 Go 根数量对账，恢复根全部保留 |
| 多 run 共用 trace | 每个 run 仍查询全部实际根；只有完整规范化列表相同才复用整树。同次读回的重复 span 保留；两次快照不一致时保留双方并明确 FAIL，即使两份快照互不重叠 |
| 因果链及身份 | W3C 身份、重复上下文、每个父节点、循环、跨 trace／跨 run 父链及所属 Go 根均检查；恢复根的真实父链也须完整 |
| 模型调用 | 全部逻辑调用、准入尝试、重试 lineage、请求模型、foreground chat 和 logical-call 父级对账，带后缀且计入分母的名称同样检查 |
| 工具请求与拒绝 | 所有请求和结果保留；dispatch 必须有执行 span。只有原生 approval/denied、身份／参数指纹匹配、已知拒绝结果且无 dispatch，才记为 denied；其执行 span 会拒绝 |
| 未知副作用 | 缺结果、tool/unknown、outcome_unknown、未确认 runtime 停止或取消状态非 stopped 均拒绝；缺失取消状态按 unknown 处理 |
| 执行结果 | 每个 run、取消状态和全部工具结果单独记录；ERROR／取消 span 原样列出。失败、拒绝、取消不计为完成轮次 |
| 输入及分析绑定 | 原实际二进制、冻结输入和原构建 manifest 核验；runtime 源码指纹来自该固定构建。当前三项 Python 分析器 SHA 与实际 Go 审计二进制 SHA 另记 |
| 最终状态 | 完整性 PASS、后端全部可用且目标调用全部结算，才在 Trace 完整性范围 VERIFIED；未结算 ID 仍使最终状态 BLOCKED，费用 unknown 和原预留不改写 |

复用现有 reader、原生 Go Ledger／Budget 审计和 CLI，没有新增任务执行链、Go 副作用权限或依赖。运行方法见[Trace 验证入口](../observability-phoenix-live-trace-runbook.md)。完整 Trace 不代替功能、项目测试、提案应用、Memory 或发布合格。

## 测试与审阅发现

| 检查 | 完整结果 |
| --- | --- |
| 新契约 RED | 新版本入口尚不存在，6 FAIL／70 deselected，退出 1 |
| 外部 CLI RED | 新参数尚不存在，1 FAIL／24 deselected，退出 1 |
| 独立 Spec 拒绝边界 | 捕获取消 unknown 丢失及带后缀模型 span 绕过专门父链检查；四个负向回归在修复前 4 FAIL／81 deselected，退出 1 |
| 最终契约文件 | 85 PASS，退出 0；旧 v1 行为兼容及新拒绝边界均保留 |
| 共享 trace 外部 CLI RED | 重复整树先复现 1 FAIL／25 deselected；补完整测试后 1 FAIL、1 PASS／25 deselected；快照冲突与不重叠快照的显式拒绝修复前 2 FAIL／27 deselected，均退出 1 |
| 最终 Trace 三文件 | `python -m pytest -q tests/eval/test_trace_contract.py tests/eval/test_product_trace.py tests/eval/test_phoenix_readback.py`；123 PASS、0 FAIL/SKIP，132.92 秒，退出 0 |
| 最终完整 Python | `python -m pytest -q -ra`；2,610 PASS、17 SKIP、0 FAIL，3 warnings，743.42 秒，退出 0 |
| 独立源码审阅 | Standards 0；Spec 的取消状态、模型名称后缀和共享 trace 三项均已解决，最终源码 0，AI_REVIEWED |

审阅发现问题时，首轮全量在输出 27% 进度后停止，退出 1，没有完整通过分母；输出及精确 owned pytest 子进程树的停止记录保留。此前共享 trace 补丁前的一轮完整结果为 2,606 PASS／17 SKIP／3 warnings／698.78 秒／退出 0，也保留原始源码指纹；上表的最终全量已绑定全部最终源码，不把较早或未完成运行计为当前通过。无单独配置的 Python typecheck 入口；未更改 Go 产品代码、frontend、协议或依赖，不重建 Windows 包或重跑无关全量，完整发布未运行。

17 项跳过为 16 项本机无符号链接创建权限，以及 1 项未启用的独立进程 fixture 断言；未执行范围不计为通过，也不升级为产品证据。

## 真实历史运行的新分析

三批实际后端分别生成新的 v2 收据，全部原 204／368／157 项封存产物共 729 项前后匹配；三份原冻结输入不变。旧 runtime／构建与新分析器分别绑定，本轮不重跑模型任务，不把新分析当作当前产品重新执行。

| 原真实任务 | 完整读回及实际分母 | 新 v2／原规则结果 |
| --- | --- | --- |
| Go 原多轮任务 | 14 run、417 span、26 ERROR；134 工具请求中 121 成功、8 已知失败、3 拒绝、2 unknown；79 模型准入 | v2 FAIL／partial／退出 3：两个工具缺最终结果，以及执行 span 与原生派发身份差异；目标 usage 仍 unknown。并列 v1 FAIL |
| Python 原多轮任务 | 11 run、540 span、10 ERROR；190 工具结果为 183 成功、7 已知失败、0 unknown；102 模型准入全部结算 | v2 PASS／complete／VERIFIED，退出 0，missing=[]、problems=[]；全部错误保留。并列 v1 FAIL |
| Go 故障重试 | 3 run／4 次实际执行、254 span、25 ERROR；104 工具请求含 1 unknown；47 模型准入 | v2 FAIL／partial／退出 3：四个 provider span 及一个工具最终结果缺失，另有执行身份差异；目标 usage 仍 unknown。并列 v1 FAIL |

本次只启动原 Compose `localcode` 的固定 Phoenix，检查后恢复原 exited 状态；原后端 ID／镜像不变，未新增或删除任何 Docker 资源。前后原生 Budget 投影及调用事件完全相同，ActiveCalls=0、任务槽为空、新模型调用为 0；历史 unknown／预留及费用 unknown 保留。

## Python 同任务 Memory 的独立核对

使用现有 Go Ledger reader 和 `LedgerMemory.Manage(list)` 只读核对原 Python Session。原 11 项 trajectory commit、8 次真实反思及 3 项 commit-blocked 均保留。八个 catalog/CAS mutation 已实际持久化 32 条经验，当前 Go 投影返回 32 条；候选 kind/key/abstract/overview/content、原生 source ID、源 event ID/checksum 逐项相同，来源和 repository 校验通过。

这项核对没有发起新反思或修改 Ledger。它证明原任务的实际反思写回及来源，而不是声称 11 次反思都成功；#18 的验收仍需与原已完成的功能／项目测试／应用、真实 Trace 和完整计量一起对照原票文字。

独立 Spec 对照 #18 AC4 确认反思归因子项已满足，原文没有“每轮反思都须成功”的门槛。父规格 #1 AC05 还要求新 Session 的召回证据，本批原 Session 的只读 list 不单独证明该项，也不据此宣布父规格通过。

## #18 按原声明范围对账

| 原验收项 | 可核对证据 |
| --- | --- |
| 冻结任务／正常输入／pin／完整分母 | 原 368 项产物全部保持相同；本轮对照原输入、原 bundle manifest 的 606 个源码指纹及实际二进制，分析器另记 |
| 至少十个有意义的真实浏览器轮次 | 原 11 轮完成，排除一次纯规划后保守计 10 轮；原浏览器截图／事件保留，不用新分析生成轮次 |
| 实际功能、测试、提案应用和原改动保护 | 原项目测试及 Go 冻结提案测试 99/99，实际批准应用五项修改；完整失败、拒绝、重试及保护证据见[原任务记录](python-browser-task-2026-10-16.md) |
| 真实反思、后端 Trace 与费用归因 | 上述同任务 8 次实际反思／32 条 Go catalog 经验、540 span v2 完整性 PASS；三次反思失败及原严格 FAIL 保留。原 102 次调用全部结算，已确认 11,641,458 tokens，费用金额 unknown 如实记录，不虚构价格 |
| 持久批次／单任务准入 | 仍为原共享批次及服务商一亿 token 硬上限，原任务已结束；本轮没有新模型调用，未释放或改写历史 unknown 预留 |

该对账只适用于 #18 冻结 Python 任务。Go #17 的真实未知结果／Memory 缺口、父规格新会话召回、#19 故障矩阵和发布范围分别保留，不凭 Trace 通过宣布它们完成。

本机测试、审阅及 Memory 原生导出／对账位于 `.scratch/trace-integrity-v2-20261016/`。最终摘要 `validation-summary-v2.json` 的 SHA-256 为 `c7823f9747eb23118f65067f1bb01a86e52e6a258abc2c521d2d657d0e13a9f8`；144 项封存清单 `artifact-manifest-v2.json` 的 SHA-256 为 `0103c63377c36193a582cf66e463738617af24b07e5e253df0a79ae06f9673ca`。此前 83 项快照保持原大小和 checksum。

最终源码的新后端收据位于 `deployments/docker/.local/receipts/trace-integrity-v2-20261016-v2/`，含每批 CLI 退出码、原构建绑定、完整 capture／report／legacy_report 及资源恢复记录。运行数据、机器路径及用户 AGENTS.md 改动不进入提交。
