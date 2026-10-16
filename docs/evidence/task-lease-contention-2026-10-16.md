# 工作区续租与 Ledger 并发写入

工程状态：`IMPLEMENTED`，源码与集成范围 `VERIFIED`。本次声明范围为 Go 工作区准入的源码与 SQLite／原生 Git 集成回归。真实浏览器、模型调用、Python、frontend 和发布未运行；#17／#18 的原 Memory／Trace 缺口仍保留。

## 问题与修复

过期 Task Workspace 已完成权限、工具和文件核验后，另一条正常 heartbeat 或工具事件可能先写入 Ledger，使续租的 expected sequence 过期。原实现直接返回冲突，产品把合法读取显示为 `task workspace admission unavailable`。

修复在既有 `TaskWorkspaces.modelWorkspace` 中处理，只对 `ErrSequenceConflict` 有限重试，最多八次冲突。每次均重新读取、校验完整 Ledger，再检查 owner、工作区、应用状态、已派发工具、活跃 run／lease 及工具结果。非 CAS 错误直接返回；续租成功后的完整重读不消耗冲突额度。`ModelWorkspace` 和 `ToolWorkspace` 共用同一实现。

未增加执行链、依赖、配置或后台重试服务。重试的是工作区准入，工具本身不因此重复执行；Trace、预算、Memory、任务结束及原验收规则未改变。

历史 Python 任务的原生 #2148–#2159 有七项 Read 准入失败，发生在 lease 到期之后、#2162 续租之前。其原始错误只保存通用分类，不能逐条还原内部原因；本次确定性回归证明该竞争缺陷存在，不把它升级为七项旧失败均已证实修复。三项旧 Memory 失败及严格 Trace FAIL 不改写。

## 验证

测试沿用已确认的公开 `TaskWorkspaces.ToolWorkspace`／`ModelWorkspace` 接缝，底层为真实 SQLite Ledger 和原生 Git 工作区。在续租 Append 前插入一条真实 heartbeat，避免靠 goroutine 时序碰运气。

- 首个 RED：合法读取因 `expected=13, actual=14` 失败，命令退出 1；最小修复后同例退出 0。
- 双轴审查发现首版的最后一次续租成功仍会返回冲突；新增该场景先 RED，父／子两项 FAIL、退出 1，修正后通过。原首版全 Go 结果单独保留，不代替最终候选结果。
- 最终定向命令 `go test ./internal/session -run '^(TestTaskWorkspaceAdmitsConcurrentDispatchedReads|TestExpiredTaskWorkspaceAdmissionReconcilesSameLease|TestTaskWorkspace.*)$' -count=1 -json`：11 PASS、0 FAIL／SKIP，退出 0。包含普通 heartbeat、前七次冲突后第八次成功、未知结果、取消、存储错误和持续竞争；原 owner／dispatch／mutation 拒绝断言保留。
- 最终 `go test ./... -count=1 -json`：1,779 PASS、81 SKIP、0 FAIL；47 个 package PASS，13 个无测试文件，退出 0。以上测试数量均按 Go JSON 的父／子测试计数，跳过项原文保留。
- 最终 `go vet ./...`、`git diff --cached --check` 均退出 0；gofmt 与 staged 源码指纹核对通过，秘密扫描不输出匹配值。

最终全量验证结果与逐项命令／退出码保存在下述封存收据。测试使用已安装 Go 1.26.0，并同步设置 PATH、`GOTOOLCHAIN=local`、`GOPROXY=off`；checksum 验证未改变。

源码指纹：

- `internal/session/task_workspace.go`：`97f20625e27f3fc171866defc95d1e31fb9343808670697a93b9643d2b07fb9e`。
- `internal/session/task_workspace_test.go`：`a5a0248064a00f32d5564b0eb7f88d402a17f29e8f26602fead1bc5c6ad600dd`。

## Standards

最终 0 项未解决问题；原最后一次续租 P2 已解决。持续竞争有界，非 CAS 错误直接返回，安全检查保留。

## Spec

最终 0 项未解决问题；未改变正式规格或票的验收范围。两个轴均为 `AI_REVIEWED`，不构成逐条人工复核或真实产品验收。

## 收据与限制

本轮基点为 `c207ea0588a4df82862bfa08b8516a6c50a74ba3`。本机收据在 `.scratch/task-lease-cas-20261016/`：`validation-summary.json` 保存完整分母和源码绑定，`artifact-manifest.json` 保存逐项 SHA-256；两个 RED、首版和最终回归、独立审查及 staged 源码快照分别保留。

`validation-summary.json` SHA-256 为 `dfc5c78e769873847fd8a8792a3bcef10ad14a21c1e03f7291bdb9b2c642dfb5`。首版全 Go 的 1,778 PASS／81 SKIP、vet 退出 0 另存；审查指出的边界问题仍经新增 RED 后修正，不用首版通过掩盖它。

未重跑十轮真实产品任务，不能据此关闭 #17／#18；真实浏览器和 Trace 验收仍需使用新构建。原 Python 原生导出 SHA-256 `272d5ad9f39ff7fd2bfae954cb9acc3c6ef4eb214be4b31d8ac9a01bb8b34b41` 未改变。用户既有 AGENTS.md 改动保留、不进入本次提交；Docker、业务数据及模型预算无变更。
