# Python 真实多轮任务：功能应用与剩余缺口

工程状态：`IMPLEMENTED`。本轮完成公开 Python 功能的模型修改、测试、提案复核和批准应用；#18 的 Memory／成功 Trace 范围未通过，票保持打开。完整发布未运行。

本轮 CodeOps 仓库变更仅包含验收说明和 GOAL 导航。功能修改由产品应用到公开仓库的冻结验收副本，原有本地文件和 index 保留。

## 冻结范围

- 任务：为 [schedule](https://github.com/dbader/schedule) 增加毫秒 fluent API，保留既有接口与调度行为、补充测试和说明，最后审核提案。公开修复答案和参考 patch 未作为输入。
- 任务 revision：`82a43db1b938d8fdf60103bd41f329e06c8d3651`，含执行前冻结的脏改动和新源文件。
- CodeOps 编译源码：`ff50d0ea703d3dbe3f9b64bfb1247cdd3a0d3b67`；执行及验证器的 610 个文件指纹、EXE、模型和镜像 pin 均完成核对。
- 共用输入 freeze SHA-256：`237f8aa7882d0a13c3d3e9ed131a1a835ec06c86c30410d06b3815be41af63c8`。
- 沿用同一便携产品、Go Session Ledger／OpenSandbox Runner、`grok-4.6` 和已批准的离线 pytz wheel；没有在宿主执行任务仓库代码。

## 按范围记录结果

| 范围 | 实际结果 |
| --- | --- |
| 完整浏览器轮次 | 11 轮全部完成；第六轮只输出未落实的规划，保留但不计入有意义轮次，保守计入 10 轮 |
| 实际功能与兼容性 | 单复数单位和原有 timedelta 路径复用；调度边界、错过触发、取消、异常、随机区间、deadline、装饰器和独立 Scheduler 等有公开 API 覆盖 |
| 正常项目测试 | 模型 Bash 中共 13 次完整运行，包含两次失败；最终 99/99、退出 0。Go 冻结提案另跑一次 99/99、退出 0，无跳过 |
| 文档及示例 | 最终文档断言实际 27/27、公共 API 探针 19/19；有限示例原样运行，产生三条回调输出，退出 0，不宣称计时精度 |
| 独立复核 | 固定脏基线与最终候选的 66 个文件指纹匹配；Standards 0 项、Spec 0 项发现。先前文档 P2 已修复；分类为 `AI_REVIEWED` |
| 浏览器提案与应用 | 展示的五项 diff、前后 checksum、冻结测试输入及归档指纹绑定一致；通过实际浏览器批准，Go 应用五项修改 |
| 原副本保护 | 共核对 132 个原文件；批准的五项应用后，127 个无关文件、两份 index、两种语言各自已有的脏改动及新文件保持原样 |
| Memory | 11 次真实反思调用，8 次形成反思提案事实，3 次留下 pending／commit-blocked 收据；该范围部分失败 |
| 成功 Trace | 11 轮、190 个工具调用和 102 个模型调用全部纳入；后端读回 540 个已结束 span，`missing=[]`，其中 10 个 ERROR，严格契约 `FAIL` |
| 资源恢复 | 所属 EXE／Go／Python 进程与浏览器退出，初始控制器状态恢复；33 个容器、96 个镜像和 332 个 volume 的身份／元数据与保留基线一致 |
| CodeOps 源码回归 | 本轮无实现变更；既有源码 CI 范围见[重复命令证据](repeated-command-2026-10-16.md)。610 个运行指纹相同不代表本次完整产品或发布通过 |
| 发布 | 未运行；本票结果不替代七条 release lane、200 轮或官方 scorer |

准备阶段还通过产品入口运行了原始 Python 基线，81/81、退出 0、停止已确认且输出未截断，见 `workspace-baseline.json`。它与表中的 13 次模型 Bash 和额外提案测试分别记录。

初始缺失 API 的探针与后续回归保留；没有单独观察到新增测试方法实施前的 RED 运行。有限示例 checksum 为 `96770c6d3a787583b4b19ed58ea41c1dcf50406e104581bfdf7034390e1c55a2`。原 Round 7 规格快照中的旧验证段落保留，实际状态以之后的原生收据为准。

最终候选只改 `README.rst`、`docs/examples.rst`、`docs/index.rst`、`schedule/__init__.py` 和 `test_schedule.py`。26 次真实修改均与 Go 事件中的前后内容连续对应，无空操作；33 个最终候选文件与独立复核快照相同。

## 保留的失败与边界

- 兼容性测试有两次相同 Bash 命令失败，分别保留独立 Go 调用身份；每次相关测试 2/4、完整测试 89/91，随后修正测试并通过，未把旧结果冒充新结果。
- 示例先后因 `/tmp` 导入路径和只读工作区写入失败，退出 1／2；随后通过 stdin 和工作区导入路径原样执行成功，失败输出未删除。
- 文档探针一度空切片却输出“检查完成”，后续提取脚本又有四次失败。最终实际逐项通过 27 项，脚本硬编码的 24 未用于验收分母；Bash 外层退出 0 不覆盖内部失败。
- 第十轮七次并发 Read 被 Go 以 `task workspace admission unavailable` 拒绝，退出 1。首次拒绝在租约过期约 14 秒后，随后续租成功、任务继续；根因仍需定向复现，不能直接认定为某一种竞争。
- 第八、九、十一轮 Memory 写入失败，Go 保留 pending 收据；三次 `memory.reflect` ERROR 的根因尚未分类，未写入未经校验的记忆事实。
- 首次 Trace CLI 在任务槽尚未释放时，被原生 quiescence 审计拒绝，退出 3、未进行后端读取。通过产品结束任务后重新执行，原生审计和 pin 核对通过，读回全部 540 个 span；七个 Read ERROR 加三个 Memory ERROR 导致原契约继续 `FAIL`，CLI 退出 3、原收据状态 `BLOCKED`／`capture_state=partial`。未过滤错误或改写验证器。

冻结的 12 项场景完整保留：10 项通过；Memory 写回与成功 Trace 两项未通过。普通功能、测试和批准应用按各自范围记录完成，#18 整体保持未通过；独立推进 #17，剩余缺口继续在原目标内修复与重验。

## 计量与资源

同一持久批次共记录本任务 102 项模型调用：91 项前台、11 项反思，102 项均结算，无新增 unknown 调用。Go Ledger 本任务结算 11,641,458 tokens，批次累计 19,724,401／100,000,000；费用仍未知。既有 unknown 和原预留保持原样，没有逐笔追加授权要求或人工清零。

任务经浏览器显式结束，原生 Budget 核对为零活跃调用、任务槽为空。Docker 对已有容器的身份、状态、分组、挂载和镜像／volume 元数据逐项核对通过，未删除业务数据。引擎摘要计数 36 与实际枚举的 33 分别记录，未据此删除未列出的资源；本次生命周期核对没有重算运行活动后的 volume 文件内容。

## 收据与复现入口

本机完整收据：`deployments/docker/.local/receipts/browser-tasks-20261015/python/command-repeat-v1/`；浏览器截图另在 `output/playwright/browser-tasks-20261015/python-command-repeat-v1/`。

- `attempt-summary.json`：12 项完整分母、各范围状态和收据绑定。
- `native-after-task-finish.json`、`admission-before.json`、`admission-after-task-finish.json`：Go 校验的事实导出；数据库仍是唯一可写事实源。
- `project-test-denominators.json`：全部 13 次模型完整测试及额外 Go 提案测试。
- `final-candidate-binding.json`、`review-round-09/`、`proposal-review-binding-ui.json`、`proposal-applied-ui.json`：复核、实际 UI 和应用绑定。
- `original-protection-after-application.json`、`task-accounting-proof.json`、`restored-resources.json`：原副本、计量和资源恢复证据。
- `trace-after-task-finish-v1/`：原验证器命令、全部后端 span 和失败报告；早期拒绝及观察脚本失败单独保留。

已运行的复现命令包括 `python .scratch/repeated-command-evidence-20261016/review-python-round09.py`（退出 0）、`python .scratch/repeated-command-evidence-20261016/verify-python-resources.py`（最终退出 0，早期观察失败保留）、`git --no-pager diff --no-index -- baseline candidate`（非空 diff 的预期退出 1）。`python -m eval.product_trace` 的完整参数和实际退出 3 保存在 `trace-after-task-finish-exit.json`。

封存清单：`deployments/docker/.local/receipts/browser-tasks-20261015/python/command-repeat-v1-artifact-manifest.json`，共 368 项，SHA-256 为 `d5d699ae10c516e18517fa4100e0a327d4271b0f270ff08e91d2180134169554`。清单逐项重算通过；私有运行产物不进入提交。
