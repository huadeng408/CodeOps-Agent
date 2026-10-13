# 请求长度与完整计费上界：2026-10-14

基线：`9d9ee9e0215ce3dfb620b3aa21f3aa2863bf659d`。工程状态：`IMPLEMENTED`。
本轮源码／集成范围验证通过；真实浏览器运行使用新的冻结 profile 单独记录。
此前失败、用量、预留和检查点不改写，见[续行证据](model-continuation-2026-10-14.md)。

## 问题与修复

Go 原来把 `OutputLimit` 同时用于请求长度和完整计费输出校验。
当 provider 的请求长度仅控制可见回答时，包含 reasoning 的完整输出可能超出该字段，
即使输入加输出仍在确认的总上界内，也被错误拒绝。

在现有 Go `ProviderConfig` 增加可选 `TotalLimit`，环境入口为
`CODE_AGENT_MODEL_TOTAL_LIMIT`。正值必须覆盖输入与请求输出之和且不超过
100,000,000；Go 在派发前预留该总量，并按完整计费输入／输出检查和结算。
输入上界及真正发送的请求输出限制保持原值。缺省或 0 保留旧严格组件计费限制；
非法、溢出、负数、过小及超过批准额度的值拒绝派发。
模型请求不能设置此字段，CLI、HTTP 和后台调用复用原 gateway。

Budget、Ledger schema、原 scope、权限、任务／调用围栏和 unknown 路径没有修改。
新 profile 不授权结算原 bound-violated 调用，不清除旧预留。
没有新增依赖、协议字段、Agent Loop 或计费数据库。

## 官方契约与 profile 来源

Grok 4.6 当前官方详情确认 500,000-token Context；本轮没有确认其独立文本输出上限，
不能猜出 reasoning 的完整上界。网页抓取曾将 `/developers/grok-4-6` 的内容标为 4.6，
复核当前页面实际为 4.7 指南，因此不采用该页的无输出上限声明作为 4.6 证据。
来源：[4.6 模型详情](https://docs.x.ai/developers/models/grok-4.6)。

xAI Responses 的可见回答限制不包括 reasoning／function-call tokens；
Grok 的原生 reasoning 不能关闭。BeeAPI 当前 Anthropic 转换的完整语义不能仅凭这些
原生说明认定一致；本轮总上界依据用户已批准、由服务商强制的累计 1 亿 token 硬额度，
不是声称模型自带此单次输出限制。来源：
[运行约定](https://docs.x.ai/developers/model-capabilities/text/best-practices)、
[reasoning](https://docs.x.ai/developers/model-capabilities/text/reasoning)、
[BeeAPI 接口](https://beeapi.dev/docs/03-api/openai)。

真实新 profile 仍使用原模型、输入窗口和请求回答长度；单独冻结新的完整计量上界，
保留同一累计批次。服务商额度不是每次调用重新获得的额度；保留的 unknown 预留是
保守记录，其总和不能当作服务商余额。

## 源码与集成验证

| 命令／范围 | 结果 | 退出码 |
| --- | --- | --- |
| 公开 Go ModelGateway gRPC 回归 | 有效 RED：原实现预留错误且拒绝账内输出；GREEN：回答 cap 不变，完整输入／缓存／输出仅结算一次 | 1 → 0 |
| 同一接缝十个场景 | 明确总上界、服务商硬上界、旧组件校验、输入／总量超限及五种非法配置均符合声明；拒绝保留原预留或不派发 | 0 |
| `go test ./... -count=1 -json` | 1,543 通过／72 跳过／0 失败；45 包通过、13 包无测试 | 0 |
| `go vet ./...` | 通过 | 0 |
| `python -m pytest -q tests/test_model_gateway_client.py` | 32 通过／0 失败，1 warning | 0 |
| Standards／Spec 审阅 | 各零项剩余发现 | — |

日志及不可变源码收据在 `.scratch/provider-total-limit-20261014/`。
Python／前端源码没有修改，本轮仅复核相关 Python gateway 接缝；
未把旧全套结果作为本轮新鲜回归。源码范围不要求付费模型或后端 Trace。
真实 provider／浏览器、完整任务和发布按各自声明范围记录；当前不关闭 #3／#5 整票。
