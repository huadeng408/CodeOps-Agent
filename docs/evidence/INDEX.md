# 范围证据导航

只打开当前任务涉及的记录。每份证据的源码、分母、失败与验收范围独立，
历史未通过项不覆盖后续已验收范围，后续修复也不改写原失败。
当前目标与缺口由 [GOAL.md](../GOAL.md) 维护。

| 范围 | 入口 |
| --- | --- |
| 首批 Windows 产品 | [父规格逐项审计](milestone-parent-acceptance-2026-10-21.md)、[五类恢复与 Memory](milestone-v5-matrix-2026-10-19.md)、[兼容／物料及 AC01–AC09](milestone-v5-compatibility-2026-10-19.md) |
| 长对话与压缩 | [独立重跑准备](product-long-dialogue-preparation-2026-10-21.md)、[本轮失败与修复](product-long-dialogue-2026-10-21.md)、[前次终态修复](product-long-dialogue-2026-10-20.md)、[当前输入保留](compaction-active-input-2026-10-20.md)、[早期独立尝试](product-long-dialogue-2026-10-19.md)、[压缩后的 Memory](memory-compaction-2026-10-20.md) |
| 浏览器及 CLI | [真实双标签页重连／重启](browser-reconnect-product-2026-10-19.md)、[上下文窗口](cli-context-window-2026-10-19.md)、[能力状态刷新](task-capability-refresh-2026-10-20.md) |
| 真实代码任务 | [Go v5](go-browser-task-trace-v5-2026-10-19.md)、[Python](python-browser-task-2026-10-16.md)、[Trace v2 对账](trace-integrity-v2-2026-10-16.md) |
| 提案、应用与冲突 | [隔离提案](proposal-closeout-2026-10-14.md)、[单文件应用](proposal-application-2026-10-14.md)、[文件生命周期](file-lifecycle-2026-10-15.md)、[中断与冲突](application-recovery-2026-10-15.md) |
| 取消、恢复与 Memory | [取消](task-cancellation-2026-10-15.md)、[故障重开](task-recovery-2026-10-15.md)、[重启召回](memory-product-2026-10-15.md)、[反思诊断](memory-reflection-diagnostics-2026-10-20.md) |
| 执行与 MCP | [OpenSandbox Go](opensandbox-product-2026-10-12.md)、[Python](opensandbox-python-2026-10-12.md)、[取消／CLI](opensandbox-lifecycle-2026-10-12.md)、[Git 快照](portable-task-git-2026-10-16.md)、[仓库 MCP](mcp-product-2026-10-15.md) |
| 模型准入与计量 | [当前契约](../token-budget.md)、[丢失 Read 结果的严格封存](canceled-file-read-admission-2026-10-21.md)、[旧取消任务实际封存](canceled-model-admission-2026-10-21.md)、[基础票范围收口](foundation-closeout-2026-10-14.md)、[真实预算续行](model-proposal-budget-2026-10-13.md) |
| 启动与交付 | [本机启动](local-core-2026-10-09.md)、[便携包](portable-windows-2026-10-15.md)、[Python 许可材料](windows-python-notices-2026-10-19.md) |
| 指令与门禁整理 | [本轮范围](policy-cleanup-2026-10-20.md)、[前次八项整理](policy-cleanup-2026-10-14.md) |

完整旧证据列表保留在 [2026-10-20 前的 Goal 原文](../archive/policy-cleanup-2026-10-20/docs--GOAL.md)。
其他原文可从 [归档索引](../archive/INDEX.md) 追溯；不把旧日志作为启动或验收指令。
