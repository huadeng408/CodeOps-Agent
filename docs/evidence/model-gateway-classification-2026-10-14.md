# 模型传输失败的安全分类：2026-10-14

基线：`70d049b185e24f40f6b01c3f801fe4d1230ff644`。工程状态：`IMPLEMENTED`。
范围为 Go Gateway 传输诊断、SessionRunner 失败投影和 Python 既有代码适配器。
[上一轮真实失败](repository-qa-continuation-2026-10-14.md)保持原记录；
不能从本轮 fixture 的结果反推旧调用的提供商根因或实际消耗。

## 修改与边界

原来的 `provider_transport_unknown` 到 SessionRunner 后丢失，产品只显示笼统失败。
现在该类别保留为固定的安全代码与文案，明确用量未知、原预留保留。
Go 发起 HTTP 或读取响应体时遇到 typed timeout，则返回 `provider_timeout_unknown`；
Python 的既有 allowed-code 适配器和 Go 失败投影同步保留该类别。

没有输出原始异常、URL、响应正文或凭据；权限、完整性／compaction 错误优先级、
计费、原 unknown、60s HTTP／65s RPC 期限、六轮工具上限和重试行为均保持原义。
取消仍保留原 transport_unknown 分类，不把未知结果升级为零用量或成功。

## 验证范围

| 验证 | 结果 | 退出码 |
| --- | --- | --- |
| SessionRunner 原公开续行接缝 | 传输类别 RED：笼统失败；GREEN：固定原因及 Ledger 类别保留，异常 fixture-secret 不进入 Ledger | 1 → 0 |
| 既有 Go `Gateway.Invoke`／受控 HTTP | header deadline RED：transport_unknown；body deadline RED：response_unknown；GREEN：两者 timeout_unknown，原 103 预留／0 已确认及单次派发保留 | 1 → 0 |
| 公开 Python `Converse` gRPC／Gateway fixture | RED：新类别被降级为 model_gateway_unavailable；GREEN：两种准入模式原类别透传，仍为 unsuccessful | 1 → 0 |
| `go test ./... -count=1 -json` | 1,548 通过／72 跳过／0 失败；45 个包通过、13 个包无测试 | 0 |
| `go vet ./...` | 通过 | 0 |
| `python -m pytest -q tests/test_model_gateway_client.py` | 34 通过／1 warning | 0 |

Go deadline 回归直接调用公开 `Gateway.Invoke`，使用真实受控 HTTP 与 5s context deadline；
没有通过 Go ModelGateway gRPC，也没有等待生产 60s client timeout。
SessionRunner 回归使用模拟 conversation adapter；Python gRPC 的 Gateway 返回 fixture。
这些是各自接口的源码／集成证据，不是完整 Go／Python 跨进程产品证据。

原始 RED／GREEN、完整分母及不可变源码收据保存在
`.scratch/model-gateway-classification-20261014/`；提交后另核对 committed blobs。
Python 全仓未在本机重跑；前端未修改，产品、Memory／Trace 和发布按其范围另行验证。
后续真实运行重新冻结源码和原问题，沿用同一持久批次，保留全部失败和 unknown。
具体分类本身不完成第二问或关闭 #3／#5。
