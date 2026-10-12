# 可结算有界重试 — 2026-10-12

关联 [#3](https://github.com/huadeng408/CodeOps-Agent/issues/3)，基点
`88fa838628fb9342f4b50e2f42a4f386c62e8453`。工程状态 `IMPLEMENTED`。
本记录验收 Go 网关、同一 admission Ledger、版本化计量契约和 Python
适配器的可结算重试；完整真实模型产品及发布未运行，#3 保持开放。

## 行为与边界

完整且不超过配置上界的 usage 可以结算成功或失败的 HTTP 尝试；响应
缺 usage、传输结果不明、用量越界或结算不明时，仍保留完整预留。不能
从 429／5xx 状态推断零用量，unknown 不触发重试。认证及其他错误即使
用量已结算也不重试；非法 HTTP 状态在写入前拒绝，不污染 Ledger。

Go 仅对已结算的 429／5xx 做最多三次尝试，退避为 1／2 秒。Retry-After
支持非负十进制秒数和 HTTP date，采用不早于其声明的等待；本项目单次
等待上限为 10 秒，超限或格式不明时保留失败并停止重试。语法依据
[RFC 9110 §10.2.3](https://www.rfc-editor.org/rfc/rfc9110.html#name-retry-after)。

每次重试由 Go 生成独立 call ID，追加同一 Ledger 的 `retry_of`，再独立
预留／结算。CAS 和回放要求 parent 已结算且为 transient，owner、task、
上界和 purpose 相同；禁止分叉和第 4 次尝试。原事实与所有尝试均保留。
每次尝试重新检查 capability、任务前提及额度，跨进程仍只放行一个
未结算上游调用；无新增依赖、可变余额文件或额度重置。

protobuf 的计量说明同步更新并由官方脚本生成 Go／Python 产物；字段号
和序列化 schema 未改变。回传计量包含当前 invocation 的全部已结算
尝试，Python 沿用真实 provider payload／response adapter，并保持
`max_retries=0`，不会自行绕过 Go。已结算错误分类通过 Converse 保留，
不可重试的终态不向用户输出 provider 原始错误或凭据。

取消退避不产生未派发的预留；取消进行中的尝试仍保留 unknown。
Python 保留原有 65 秒调用限额，合法但较慢的重试可能在限额内被取消；
这不能成为放行额外调用或清除预留的依据。真实 relay 错误是否提供
usage 必须另验，本记录的 HTTP fixture 不证明该外部契约。

## 新鲜验证

| 范围／命令 | 完整分母／退出码 |
| --- | --- |
| `go test ./... -count=1 -json` | 1,492 pass、71 skip、0 fail；45 测试包通过、13 无测试包；0 |
| `go vet ./...` | 0 |
| `python -m pytest -q` | 2,483 pass、17 skip、31 warnings、0 fail；572.66 秒；0 |
| `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 go test ./tests/e2e -run '^TestProductionLocalCore' -count=1 -json -timeout 300s` | 14 pass、2 现场环境 opt-in skip、0 fail；223.71 秒；0 |
| `python -m pytest -q tests/test_model_gateway_client.py` | 21 pass、1 warning；0 |
| `TestGatewayHonorsBoundedRetryAfter` | seconds／HTTP date 两场景实际等待后成功，1 父＋2 子；0 |
| `TestGatewayRejectsContradictoryRetryLedgerFacts` | 9 个非法事实场景拒绝新增 HTTP 和改写，1 父＋9 子；0 |
| `TestGatewayCancelBackoffRetainsOnlySettledAttempt` | 1 次已结算尝试，取消后用量／预留不变、无 unknown；0 |
| `TestGoAccountedRetryReachesActualPythonClient` | 真实 Go gRPC＋Python 子进程，外部 HTTP fixture；0 |

计量 tracer 使用公开 Gateway／gRPC 与 SQLite：第一次 429 报告 8 input、
0 output，第二次成功报告 3 input、2 output；每次派发前检查自己的
103-token 预留。最终 Go 批次与 Python 均读到 11 input＋2 output，费用
unknown。试验数据为 synthetic，不计入原首批真实 API 用量。

边界检查覆盖三次耗尽、503、认证、过长／畸形／带符号的 Retry-After、
重试缺 usage、方向上界违反、capability 撤销与真实 Ledger 重开。HTTP
600 的完整 103 unknown 保留，Ledger 仍可读。非法事实 fixture 覆盖
缺 parent、foreign owner／task、改上界／purpose、未结算或非 transient
parent、分叉和第 4 次；这是显式协议故障 fixture，不声称 provider
真实产生这些事实。Go 数量包含父／子 actions，所有失败与跳过保留。

原 1 亿 token 批次只读核对，身份仍为
`02cfc8d9c93975b3ea63f27cc0a3331d`，上限 100,000,000，used／reserved 均
0、任务槽空、usage 已知、费用 unknown。本轮真实模型调用 0，未读取
API 密钥文件；没有替代原批次或重置累计额度。前端布局与源码未改动，
本轮未另跑浏览器模型场景。

## Standards

`AI_REVIEWED`：最终未解决 0，硬违规 0、需处理的 Fowler 项 0。原 HTTP
600 可写入不可回放事实的 P2 已经 RED／GREEN 修复，Gateway 与 settle
均在写入前校验。未声明 HUMAN_REVIEWED。

## Spec

`AI_REVIEWED`：最终未解决 0，未见越界。原同一 HTTP 600 路径的 P1
已修复，符合未知用量保留和可恢复失败要求。完整 #3 的真实外部契约、
usage 对账及产品验收尚未完成。两轴各 0 项未解决发现。

## 失败与不可变收据

保留已计量 429 错误原被归为 unknown 的 RED、Python 分类 RED、带符号
Retry-After RED 和初始 fixture TempDir 关闭遗漏诊断、HTTP 600 无效结算
RED。fixture 清理已修正，不删除业务数据。另保留一次 apply_patch 因
gofmt 后空白变化未命中的工具诊断；生成物仅由官方命令更新。

不可变收据 `.scratch/model-accounted-retry/receipt.json`，绑定 968 个
源码／依赖摘要、本地指令摘要、完整命令、分母、退出码、原日志和原批次
读回。SHA-256：
`1892a0a4745274fdbbbd8f3308eed04f5a3ebee4d69d963d9636f6ad842f75cc`。
精确 staged paths、秘密扫描、提交
及当前源码 CI 绑定另存补充；旧收据不改写，用户 AGENTS footer 未暂存。

本切片单测／集成按声明范围验收。仍需未知用量及活跃 holder 的失败
释放对账、真实 provider 的重试与完整模型／Python／反思／child／故障
恢复、浏览器代码任务和真实后端 Trace。源码 CI 和 fixture 不代替完整
产品或发布合格，不据此关闭 #3。
