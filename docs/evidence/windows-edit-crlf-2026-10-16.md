# Windows Read/Edit 换行兼容：源码范围证据

工程状态：`IMPLEMENTED`。验证状态：`VERIFIED`，范围为 Go 文件工具、完整源码回归和便携构建。
#17 的真实十轮代码任务仍需单独验收；本记录不证明产品任务或发布合格。
结构化绑定见 [receipt](windows-edit-crlf-2026-10-16.receipt.json)。

## 问题与改动

实际浏览器任务中，`Read` 将 CRLF 显示成 LF，而 `Edit` 使用原始字节精确匹配。
模型第一次编辑还存在额外缩进；该请求按既有规则失败且没有改文件。
模型随后提出的正确 LF 片段在 CRLF 副本中仍不能按原始字节命中，只有换行归一化后才唯一匹配。
该恢复请求没有执行，任务已通过实际浏览器取消并结束；公开 Executor 回归随后复现了同样的换行缺口。

源码提交：`91c45e7d39e875335ca94cb635784da9e4d056fb`。
只修改 `internal/tools/edit.go` 和对应 `file_safety_test.go`。
原始精确匹配仍优先；未命中的多行 LF 片段仅在文件换行统一为 CRLF 时映射回 CRLF。
替换保持 CRLF、外部内容、权限和完整 Change Before/After 字节。
非唯一匹配、变化的缩进及混合换行仍拒绝推断；显式 `replace_all` 保留。
未改变路径、符号链接、授权、原子写入、协议、模型、依赖或 Trace 成功契约。

## 当轮验证

| 范围 | 命令与结果 |
| --- | --- |
| 首次 RED | `go test ./internal/tools -run '^TestFileToolsEditLFExcerptInCRLFFile$' -count=1`：1 项失败，`old text not found`，退出 1 |
| 首次 GREEN | 同一测试：1 项通过，退出 0 |
| 审阅补充 RED | 公开 Executor 的独立 CR/CRLF 混合用例出现不应发生的修改，退出 1；随后修正判定并保留失败日志 |
| 最终定向 | `go test ./internal/tools -run '^TestFileTools(Edit\|WriteAndEdit\|RejectWorkspace)' -count=1`：退出 0；含正确换行、歧义、替换全部、缩进、混合换行及不改文件的边界 |
| 最终 Windows 源码回归 | `go test ./... -count=1 -json`：60 个包，47 通过、13 无测试；测试及子测试 1,745 通过、79 跳过，0 失败，退出 0。顶层测试为 1,221 通过、76 跳过；两种统计不相加 |
| 静态与差异检查 | `go vet ./...`、`git diff --check`、精确 staged 路径密钥扫描：退出 0；扫描 0 发现 |
| 双轴审阅 | `AI_REVIEWED`：Standards 0 违规／0 smells；Spec 初始 1 项独立 CR 边界问题已先红后绿修复，复审 0 未解决项 |

初稿完整 Go 回归也曾通过，但其源码指纹单独保留，未代替最终回归。
全部 RED、初稿、最终输出、跳过和审阅记录都进入产物索引。

[当前源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38781360295)
四项均成功：Go 全部 47 个有测试包通过、13 个包无测试，`go vet` 成功；
Python 2,563 通过／12 跳过／3 warnings；frontend 12/12 和 build 成功；Windows 启动契约 10/10。
CI 的 Go 命令不输出单项用例总数，不为它补造用例分母。

## 保留的真实失败尝试

本次停止的 Session `61c710ae77171582cf55301564c6aaaa` 有 3 次浏览器消息尝试：
2 次完整回答、1 次取消，没有代码修改或提案应用，不能计作十轮任务成功。
原生 Go 导出保留 462 个事件；真实后端读回 101 个 spans，含 2 个 ERROR，整项 Trace 为 FAIL。
两次已完成的只读轮次曾分别通过自己的后端诊断，未用这些成功子集替代整个任务结果。

本次 20 次模型调用全部结算，确认 583,771 tokens，新增 unknown 为 0，费用仍 unknown。
历史 unknown 与原预留不改写。实际浏览器取消返回 202，任务结束返回 200；
结束后活跃调用为 0、任务槽为空，所属产品进程和本轮启动的两个服务均已回收或恢复原停止状态。
原工作副本及隔离副本各 99 个文件、原 index 和既有 dirty/new 改动保持原值。

此前六轮额度失败、180 秒 timeout 和所有原收据继续保留，见
[工具额度及前次失败证据](harness-tool-allowance-2026-10-16.md)。
本次失败索引：`deployments/docker/.local/receipts/browser-tasks-20261015/go/timeout600-v1/failed-crlf-attempt-artifact-manifest.json`，
47 项产物，SHA-256 `3a5e3c6d4e0c92b5116bec6a66aa594eee6e02e234ec8c3e690011c277907936`。

## 新构建与验收边界

`python scripts/build-windows.py --output 'deployments/windows/.local/迁移空间/CodeOps Agent crlf'` 退出 0。
新包有 3,219 个运行文件，manifest SHA-256
`e7f25bdf9110a9587e19a62e7c9a5df65aa5df8633b2f94f0154ba1d12136d97`。
两项原公开任务、原输入和 12 项场景分母重新冻结，603 个构建源码及 607 个参与源码／验证器指纹均已核对；
新 freeze SHA-256 `ca16b4040332efc855ad99958a5a9d405b4cb50817f21641ae1e7948d0b30220`。
沿用原模型、镜像、600 秒有界请求、同一批次、单任务和进程内代理约定。

源码产物索引：`deployments/docker/.local/receipts/browser-tasks-20261015/crlf-edit-source/artifact-manifest.json`，
41 项产物，SHA-256 `d79a89ae42cd15a891eee53de399b55b4bad1d83f662b4f343ab67e840617be0`。

源码／集成范围已验证。真实新代码任务及完整成功 Trace：未验收；完整发布：未运行。
本次源码修复没有未解决 blocker，#17 继续保持 OPEN，后续 #18/#19 按各自范围推进。
