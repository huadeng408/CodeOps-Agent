# 真实仓库问答与恢复修复：2026-10-14

基线：`eb66b786e34c6308db781b8a76befc3104cb445a`。工程状态：`IMPLEMENTED`。
单元／集成及前端重试派发范围已验证；公开仓库完整问答与失败运行恢复仍未完成。
本轮不验收 #3／#5 整票、完整代码任务、Memory 写回、完整 Trace 或发布。

## 修复与回归

实际问答中，Go 已派发的并行 Read／Grep 被另一个等待结果的只读调用拒绝。
Task Workspace 现在只允许同一个有效运行租约内、由 Go 派发的只读调用并行；
变更工具、未知结果、不同运行／owner、缺失派发和失效租约仍拒绝。
公开 `TestTaskWorkspaceAdmitsConcurrentDispatchedReads` 的有效 RED 复现拒绝，修复后 GREEN；
相关十项 Task Workspace 边界测试通过，退出码 0。两次早期 fixture 错误另行保留。

失败运行的 automatic checkpoint 不出现在手工 checkpoint 清单中，旧前端“再次运行”因此未派发。
前端改用运行自身的 checkpointHash；手工清单继续保持原契约。
fixture 浏览器检查先超时失败，再通过：无手工 checkpoint 时点击一次，发送一次续行请求，
保留 expectedSeq、锚点和请求身份。此 fixture 仅验证前端请求，不作为模型证据。
`npm test` 12 项通过；`npm run build` 通过，均退出码 0。
完整 Go／Python 和门禁回归见 [清理证据](policy-cleanup-2026-10-14.md)。

## 实际运行的完整尝试分母

冻结 chi 仓库 `9b9fb55def404397748a9fc7e044efe9db1d618e`，98 个源文件，
经实际浏览器、Go、Python、grok-4.6 与已批准 OpenSandbox，沿用原 Ledger 和 1 亿 token 批次。
问题输入在派发前冻结；没有加入参考答案、补丁或修复提示。

| 尝试 | 结果 |
| --- | --- |
| 初次驱动启动 | Docker named pipe 权限失败；没有启动子进程或派发模型 |
| v1 第一个问题 | completed；有限计划、真实读取和回答；8 个工具结果中 6 成功、2 个 Grep 准入失败 |
| v1 第二个问题 | failed；13 个工具结果中 5 成功、8 个准入失败；没有第二条 assistant 回复 |
| v2 旧前端点击重试 | 未派发；automatic checkpoint 查找缺口，观察超时原样保留 |
| v2 修复后点击重试 | HTTP 202，同一 checkpoint、新 request ID、retryOf 原失败运行；后续运行 failed，没有新的前台调用预留事实，不能视为重试完成 |
| 显式结束任务 | 实际浏览器 HTTP 200；Go 验证 ActiveCalls=0 后释放任务槽，历史和未知计量保留 |

三条真实运行保留：`run:6dc5f94364f7f2bde10afde14440e28c`（完成）、
`run:8bf10a6bfe73f5f0899dc1f30ba61205`（失败）、
`run:bed90517cecbe3ab241daf67dd1338d1`（失败重试）。
共 21 个工具结果，11 成功／10 失败，没有 Shell、修改工具或子代理派发。
原仓库 98 个文件、HEAD、index 和任务副本 98 个源文件校验保持一致。
重试失败沿用 `continuation_runtime_error`；先前前台调用的 `bound_violated` 事实仍保留，
本轮没有把该事实清零或放宽上界。

该会话共 16 次模型预留：13 前台、3 反思；12 次结算、4 次 unknown，其中 1 次上界违反。
批次结束时已确认累计 338,027 tokens，原预留 4,016,384，ActiveCalls=0，费用 unknown。
本轮增加已确认 255,024，增加 unknown 预留 2,008,192；结束动作不改变计量。
Go／Python 由本次驱动管理的进程树已停止，18091／50061 无监听。
既有 Docker 容器、volume、镜像与业务数据保留。

## 冻结输入与产物

私有产物在 `deployments/docker/.local/{profiles,logs,receipts}/repository-qa-20261014/`，
包含原始输入、事件、失败截图、已脱敏日志及结束记录，不进入公开提交。
原 v2 输入保留；前端修复后的绑定另冻结为 `inputs-v2-pre-retry.json`。

| 产物 | SHA-256 |
| --- | --- |
| `inputs-v1.json` | `cbb746a52cdb3b1ecdb845cd5d0ee1df0f169a4d30a07a8cb22ce1bdf8581889` |
| `inputs-v2.json` | `c8ba040fdc52b66e9298d12b7866812f9b68974cd5cbb5a3b62a2885bed327f3` |
| `inputs-v2-pre-retry.json` | `a5f34fc9df77bbd4e795a2aab694b3ba6ae39c89c06777bfa1710d6344373fc4` |
| `retry-v2-admission.json` | `a0a6339a7e434cd51f61b4c41293bda6dfd3afd066bc73e817a50494b608e54d` |
| `ledger-after-task-finish-v2.json` | `03bf554b7facb80683d838dfea8293c231e575bfccee0921fae0648bf79242a1` |
| `task-finish-v2.json` | `bad86efe035826b49ec8d9fb37bdff8c8b7a3e70694b62260d709fc46dd09cf2` |
| `repository-after-v2.json` | `01e52ab25acb543db4bc6faced51951bd458a6af733501fe46d1e3109de3c91b` |

认证复用已有测试身份的 Go 签名凭据，未证明正常注册／登录流程。
没有真实 Trace 后端读回，反思为 unknown，没有新的成功写回证据。
公开仓库问答、真实错误恢复及完整模型任务继续保留对应缺口。
