# #19：现用 v5 的重开、Memory 检索与边界补验

对应 [#19](https://github.com/huadeng408/CodeOps-Agent/issues/19)。
本轮只验证旧 Session 只读重开、空新会话的原生 Memory 检索、指定 Windows 源码边界及物料清点。
旧会话重开和十条经验的工具结果绑定为相应范围 `VERIFIED`；经验检索后的最终模型答复及一次恢复均失败。
完整 #19、父 milestone 与发布继续单独验收，没有勾选未完成条件。

## 运行输入与保护

沿用 [#17 的原 v5 包与源码绑定](go-browser-task-trace-v5-2026-10-19.md)，
运行源码为 `01977c2872da7cea4bb3564cdc2531389596e7ea`。
本轮未修改运行源码、工具 schema、Trace 分析器或门禁。
说明基线为 `820098a7d07917236791d4790d09439f0f2a5bdd`；
该提交的 [源码 CI 四个 job 全通过](https://github.com/huadeng408/CodeOps-Agent/actions/runs/39167927357)，与本轮产品结果分别记录。

模型仍为 grok-4.6，复用同一服务商一亿 token 硬上限批次、同一已授权测试账号和固定 Go OpenSandbox 镜像。
运行前冻结 234 个原文件、三份原 index、612 份源码、3,219 份运行文件及五类恢复和文件／兼容场景分母。
Go 原副本以 #17 已批准应用的两项修改为新基线，没有恢复成旧内容。
公共输入 SHA-256：`54c1f3cf6a5a286fe430a825ef7e743fa573bf2403d894b7bc0172b5096a59e2`。

所有会话创建、准备、消息、批准、继续和结束均由实际 Chromium 产品界面操作。
主机只做读取审计、所拥有进程的生命周期管理和既有测试；没有从主机改写 Task Workspace 或调用变更 API 捷径。

## 旧 Session 与三次空会话尝试

实际停止并重启所属 Windows Job 中的 Go/Python，以同一账号登录。
旧 Go Session 的 1,493 个事件在重开、运行及停止后逐项一致，原提案和历史仍可查看，预算未因只读重开增加。
这只证明已结束 Session 的重开，没有冒充活跃任务跨进程续做。

三个新 Session 在首条消息之前都由 Go 原生导出证明只有一个 `session/created`，
没有注入旧对话。各自通过实际右侧入口准备工作区，99 个基线文件与冻结输入一致。

| 尝试 | 结果与边界 |
| --- | --- |
| 普通只读查询 | 真实模型通过 Go RecallMemory 返回原 Go 任务的一条轨迹，最终完成；它证明轨迹检索，不能替代反思经验检索 |
| 观察器指定 `kind=experiences` | 该名称不在工具枚举中；产品如实说明，并调用 trajectories 返回上一查询的轨迹。完整结果保留，不计为经验检索通过 |
| 新空会话的修正查询 | 按现有 schema 请求 patterns 和 cases，两次实际工具调用各返回五条原经验；最终答复发生 provider 临时错误，usage unknown |
| 同会话的一次有界恢复 | 第一次 UI 点击因异步 Memory 事件导致旧游标而返回 409，未派发；刷新后的第二次点击返回 202，只准入一次恢复。该次 provider 再次失败，随后停止重试并经实际 UI 结束任务 |

完整分母为三个 Session、四个 canonical run：两个 completed、两个 failed。
四个 Go RecallMemory 请求／结果均为退出 0，没有代码修改、Shell 执行或副作用 unknown。
无效验证及两次失败没有从分母删除，也没有把工具检索成功升级为最终答复成功。

修正查询的十条结果均匹配重启前 Go 校验的 53 条 Experience catalog：
ID、类型、namespace、来源 Session、source URI/checksum、revision 和完整正文逐项核对。
前置原生投影是 abstract，实际查询是 full；初次观察器直接比较两种显示摘要而失败。
修正审计对照原 catalog 完整内容，并按既有 Go canonical 算法分别核对 abstract/full checksum，
没有改变任何原数据或放宽来源校验。
十条原经验均为 revision 1，五条 patterns、五条 cases。
绑定产物 SHA-256：`8dbc5914b43473ae3a959323555424582d18abfc7163c205716e908c5d169fd2`。

## Trace、用量及停止

未改动的 Phoenix 只读适配器对全部四个 run 从真实 default 项目读回四棵树：
45 个 span 全部结束，14 个 ERROR 保留，无缺父 span、无缺原生 model call span。
这是实际链路采集核对；没有声明成功代码任务的正式 CLI PASS，也没有声明发布合格。
完整读回摘要 SHA-256：`a66f72a7316dcd703ad0a4efed6b6a76223a527b80e9ffe985866e52880ac851`。

Go 原生准入导出保留 #17 封存前缀，新增 11 项调用：7 foreground、4 memory_reflection；
7 settled、4 unknown。本轮确认增加 81,244 tokens，累计 51,085,307。
新增 unknown 保留 400,000,000 tokens 原预留，批次总预留为 5,206,024,576；预留不是消耗，费用仍未知。
没有虚构零消耗、费用或结算，也没有逐笔请求预算批准。

三次实际“结束代码任务”均返回 200。最终 ActiveCalls=0、TaskID 为空。
所属 Go/Python Job 与验收浏览器停止，两项既有 Compose localcode 控制服务恢复原 exited 状态；
未删除容器、volume、索引或业务数据。只读交互观察进程保留供后续 #19 使用。

## Windows 边界回归与物料清点

| 验证 | 新鲜结果 |
| --- | --- |
| Go 1.26.0 下五包文件／提案／应用／生命周期／兼容定向测试 | 161 父子 PASS、0 FAIL、7 SKIP，五包通过、退出 0 |
| 两项原生 junction 拒绝与两项 Python 冻结提案补验 | 4 PASS、0 FAIL/SKIP，退出 0；复用已批准固定 wheel，无下载 |
| 源码／运行文件与原副本 | 612／3,219 项指纹匹配；234 原文件与三份 index 不变，用户 AGENTS.md 不变 |
| 已封存 #17 产物 | 两份 seal 的 865／13 项均保持原摘要 |
| 本轮 Python／frontend／完整发布 | 未运行；本轮没有对应源码变化，前述源码 CI 不代替产品或发布证据 |

定向命令为 `go test ./internal/worktree ./internal/session ./internal/tools ./internal/handler ./internal/cli -run 'Proposal|Application|Lifecycle|TaskWorkspace|File|Rename|Symlink|Link|Legacy' -count=1 -json`。
七项跳过包含四项 Windows symlink 权限不足、一个仅供子进程使用的 helper 和两项未指定缓存 wheel 的 Python 测试。
补验为 `go test ./internal/worktree ./internal/session -run '^(TestCaptureBaselineRejectsJunction|TestCaptureBaselineRejectsNestedMetadataAliasBeforeRunningGit|TestPythonProposalTestsFrozenChangesWithApprovedDependency|TestPythonProposalRefusesChangedApprovedDependency)$' -count=1 -json`。
设置已批准缓存 wheel 后两项 Python 测试实际执行；原跳过记录仍保留。
junction 拒绝不能冒充四项未运行的 symlink 测试。
两次完整输出 SHA-256 分别为 `fe35fc4d69bcfcff993183514417deacca8915baeae6b450f27cd0e1eb75cb87`／`d6d3f08506fda009668c8eb3ecd330d9a0f4aa14de6519e441a2c92704abea8a`。

从实际 EXE build info、随包文件和锁定依赖核对 98 个 Go 模块、3 个 frontend runtime 包、
53 个顶层 Python wheel；另单列 setuptools 的 12 份 vendored metadata，未丢弃或混入顶层分母。
222 个 notice 文件的存在及指纹匹配。
grpcio-tools、langchain-core、langsmith、sqlite-vec 在本次清点中仅找到许可 metadata，
未找到独立随包 notice；仍需按固定上游版本核对及补齐适用材料，未声明完整物料合格。
物料清点 SHA-256：`9d4b225fddfd8cef879cc6b4e333b4e07023b7316ac8b9fa6672ef9f8fc526a6`。

## 产物与未完成范围

本机只读产物位于 `.scratch/milestone-current-v5-20261019/`、
`deployments/docker/.local/profiles/milestone-current-v5-20261019/`、
`deployments/docker/.local/receipts/milestone-current-v5-20261019/v1/` 和
`output/playwright/milestone-current-v5-20261019/v1/`。
范围摘要 SHA-256：`62246c24b045b13e087f95ac06b6cbdeb29d05b55c727e5b2b8a460ca708eb7a`；全部观察器错误、无效请求、失败、跳过及原生导出保留。
这些是验证投影，Session Ledger 继续是唯一可写事实源。

当前完整故障／活跃续做／并发拒绝与旧调用方、profile 和回退矩阵仍需按实际运行内容补齐；
新 Memory 最终答复失败及随包许可材料缺口分别保留。
本轮不关闭 #19 或父规格，不改旧收据，也不追加为已关闭基础票的门禁。
