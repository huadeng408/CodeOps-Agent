# 组件候选导航

以当前任务的源码、调用方和测试确定需要替换的部分。现有成熟实现优先，
没有明确需求或收益证据时保留现状。当前目标与验收范围见 [GOAL.md](GOAL.md)。

| 领域 | 当前任务的入口 |
| --- | --- |
| Go 执行、权限、Session 与沙箱 | [架构术语](../CONTEXT.md)、[ADR](adr/)、[OpenSandbox 部署契约](opensandbox-local.md) |
| 模型调用与计量 | [Provider 配置](provider-configuration.md)、[Token 契约](token-budget.md) |
| Context、Memory、Skills 与子 Agent | [现有接口契约](skills-memory-agents.md)、[领域证据导航](evidence/INDEX.md) |
| 前端、启动和交付 | [Windows 交付](../deployments/windows/README.md)、当前 frontend 源码与对应测试 |
| Trace、评测与发布 | [Trace 入口](observability-phoenix-live-trace-runbook.md)、[发布契约](release-gate.md) |

候选调查不是执行指令。接入时固定实际依赖版本，确认许可、调用边界和回滚方式，
按本任务范围验证；不把调查中的其他专项或全部候选加入当前任务门禁。

## 历史调查

2026-10-08、源码基线 58f42bb 的全仓调查保留原文：
[完整报告](archive/policy-cleanup-2026-10-20/docs--reuse-landscape.md)、
[CSV 清单](archive/policy-cleanup-2026-10-20/docs--reuse-landscape.csv)、
[前一份组件调查](archive/policy-cleanup-2026-10-20/docs--reuse-components.md)。

报告中的 P0/P1/P2、旧缺口与运行状态只描述当时调查，不作为当前任务排序。
[归档清单](archive/policy-cleanup-2026-10-20/manifest.json)记录字节数与 SHA-256。
