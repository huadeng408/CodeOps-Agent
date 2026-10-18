# 失败反思父链：实际浏览器与后端专项

`VERIFIED` 限于失败／完成两轮的反思父链回归。源码为
`f774088e09e6b596d25099e4c1ee3855f5f0ec51`，对应远端
[四项 CI 全通过](https://github.com/huadeng408/CodeOps-Agent/actions/runs/39149751418)。
新 Windows 便携包重新构建，核对 612 项源码与 3,219 项随包文件；manifest SHA-256
`8d8ff9f8491e7a5a219205f9dcb78c5496abdf783a9900324397e4e2662f9547`。

## 声明范围与完整分母

新账号、Session 和当轮输入／source/model/image/controller/backend pin 在调用前冻结。
第一轮通过真实 Chromium composer 请求逐次只读检查，触发产品既有的工具轮次上限；
第二轮在同一 Session 要求不调用工具的简短回复。共两次尝试：一失败、一完成，
均保留；没有修改轮次上限、注入 Runner/provider 结果、从宿主补写源码或直接调用
mutation API。此诊断不计入 [#17](https://github.com/huadeng408/CodeOps-Agent/issues/17)
至少十轮正常开发，也不替代其原任务。

实际后端两次完整读回字节相同：两棵树、64 个已结束 span，保留一个 Agent ERROR。
两份 `memory.reflect` 均直接指向各自 Agent；两次真实反思的 provider.call／attempt
均在相应父链上结束。全部 20 个模型调用 ID 与原生准入逐项对应：18 foreground、
2 memory_reflection，20 次全部结算。新增确认 271,640 tokens，批次累计
46,023,206／100,000,000；历史预留 4,206,024,576 不变，费用仍 unknown。
该回归说明失败出口的新 context 已真实穿过 Go／gRPC／Python／provider。

两份完整轨迹／真实反思提案、一次 catalog 变更和一次 noop 均由原生导出核对。
两条 Experience 的来源、checksum、proposal、catalog/CAS 与当前投影一致；
projection error、commit-blocked 均为零。实际浏览器结束返回 200，ActiveCalls=0、
TaskID 为空；owned launcher／浏览器停止，既有两个控制容器恢复 exited，业务数据保留。

## 验证边界与保留的失败

| 范围／命令 | 结果 |
| --- | --- |
| `scripts/build-windows.py`、当前源码远端 CI | 构建退出 0；四项 CI 成功，不能替代本项产品核对 |
| 两轮实际浏览器与原生 Ledger／准入导出 | 失败和完成均保留；20／20 结算、两份 Memory 来源一致 |
| 本机 `audit-parent.py` | 退出 0；仅上述父链断言，完整保留 64 span／一个 ERROR／全部调用 |
| 本机 `audit-memory.py` | 退出 0；两条持久 Experience／两条投影，无新增模型调用 |
| 未改动的 `eval.product_trace --contract-version product-integrity-v2` | 退出 3／BLOCKED／unknown；原生审计后因无 `code/modified` 拒绝，未执行正常任务的正式后端验收 |
| 原工作副本保护检查 | 首次退出 1；所有 135 个原文件和暂存条目未变，Go index 缓存字节变化，不能声明完整字节保护通过 |
| #17 正常任务／#19 故障矩阵／完整发布 | 本专项不验收，原缺口保留 |

读取正式 CLI 后、第一次模型调用前已保存范围澄清：只读诊断缺少真实代码修改，
不能获正常代码任务的 v2 收据。不修改分析器、不制造修改事件；正式拒绝和其
`real code modification required` 原因独立保留，父链断言不升级为正式任务通过。

首次消息请求实际以 202 接受；观察器错断言为 200 的失败保留，原请求未重复提交。
准备脚本先取 index hash 再运行 `git status`，导致 Git 刷新 Go index 的 stat 缓存。
全量条目与冻结 HEAD 一致，所有文件未变，Python index 字节也未变；没有恢复或重写
原 index。原始两份摘要及保护失败保留，后续只读准备须禁用可选写锁并核对前后摘要。

## 封存产物

本机收据位于 `deployments/docker/.local/receipts/failed-reflection-trace-20261019/go/v1/`。
82 项清单为 `.scratch/failed-reflection-closeout-20261019/artifact-manifest.json`，SHA-256
`15f38b619ff26917b3ae1520d324d5f2fd60411d117c4bd42b87cf3981f536ef`；
范围摘要 `02d48396879351843d668dbc36dba6745140dd7f42a328948a563ff9fa2ae262`。
父链两份原始读回 SHA-256 均为
`6c9ad44e0623f70eca2fd42730e6d9cb8cd94d9493d8d9ab966f72dd37fb6608`。
原 Go 任务 777 项与更早 514 项清单重新核对均匹配；旧失败、原收据和分母不回填。
源码／集成结果仍见[源码修复证据](failed-reflection-trace-2026-10-19.md)，不宣称完整发布合格。
