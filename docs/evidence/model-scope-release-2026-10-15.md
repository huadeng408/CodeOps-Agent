# 并发取消的模型 scope 释放

范围：现有 Go Gateway/SessionRunner 取消契约。工程 `IMPLEMENTED`，公开 Go 接缝单测／
集成 `VERIFIED`。不声明完整取消故障矩阵、#14 Memory 产品或发布通过。

[源码 CI 38664584874](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38664584874)
在 `TestGatewayOldRunCancellationCannotRevokeTheNewRunsProviderRequest` 提交第二条消息时失败：
`session state conflict: another session run is active`。其余三个 job 成功，原失败保留。

竞争清理会在 Ledger 释放完成前移除内存 scope；另一清理路径可能因此把已停止请求的
取消结果写成 `unknown`。普通时序 50 次及仅延迟回调 5 次均未复现；公开 EventLog
fixture 暂停第一条 scope release，等待竞争清理的释放／取消决定后，原实现 3/3 失败。
没有把这个真实竞争窗口掩盖成宽松等待或无条件成功。

共享 stopScope 现在先在准入锁下撤销 context，等待已有调用及计量完成，Ledger 释放
成功后才删除精确 scope。首次调用和重试都拒绝已撤销 context；释放失败仍保留 fence。
普通 release 与取消复用该实现。既有 CAS 保证并发释放幂等，旧 run 不能撤销新 run。

| 新鲜命令 | 结果 | 退出码 |
| --- | --- | --- |
| `go test ./internal/admission -run '^TestGatewayOldRunCancellationCannotRevokeTheNewRunsProviderRequest$' -count=3 -json`（持久释放暂停 fixture，修复前） | 3 fail | 1 |
| 同一公开回归，修复后 `-count=10` | 10 pass，明确断言 stopped | 0 |
| `go test ./internal/admission -count=1 -json` | 135 pass / 4 skip | 0 |
| `go test -race ./internal/admission -run 'TestGatewayOldRunCancellationCannotRevokeTheNewRunsProviderRequest\|TestGatewayCancellation' -count=10 -json` | 10 pass | 0 |
| `go vet ./internal/admission`、`git diff --check` | 通过 | 0 |

完整输出保存在本机 `.scratch/memory-product-20261015/`，摘要及 SHA-256 见
[范围收据](model-scope-release-2026-10-15.receipt.json)。原全量 Memory/Go/Python
结果仍绑定 [9ee7a6bb 的范围证据](memory-scope-2026-10-15.md)，不冒充本补丁的新全量结果。
修复提交 `819216856e4eb80a2c55931afeb6e82172b4d588` 的
[完整源码 CI 38666102285](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38666102285)
四个 job 全部通过；原范围收据保留提交时的状态。Standards：硬违规 0／Smell 0；Spec：源码缺陷 0，
属于现有取消契约的必要修复。均为 `AI_REVIEWED`。
