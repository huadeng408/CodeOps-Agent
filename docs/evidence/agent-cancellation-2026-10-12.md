# Agent 取消终态竞态：Go 范围证据

本次普通修复限 SessionRunner／AgentTask 的取消持久化与 Go 源码回归。
完整 OpenSandbox 物理取消、产品浏览器、模型 Trace 和发布验收分别待验证，
不影响本次 Go 范围的交付状态。

## 触发与修复

基线提交 `4f755328bd6e259f93df97aa900f3e026666feb3` 的
[源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38153140682)
在 `TestIndependentAgentCancellationAndReadOnlyPolicy` 失败。
取消路径先通知 worker 停止，再写取消终态，因而 worker 可能抢先写入
`session/run-failed`；公开 Session 视图将已取消任务显示为失败。

固定 Linux 镜像下，原用例 1,000 次出现 3 次失败；新增公开视图最小用例
1,000 次出现 1 次失败。临时在停止 worker 后增加 20ms 调度窗口，
50 次全部出现 `agent/task-canceled` → `session/run-failed` 的同一事件顺序。
所有失败和分母均保留。正式修复已移除该延时和诊断打印。

Go 现在先用 Ledger/CAS 持久化取消 fence，再停止 worker。
自然终态、失败时停止、父 Session 删除和正常关闭时的 lease 恢复契约保留。
新增回归通过 `SpawnAgent`、`AgentTask` 和 `Workbench.Get` 检查公开行为：
取消后运行必须为 `RunCanceled`，且具有完成时间。

## 验证范围

| 验证 | 本轮结果 |
| --- | --- |
| Windows 两个取消用例，各 `-count=50` | 100 通过，0 失败，退出 0 |
| 同一源码编译的 Linux 两个取消用例，各 `-test.count=1000` | 2,000 通过，0 失败，退出 0，输出未截断 |
| `go test ./... -count=1 -json` | 1,391 通过、60 跳过、0 失败；43 包通过、14 包无测试，退出 0 |
| `go vet ./...` | 退出 0 |
| Standards／Spec 审查 | 分轴记录在本次收据的 review artifact |
| 实际 OpenSandbox／产品／模型 Trace／完整发布 | 本次未运行；见 [当前缺口](../GOAL.md) |

完整测试动作分母包含父测试与子测试，不能直接视为独立测试用例数量。
Linux 诊断沿用已有 Docker Runner 和本地固定镜像
`golang@sha256:699337d620559a59b4a2bb298ad59611e535d2ee755a34cf2d2a98f37578dc80`，
该诊断不作为 OpenSandbox 证据。

原始 CI 日志、RED／GREEN 输出、退出码、编译产物和 441 份 Go／模块文件指纹保存在
[本次不可改写收据](../../.scratch/opensandbox-integration/cancellation-f29677dd-1b4d-45c5-b4e6-da0193c43a91/receipt.json)，
SHA-256 为 `d2f63261925e66b7e9569c6c7ba6e4ed86f8124c51b0c8f81cdae8b4708df716`。
Standards 与 Spec 均为 `AI_REVIEWED`、剩余 0 项；
[审查记录](../../.scratch/opensandbox-integration/cancellation-review.md)限该修复。
提交绑定另行核对运行源码，不改写旧 foundation 收据。
