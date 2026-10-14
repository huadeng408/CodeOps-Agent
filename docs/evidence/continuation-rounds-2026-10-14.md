# 续行的有界轮次与待执行请求恢复：2026-10-14

基线：`e87f2a9ea34c02b401f618333983c6d0c729adb8`。工程状态：`IMPLEMENTED`。
范围为现有 Python ConversationRunner 的轮次游标、checkpoint 投影和公开 `Converse` 回归。
[前轮真实失败](repository-qa-classified-2026-10-14.md)保持原分母、用量和收据，
本轮源码修复不反推旧请求的提供商根因，也不把 fixture 回答作为产品答案。

## 问题与修复

对前轮 Python checkpoint 投影的只读核对显示：原 run 在 `tool_after` 第 6 轮停止，
两次获准续行均从第 6 轮开始并停留于该轮。旧代码把累计游标截回六轮上限，
新的 run 只剩一次工具迭代，不能获得完整的有界续行机会。

现在只有通过既有 lineage／Surface 校验、承接前一 run 的新 run 才获得六轮额度，
累计游标继续前进。该 run 的起始轮次保存在既有 checkpoint metadata；
同一 run 重开恢复剩余额度，耗尽后不会因重复恢复获得新工具额度。
Go 准入、预算、权限、原 unknown、默认六轮和 HTTP／RPC 期限保持原义。

Spec 审阅发现等待工具结果时的另一处恢复缺口：`tool_before` 原来被映射为第 1 轮，
单个工具的待执行参数也未保留。公开协议回归复现后，恢复改用实际游标和既有缓存重放路径。
Read、AskUser 和独立 SpawnAgent 写入当前待执行调用及顺序尾部；batch 沿用原请求缓存。
已完成的顺序调用排除在该缓存之外，Go 继续负责工具授权和执行结果。
旧 `tool_before` 缓存若缺少可重放 payload，明确拒绝恢复，不推测参数或重做已完成副作用。

## 验证范围

| 验证 | 结果 | 退出码 |
| --- | --- | --- |
| 公开 `Converse`：原 run 实际耗尽六轮后续行 | RED：新续行仍 tool_round_limit；GREEN：两个新 Read 后完成，旧六项不重放，累计 turn 为 9 | 1 → 0 |
| 同配置关闭重开应用／gRPC 服务 | RED：该 run 的续行额度丢失；GREEN：从持久 checkpoint 恢复额度并完成 | 1 → 0 |
| 等待单个 Read／Read batch 结果后重开 | RED：两个 case 拒绝有效 checkpoint；GREEN：原待执行请求重放；fixture 在结果返回前拒绝任何新的模型调用 | 1 → 0 |
| 续行六轮上限与同 run 耗尽恢复 | 原 run 和新续行分别最多六轮；耗尽的同 run 恢复无工具派发，仍 tool_round_limit，累计 turn 为 13 | 0 |
| 最终定向四项测试文件 | 149 通过／1 warning | 0 |
| `python -m pytest -q` 最终源码 | 2,500 通过／17 跳过／31 warnings | 0 |
| `go test ./internal/orchestrator ./internal/session ./cmd/server -count=1 -json` | 223 通过／1 跳过／0 失败，三个相关完整包通过 | 0 |
| `go vet ./internal/orchestrator ./internal/session ./cmd/server` | 通过 | 0 |
| `python -m compileall -q orchestrator/runtime/conversation.py tests/test_server.py` | 语法编译通过；仓库没有配置独立 Python 静态类型命令 | 0 |

定向文件为 `tests/test_server.py`、`tests/test_context_runner_events.py`、
`tests/test_agent_loop_plugins.py` 和 `tests/test_model_gateway_client.py`。
测试使用真实 gRPC 与实际 checkpoint 持久化，模型及 Go ToolResult 为边界 fixture；
“重开”是同一测试进程内关闭并重新初始化应用／gRPC 服务，不代表真实跨进程故障验收。
恢复证明限于 provider、Read 和 batch；AskUser／SpawnAgent 的完整恢复未单独验收。

原始 RED／GREEN、测试准备中的类别／history／字段纠正、审阅前 2,498／17 的全量诊断、
最终源码指纹和完整输出保留在 `.scratch/continuation-rounds-20261014/`。
审阅前运行不作为最终源码证据；两轴最终复审均无剩余发现。
不可变源码收据在该目录保存，提交后核对 committed blobs 和 CI。

真实原问题将另冻结新运行，沿用持久批次和镜像，保留每个失败与 unknown。
当前不关闭 #3／#5，不宣称实际模型问答、Memory／CAS、Trace 或完整发布通过；
未修改前端，发布验证未运行。
