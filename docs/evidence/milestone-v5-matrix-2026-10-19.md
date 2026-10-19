# #19：现用 v5 的五类产品恢复场景

工程状态 `IMPLEMENTED`；本轮 `VERIFIED` 限于下列实际浏览器恢复行为和原生事实核对。
使用已封存 v5，一键 EXE、Go/Python、真实 grok-4.6 和固定 OpenSandbox 均未替换。
没有修改 Trace 分析器、验收解释或旧收据。兼容、物料与首批结论见
[同轮对账](milestone-v5-compatibility-2026-10-19.md)，完整发布单独验收。

## 冻结输入与实际结果

运行源码 `01977c2872da7cea4bb3564cdc2531389596e7ea`；包 manifest SHA-256 为
`72981044fe9885e0bc7bc56704562121b804be8370a9051ae6e6e288e5800957`。
启动前冻结五类场景、十四项文件／兼容检查、prompt、预算和所属控制服务。
新故障步骤在首次调用前另行冻结：先用 Go Write 留下 marker，再运行只读 `sleep 120`，
符合原只读沙箱挂载契约；原未派发的 Shell 写文件 prompt 保留。

| 场景 | 实际产品结果及边界 |
| --- | --- |
| 取消 | 实际 UI 在 provider 活跃时取消，返回 202；原生终态为 canceled／stopped。没有工具派发；该调用 ended usage unknown 和预留保留，随后显式结束任务 200 |
| Go/Python 重启后旧 Session | 实际 Go Write 建立 marker，在 Bash 待审批时停止所属 launcher Job，确认 Go/Python 子进程均退出。重启、真实登录后，同一 run/checkpoint 为 attempt 2；批准原待审批 Bash 后读取成功。原 56-event 前缀保持，Write result 始终一次，marker 不变；任务结束 200 |
| 任务进程故障／恢复 | 实际批准 Write 与 `sleep 120`，核对 Go operation、task/egress ID、镜像、localcode 分组、只读挂载及正在运行的 sleep 后，仅 SIGKILL 该 owned task。Go 保留失败、unknown 作用和 marker，停止已确认；新游标继续请求仍返回 409，没有重放。原生任务和 egress 已不存在，结束任务 200 |
| 原仓库并发修改拒绝 | 独立 Go fixture 在调用前冻结，含 dirty/new/empty/missing。模型通过实际 Read/Edit 和审批修改注释；右侧冻结提案的真实 `go test ./... -count=1` 退出 0、停止已确认。随后只向 owned 原副本注入已声明并发修改；实际批准返回 409 `original repository baseline changed`，并发内容、index 和提案均保留；结束任务 200 |
| 重启后空新 Session 的 Memory | 原生起点只有 created，没有旧 transcript。一次 RecallMemory 返回五条原 Go 任务经验，最终答复 completed。五个 ID、来源 Session、revision 1、full/abstract checksum 和内容均与重启前 Go 验证的 catalog 相同，答复没有虚构来源；后台真实反思结算后结束任务 200 |

重启场景证明原任务的自动恢复和原审批接续，不将其称为新的 HTTP Continuation 请求。
前轮实际 Continuation 的 409、获准 202 和最终 provider 失败仍见
[旧 Session／召回记录](milestone-v5-recovery-2026-10-19.md)。故障场景第一次 409 是旧游标冲突；
实际重新加载后，以新游标再次请求为 state conflict，准入调用事件未增加。
这些受控场景不计入 #17/#18 的正常十轮开发分母。

## 新 Memory 答复与完整尝试

新 Session 为 `e29f7d4066b80e3e8b8b739f3a15e3dd`，原经验来源为
`257b0c9483ed21a0f4d0dd6776293a36`。查询有 9 个候选，返回 5、按 1,200-token 检索上限
丢弃 4，估计使用 1,050；这是检索选择的完整分母，不是删除失败实例。
原 Go Session 的 1,493 条事件不变，真实 Go reader 再次核对 catalog 和来源。
新答复只引用实际五个 Experience ID，并保留各经验的适用限制，没有把观察到的做法改写成测试通过。

前轮三个空新 Session／四个 run 的两次错误 kind 和两次最终 provider 失败全部保留；
其中已有十条工具检索成功不能冒充成功最终答复。本轮是另行冻结的单次补验，没有改写或替换它们。
后台反思仍活跃时，观察器首次等待结束按钮超时；没有发出结束请求。
等真实调用结算、ActiveCalls=0 后，实际结束返回 200。

## 原生分母、Trace 与计量

五个新 Session 最终为 225 条 canonical events；7 个 run：5 completed、1 failed、1 canceled。
共有 8 次 lease attempt，包含上述恢复的 attempt 2；7 次工具请求／7 个结果、3 个 `code/modified` 事实。
故障 Session 后续增加的两项 memory/commit-blocked 另存原生导出，原导出及前缀保持。
初版观察摘要误用了不存在的 code/modification 名称，纠正后的 v2 摘要保留三个实际修改，旧摘要仍保存。

未修改的后端 reader 读回 7 棵有根树、77 个 ended span，12 个 ERROR 全部保留，
这些已返回树的 missing-parent 列表为空。被强制终止的旧尝试另有 3 个已结束子 span，
但 agent.main 根未导出；原生 lease／已结算调用与这棵无根树均保留，不能宣称完整 Trace PASS。
两个 provider.call ERROR 是工具 unknown 后、Go 准入之前被拒绝的意图，没有对应模型预留；
不把它们计为额外付费请求。完整性判定与这次采集、预期故障处理分别记录。

#17/#18 的成功代码任务仍使用各自正式 v2 完整性收据；本轮故障树不替代它们，
也不因已知错误而删除 ERROR 或放宽缺根／unknown 的拒绝边界。

原生 admission 的 2,001 个调用事件前缀不变。新预留 18 个唯一调用：13 foreground、
5 memory_reflection；17 settled、1 unknown，新增确认 178,173 tokens。
累计确认 51,263,480，原预留累计 5,306,024,576；预留不是消耗，费用仍 unknown。
服务商 100,000,000-token 硬上限保持，未重置预算或逐笔请求批准。
五次实际任务结束均为 200，最终 ActiveCalls=0、TaskID 为空。

## 保护、产物与验证范围

234 个原文件、三份 index、用户 AGENTS.md 和 v5 的 3,219 个运行文件均不变。
受控并发 fixture 仅保留声明的原文件变化，其余七项基线状态和 index 不变。
所属产品 Job／验收浏览器已停止，两项原控制服务恢复 exited；没有删除业务数据、volume 或索引。

本轮实际调用、审批、取消、提案测试、拒绝及结束均经过真实浏览器和 Go 产品接口。
主机写入仅用于预先冻结 owned fixture、声明的原副本并发注入及精确 owned 故障注入。
观察器的 DTO 字段、Docker 缺失对象大小写、初次控件等待、导入路径和编码错误单列保留，
未算成成功产品场景或正常开发轮次。

只读产物位于 `.scratch/milestone-v5-browser-matrix-20261019/`、
`deployments/docker/.local/profiles/milestone-v5-browser-matrix-20261019/`、
`deployments/docker/.local/receipts/milestone-v5-browser-matrix-20261019/v1/` 和
`output/playwright/milestone-v5-browser-matrix-20261019/v1/`。
`verify_memory_answer.py`、`audit_matrix.py`、`read_matrix_traces.py` 和
`read_crash_diagnostics.py` 的本轮执行均退出 0；原始导出、修正版及无根诊断同时保留。
这些是 Ledger 的只读验证投影，不是另一份可写 Session 事实源。

本轮未修改产品运行源码，没有重跑无关单测或完整发布；完整发布仍 `BLOCKED`。
