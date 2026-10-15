# #14 Memory 仓库范围与来源校验

范围：[GitHub #14](https://github.com/huadeng408/CodeOps-Agent/issues/14) 的
Go 召回隔离、Experience 来源、兼容身份及反思期限接线。
工程状态 `IMPLEMENTED`；单测与集成范围 `VERIFIED`。
真实反思写回、进程重启后的空新 Session 召回仍在验证，#14 尚未关闭。
本记录不声明产品或完整发布通过。

## 行为

- Server、CLI、子任务的 Go 调用方注入当前 Session，模型不能提供或覆盖目标仓库。
  自动 Context、显式 RecallMemory、Memory 管理命令及 CLI 入口共用仓库过滤。
- 仓库身份来自已验证的原 Session 工作目录；CLI 后续改变工具目录不会改变它。
  Windows 路径大小写别名保留身份，同名 key 在不同仓库拥有独立 Experience。
- 兼容的旧 ID、revision 和 forget tombstone 保留，不改写旧 Ledger 或新增 Memory 数据库。
- 手工与反思来源均核对不可变事件的 Seq、EventID、Type、checksum 及来源内容摘要；
  反思还核对原 trajectory，拒绝损坏或仓库身份不符的内容。
- 全部 Server/CLI 反思调用方沿用 Go 显式请求期限；未配置时保持原 30 秒默认。
  反思失败仍独立于代码任务终态，原调用用量和预留不改写。

## 本轮验证

| 命令 | 结果 | 退出码 |
| --- | --- | --- |
| `go test ./internal/memory ./internal/session ./internal/cli ./internal/admission ./cmd/server -run 'Memory\|Reflection\|Recall\|Deadline' -count=1 -json` | 76 pass / 1 skip | 0 |
| `go test -race ./internal/memory -run 'TestMemoryConcurrentReflectionPromotesOneDurableProposal\|TestReflectionKeysRemainIndependentAcrossRepositories\|TestNewSessionRecallsOnlyItsOwnersRepositoryMemory' -count=1 -json` | 3 pass | 0 |
| `go test ./... -count=1 -json` | 1,719 pass / 79 skip | 0 |
| `go vet ./...` | 通过 | 0 |
| `python -m pytest -q` | 2,513 pass / 17 skip / 3 warning | 0 |
| `go build -o <owned-product-binary> ./cmd/server` | 通过，产品使用 v2 二进制 | 0 |
| `git diff --check` | 通过 | 0 |

Go 数量含子测试。跳过保留在完整日志中，不能作为相应外部 runtime 通过。
公开测试接缝包括实际 SessionRunner、Memory Manage/Recall、旧 CLI Adapter，以及实际
CLI App 与 gRPC fixture；fixture 不代表真实模型。并发回归核对只晋升一个持久 proposal
和一个 catalog revision。TTL、forget、来源失效、owner/子任务拒绝及旧身份兼容均保留。

共保留 35 次已完成命令，其中 13 次非零退出；包括各项 RED，以及来源校验首次接线
影响已删除 Session 的回归失败和后续修复。没有用最终成功子集覆盖原失败。
完整索引位于本机 `.scratch/memory-product-20261015/test-summary-v1.json`，
SHA-256 `d02903e4c2c0d3cfd7f793c00da52daa185f00956f91857725db397c6e686756`。
结构化记录见[范围收据](memory-scope-2026-10-15.receipt.json)。

## 审阅与边界

Standards：剩余硬违规 0，独立 Smell 0。Spec：剩余源码缺陷 0，范围扩张 0。
结论属于 AI 源码审阅，不是 `HUMAN_REVIEWED` 或真实产品验收。

真实产品输入已冻结，首个原任务及其续行均因工具轮次上限停止且未写代码；
首轮真实反思返回空候选，不能视为 Experience 写回通过。原失败与事件游标冲突保留，
后续原需求增加单轮操作范围约束继续验证，未提供实现或 patch。
相关原始输入与产物在本机 `deployments/docker/.local/receipts/memory-product-20261015/v1/`；
初始 inputs SHA-256 `8d06f2a78a18d93d39990c92884c036e36d6bf45681c92eeb60cee5ac7337c9b`。

不包含路径迁移/跨仓库晋升、语义向量、广泛晋升 UI、Trace 后端验收或完整发布。
原有私有 AGENTS.md 改动保留且不提交；前端、Python 实现、协议与依赖未改。
