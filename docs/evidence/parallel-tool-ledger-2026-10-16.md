# 并发只读工具入账：源码范围证据

工程状态：`IMPLEMENTED`。验证状态：`VERIFIED`，范围为 Go SessionRunner、真实 SQLite Ledger 集成、源码回归和 Windows 便携构建。
#17/#18 的十轮真实浏览器代码任务仍需单独验收；本记录不证明产品任务或发布合格。
结构化绑定见 [receipt](parallel-tool-ledger-2026-10-16.receipt.json)。

## 问题与改动

实际 Python 浏览器任务的首轮只读分析在五个工具并发时失败。
从对应的只读 checkpoint 恢复的 Read/Grep 参数均为合法 JSON，指向现存源文件。
公开 SessionRunner 与真实 SQLite Ledger 的回归复现了 `tool preparation persistence failed`；
临时诊断确认同一 Runner 的 leased writers 反复抢占序号，耗尽已有的 16 次 CAS 重试。
未持久化结果阻止任务成功的保护规则正确，本次保留。

源码提交：`29f5618c94f6c27aff27215bc174a74865fae2a9`。
只修改 `internal/session/runner.go` 和新增的公开接缝回归 `runner_parallel_tools_test.go`。
Runner 的两处 leased append 共用一个 mutex，覆盖读取、投影及 CAS 追加；
工具执行和批准等待仍在锁外，可以并发。权限、lease、取消、unknown-effect 和外部 writer 的 CAS 检查均保留。
没有修改协议、前端、模型、依赖或 Trace 成功契约。

## 当轮验证

| 范围 | 命令／结果 |
| --- | --- |
| 初始 RED | 八批五并发、重复十次：10/10 失败，均在首批返回准备持久化失败，退出 1 |
| 缩小对照 | 单批两个并发：3/3 通过；单批五个并发：3/3 失败，退出分别为 0／1 |
| 根因诊断 | 单批五并发，临时诊断得到 `ErrSequenceConflict`，退出 1；诊断输出已从生产源码删除 |
| 最终 GREEN | `go test ./internal/session -run '^TestSessionRunnerCompletesParallelReadonlyCalls$' -count=10 -v`：10/10 通过，退出 0；五个调用和结果各准确入账一次，完整 snapshot 验证通过 |
| 相关包 | `go test ./internal/session -count=1 -json`：251 个测试及子测试通过、1 个 owned subprocess helper 跳过，退出 0；后续最终全量回归绑定最终源码 |
| 最终 Windows 源码回归 | `go test ./... -count=1 -json`：60 个包，47 通过、13 无测试；1,746 个测试及子测试通过、79 跳过、0 失败，退出 0。顶层为 1,222 通过、76 跳过，两种统计不相加 |
| 静态与差异检查 | `go vet ./...`、`git diff --cached --check`、精确 staged 路径及密钥扫描：退出 0，0 发现 |
| Standards 审阅 | `AI_REVIEWED`：0 文档规范违规、0 actionable smells；检查相连的 append、取消、heartbeat、通知路径 |
| Spec 审阅 | `AI_REVIEWED`：0 未满足要求、0 越界、0 实现错误；未发现递归加锁或新的锁顺序环 |

外部 conversation 和工具执行在回归中使用 fixture，Ledger 使用真实 SQLite。
全部初始失败、缩小对照、临时诊断、跳过和最终日志保留，未用成功子集覆盖分母。
两轴审阅的 staged diff 与提交后的 three-dot diff checksum 一致。

[当前源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38792774597)
四项通过：Go 的 47 个有测试包及 vet 通过、13 个包无测试；
Python 2,563 通过／12 跳过／3 warnings；frontend 12/12 及 build 通过；Windows 启动契约 10/10。
CI 的 Go 命令不输出单项用例总数，不补造该分母。

## 保留的真实失败与下一轮输入

Python 原失败 Session 为 `02003e3a0025f1f3ff3227275f346dbb`。
产品入口的实际 Python 基线已通过 81/81；随后第一轮模型分析失败，不能计作十轮成功。
13 次模型调用全部结算，确认 469,789 tokens，新增 unknown 为 0，费用仍 unknown。
36 个 durable 工具结果均为退出 0，实际后端有 38 个工具 spans，其中两次准备失败；
该 run 的 75 个 spans 含 3 个 ERROR，完整 Trace 结果保留为 FAIL。
故障反思另有独立 Trace，其缺口没有被本次源码修复改成成功。
原 33 个工作副本文件、隔离副本、index 和已有改动保留；任务已结束，ActiveCalls 为 0、任务槽为空，所属资源已恢复原状态。

同阶段 Go HTTP-502 失败、一个未确认用量调用及其反思／Trace 缺口继续保留，未归因于本次已证实的 Python CAS 根因。
两份失败产物索引各有 42／32 项，checksum 及全部索引成员重新核对一致。
本次源码修复没有新增模型调用，也没有改写历史 unknown 或释放其原预留。

官方 `python scripts/build-windows.py --output 'deployments/windows/.local/迁移空间/CodeOps Agent ledger'` 退出 0。
新包的 3,219 个运行文件和 604 个构建源码指纹核对一致；manifest SHA-256
`61e1a8162da6c691ec742965c382f39d07faf8942ce2500b7373bdca64c75bcf`。
保留原两个公开任务及其工作副本、model/image pin、同批次、16 次工具轮次和 600 秒 Go 请求边界，
重新冻结 608 个源码／验证器指纹、每项任务原有的 12 项场景分母；freeze SHA-256
`65f1042abd56ab65ab870d27a579f9f7642bdc48628fc14fe1e182cf040bcfcf`。

源码产物索引：`deployments/docker/.local/receipts/browser-tasks-20261015/parallel-ledger-source/artifact-manifest.json`，
37 项产物，SHA-256 `604364284e259f93f04bec2f41d2967963c604df2ab68871c184990af11ce2bd`。

源码／集成／构建范围已验证且无未解决 blocker。真实十轮代码任务：尚未验收；完整发布：未运行。
#17/#18 保持 OPEN，#19 的完整故障矩阵继续独立验收。
