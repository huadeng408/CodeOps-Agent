# #13 源码 CI 的异步取消回归修正：2026-10-15

工程状态 IMPLEMENTED；本次只修正测试时序，生产取消与恢复实现没有改变。
实际产品恢复范围仍见[原证据](task-recovery-2026-10-15.md)和[原收据](task-recovery-2026-10-15.receipt.json)。
原收据的 SHA、结果、分母和 checksum 均保留。

## 失败与修正

提交 `1646cd0da818a121ad952b7df28019c5acfe41ad` 的[首轮源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38658859177)
有三项 job 成功、Go job 失败。失败测试为
`TestIndependentAgentRecoveryRetriesWorkspaceLifecycleUntilAcknowledged`，
原断言在取消 fence 被接受时立即要求异步 lifecycle 回调已发生。
此时 child task 已被禁止继续执行，但 worker 仍可能占用，回收尚未结束。

固定已有 fixture 的 `inputRelease` 后，旧断言十次均失败，稳定复现首轮 CI 的 `calls=0`。
测试现在等待公共 Run 状态到达 canceled，再核对回收失败仍为 unknown、至少一次回调、
再次取消确实重试，以及重启恢复只写一次 ACK。没有把资源回收失败改成成功，也没有放宽生产 fence。

## 验证范围

| 命令 / 尝试 | 结果 |
| --- | --- |
| 原测试 `go test ./internal/session -run '^TestIndependentAgentRecoveryRetriesWorkspaceLifecycleUntilAcknowledged$' -count=50 -json` | Windows 50 pass，退出 0；保留为原随机窗口未复现的尝试 |
| 固定 worker 等待点后的旧断言，`-count=10` | RED 10 fail，退出 1 |
| 修正后相同等待点，`-count=50` | 50 pass，退出 0 |
| 修正后 `go test -race ./internal/session -run '^TestIndependentAgentRecoveryRetriesWorkspaceLifecycleUntilAcknowledged$' -count=10 -json` | 10 pass，退出 0 |
| `go test ./internal/session -count=1 -json` | 247 pass / 1 skip / 0 fail，退出 0 |

本轮没有新增模型调用、Docker 资源或产品故障注入。Python/前端/依赖未改；完整发布和完整故障矩阵未运行。
首轮 CI 失败日志、所有上述本地尝试、退出码和校验和保留在 `.scratch/task-recovery-20261015/`。
后续准确提交的源码 CI 结果在对应 Issue 交付记录中单独绑定。
本地 Session 全量测试也编译了尚未交付的 #14 Memory 调用方改动；该部分不纳入此次提交，后续 CI 按准确提交运行。
两轴 AI 审查均为 0 项问题，不等于 HUMAN_REVIEWED；[独立收据](task-recovery-ci-2026-10-15.receipt.json)记录完整尝试及其绑定。
