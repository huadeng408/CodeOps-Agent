# CodeOps-Agent 当前目标

完成本机单用户 Windows 代码产品，继续同一 Session 的长对话与恢复专项，最终按各自范围验收全部已授权任务。
工作约定见 [AGENTS.md](../AGENTS.md)，正式任务从[任务导航](refactor-tickets.md)进入。

| 当前范围 | 真实状态与下一步 |
| --- | --- |
| 仓库整理 | 已清理23个废弃函数、重复依赖和9份过期叙述；定向回归与双路审查通过，见[处理与验证记录](evidence/repository-cleanup-2026-11-04.md)。完整源码CI按提交单独记录 |
| [#27 长对话](https://github.com/huadeng408/CodeOps-Agent/issues/27) | **193/200 合格；202 个实际 Run＝193 完成、8 失败、1 取消。** 最新段已正常停止并封存，活动调用为0、任务槽空，未知用量及原预留保留。修正共享续跑判定后，从第194项累计 attempt3继续；见[最新断点](evidence/product-long-dialogue-checkpoint-2026-11-04.md) |
| 故障恢复与 Memory | 后续段核对目标召回、续行、完整工具结果和原前缀；未知副作用先对账。最终累计 Trace、语义、Memory 来源和完整200项仍待验收 |
| [#20 Cloudflare](https://github.com/huadeng408/CodeOps-Agent/issues/20) | 用户暂缓；不作为本机开发或其他票的门禁 |
| 完整发布 | [七条 lane](release-gate.md)的必需证据未齐，**完整发布范围 BLOCKED**；普通修复和单项任务按自身范围交付 |

后续执行段保持原 Session、已完成前缀、全部失败／重试和累计分母；冻结实际源码、配置、指令、模型／镜像 pin 及输入，已接受的请求只对账原 ID／Run。
模型常规调用在已批准的提供方硬上限内推进；已结束的计量 unknown 不阻塞新的独立调用，原预留不清零。
源码 CI、fixture 或阶段通过不代替真实产品／发布验收；当前服务可用性实际核对，不沿用历史环境 blocker。

## 证据链接

- [三组 Agent 对照实验](evidence/agent-evaluation-experiments-2026-11-03.md)：#36及五张子票按小规模真实实验范围验收，不代替长对话／发布。
- [工程执行提效](evidence/engineering-execution-efficiency-2026-11-02.md)：#42首阶段已关闭，后续[只读锁、能力刷新与续办修复](evidence/product-long-dialogue-resume-2026-11-03.md)按各自范围交付。
- [模块深化](evidence/module-deepening-2026-10-22.md)、[首批 Windows 产品](evidence/milestone-parent-acceptance-2026-10-21.md)：保留各自 source／unit／integration／runtime 证据边界。
- 其他能力和历史负结果从[范围证据导航](evidence/INDEX.md)、[归档索引](archive/INDEX.md)按需读取；当前设计入口为 [DESIGN-MAP.md](DESIGN-MAP.md)。

量化长期目标和发布验收以 [release-gate.md](release-gate.md)及正式专项票为准，避免在多个入口维护副本。
