# 模型失败后的续行恢复：2026-10-14

基线：`55ce8efd6f8b8e4871ba5fba82be155b1f235643`。工程状态：`IMPLEMENTED`。
本轮验收 Python 检查点与 Go 准入续行的源码／集成边界，不验收 #3／#5 整票或发布。
原真实问答、失败和计量分母保留在[上一轮证据](repository-spill-2026-10-14.md)。

## 根因与修复

使用原失败运行的只读 Ledger 导出、Plan／Todo、历史和检查点副本，在公开 gRPC
`Converse` 接缝离线重放。原检查点记录 Ledger head 7，重试请求为 head 116；
同一不可变 Surface 保持一致。Python 原先要求两个 head 完全相等，因 Go 正常追加
continuation／lease 事实而抛出 ValueError，在再次模型派发前返回 `provider_runtime_error`。
该结果重复复现，并缩小为独立的公开 gRPC 回归。

现在仅允许 Harness 管理且使用 Go 准入模型 gateway 的续行，采用向前的新 Ledger head。
旧序号必须明确存在、为非 bool 的非负整数，新旧 checksum 必须为 64 位 hex。
Session、Surface 和明确的 retry lineage 校验保留；旧 head 回退、同序号换 checksum、
畸形新 checksum、异常旧游标、其他 Surface／predecessor 均在第二次派发前拒绝。
未经过 Go 准入的普通模型客户端保持原严格比较。

Go 的权限、Ledger 校验、调用上界、预留、沙箱及原失败事实未在本补丁修改。
离线重放仅从只读连接复制原 SQLite 投影，所有写入落在临时副本；没有新付费调用。
临时诊断 tracing 已移除。
重放前后核对原 Ledger、身份库和检查点的 6 个数据库／WAL 文件，SHA-256 全部一致。

## 验证范围

| 命令／范围 | 结果 | 退出码 |
| --- | --- | --- |
| 最小公开 gRPC 续行回归 | 原实现有效 RED；修复后新 run 重试与同 run 恢复 GREEN | 1 → 0 |
| 两项审阅边界回归 | 畸形新 hash、旧序号 missing／负数／None／True：修正前 5 失败、6 通过；修正后拒绝且无第二次派发 | 1 → 0 |
| `python -m pytest -q tests/test_model_gateway_client.py tests/test_context_runner_events.py tests/test_server.py` | 132 通过／0 失败，1 warning | 0 |
| 原检查点副本在公开 gRPC 重放 | success；一次本地 fixture gateway 派发；原失败和运行数据库不改写 | 0 |
| `go test ./internal/session ./internal/orchestrator ./tests/e2e -count=1 -json` | 195 通过／45 跳过／0 失败，3 包通过 | 0 |
| 最终源码的 `python -m pytest -q` | 2,493 通过／17 跳过／0 失败，3 warnings | 0 |
| `python -m compileall -q orchestrator/runtime/conversation.py tests/test_model_gateway_client.py`、`git diff --check` | 通过 | 0 |
| Standards／Spec 审阅 | 各一项边界发现已修复，最终各零项剩余发现 | — |

提前启动的完整 Python 回归为 2,487 通过／17 跳过／0 失败，退出码 0；
运行期间新增两项审阅修复，因此该次仅保留为过程证据；最终源码已重新执行全套回归。
初期 fixture 的字段、metadata、编码和临时 SQLite 连接清理错误保留，
不作为产品错误或有效 RED；未触发原根因的中间 GREEN 也保留。

## Provider 契约核对

Grok 4.6 的原生 reasoning 不能关闭，未指定 effort 时默认 high；reasoning tokens 计费。
此前 `THINKING_ENABLED=false` 因此不能证明关闭了原生推理。
来源：[xAI reasoning 文档](https://docs.x.ai/developers/model-capabilities/text/reasoning)。

xAI Responses 的 `max_output_tokens` 约束可见回答，reasoning 和 function-call tokens
不计入该请求字段的上限。因此不能仅凭计费输出超出可见回答限制，就断言 upstream
违反请求长度限制。来源：[xAI 运行与评测约定](https://docs.x.ai/developers/model-capabilities/text/best-practices)。

BeeAPI 说明存在协议转换，并建议原生 reasoning 能力使用 Responses。
其公开说明未明确承诺当前 Anthropic 转换对 Grok 的全部计量／上限语义；
原生 Responses 的说明不能直接视为此转换路径的已验证契约。
来源：[BeeAPI OpenAI 兼容接口](https://beeapi.dev/docs/03-api/openai)。
下一轮真实运行仍需固定经核对的请求长度与计量配置。本轮不修改预算代码、原模型配置、
原失败或原预留，也不把旧 `provider_bound_violated` 改成成功。

## 产物与剩余范围

日志、只读副本重放、完整尝试和当轮不可变收据保存在
`.scratch/model-continuation-recovery-20261014/`，不进入公开提交。
公开证据不包含原数据库、模型输入、凭据或本机绝对路径。
本轮源码／集成范围通过后可交付；完整真实问答、实际 provider 重试、反思／子调用计量、
模型代码任务、Memory、后端 Trace 和发布仍分别按正式任务验收。
