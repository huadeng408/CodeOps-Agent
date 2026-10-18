# 待审批工具取消与执行 Trace

工程状态 `IMPLEMENTED`。本次修复 Go Harness 在取消待审批工具时缺少最终原生结果，以及执行 span 在授权前创建的两个问题。固定点 `afc99c722d11a1a2736f0bc3064d9158f3e13a10` 加本轮六项源码／测试修改；Trace 分析器和已批准的验收规则保持。

## 真实运行发现

| 实际尝试 | 完整结果与保留的失败 |
| --- | --- |
| 原被中断 Session 续行 | 同一 run 两次 native attempt；旧进程的第一棵 Trace 缺失，新读回只有 16 span。全部八次模型调用已结算，实际 UI 结束任务后 ActiveCalls=0、任务槽为空；v2 仍 FAIL／partial，CLI 退出 3。首次外层 PowerShell 未转发 CLI 退出码，观察到 1，分别保留 |
| 原任务的新 Session | 基线全项目测试通过；两轮完整编码分别完成真实 RED 和修复后的定向／middleware 测试。第三轮两次 Edit 精确匹配失败，随后一个未批准的 Edit 被实际 UI 取消；不计为第三个完成轮次，没有生成或应用新提案 |
| 新 Session 全部三次运行读回 | 103 span、五个 ERROR、30 个工具请求／29 个 dispatch、21 次模型调用均结算；最后一个待审批请求没有原生结果，且存在未派发请求的执行 span。v2 FAIL／partial／退出 3；所有失败和 unknown 保留 |

浏览器观察器曾因禁用的结束按钮超时且遗留拒绝的响应 promise，驱动退出；另有一次启动观察超时和一次源码 pin 拒绝。它们分别保留，未自动重派付费调用。最终恢复进程通过实际 UI 结束任务（HTTP 200），原生 ActiveCalls=0、任务槽为空；准确停止本次 launcher/job，两个原控制容器恢复 exited。持久身份恢复使用明确的测试 token 恢复，不能当作一次新的真实登录证据。

## 实现边界

- `internal/session/cancellation.go`：拥有该 run 的 worker 返回后，仅关闭身份匹配、未决定且未派发的待审批调用，先追加取消导致的 denial，再追加已知未执行结果，最后结束取消。只处理投影选中的 canonical pending；重复同身份 pending 不产生重复结果。已派发、commit-pending、unknown、其他 run 和历史终态不补写为成功。
- `internal/session/runner.go`：Go 的 durable dispatch 写入后才创建 managed `execute_tool` span；正常执行、已知失败和异常退出保留相应状态，不记录工具参数或错误正文。
- `internal/orchestrator/client.go`：Go context 仅标明 Harness 已拥有工具 tracing，抑制 Client 在回调前创建的重复 span；没有该上下文的旧调用方继续记录执行 Trace。
- 三项测试文件通过公开 SessionRunner／Workbench、真实 gRPC Client、真实文件 executor 和 telemetry recorder 验证。重复 pending 仅在 arrange 阶段通过公开 EventLog 复制，断言仍走公开取消与事件接口，不直接验证内部投影或数据库。既有异步 Agent 测试同步验证 `agent.main → agent.subagent → execute_tool PublishArtifact → artifact.publish` 的每条父链及具体 Session／隐私属性。

没有新增依赖、协议、配置开关或前端布局；不改写旧 Session、旧收据或原 FAIL。

## 本轮源码验证

| 检查 | 完整结果 |
| --- | --- |
| 待审批取消 RED → GREEN | 修复前缺 denial/result，退出 1；补实现后定向通过，退出 0 |
| 授权前 span RED → GREEN | 修正并行 fixture 后六个批准／拒绝／取消子场景全部在修复前失败，退出 1；修复后通过，随后增加真实写入失败及旧 Client 兼容覆盖 |
| 重复 pending RED → GREEN | 修复前重复决定导致完整性错误，退出 1；canonical guard 后通过。审阅进一步修正晚到批准所用的 pending ID/Seq，避免只证明旧身份被拒绝 |
| race fixture 修正 | 首轮最终 race 的 1 项失败为 fixture append 与 progress append 的 sequence conflict；不是 data-race 报告。arrange 改为最多 16 次 CAS 重试，其他错误／耗尽仍失败，原 FAIL 保留 |
| 最终相关 race | `go test -race ./internal/session -run 'Test(ManagedToolTraceStartsOnlyAfterDurableDispatch\|UnmanagedClientRetainsToolExecutionTracing\|CancellationStopsPendingApprovalWithoutExecutingWrite\|CancellationRetainsDispatchedToolResultAndUnknownOutcome\|SessionCancellationRefusesLateToolDispatch\|CancellationReachesTheWorkerHoldingAnotherRunnersLease)' -count=1 -json`；14 PASS／0 FAIL/SKIP，退出 0。另定向运行异步 Agent Trace 的 race：1 PASS，退出 0 |
| 全量发现与关联 GREEN | 较早完整 Go 为 1,794 PASS／81 SKIP／2 FAIL，45 包通过、2 包失败、13 无测试，退出 1。一项为旧异步父级断言，更新后定向 1 PASS；另一项启动器子进程空输出（46.08 秒），原测试单独 4.72 秒通过。启动器原因未确认，未改其代码／超时；原完整失败保留 |
| 最终 `go test ./... -count=1 -json` | Go 1.26；1,796 PASS／81 SKIP／0 FAIL，47 包通过、13 无测试，退出 0；该轮未并行运行 race，仍不据此宣称已确定较早启动器失败的根因 |
| `go vet ./...` | 退出 0 |
| `git diff --check` | 退出 0；用户已有 AGENTS.md 换行提示保留 |
| 独立双轴审阅 | Standards 0；Spec 的 canonical ID 验证盲点已修正，最终 0；`AI_REVIEWED` |

为修正审阅和 race 发现的 fixture 问题，较早两次全量主动停止，退出 1，均保留未完成日志，不计作完整通过。前一个定向场景准备错误（batch 未设置 parallel）的失败日志同样保留。

本次单测／集成／相关 race 范围 `VERIFIED`；81 项跳过保留，不能作为对应真实外部能力证据。本次源码范围不要求 Python／frontend 无关全量或完整发布；这些范围未运行。修复后的真实便携 runtime 取消、完整十轮 Go 任务、同任务 Memory／Trace、#19 故障矩阵及完整发布仍分别待验收；本记录不关闭 #17。

## 保护与产物

只读核对四份原工作副本的 264 个文件与四个 index、原 144 项 Trace v2 封存产物、旧包全部 3,219 个文件和用户 AGENTS.md 均未变。Docker 前后原 33 个容器、96 个镜像身份、332 个 volume 身份完全相同，两个固定控制服务恢复 exited；未删除资源或业务数据。

原生批次已确认累计 27,063,833 tokens，ActiveCalls=0、任务槽为空；历史 unknown、原预留和费用 unknown 保留。本轮源码修复与测试未调用模型。

本机原始日志、审阅与保护检查位于 `.scratch/go-task-closeout-20261016/`；实际运行的完整 native export、后端 capture、旧 bundle／新分析器绑定及停止收据位于 `deployments/docker/.local/receipts/go-task-closeout-20261016/go/`。运行数据、账号、机器路径及用户 AGENTS.md 不进入提交。

验证摘要 `pending-cancel-validation-summary.json` 的 SHA-256 为 `6e6705bab1c77d415496632bf5703d6ab9b889f9408f54f848652bbd10e0293b`；161 项不可变产物清单 `pending-cancel-artifact-manifest.json` 的 SHA-256 为 `f372c2b68f1f8d7e23e2a5a34f4d7e6b72d86b1dd468ddef235c142a3c2f3b65`。
