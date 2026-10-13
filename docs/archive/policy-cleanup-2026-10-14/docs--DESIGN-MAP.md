# CodeOps-Agent Design Map

这是本地任务导航。当前对话与 [GOAL.md](GOAL.md) 决定验收；正式规格和已发布任务
以 [GitHub Issue #1](https://github.com/huadeng408/CodeOps-Agent/issues/1) 及其批准后的拆解为准。
`design/state.yaml` 是规划投影，不能替代源码绑定的 runtime receipt。

## 按任务加载

1. 按 `AGENTS.md`、`docs/GOAL.md` 启动，再读取目标任务源码、测试及需要的任务卡。
2. 按任务需要读取和扩大 `read_set`，不设固定文件数量上限；归档和生成树按明确需要加载。
3. 结束时报告路径、命令/退出码、artifact/checksum、状态及 blocker。

## 当前入口

| 工作 | 入口 | 状态范围 |
| --- | --- | --- |
| 仓库清理 | `.scratch/repository-cleanup/map.md`、`docs/repository-cleanup-2026-10-09.md` | 工程审计与归档，不作 runtime 结论 |
| Windows 代码任务 milestone 1 | `.scratch/matt-refactor/map.md`、正式规格 #1 | `DESIGNED`；拆解批准后发布任务 |
| 复用候选 | `docs/reuse-landscape.md` | 候选比较，不等于安装或批准全部组件 |
| Agent 沙箱当前选型 | `docs/sandbox-options-2026-10-11.md`、正式 #6 | `DESIGNED`；优先验证 Windows microsandbox，候选尚未安装或验证 |
| 完整验收与发布 | `docs/GOAL.md`、`docs/release-gate.md` | 缺新鲜 receipt 时 `BLOCKED` |

`python scripts/design_map/check.py` 只检查本地导航结构和路径。其 PASS 不是 release gate，
也不验证运行证据的完整性、真实性或新鲜度。

## 历史边界

需要追溯旧 Loop/P2/子 Agent/checkpoint 实现时，再读
`docs/archive/design-map/cleanup-2026-10-09/` 和归档索引点名的旧任务卡。
旧状态、日更日志和评测运行树按需加载；当前工作指令以 `AGENTS.md` 为准，验收范围以当前对话与 Goal 为准。
