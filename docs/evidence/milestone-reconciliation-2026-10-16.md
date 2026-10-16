# #19 首批验收对账：当前缺口与已有证据

工程状态：`IMPLEMENTED`（对账范围）。本轮 `VERIFIED` 仅指证据清单、完整分母和验收映射的只读核对；没有重新运行产品场景。Windows milestone 1 及完整发布仍为 `BLOCKED`，#17、#18、#19 和父规格 #1 保持打开。已完成基础票的原声明范围不变，不追加专项门禁。

对账固定点为 `6241044086876cf44131b1b485c530927e7fedb0`；其 [CI 四个 job 全部通过](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38868491329)。原产品结果继续绑定各自的源码、二进制、输入和运行时间；下面的历史通过不代表整个当前构建已重验。

## 五类产品入口场景

| 场景 | 已有结果与验收边界 | 证据 |
| --- | --- | --- |
| 取消 | 实际浏览器分别取消 provider、长工具和待审批阶段；真实 owned 资源停止，unknown 作用和计量保留。原十二场景为 11 项完整、1 项代码候选留存部分完成；正式提案留存另有 Go 集成。新构建另验证待审批取消后同会话续行、真实反思 | [#12](task-cancellation-2026-10-15.md)、[新 Memory 专项](memory-cancellation-product-2026-10-16.md) |
| 任务进程故障／恢复 | SIGKILL 精确 owned 沙箱，保留 partial 文件及 unknown，继续请求 409；不重放作用，停止确认后可结束任务 | [#13](task-recovery-2026-10-15.md) |
| Go／Python 重启后旧 Session | 实际终止、重启两侧进程；同一 Session/run/checkpoint 续行，Write result 仍一条，13 个 checkpoint 父链与 Ledger 绑定核对；外来 checkpoint 404、旧游标 409 | [#13](task-recovery-2026-10-15.md) |
| 重启后空新 Session 召回 | 新会话起点只有 created，真实模型召回原 Experience ID/revision/provenance；无旧 transcript 注入。跨仓库查询不带入 chi 经验；异 owner 拒绝来自 Go 集成，不能冒充两账户模型 E2E | [#14](memory-product-2026-10-15.md) |
| 原仓库并发冲突拒绝 | 实际 HTTP 检查内容、存在、index 三类冲突，浏览器保留提案和 409；不覆盖并发修改 | [#10](application-recovery-2026-10-15.md) |

这些场景均已有逐项结果，接缝和未覆盖部分如表。#13 原十二项产品场景完成；#10 原应用器子进程注入与生产恢复不等于十六次 HTTP 批准请求都遭进程杀死。

## 文件、兼容和数据保护

| 必查范围 | 原结果／仍需注意的边界 |
| --- | --- |
| dirty／new／empty／missing | [工作副本准备](workspace-offline-2026-10-11.md)覆盖 staged、dirty、new、empty、deleted 和排除项；[#8](proposal-application-2026-10-14.md)核对 dirty 基线、空与缺失、HEAD/index 和无关文件保护 |
| create／edit／delete／rename | [#9](file-lifecycle-2026-10-15.md)实际四项效果包括新嵌套空文件、既有空文件删除及 rename 双路径；四次顶层产品尝试保留前两失败、后两通过，最终 7/7 浏览器检查 |
| path／link／reparse | rooted 路径、祖先和独占打开检查由 Go 执行；既有公开回归证明拒绝，不能把这些集成检查改称全部真实浏览器攻击矩阵 |
| 重复请求 | #8/#9 的相同 request identity 回读／重放不再次写入；#10 只读核验保留原 intent，不把未派发项变成已应用 |
| 多文件中断 | #10 冻结 16 个边界、3 类并发冲突、新基线再次批准 1 项；最终 16/16、3/3、1/1，生产浏览器 18 场景／123 检查。四次产品运行中前三次中止或失败均保留 |
| CLI／HTTP 兼容 | #9 的十五项 legacy fence 回归通过；旧 CLI 生命周期 `/undo` 明确拒绝不支持的还原。HTTP 共用 Go 应用模块；没有新 CLI 提案应用入口，不能要求不存在的调用方提供产品证据 |
| 旧 full-stack profile | [#2](local-core-2026-10-09.md)的隔离服务栈启动 8/8 历史收据 checksum 本轮仍匹配；当前构建未重新启动该 profile |
| 数据与回退 | 既有 Ledger 前缀、原仓库/index、partial/unknown 和隔离结果按原收据保留；没有双写迁移或数据回退操作。本轮只重算产物，不重新检查 volume 全部内容，也不宣称新增真实回退演练 |

## 两个公开任务的完整分母

| 任务 | 功能、测试与应用 | 未通过范围 |
| --- | --- | --- |
| [Go #17](go-browser-task-2026-10-16.md) | 14 次尝试：10 完成、3 失败、1 取消；10 个有意义完整轮次。最终项目 88 顶层／174 父子 PASS、0 FAIL/SKIP；应用 2 项修改，保护 130 个无关原文件和两份 index | 原运行 13 个 Memory commit-blocked、0 次真实反思；正式 Trace 审计拒绝／CLI 3。诊断保留 14 棵树、417 个 ended spans、26 ERROR、missing=[]，原严格契约 FAIL |
| [Python #18](python-browser-task-2026-10-16.md) | 11 次完成，纯规划轮不计，保守计入 10 个有意义轮次；最终项目及冻结提案 99/99，应用 5 项修改，保护 127 个无关原文件和两份 index | 11 次反思有 8 项 proposal、3 项 pending；540 个 ended spans、10 ERROR、missing=[]，正式 CLI 3／原严格契约 FAIL |

Python 三项 pending 对应 #1823、#2054、#2384，均在各自完整 trajectory 提交之后；排除了旧 Go 取消投影导致“轨迹尚未完整”的同一根因。原产物没有足够细的错误分类，不能进一步断言是 provider、响应校验还是其他反思失败。

新增取消／Memory 专项是两个只读轮次，确认 4 条经验、8 次调用结算；不能拿它替代上表同任务的十轮代码修改、Memory 或正式成功 Trace。旧反思、失败和源码 pin 均不回填。

## AC01–AC09 映射与当前结论

| 父规格组 | 对应已有范围 | 当前 milestone 结论 |
| --- | --- | --- |
| AC01 启动／鉴权／能力 | #2、[#16 便携包](portable-windows-2026-10-15.md)，新产品另有真实注册／登录 | 原范围已验证；当前整套启动/profile 未在本轮重跑 |
| AC02 两个十轮任务 | 上表 #17/#18 的实际代码、测试、批准应用及完整失败分母 | 功能轮次已建立；整票剩余项不自动通过 |
| AC03 工作区／应用保护 | #4、#8–#10 及文件矩阵 | 原范围已验证；先核对相关运行内容变化，再补受影响范围，不按提交数整批作废 |
| AC04 五类恢复场景 | 第一张表及原十二项／十六边界分母 | 逐项结果已映射；不能当作当前版本完整故障矩阵重验 |
| AC05 真实 Memory／范围 | #14 与新取消后反思；公开任务各自的 Memory 结果 | Go 十轮任务没有真实反思，Python 有三项 pending，保持具体缺口 |
| AC06 后端 Trace | [#15 单任务成功证据](product-trace-2026-10-15.md)、两公开任务完整失败读回 | #15 不替代 #17/#18；原两项严格验收仍失败，采集完整性与运行失败的合同需继续对账 |
| AC07 计量／并发 | 原持久批次和各次 admission/native 收据；服务商一亿 token 硬上限 | 最近已确认 24,876,914 tokens；原 unknown／4,006,024,576 预留保留。预留不是消耗，费用未知；本轮无新调用 |
| AC08 旧调用方／迁移回退 | CLI/HTTP legacy fences、旧 full-stack、Ledger 和原数据保护 | 历史范围可追溯；当前完整兼容／实际回退未在本轮重新验证 |
| AC09 UI／便携物料 | #16 的迁移目录、空 PATH、组件/许可/asset pin 和生命周期；当前构建的固定物料 | 原便携范围已验证；本轮不重验当前 bundle 的全部随包许可或发布资格 |

费用未知已如实记录，不要求逐笔批准或把已结束 unknown 变成新的常规调用门禁。服务商报告模型身份不等于已证明不可变 revision；后续要求精确 revision 的对照专项分别处理。#20 可选云能力仍是独立规格，不加入本 milestone 的基础验收。

当前优先补齐 #17/#18 的 Memory／Trace 范围，并核对 Trace 合同是否把完整记录的失败尝试混同于采集缺失；不删除 ERROR、过滤轮次或改写原 FAIL 来换取通过。然后根据实际相关源码变化补 #19 受影响的产品／兼容检查。父规格不改写，不关闭。

## 本轮核验与产物

`python .scratch/milestone-reconciliation-20261016/audit.py` 退出 0：8 份原清单的 1,872 项全部匹配，加相关命令、兼容及应用产物，共 1,928 个去重后的路径／checksum／长度约束记录匹配，0 缺失、0 不符。它证明产物一致性，没有再次执行模型、测试、故障注入或 native Ledger 审计。

原取消／恢复／召回／生命周期／应用输入与当前固定点分别有 44／43／33／67／55 项变化或缺失（含测试、说明和运行代码），因此不声明完整等价。最新 Memory 浏览器冻结的 610 项中 609 项相同，唯一变化是随后已验证的跨进程 test-only fixture；真实产品仍绑定 7bba5f5f。完整差异路径和两任务原生 run 分母在本机审计中保留。

首轮辅助检查把四份已存在的 `runtime.jsonl` 误写为 `runtime.log`；helper 的问题分支返回 3，外层 PowerShell 观察到退出 1。原报告和 helper 保留，纠正路径并显式传递退出码后为 0，四份文件均匹配原公布 checksum。没有重建、移动或删去原产物。便携包采用原已记录的修正版封存清单，保留首次清单及其 stdout 仍在写入的失败说明。

最终只读审计：`.scratch/milestone-reconciliation-20261016/audit-v2.json`，SHA-256 `8621ef463952d9eaf55bd944e46461c937064d75f7082533f43c65895472b882`。原 Go／Python 清单继续为 `7cfc9e860c6a90361f6cacfb66489cb00c509cd50168f934e9e2bb530761fe27`／`d5d699ae10c516e18517fa4100e0a327d4271b0f270ff08e91d2180134169554`；原结果和分母不变。

本轮单测／集成／产品 E2E／完整发布均未重新运行。审计和说明文件不创建新的 Session 事实源；真实运行事实继续只来自 Session Ledger。
