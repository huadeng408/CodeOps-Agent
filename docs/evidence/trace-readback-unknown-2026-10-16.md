# 已停止 unknown 调用的 Trace 读回

工程状态 `IMPLEMENTED`；`VERIFIED` 限于 native 导出完整性审计、CLI／外部 HTTP fixture 回归及原 Go 导出的只读复查。真实后端重新读回、公开代码任务重新运行、完整产品和发布没有在本轮执行。#17/#18 原 Memory／严格 Trace 结果与 #19 剩余范围继续保留。

源码固定点为 `5e872ee50dd6d7576e64ad8d46acd72d622e4774` 加本轮五项源码／测试修改，精确指纹见下方验证摘要。使用现有 `cmd/trace-audit`、`eval.product_trace` 和同一原生 Budget／Ledger 读接口，没有新增执行器或模型准入路径。

## 问题与结果

原 native 审计在查询后端前要求目标任务全部调用结算；原 Go 任务虽然已停止并结束，两个 usage unknown 仍导致拒绝。最小案例通过真实 `Budget.MarkUnknown`／`FinishTask` 构造：owner、事件链、前缀及静止状态有效，仍报 `product model usage is incomplete`。未结束任务的负向对照继续拒绝。

现在 native 审计负责确认导出完整性和可安全观察的静止状态；Python CLI 在读回后单独检查最终结算条件。

| 边界 | 当前行为 |
| --- | --- |
| 事件链、Actor／owner、Session/root、原 admission 前缀 | 仍由 native reader 与 Budget 投影校验；篡改、错 owner、缺准入继续拒绝 |
| 活跃调用／未结束任务 | 仍要求 ActiveCalls=0、TaskID 为空；unknown 本身不能替代停止或结束证明 |
| 已结束 unknown | 允许后端采集；不结算、不清预留、不补造费用或重新派发 |
| 最终 Trace 验收 | 仍同时要求原严格 Trace 契约 PASS、后端完整可用及目标任务全部调用结算；只要存在未结算 ID，receipt 保持 BLOCKED／CLI 退出 3 |
| 采集和用量分别记录 | receipt 新增 `model_usage.state` 与完整 `unsettled_call_ids`；全树采集可以 complete，同时最终状态因 usage unknown 保持 BLOCKED |

新增两轮外部 HTTP fixture 的全部九个 span 均读回、原严格 report 为 PASS，结果仍为 `status=BLOCKED`、`capture_state=complete`、`model_usage={state:unknown, unsettled_call_ids:[call-2]}`。最初只移除 native 前置条件时该回归实际错误返回 VERIFIED；随后补齐最终用量门禁，RED／GREEN 均保留。原工具／模型／全部轮次分母、拓扑、源／二进制 pin、敏感数据检查和严格 Trace 合同没有放宽。

## 原 Go 导出的只读复查

相同三份封存导出与最后一个原 run，在修复前 native 命令退出 1，修复后退出 0；三份输入 checksum 前后相同。它仅证明当前审计器能检查这份真实历史导出，不是新的后端 Trace 或新产品运行。

原十四轮、两个 unknown、原预留、十三次 Memory commit-blocked 和严格 Trace FAIL 均未改写；没有打开已关闭任务或调用模型。原任务的[完整范围证据](go-browser-task-2026-10-16.md)继续适用其原始运行和失败分母。

## 验证与完整失败分母

| 命令／接缝 | 结果 |
| --- | --- |
| `go test ./cmd/trace-audit -count=1 -json` | RED：ended-unknown 子案例被旧全结算条件拒绝；GREEN：9 个父／子 PASS、0 FAIL/SKIP，退出 0；其余篡改、错 owner、未结束任务仍拒绝 |
| `python -m pytest tests/eval/test_product_trace.py -k unknown -q --tb=short` | RED：错误返回 VERIFIED；GREEN：1 PASS、17 deselected，退出 0，所有输入导出 hash 不变 |
| Trace 三文件完整回归 | `python -m pytest tests/eval/test_product_trace.py tests/eval/test_trace_contract.py tests/eval/test_phoenix_readback.py -q --tb=short`；72 PASS、0 FAIL/SKIP，退出 0 |
| 首次完整 Go | 1,772 PASS、81 SKIP、1 FAIL；46 包 PASS、1 包 FAIL、13 无测试，退出 1。`TestLauncherRejectsUnlistedRuntimeFile` 退出时未输出预期拒绝原因；启动器未改动 |
| 完整启动器包复查 | `go test ./cmd/launcher -count=1 -json`；统一 Go 1.26 的 PATH 后 6 PASS，退出 0 |
| 统一工具链后完整 Go | `go test ./... -count=1 -json`；1,773 PASS、81 SKIP、0 FAIL；47 包 PASS、13 无测试，退出 0 |
| 静态检查 | `go vet ./...`、相关 `git diff --check` 均退出 0 |

首次全量的主命令使用固定 Go 1.26.0，但嵌套 build 从默认 PATH 解析到 Go 1.24.5。复查和最终全量统一 PATH 为已安装的 Go 1.26.0，保留 `GOTOOLCHAIN=local`、`GOPROXY=off`，checksum 验证未关闭。首轮失败及环境差异保留；尚不能仅由重跑通过证明该失败的唯一根因。

本轮没有修改 frontend、orchestrator 产品运行模块、protobuf、依赖或 Windows 启动器。完整 Python／frontend 与 Windows 包未在本机重跑或重建；对应 Trace Python 文件已完整回归。原源码审查 Standards 0／Spec 0，分类 `AI_REVIEWED`，不代表人工复核或发布合格。

全部命令、退出码、原失败、两份 CLI fixture 的输入／receipt／完整 readback、原 Go 导出只读 RED／GREEN、源码指纹及输出 checksum 位于 `.scratch/trace-readback-unknown-20261016/`。验证摘要 `validation-summary.json` 的 SHA-256 为 `c7509d14e3e0e449ed2e0839da5913981fa1bcc34147c66eb67ef69559d45048`。运行数据保持私有，不进入提交；Session Ledger 继续是产品事实的唯一写入源。
