# #15 实际产品任务的后端 Trace 读回

范围：[GitHub #15](https://github.com/huadeng408/CodeOps-Agent/issues/15) 的五项验收。
工程 `IMPLEMENTED`；单测、集成及本次产品 Trace 范围 `VERIFIED`；完整发布未运行。
源码为 `4895c3cf6be7b7d488f8efafc02bcc51ea851e1f`，
[源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38674118910) 四个 job 全部通过。
这张票不包含后续 Go/Python 十轮对话、完整故障矩阵或发布资格。

## 验收映射

| 验收项 | 证据 |
| --- | --- |
| Go 根及跨进程因果链 | Go 根携带实际 Session/run；同步、流式 chat 激活当前 span；W3C 传到 Go Gateway，每次 HTTP attempt 单独记录。tools-disabled 反思恢复 Go 上下文，RPC 结束后不泄漏给下一请求 |
| 真实成功任务读回 | 浏览器派发真实 grok-4.6 代码任务，Go 执行 Read/Write；生成提案后在离线 OpenSandbox 跑完整 Go 测试。Phoenix API 按实际根、trace ID 分页读回 15 个 ended spans；外部命令首轮返回 PASS |
| 缺失、失败及不可用 | 公共契约/HTTP/gRPC 回归覆盖缺 span、未结束、错误、取消、断开父链、重复调用、全零 ID、错误模型、原始敏感字段及后端 503；保留 partial/unknown/BLOCKED，不产生成功收据。429→200 的两次真实 HTTP fixture attempt 均保留，错误尝试不被成功重试隐藏 |
| pin、审计与脱敏 | 冻结 927 个源码/指令/配置/依赖文件、浏览器产物、实际二进制、模型与任务/后端镜像；运行后再次核对。原生 Go 校验完整事件链、准入前缀、目标 run 的 Actor 映射和已结算末态；Phoenix 原始 span 在允许字段筛选前检查 |
| 可复用产品边界 | `python -m eval.product_trace` 复用共享 Phoenix reader 与 product-v1 契约；运行方式见[入口说明](../observability-phoenix-live-trace-runbook.md)。旧 Python-only eval 和独立探针分类不变 |

## 实际产品结果

Session `5a0b60761091d7987d6b92237be81ef4`，
run `run:d050136132fd0483938e1840292e88ee`，
实际 trace ID `9177aa067e0de8e37efc4b8812a89788`。
整棵树包含 Go `agent.main` 1、Python `chat` 3、`provider.call` 4、
Go `provider.attempt` 4、Go Read/Write 各 1、`memory.reflect` 1。
四次模型调用为前台 3、后台反思 1；反思返回 4 个候选。
所有实际工具与模型准入 ID 都在读回分母中，缺失项和问题均为空。

模型只新增 `trace_context_regression_test.go`，SHA-256
`d55eb8eb182ad549b376f5b021e9a5bd7799a3060c7712bf64f4f54be5ad7917`。
提案 `629df8b554692839942f397eec8da53a03d836cc772c5eea7f6bb4e47fc4ce64`
运行 `go test ./... -count=1`，退出 0，两个包通过，输出未截断，runtime 已停止。
Go 任务镜像为 `sha256:c35ffc50ff2d4e97ea90f973b29836945912397204592a0b06a0b6fbbba59b58`。
原 chi HEAD `9b9fb55def404397748a9fc7e044efe9db1d618e`、98 个原文件及 index 不变；
隔离结果保留，没有应用到原仓库。任务通过浏览器“结束代码任务”入口返回 200。

后端为原有 Compose `localcode/phoenix`，镜像
`sha256:3092f5543a3ddd35db7390cf971027c33be6be1f171274d57f3c8658c2193d67`。
运行前后实际 inspect 身份一致；Go 与 Python 均使用该本机 HTTP OTLP 后端。
验收依据为 API 读回，不是 health、export 日志或合成 spans。

原批次 `02cfc8d9c93975b3ea63f27cc0a3331d` 的四次调用全部结算，新增 unknown 为 0；
已确认用量从 3,098,371 增至 3,149,395，差额 51,024 tokens。
历史 unknown 与原预留 3,606,024,576 保留，费用仍 unknown；末态 active calls 0、任务槽为空。
本次拥有的 Go/Python 进程和浏览器已停止，控制服务与 Phoenix 恢复原先 exited 状态，数据保留。

## 验证、失败与绑定

| 命令/范围 | 完整结果与退出码 |
| --- | --- |
| 本轮 `go test ./... -count=1 -json` | 1,725 passed、79 skipped；46 包通过、13 包无测试；退出 0。最后 Actor 审计收紧后另外运行下行定向验证，最终源码再由 CI 全量验证 |
| 最终 `go test ./cmd/trace-audit ./internal/admission -count=1 -json` | 142 passed、4 skipped，两个包通过，退出 0 |
| 本轮 `python -m pytest -q` | 2,542 passed、17 skipped、31 warnings，退出 0；最后收紧后的当前五文件定向为 106 passed、1 warning，退出 0 |
| `go vet ./...` | 最终运行退出 0 |
| Gateway 429→200 race 回归 | `go test -race ./internal/admission -run TestGatewayRetriesOnlyAfterErrorUsageSettles -count=3 -json`，3/3 passed，退出 0 |
| 实际产品及外部命令 | 浏览器提案测试退出 0；`python -m eval.product_trace` 首次正式读回 `VERIFIED/complete`，退出 0；只读产品断言与封存退出 0 |

所有 RED、早期失败和一次错误测试选择的退出 5 都保留在本轮测试产物中，
不是从成功子集重建分母。早期启动 v1 因 stdin 结束而停止，没有派发模型；正式产品为 v2。
浏览器选择器等待超时、误将创建接口 200 断言为 201，以及提案首次 409 均保留。
409 的实际原因是后台反思追加事件后工作区游标过期；刷新后沿用同一提案 request identity，
首次拒绝没有执行测试，随后真实执行成功，没有删除失败或重复派发模型。
结束前的一次诊断读回为空，也保留为未完成诊断，不作通过证据。

本机原始证据：`deployments/docker/.local/receipts/product-trace-20261015/v2/`，
48 个封存产物；manifest SHA-256
`24e565b4636317325bab66aaac9f96dea6049994a45e92f8c82b365362d9679e`。
85 个测试日志/退出码的原始 checksum 另在 closure-binding 中保留。
实际二进制 SHA-256 `ba24fd39cbd315b0425a8dd722a0b555fae6c26e0ec91f923cff4508b5e7d090`；
冻结文件与源码提交的内容核验一致，没有只凭提交号换绑收据。
更多绑定见[收据](product-trace-2026-10-15.receipt.json)。

双轴源码复核最终为 Standards 硬违规 0／Smell 0、Spec 未解决项 0／范围扩张 0；
分类 `AI_REVIEWED`，不声明人工复核。发布未运行，完整专项不作为本票的附加门禁。
