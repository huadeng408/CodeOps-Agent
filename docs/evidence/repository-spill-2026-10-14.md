# 仓库只读缓存与错误分类：2026-10-14

基线：`7fe091227fb4827a44cd35e941a2b4c1e56b7c5d`。工程状态：`IMPLEMENTED`。
单元／集成验证通过；真实运行证明缓存不再破坏工作区，完整只读问答与重试仍未完成。
本轮不验收 #3／#5 整票、完整模型代码任务、Memory 写回、完整 Trace 或发布。
此前八项清理及旧尝试分母见[清理证据](policy-cleanup-2026-10-14.md)和
[原问答证据](repository-qa-2026-10-14.md)。

## 修复范围

真实 v3 运行的长 Read 把完整输出缓存写入隔离任务的 `.runtime/spill`，
严格库存核验因此拒绝后续模型调用。现在仅为 Task Workspace 配置 Go 管理的外部缓存，
按 Session／工作目录隔离；缓存创建及每次读写复用目录 pin，拒绝 junction／reparse point，
目录使用当前用户私有 DACL。内容脱敏、opaque locator 和 SHA-256 校验复用原接口。
任务库存、工具权限、模型上界、批次及旧计量事实保持原契约。

公开 SessionRunner 终态保留 `provider_bound_violated`、`provider_usage_unknown` 和
`task_prerequisites_missing` 三种安全分类，只写固定消息；typed integrity／compaction
错误保持优先级，provider 原始异常和敏感值不进入 Ledger。

## 验证

| 命令／范围 | 当轮结果 | 退出码 |
| --- | --- | --- |
| `TestSessionRunnerRetainsSafeModelFailureClassification` 及既有错误回归 | 三种分类有效 RED 后 GREEN；两种 typed 错误碰撞有效 RED 后 GREEN；固定消息及脱敏检查通过 | 1 → 0 |
| `go test ./cmd/server -run '^TestContinuationLongReadPreservesPreparedTaskWorkspace$' -count=1 -v` | 长 Read → ReadSpill → 后续 ModelWorkspace 的有效 RED 后 GREEN；junction 拒绝场景通过，外部 marker 与目录不变 | 1 → 0 |
| `go test ./... -count=1 -json` | 1,532 通过／72 跳过／0 失败；45 包通过、13 包无测试 | 0 |
| `go vet ./...` | 通过 | 0 |
| 实际 Windows ACL 核验 | 恢复缓存目录／文件为受保护 DACL、单一当前用户 Allow ACE；新缓存目录私有，文件仅继承当前用户访问 | 0 |
| Standards／Spec 审阅 | typed 优先级及缓存私有权限／junction 两项发现已修复；最终各零项剩余发现 | — |

测试与原始失败日志保留在 `.scratch/repository-qa-next-20261014/`，最终完整日志为
`go-final-secure.jsonl`、`vet-final-secure.log`。Python／前端源码本轮未修改，整套测试本轮未重跑；
上一提交的四项 CI 均通过，不能据此升级本轮产品验收。

## 真实运行与恢复的完整分母

仍使用原批准的 grok-4.6、OpenSandbox manifest／镜像、Ledger 和 1 亿 token 批次，
冻结 chi `9b9fb55def404397748a9fc7e044efe9db1d618e` 的 98 个源文件，
沿用原两个只读问题及单次 500,000 输入／2,048 输出上界，没有加入答案或修复提示。

| 尝试 | 结果 |
| --- | --- |
| v3 第一问 `run:c761ace50ea3ce36488d4639d678ae0d` | failed；6 个 Read／Grep 全部成功，随后 `task_prerequisites_missing`；Go 创建的长 Read 缓存使库存不符，没有 assistant 回答 |
| v3 缓存恢复 | 仅核对并复制该 8,915-byte Go 缓存至外部缓存目录；原任务 `.runtime` 可恢复归档。SHA-256 一致，没有删除产物或修改源文件 |
| v3 原任务重试准备 | 旧 lease 已到期，未延长或改写；标题选择／响应观察超时保留。旧驱动退出时未导出其内存控制台日志，Canonical Ledger 与已有 app.log 保留 |
| v3 停止任务结束 | v6 浏览器选择原会话，HTTP 200；ActiveCalls=0 后释放任务槽，98 个源文件及原仓库 HEAD／index 校验一致，原失败及 unknown 保留 |
| v6 新会话准备 | HTTP 200，新工作区 prepared；冻结当轮二进制和源码，包含外部缓存实现 |
| v6 第一问 `run:ce12d494a1c5d606a5d945548552fd33` | failed；12 个 Read／Grep 全部成功，其中 2 个长 Read；缓存位于任务副本外，工作区仍 prepared。最终为 `provider_bound_violated`，没有 assistant 回答 |
| v6 首次点击重试 | HTTP 409，expectedSeq=114、actual=115；没有新增运行，冲突响应保留 |
| v6 刷新后重试 `run:c6902adb8ce7e8e3263ca71f3bb3e16f` | HTTP 202；同 checkpoint、新 request ID、retryOfRunId 指向第一问；运行 failed，Python 报 ValueError／provider_runtime_error，没有新的前台调用预留或读取结果 |
| v6 第二问 | 未运行；第一问已违反冻结输出上界，保留原输入和分母，未放宽上界以接受失败响应 |
| v6 停止任务结束 | HTTP 200；98 个源文件、原仓库 HEAD／index 一致，任务副本无 `.runtime`；任务槽释放，未知用量及费用保留 |

v3 缓存 SHA-256：`9fd61525b59737e04173da38e9292fa80262d8e025374c48e5b67ac903e9e679`。
两会话共 18 个工具结果，18 成功／0 失败，没有 Shell、修改工具或子代理派发。
v3 共 4 次模型预留（3 前台、1 反思），3 结算／1 unknown，无上界违反，已确认 40,288 tokens。
v6 共 9 次预留（7 前台、2 反思），6 结算／3 unknown，其中 1 次上界违反，已确认 95,528 tokens。
该上界违反记录保留观察到的总量 20,143；输入／输出拆分没有在此记录中保留，不推测具体超额值。
本轮增加已确认 135,816；批次最终累计 473,843、原预留 6,024,576、ActiveCalls=0、费用 unknown。
由驱动管理的 Go／Python 及子进程均停止，18091／50061 无监听；既有 Docker 资源保留。

## 产物与未完成范围

私有运行物料仍在 `deployments/docker/.local/{profiles,logs,receipts}/repository-qa-20261014/`；
校验、ACL 与验证收据在 `.scratch/repository-qa-next-20261014/`，不进入公开提交。
原 v3 输入与未派发重试的绑定分别保留，不用 v6 结果改写它们。
`inputs-v6.json` SHA-256：`50dbcbea942a6e69f66bc6330f911bd15661e487e467b7a0da773939154e1dde`。
缓存恢复收据 SHA-256：`b695b2680025354ebede52c8cd107fda76861545faab17bcf89117128f3ad94c`。

仍需核对实际 provider 的输出限制契约及前台重试的 ValueError 根因，再完成原第二问和真实恢复。
真实反思没有成功写回证据，Trace 后端导出连接被拒绝，未从后端读回完整 Trace。
认证复用已有测试身份，未验正常注册／登录。上述缺口不否定本轮已完成的源码修复范围，
也不能把源码修复、正确拒绝或读取成功升级为整票产品或发布通过。
