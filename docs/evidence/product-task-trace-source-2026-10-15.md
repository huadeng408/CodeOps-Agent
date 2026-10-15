# 多轮产品 Trace 读回：源码验证

状态：`IMPLEMENTED`；`VERIFIED` 范围为验证器单测、集成和源码 CI。
这份记录不证明 #17/#18 的十轮真实浏览器任务已完成；产品验收仍单独进行。

实现提交：`2b27c4483c35ecbf223f0b3b61a424517a79ab0f`。
`eval.product_trace` 接受重复 `--run-id`，必须覆盖 Session 的全部 canonical run。
各轮匹配独立 Go 根及完整父链，工具身份按 run 区分，整个任务仍核对完整
模型／重试分母、真实修改、完成与预算静止状态。模型的 Ledger 归属为任务级；
每轮的模型父链来自实际后端，不虚构原生 Ledger 中不存在的 call-to-run 字段。

只读对话轮可以没有工具。整个代码任务仍必须有工具执行和代码修改；
遗漏没有模型准入的轮次、跨轮错误工具归属、根节点带父链均拒绝验收。
后续轮读回失败时保留先前安全 Trace、完整分母和失败轮次，维持部分／受阻状态。
CLI、公共合同及外部 HTTP 接缝的 fixture 仅为单测和集成证据。

| 验证 | 完整分母与结果 |
| --- | --- |
| 当前受影响测试 | CLI／合同／Phoenix 三文件，71 passed，退出 0 |
| Windows 完整 Python 稳定快照 | 2551 passed、17 skipped、31 warnings，退出 0；随后三项修复另有定向验证和当前完整 CI |
| 当前提交完整 Python CI | 2561 passed、12 skipped、3 warnings，退出 0 |
| 本机完整 Go 快照 | 1733 passed、79 skipped，共 1812 项，退出 0；生产 Go 未改，最终 fixture 分支另经 CLI 测试和当前 Go CI |
| 静态检查 | `go vet ./...`、最终 fixture 包 vet 均退出 0；gofmt 无差异 |
| [当前源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38763374370) | Go、Python、Frontend、Windows startup contracts，4/4 jobs success |
| 独立代码审查 | Standards 硬问题／代码味道 0／0；Spec 缺失／越界／错误 0／0，属 `AI_REVIEWED` |

审查前的漏轮错误通过、错误 Go 根父链通过及部分读回丢失均先复现失败，
再修复通过。旧混合源码 Python 全量失败、fixture CAS 失败、定向红灯、
已存在的负向凭据测试样例导致的初次扫描命中均保留，未用成功子集替换。
扫描例外仅匹配已提交负向 fixture 的精确 checksum，真实凭据扫描不豁免。

结构化结果与完整 artifact 索引 checksum 见
[源码收据](product-task-trace-source-2026-10-15.receipt.json)。
当前源码范围无 blocker；真实任务 Trace 和完整发布分别尚待对应验收。
