# CodeOps-Agent 设计导航

需要设计边界时，从下表选择与任务有关的入口。当前目标和缺口见 [GOAL.md](GOAL.md)，
工作指令见 [AGENTS.md](../AGENTS.md)。正式任务的状态、负责人和依赖以 GitHub Issues 为准。

| 任务范围 | 入口 |
| --- | --- |
| 领域术语与模块边界 | [CONTEXT.md](../CONTEXT.md)、[领域配置](agents/domain.md)、[ADR](adr/) |
| Windows 代码产品 | [正式规格 #1](https://github.com/huadeng408/CodeOps-Agent/issues/1)、[任务导航](refactor-tickets.md) |
| 执行环境 | [Docker 目录约定](../deployments/docker/README.md)、[OpenSandbox 基础接口证据](evidence/opensandbox-foundation-2026-10-11.md) |
| 模型调用与计量 | [Token 批次契约](token-budget.md)、[Provider 配置](provider-configuration.md) |
| 仓库 MCP | [固定只读服务、Go 授权与 catalog 契约](mcp-repository.md) |
| 复用候选比较 | [复用清单](reuse-landscape.md)；这是候选调查，具体接入仍须按当前任务核对 |
| 专项与发布 | [GOAL.md](GOAL.md)、[发布契约](release-gate.md) |

当前采用用户批准的 OpenSandbox；Go／Python 基线与取消、故障重开、CLI 证据从 Goal 进入。
前端沿用已选右侧卡片，新增布局或明显视觉改版按 AGENTS.md 的选择流程执行。

历史设计地图、规划 YAML 和本地路径检查器已归档，见
[清理索引](archive/INDEX.md#policy-and-navigation-cleanup--2026-10-14)。它们不构成启动或验收前提。
