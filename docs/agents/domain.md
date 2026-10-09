# Domain Docs

采用 single-context：根目录 CONTEXT.md 和 docs/adr/。

## Reading

- 保持 AGENTS.md → AGENT.md → docs/GOAL.md 的启动顺序。
- 探索领域时读取 [CONTEXT.md](../../CONTEXT.md)，使用其中定义的术语。
- 只读取与任务有关的 [ADR](../adr/)。
- 执行边界相关任务读取 0001-go-harness-python-orchestrator-ownership.md。
- Session、恢复及状态迁移相关任务读取 0002-single-append-only-session-ledger.md。
- 需要设计边界时读取 docs/DESIGN-MAP.md 和对应任务卡的 read_set。
- 只有任务卡点名历史证据时才读取对应归档，不递归加载历史材料。

## Changes

- 规格、任务、测试及设计使用已有领域词汇。
- 与已接受 ADR 冲突时明确说明，并提出重新讨论的理由。
- 领域文档缺失时继续任务；仅在术语或决策明确后按需补充。
- 不新增 CONTEXT-MAP.md 或重复的架构、验收、Session 状态记录。
- 当前对话与 docs/GOAL.md 继续决定验收，不由任务标签覆盖。
