# CodeOps-Agent Goal

## 当前目标

完成本机单用户 Windows 代码产品，并继续完成真实长对话、故障恢复和完整发布验收。
正式规格为 [#1](https://github.com/huadeng408/CodeOps-Agent/issues/1)，
任务入口见 [任务导航](refactor-tickets.md)，工作约定见 [AGENTS.md](../AGENTS.md)。

- 保持 Go Harness 的授权、Session Ledger、隔离工作区、提案审批和进程生命周期边界。
- 已交付能力的修改按自身范围回归；首批 milestone 的已验收范围见下方证据。
- 模型调用在服务商已设置的 100,000,000-token 硬上限内推进；保留未知计量和原预留，
  已结束调用的计量缺口不阻塞新的独立调用。
- 新的真实运行冻结源码、输入、模型／镜像 pin 和完整场景分母；失败、重试和未运行项均保留。

## 当前缺口

| 范围 | 下一步与验收边界 |
| --- | --- |
| [#27 长对话](https://github.com/huadeng408/CodeOps-Agent/issues/27) | 最新独立运行已结束，完整 200 轮仍未验收；核验模型链路恢复后，重新冻结独立运行；不能拼接不同尝试 |
| 故障恢复与 Memory | 在新的独立 200 轮中验证目标召回、续行和恢复，保留历史 unknown；旧测试任务不再占用任务槽 |
| 可选 [#20 Cloudflare](https://github.com/huadeng408/CodeOps-Agent/issues/20) | 按用户要求暂缓；不作为本机开发或首批 milestone 的门禁 |
| 完整发布 | 七条 lane 的真实运行证据与对应指标仍待齐备，完整发布范围为 BLOCKED；普通任务按声明范围分别交付 |

凭据、沙箱和外部服务在每次相关运行准备时核对，历史环境错误不作为当前事实。
单测、集成、产品验证和发布分别记录，源码 CI 不代替产品或发布验收。

## 证据链接

- 首批 Windows milestone：[父规格逐项审计](evidence/milestone-parent-acceptance-2026-10-21.md)、
  [五类恢复与 Memory](evidence/milestone-v5-matrix-2026-10-19.md)、
  [文件／兼容、许可包重开与 AC01–AC09](evidence/milestone-v5-compatibility-2026-10-19.md)。
- 最新独立长对话：[阶段证据、完整失败分母与正常收尾](evidence/product-long-dialogue-v7-2026-10-21.md)、
  [构建与冻结输入](evidence/product-long-dialogue-preparation-2026-10-21.md)。
- 失败尝试与修复：[2026-10-21](evidence/product-long-dialogue-2026-10-21.md)、
  [2026-10-20](evidence/product-long-dialogue-2026-10-20.md)。
- 当前输入的压缩保留：[源码修复与离线复现](evidence/compaction-active-input-2026-10-20.md)。
- 旧取消任务的模型准入：[已批准的实际封存与副本验证](evidence/canceled-model-admission-2026-10-21.md)。
- 本轮丢失 Read 结果的取消任务：[严格封存边界与副本验证](evidence/canceled-file-read-admission-2026-10-21.md)。
- 压缩后的 Memory：[真实写回与 Trace](evidence/memory-compaction-2026-10-20.md)、
  [反思失败诊断](evidence/memory-reflection-diagnostics-2026-10-20.md)。
- 浏览器重连：[真实双标签页与空闲重启](evidence/browser-reconnect-product-2026-10-19.md)。
- 其他能力与历史运行：[按领域的证据导航](evidence/INDEX.md)。
- 本轮指令与门禁整理：[变更范围](evidence/policy-cleanup-2026-10-20.md)。
- 原始日志和旧结论：[归档索引](archive/INDEX.md)；只在追溯对应范围时读取。

## 长期能力与发布目标

| 专项 | 当前目标 |
| --- | --- |
| 可扩展 Agent Loop | 同等模块范围，以现有 2 天级基线对照插件接入达到数小时级；记录实际接入工时 |
| 多 Agent／Workflow | 8 Worker、200 个长任务、30 次真实进程故障，恢复率至少 98.5%；checkpoint 为 Go Ledger 投影 |
| Context／Memory | P0／P1／P2 按需加载；同任务、同模型、同预算对照，输入 token 降幅至少 60% 且质量不回退 |
| Skills | 至少 40 个可运行 Skill，在锁定的 1,000 案例上至少 948 项正确 |
| 官方代码评测 | 固定预算及官方 scorer；SWE 锁定 20 项至少 18 项通过，对照固定 8/20 基线 |
| 其他产品验收 | 200 assistant-turn／compaction、浏览器 reconnect、CLI／HTTP／Git 恢复、多模态 RAG 和真实评测数据保留各自完整专项验收 |

完整发布契约见 [release-gate.md](release-gate.md)。各专项不自动成为普通修复的前提。
