# 已知工具结果后的重复命令

范围：#18 实际 Python 任务暴露的 Harness 重复命令误拒绝，以及旧失败任务的显式结束。
工程状态 `IMPLEMENTED`；`VERIFIED` 仅指本轮源码回归、实际旧任务结束和新便携构建。
原 Python 尝试为 `FAILED`，新源码的十轮产品任务与发布验证尚未完成。

源码提交：`ff50d0ea703d3dbe3f9b64bfb1247cdd3a0d3b67`；基线：
`8581c5ede38ca21c0b5b1590496f13649aa0a26d`。三文件提交差异与审查的暂存差异一致，
SHA-256 `415c73f265c9347c5854aa1cc3b2b07f33d280645e13a110b8968b249ab5bbae`。
规范与规格两个审查轴均为 `AI_REVIEWED`、0 发现，不代表人工复核。

## 实际失败与完整分母

旧尝试使用源码 `8102785a1764ce16790682567ab8387f1a16b0b5` 及原冻结正常任务，
Session 为 `279ef7a6894d110e14b92c871f02ac16`。真实浏览器完成三轮，第四轮失败：

| 阶段 | 实际结果 |
| --- | --- |
| 右侧 Python 基线 | 81/81，通过 |
| 第二轮实现 | 毫秒调度及测试；完整项目测试 82/82，通过 |
| 第三轮边界 | 定向 7/7；一次完整命令报告 85 项但跳过 41 项，随后模型补齐同一沙箱内的依赖准备，完整 85/85、无跳过，通过 |
| 第四轮兼容测试 | 定向 14 项与完整 88 项各有一个 TypeError，内层退出码均为 1；外层 Shell 因打印退出码而返回 0，不能记为通过 |
| 第四轮修复后重跑 | Go 在派发前误拒绝新调用，run 失败；未完成候选代码未提案、未应用 |

原生 Session 保留 101 对工具调用／已知结果、12 项 `code/modified`，其中一项是相同内容替换，
不计作实际改动。候选 33 个文件与逐条 Ledger 变更链一致；第四轮的未完成测试修复另有重复尾部，
不把它当作合格成果。两个原工作副本全部 132 个文件、原 index 和 dirty/new 状态均保持原值。

46 次模型调用全部结算：foreground 43、反思 3，确认 2,887,749 tokens，无新增 unknown；
费用仍 unknown。四轮真实 backend capture 为 71／75／53／60 个 ended spans，共 259 个，
最后一轮有 4 个 ERROR。三项反思 proposal 和实际失败／阻断均保留，成功子集不替代整个分母。

## 根因、修复与旧任务结束

同一 run 的 `commit_pending` 到终态才关闭。前次 Bash 已有 durable 已知结果且沙箱停止，
模型 Edit 后以新调用 ID 再跑相同测试命令，原 guard 仍将它当作未确认副作用，
在新调用的准备／派发之前写入 `tool/unknown` 并拒绝执行。

- 共享 Runner 现在识别完整的前次 call/dispatched/result 链；结果已知且 runtime 已停止时，允许新身份再次执行，包括前次测试已知失败的情况。
- 同一调用 ID 继续走已有幂等结果重放；结果未知或 runtime 未停止时仍拒绝。授权、审批和外部执行边界不变。
- `ModelTaskIdle` 仅识别旧 guard 的可证明未派发拒绝：本身份没有准备、派发或结果，并有同 run／工具／精确参数的已知停止前次调用。实际 pending、活跃 run、关联任务和 CLI runtime 继续检查。
- 旧工作区和应用入口仍拒绝保留的 unknown 事实；没有重写旧事实或把失败候选升级为可应用提案。

第一次浏览器结束请求 HTTP 409 原样保留。修复后，经两轴审查的限定本机 operator
从已验证 continuation 取得原生 actor，核对固定 batch／owner、ActiveCalls=0 和原用量／预留，
再通过公共 Go `Gateway.FinishTask` 结束。实际退出 0，任务槽为空；
Session 的原有 1112 条事件完全不变，admission 仅增加一条 task_finished（1172→1173）。
已用 8,082,943、历史预留 3,806,024,576、unknown 与费用状态均未改变。
没有直接调用 Budget.FinishTask、重置凭据或编辑 SQLite。

浏览器、所属 Windows job／子进程已退出；Go／Python 控制服务和 Phoenix 恢复原停止状态。
33 个既有容器及 332 个 volume 身份保留。生命周期退出 0 只证明回收成功，不改变任务失败结果。

## 源码验证

| 验证 | 完整结果 | 退出码 |
| --- | --- | --- |
| 定向公开 SessionRunner／ModelTaskIdle 接缝 | 顶层 12 通过；含子项 16 通过，0 失败、0 跳过；真实临时 SQLite，只有外部 conversation/tool 使用 fixture | 0 |
| 本机 Windows 完整 Go | 顶层 1228 通过、78 跳过；含子项 1760 通过、81 跳过；47 个测试包通过、13 个包无测试文件 | 0 |
| `go vet ./...` | 通过 | 0 |
| 旧失败任务显式结束及原生读回 | 原 Session 不变，任务槽释放，计量保留，零模型调用 | 0 |
| 官方 Windows 便携构建和冻结检查 | 3219 个运行文件、606 个源码指纹、610 个参与源码／验证器指纹通过 | 0 |

命令：

```text
go test ./internal/session -run 'TestSessionRunner(ExecutesFreshCommand|ReplaysSameCommand|FencesFreshCommand|RetryReusesCommittedToolResult|WaitsForDurableApprovalBeforeToolDispatch|DenialReturnsToolErrorWithoutDispatch|RetrySearchesAllFailedPredecessorsForToolReceipt|RetryReadOnlyUnknownInvocationReexecutes)|TestPendingCommitsRecoveryProtocol|TestModelTaskIdleAfterUndispatchedRepeatRefusal' -count=1 -json
go test ./... -count=1 -json
go vet ./...
python scripts/build-windows.py --output 'deployments/windows/.local/迁移空间/CodeOps Agent command-repeat'
```

两个最初 RED 分别证明重复命令误拒绝和旧任务结束被阻断，均退出 1；GREEN 退出 0。
后续一次夹具 CAS 失败（expected 15、actual 16）也保留，改为等待公开 Runner.Close
完成后台收尾后构造历史事实。本机完整测试保留所有跳过；本轮未注入测试专用 wheel 输入，
相应两项 Python proposal fixture 明确跳过，没有隐式下载依赖。

[源码 CI 38818531022](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38818531022)四项通过：
Go／vet；Python 2563 通过、12 跳过、3 warnings；Frontend 12 通过及 build；Windows startup 10 通过。
这些结果不替代实际公开任务或发布验收。

## 产物与下一次输入

- 完整失败封存：`deployments/docker/.local/receipts/browser-tasks-20261015/python/python-proposal-v1/failed-repeated-command-artifact-manifest.json`，128 项，SHA-256 `8b1a9d25be337a9ba8b7e4c61f2132eeb8bcd5ba5c6b0e75f000398da4dc885e`。包含原始失败、跳过、审批、截图、backend、原生导出、候选副本及 Go 结束结果；全部 checksum 与文本密钥形状扫描通过。
- 源码修复／CI／构建／冻结索引：`deployments/docker/.local/receipts/repeated-command-20261016/artifact-manifest.json`，47 项，SHA-256 `54dc05203b58bb1db2347073765c5a0ed4877d59d9d8f90ff2f6b5b8f4416f32`；全部 checksum 与文本密钥形状扫描通过。
- 新包 manifest `1ba8d097268fce67892fb69ff402af11bb10e4322affba2e94da7fa4ec65d394`；server `071af67561ecea976bb5f193dfc3411e442addd89ca8575c765ca708f75eb362`；launcher `0e92e6fe1815b1f2fc7ae635c69f129856bbf368dd60d68ed7beff72923cef31`。
- 下一冻结输入 `237f8aa7882d0a13c3d3e9ed131a1a835ec06c86c30410d06b3815be41af63c8`；两个正常公开任务、每任务 12 场景、model/image pin、依赖和持久 batch 均保持原值。新驱动仅调整 freeze／bundle／输出路径与独立账号身份，沿用原有浏览器和资源生命周期。

#17/#18 保持 OPEN。下一次从新源码独立开始同一正常 Python 任务，旧三轮不能累计为新源码的十轮成功；
最终正常项目测试、审查应用、完整成功 Trace 和 #19 故障矩阵继续按各自范围验收。
