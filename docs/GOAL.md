# CodeOps-Agent 当前目标

完成本机单用户 Windows 代码产品，继续同一 Session 的长对话与恢复专项，最终按各自范围验收全部已授权任务。
工作约定见 [AGENTS.md](../AGENTS.md)，正式任务从[任务导航](refactor-tickets.md)进入。

| 当前范围 | 真实状态与下一步 |
| --- | --- |
| 仓库整理 | 23个废弃函数、重复依赖和9份过期叙述已处理；清理提交完整CI五项成功，见[处理与验证记录](evidence/repository-cleanup-2026-11-04.md)。普通修复与发布门禁已分开 |
| [#27 长对话](https://github.com/huadeng408/CodeOps-Agent/issues/27) | **200/200已完成；211个实际 Run＝200完成、10失败、1取消。** 正常停止并封存，211原生核验及Memory结构来源通过；摘要约束/事实质量和旧Trace关联缺口保留，整票仍开放。见[最终累计结果与修复](evidence/product-long-dialogue-final-2026-11-05.md) |
| 后续修复 | Git只读沙箱信任、无增益压缩重试、Python OpenAI流内错误被吞已修并通过相关回归；真实Git Runner验证通过。继续处理摘要质量，自动Recall注入/因果贡献仍UNKNOWN；不重跑已完成的200项或改写旧负结果 |
| [#20 Cloudflare](https://github.com/huadeng408/CodeOps-Agent/issues/20) | 用户暂缓；不作为本机开发或其他票的门禁 |
| 完整发布 | [七条 lane](release-gate.md)的必需证据未齐，**完整发布范围 BLOCKED**；普通修复和单项任务按自身范围交付 |

后续执行段保持原 Session、已完成前缀、全部失败／重试和累计分母；冻结实际源码、配置、指令、模型／镜像 pin 及输入，已接受的请求只对账原 ID／Run。
模型常规调用在已批准的提供方硬上限内推进；已结束的计量 unknown 不阻塞新的独立调用，原预留不清零。
源码 CI、fixture 或阶段通过不代替真实产品／发布验收；当前服务可用性实际核对，不沿用历史环境 blocker。
上游服务故障经一轮定向排查和一次复查仍阻塞时，优先改用cc-switch中已配置、协议兼容的独立provider，维持满思考配置并做一次有界验证；新provider／模型单独冻结和报告，旧Run、失败与证据不改写。仍受阻则按依赖推进其他未暂缓的开放票，或当前票不依赖上游的工作，避免在同一路径反复耗时。

## 证据链接

- [三组 Agent 对照实验](evidence/agent-evaluation-experiments-2026-11-03.md)：#36及五张子票按小规模真实实验范围验收，不代替长对话／发布。
- [工程执行提效](evidence/engineering-execution-efficiency-2026-11-02.md)：#42首阶段已关闭，后续[只读锁、能力刷新与续办修复](evidence/product-long-dialogue-resume-2026-11-03.md)按各自范围交付。
- [模块深化](evidence/module-deepening-2026-10-22.md)、[首批 Windows 产品](evidence/milestone-parent-acceptance-2026-10-21.md)：保留各自 source／unit／integration／runtime 证据边界。
- 其他能力和历史负结果从[范围证据导航](evidence/INDEX.md)、[归档索引](archive/INDEX.md)按需读取；当前设计入口为 [DESIGN-MAP.md](DESIGN-MAP.md)。

量化长期目标和发布验收以 [release-gate.md](release-gate.md)及正式专项票为准，避免在多个入口维护副本。
