# 长对话失败与终态持久化修复

这是 2026-10-20 截止记录，不是完整 200 轮通过或对旧 Ledger 状态的改写。
正式范围与后续结果见 [#27](https://github.com/huadeng408/CodeOps-Agent/issues/27)。

## 原产品尝试

v11／v5 冻结运行源码为 `1723a5fedc7b3be9ae003e8884d85824a83b8f2a`，
冻结输入 SHA-256 为 `3fd607147621e3a646f1c77b5dd173fed23518652bb772300b1cec8652cfe038`。
计划 200 轮，完成 137 个非空 assistant-turn，实际发生 31 次 compaction。
原第 138 轮出现 `event_integrity`，没有 assistant 或 failed 终态；后续 62 轮未运行。
这次尝试不合格，不与其他尝试拼接。

6 次 Read 的退出 0 结果与 commit_done 均保留。一次实际 UI 取消获 HTTP 202；
截止 Ledger 状态仍为 canceling／unknown，3510 个事件、138 个用户消息及 137 个 assistant 消息未改写。
模型 ActiveCalls=0，任务槽保留；未手动结算或释放。所属启动器及 5 个子进程已停止，
两只配套容器恢复原先 exited 状态；物理停止不代替 canonical 取消确认。
[原失败记录](https://github.com/huadeng408/CodeOps-Agent/issues/27#issuecomment-6283031158)保存完整边界。

## 已交付源码修复

[051a83bf](https://github.com/huadeng408/CodeOps-Agent/commit/051a83bf5316a7cca77f63614a7eefe0c784c23c)
将失败注记与终态持久化的期限分开，并仅对期限／序号冲突作一次有界重试。
沿用 lease、CAS、取消和 commit-before-terminal 约束，不重新调用模型或工具。

公开 SessionRunner＋真实 SQLite 接缝先 RED（退出 1）后 GREEN（退出 0）。
相关三个包为叶子 505 PASS／7 SKIP／0 FAIL；完整串行 Go 为 1744 PASS／82 SKIP／0 FAIL，
两者均退出 0。首次完整并行回归的 1743 PASS／82 SKIP／1 FAIL（启动器 30 秒超时，退出 1）保留，
最终完整分母仍为 1826。`go vet ./...` 退出 0；两轴独立 AI_REVIEWED 各 0 项。
[同 SHA CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/39368411894) 四项通过。
[交付记录](https://github.com/huadeng408/CodeOps-Agent/issues/27#issuecomment-6283290456)已发布。

## 证据与剩余范围

本机原始截止收据位于
`deployments/docker/.local/receipts/product-long-dialogue-20261020/go/v5/v5-failure-cutoff-summary.json`，
SHA-256 为 `288c5f95ac47d354b7e75328e03113ebeee3127b200926bbaae893685a38f866`。
源码交付收据为 `.scratch/product-long-dialogue-20261020-v5/failure-finalization-delivery.json`，
SHA-256 为 `5ef81fedb4c0f99d791c50d7f2df766ff4aa4beb82ecac0412ef4ff5bd114ff7`。
这些忽略目录没有随源码发布。

旧包未部署本次修复。原 event_integrity 触发原因、取消恢复、约束／Memory 质量、
持久存储反复失败和重启中断仍待验证。修复后必须独立冻结并运行完整 200 轮；
本次源码验证不代表产品或发布合格。
