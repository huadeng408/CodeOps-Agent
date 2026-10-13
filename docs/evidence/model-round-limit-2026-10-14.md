# 工具轮次上限的终态与缓存展示：2026-10-14

基线：`a8213b9bd7d8554100e2d36c7908843595cc08fa`。工程状态：`IMPLEMENTED`。
范围为共享 Converse 终态、SessionMeta 投影和 Go 的安全错误分类。
[真实两轮问答](repository-qa-product-2026-10-14.md)仍保留第一问完成、第二问失败，
本轮源码验证不替代第二问、真实重试、Memory 或发布验收。

## 问题与修改

六轮工具调用后，Python 请求一次不调用工具的收尾回答，并保留 success=false。
原路径没有填写 Done.error_code，Go 只收到未指定的失败，因此公开显示
`agent continuation failed`。现在同一终态携带 `tool_round_limit` 和 retryable，
Go 通过已有分类显示具体上限原因。没有增加轮次、改变权限或把部分回答标为任务完成。

同一收尾调用的 normalized cached-input 原来未加入 SessionMeta.cached_tokens。
现在该展示投影累计一次；输入、输出、计费与原预留继续以 Go Ledger 为权威。
协议、依赖、Go 计费逻辑及旧运行事实均没有修改。

## 验证

| 命令／范围 | 结果 | 退出码 |
| --- | --- | --- |
| `python -m pytest -q tests/test_server.py::test_tool_round_limit_gets_final_no_tool_summary` | 公开 gRPC 有效 RED：错误码为空；GREEN：具体代码与 retryable 保留，success=false、六轮及无工具收尾不变 | 1 → 0 |
| 同一公开接缝的缓存 tracer | 有效 RED：12 而非独立例子要求的 15；GREEN：15，输入 80／输出 20 不重复加缓存 | 1 → 0 |
| `python -m pytest -q tests/test_server.py tests/test_model_gateway_client.py` | 93 通过，1 warning；在最终缓存修复前运行，最终源码另按下行全套核验 | 0 |
| `python -m pytest -q` | 最终 2,493 通过／17 跳过／3 warnings | 0 |
| `go test ./internal/orchestrator ./internal/session ./cmd/server -count=1 -json` | 221 通过／1 跳过／0 失败，三个相关完整包通过；包括安全分类、脱敏及恢复边界 | 0 |

第一次缓存断言误用了 ModelCallResponse 的字段名，属于无效探针；已改用正式协议的
SessionMeta.cached_tokens 后取得有效 RED。两份日志分别保留，不把字段错误当作产品根因。
测试使用 provider fixture，并明确清空凭据；没有从 Python 直接调用真实 API。

原始失败、GREEN 与完整分母保存在 `.scratch/model-round-limit-20261014/`；
最终源码及日志指纹绑定于该目录的 `receipt.json`，提交后另核对 committed blobs。
Go 生产源码和前端未变；完整 Go 全仓、前端测试及发布本轮未重跑，相关 Go 包与全套 Python
分别记录。后续实际浏览器运行需重新冻结源码和输入，沿用原问题与同一持久批次，
保留旧失败和 unknown；仅有具体错误分类不能完成 #3／#5 或整体目标。
