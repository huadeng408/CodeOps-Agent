# 取消后的 Memory 轨迹与暂时性 provider 错误分类

工程状态：`IMPLEMENTED`。本次修改 Go 的两处共享投影边界及对应测试，不改变模型重试、准入、计量或持久事实。声明验收范围为 Go 源码回归与只读原生轨迹重放；真实反思、浏览器错误展示及完整发布另行验证。

## 问题与修复

[Go 十轮任务](go-browser-task-2026-10-16.md)的第一轮实际取消并记录 `cancellation_state=stopped`，后续轮次正常完成，但 `BuildSessionTrajectory` 只识别 completed／failed 终态。原 run 因此始终没有终态，Memory 在 provider 派发前被拒绝。

`internal/memory/trajectory.go` 现在保留 canceled 原始终态及来源。只有已确认 stopped、从未批准或派发的 pending 工具调用可以从 open 集合释放；取消本身仍不能晋升完整轨迹。后续完成可以满足轨迹条件，unknown 取消、缺失状态、未收结果的 approved／dispatched 调用、旧式 open 调用及 unknown 工具结果继续拒绝。复用已有终态重复检查和失败路径，不改写旧事件，不写入第二份状态。

同次任务的两次 HTTP 524 已由 Python／Go client 传递为 `provider_transient_unknown`，Session Runner 却将其降为通用错误。`internal/session/runner.go` 现在保留该稳定分类并返回固定提示：provider 请求暂时失败、用量未知、保留原预留。provider 原文不进入公开错误；没有新增重试、结算或清零。

## 验证范围

- 先在编译器 overlay 中观察 RED：取消后完成的四个正例失败，六个 fail-closed 反例通过；暂时性错误仍被分类为 `continuation_runtime_error`。随后两个最小候选通过；实际写入的四个文件与双轴复核候选 SHA-256 完全相同。
- 原生只读重放使用已封存第二轮输入 SHA-256 `6f9e59060733bb0d3c4b5a73fbdc3c7f88bcae1eb867cf75e838ccc5db1dfb62`，验证真实 641 事件前缀。旧代码在写入前拒绝；修复后到达真实 `Memory.Commit` 的首次写入边界并由观察器拦截。没有打开 live Ledger、实际写入或模型调用；它只证明准入缺口，不能替代真实反思成功。
- 实际源码定向命令 `go test ./tests/go ./internal/memory ./internal/session -run 'Trajectory|LedgerMemory|PublicRunError' -count=1 -json`：退出 0，60 个父／子测试 PASS、0 FAIL；可选跨进程 Memory fixture 测试跳过 1 项。

- 完整实际源码 `go test ./... -count=1 -json`：退出 0，1,771 个父／子测试 PASS、81 个 SKIP、0 FAIL；47 个 package 通过、13 个 package 无测试。完整 skip 名单保存在收据中。`go vet ./...` 退出 0。
- 同一真实 641 事件前缀在实际源码中再次只读重放，退出 0；首次写入仍被拦截，实际写入 0、模型调用 0。该结果与编译器 overlay 分别保存。
- 额外开启既有 `CODE_AGENT_RUN_MEMORY_INTEGRATION=1` 跨进程 fixture：命令退出 1，10 项检查中 7 项通过，owner isolation／source provenance／recovery without model 三项失败。对本次四个改动文件全部使用精确 HEAD 内容的 overlay 再运行，仍退出 1；相同三项失败，另有 token-budget 检查失败。两份失败和全部分母保留，未修改断言或用默认 skip 隐去。该 fixture 缺口在本次修改前已存在，其具体根因尚未确认；fixture 集成范围失败，普通源码修复按定向／完整 Go 范围交付。

上述命令使用已安装的 Go 1.26.0、`GOTOOLCHAIN=local`、`GOPROXY=off`，未关闭 checksum 验证。未修改 Python、protobuf 或 frontend，本次未重跑其测试／build。新便携包、真实反思、实际浏览器错误展示和发布未运行；旧产品 receipt 不为新源码背书。

## 绑定与收据

比较基点为 `04e31125c9a29bdbb2671e8bc9b3e6c94f4bc1af`。实际源码内容如下，用户的 `AGENTS.md` 改动保留且不进入本次提交。

| 文件 | SHA-256 |
| --- | --- |
| `internal/memory/trajectory.go` | `a93567834b3b296fba94f4bec03275e5df4f6105d7e4d68396f85718973af8e5` |
| `tests/go/memory_trajectory_test.go` | `c8ef40ff9c0941bcc98f64af639f455a6cf4fc07adcb72614e191d275fe9b116` |
| `internal/session/runner.go` | `7588b1c1b273209445bd3621accf8051f8b3661ef8c160614635ed87f6e99418` |
| `internal/session/run_error_test.go` | `1e7f92f6dd18a138ea803ccfe41dd44fe2dde56eb433bb0cde4c7597a544881f` |

本机收据为 `.scratch/go-browser-command-repeat-20261016/memory-overlay/` 的 RED／GREEN、只读 native replay 和候选双轴审查，以及同级 `source-validation/` 的实际源码完整命令、退出码、分母和 stdout／stderr 指纹。候选 Standards 0 项、Spec 0 项，分类为 `AI_REVIEWED`。

交付检查清单为同级 `source-validation-artifact-manifest.json`；原生 fixture、候选、实际源码检查和审查记录按各自原始结果保存，私有产物不进入提交。

交付实际 diff 的双轴审查也分别为 Standards 0 项、Spec 0 项；记录在同级 `delivery-standards-review.md`、`delivery-spec-review.md`，未将 AI 审查标为人工审查。可选集成原生 fixture 收据分别在 `.runtime/e2e/memory-ledger-process-459274614/` 与 `.runtime/e2e/memory-ledger-process-1710861566/`；失败根因未随本修复扩展处理。

后续已定位并修正该可选 fixture 的异步读取竞态，实际源码三次各 10/10；另完成取消后同会话真实反思写回，见[产品与 fixture 后续证据](memory-cancellation-product-2026-10-16.md)。本页保留上轮命令、分母和当时的失败，不用后续结果覆盖原收据。

旧 Go／Python 产品任务收据保持原源码和失败结果。该源码修复不回填旧反思、删去失败轮次、重写 Trace 或宣称 #17／#18 整体验收通过。
