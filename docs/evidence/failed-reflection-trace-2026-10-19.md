# 失败轮次反思保留 Agent 父链：源码修复

工程状态：`IMPLEMENTED`；`VERIFIED` 限于下面的源码／集成范围。完整 Go 检查和
独立审阅已完成，新真实运行时仍待验证。本修复不把原 #17 任务改成 Trace 通过，也不修改
`product-integrity-v2` 分析器、原收据、失败／取消分母、Memory 事实或发布门槛。

## 原始症状与边界

[#17 新 Go 原任务](go-browser-task-memory-2026-10-19.md)的功能、正常测试、提案应用和
34 条同任务经验均可核对，143 次调用全部结算；原正式 v2 仍退出 3／INCOMPLETE。
全部 canonical run 的 731 个 span 中缺一个失败轮次反思的 provider.call／attempt。
实际后端另有一个三 span 的 memory.reflect 空父根，没有指向本轮 Agent 的 parent/link。
该诊断树和原失败收据已封存，没有人工拼接成通过结果。

问题位于 Go Runner 的共用失败出口：正常完成将本轮 context 交给 Memory，失败出口
却使用服务级 `r.ctx`。反思和 provider 因此另起根，虽然真实调用、计量和经验仍成功。
本次只把当前 Agent context 传入该出口及全部七个调用点；终态写入继续使用原有独立
五秒期限，Memory 继续使用自己的期限，并保留 Runner 关闭时的取消传播。
Go 鉴权、Ledger/CAS、模型准入、失败状态、工具结果及 gRPC/schema 均保持原契约。

## 公开接缝的 RED → GREEN

在既有 `SessionRunner.SubmitMessage`／`RunStatus` 和 `SessionMemory.Commit` 接缝，
使用现有真实 OpenTelemetry SDK recorder 观察 Agent／反思父子关系，不调用私有 fail
方法，也不手工构造一个声称通过的产品 Trace。

修复前运行：

`go test ./internal/session -run '^TestRunnerReflectionKeepsTerminalRunTrace$' -count=1 -v`

退出 1：completed 控制通过，failed 精确失败于反思 Parent SpanID 全零且 TraceID 不同；
该回归本体约 0.09 秒。先保留 RED，再改生产源码；同命令退出 0，两个子例均通过。

随后补充 failed-shutdown 控制：反思保持阻塞直到 Runner 取消，关闭必须在测试的
三秒上界内结束，不能等到 Memory 自己的五秒期限。仍核对失败 RunStatus／ERROR
没有被改成完成，反思父链与准入 run identity 均保留。

按原始信号依次区分三种预测：仅失败出口丢 context 应在 Go 接缝直接复现；若是
gRPC 提取问题，Go 本地应通过；若只是后端筛选遗漏，真实额外根应有合法关联。
本地 RED 和无 parent/link 的真实树排除了后两种解释；只改变共享出口的 context 后
同一接缝转绿，没有添加调试日志、依赖、配置、重试或兼容层。

## 验证记录

| 命令／范围 | 当前结果 |
| --- | --- |
| 新父链回归，修复前 | 退出 1，completed 通过／failed 父链断开 |
| 同一回归，最小修复后 | 退出 0，两个子例通过 |
| 父链三场景及已有默认／显式反思期限 | 退出 0，2 个顶层／7 条父子 PASS |
| 上述定向 `go test -race` | 退出 0，7 PASS，无 race 报告 |
| `go test ./internal/session ./internal/memory -count=1 -json` | 退出 0，330 PASS／4 SKIP／0 FAIL，两个包通过 |
| `gofmt -d`、`git diff --check` | 退出 0，无本次格式／空白问题 |
| `go test ./... -count=1 -json` | 退出 0，1,829 PASS／81 SKIP／0 FAIL；47 包通过，13 包无测试 |
| `go vet ./...` | 退出 0，无诊断 |
| 独立 Standards／Spec | 各 0 项发现，均为 `AI_REVIEWED`；冻结五文件候选，不含用户 AGENTS.md |
| 修复后的真实产品／完整发布 | 未运行；原 #17 Trace 缺口保留 |

本机源码验证产物位于 `.scratch/failed-reflection-trace-20261019/`：原 RED、GREEN、
定向、race、相关包及完整 Go JSONL、vet 输出、两轴审阅和短计划均保留；
`validation.json` 记录完整分母、命令、退出码、源码与产物 checksum。81 项跳过仍按原始
JSONL 记录，不作为真实 runtime 通过。原产品 777 项封存清单继续独立绑定
旧运行，不随本次源码变动改写。用户已有 AGENTS.md 改动保留并排除提交。
