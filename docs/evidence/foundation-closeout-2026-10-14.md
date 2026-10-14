# 基础票按声明范围收口：2026-10-14

用户指定顺序：先同步预算和停止规则，再收口 #3、#5，然后推进 #7、#8。
当前运行源码为 `7144118e58ca00f434d258558de18e3c1dda0e03`。本轮不改变发布指标。

## 预算同步

正式规格 #1 的未知计量停止条款和 #17／#18 的旧人民币 100 元条款已更新并回读。
共同规则为服务商已设置的 100,000,000-token 硬上限、同一持久批次、单代码任务；
常规调用无需逐笔批准。已结束调用保留原预留与 unknown，可以独立续行或结束已停止任务。
活跃调用、防重复派发、无法确认的上界／权限／工具结果／沙箱继续拒绝；服务商额度耗尽时停止。
费用仍独立记录，未知费用不写为零。历史收据和旧运行结果不改写。

## #3 准入与计量验收映射

| 原验收项 | 对应证据与边界 |
| --- | --- |
| 具体缺失条件、凭据安全注入与脱敏 | [默认准入](model-default-admission-2026-10-12.md)、[工作区准入](model-local-workspace-2026-10-12.md)、当前浏览器右侧能力入口；Python required mode 不接收 provider 凭据 |
| 持久批次、全部调用统一预留／结算、usage 归一化 | [真实五调用探针](token-gateway-2026-10-10.md)覆盖 foreground／reflection／compaction／Worker；该历史批次与当前产品批次分别保留，不合并累计用量。当前真实产品前台及反思见 [v13](continuation-recovery-product-2026-10-14.md)。当前 Go admission 回归覆盖 cache／reasoning、重复字段、CAS、重启与跨进程准入 |
| unknown 保留与独立续行、服务商额度、费用未知 | [真实 Go 提案与预算续行](model-proposal-budget-2026-10-13.md)、[停止 scope 恢复](model-scope-recovery-2026-10-12.md)、[v13 结束恢复](continuation-recovery-product-2026-10-14.md)；原预留是估计，不是实际消费或服务商余额 |
| 单任务／聚合 relay 与有界重试 | 当前 Go admission 回归；[已结算 429／5xx 重试](model-accounted-retry-2026-10-12.md)和[期限协议](model-deadline-2026-10-14.md)采用受控 HTTP fixture／实际 Go-Python 进程。没有声称真实 BeeAPI 曾返回可结算 429 |
| 正常、重试、反思、子调用、重启、费用未知、草稿及完整分母 | 上述各轮原始尝试及收据；[草稿与幂等](model-message-preflight-2026-10-12.md)、[入口恢复](model-entry-recovery-2026-10-13.md)、[真实分类／启动草稿恢复](repository-qa-classified-2026-10-14.md)。源回归与真实 provider／浏览器结果分别记录 |

#3 的关闭边界是这些准入与计量行为。完整反思内容写回与新会话召回属于 #14，
完整成功代码 Trace 属于 #15，公开仓库十轮任务属于 #17／#18；它们不阻止 #3 按范围交付。
未知 usage 保留计量结果即可，不能将其升级为已结算或证明反思写回成功。

工程状态为 IMPLEMENTED；#3 的上述准入／计量验收为 VERIFIED。
两轴审阅确认原五项验收没有额外的真实 429、Memory、Trace 或十轮要求，
初审 Standards 0 项、Spec 0 项。完整产品及发布没有据此变为通过。

## #5 问答验收范围

按原票验收首个 provider、P0／P1 元数据、经 Go 授权获取 P2、计划与进展、
拒绝／错误分类，以及新 user turn 与实际 Continuation 的语义。
当前 Go ContextEnvelope 回归验证 metadata-only、来源 checksum 与版本字段；
当前 Python gateway／Context／runner 回归验证 required gateway、权限与历史／续行边界。

本轮独立冻结一条 Use／With 只读问答，最多一个初始 run 和两次续行，使用真实浏览器、
Go／Python、BeeAPI 与已批准的保留工作区。原 v13 两问及补问的 PASS／PARTIAL／FAIL
均保留；本轮不把旧两问整体改为通过，不要求十轮、完整 Memory 或后端 Trace。
该新 profile 的产品结果为 FAIL，不能声明其通过。三项获准 run 均失败：
首项工具轮次耗尽、首次续行 provider timeout、末次续行轮次耗尽，末次四项只读工具
因准备租约到期被 Go 拒绝。没有新增持久答案，不追加第四次尝试。

#5 的原五项验收按以下既有真实结果与当前范围核验收口；不是把 v14 的失败改为通过。

| 原验收项 | 关闭依据 |
| --- | --- |
| 首个实际 provider／现有 client 与 Loop Registry | v13 的实际 BeeAPI／grok-4.6 Use／With 问答已完成，兼容 adapter 保持，未新增执行器 |
| P0／P1／P2 按需加载、来源与 revision、Go Read | v13 第二问核心文件／函数来源核对 PASS；ContextEnvelope、授权读取及正常任务源码不变；当前 metadata-only、checksum／字段兼容回归通过 |
| 浏览器有限计划、进展、失败状态与受控 Context | v13 回答／计划／工具显示和 v14 的实际有限计划、工具进展／失败均可核对；Harness-managed Python 不读取独立 LayeredContext，使用 Go 传入的 Context 与 Read |
| 参数变换不授权、拒绝与错误／取消分类 | 当前 admission／gateway／runner 回归及各原拒绝记录；v14 timeout 与租约拒绝原样保留，未重复未知副作用 |
| 新 user turn／实际 Continuation、CLI／HTTP 共用边界 | v13 新第二问及同请求冲突恢复、真实获准续行，当前公开 gRPC／HTTP 与缓存游标回归保持；不放宽跨 Session 权限 |

v13 的 895 项输入中 886 项指纹仍相同，九项变化仅为 Go 模型期限配置／网关、
protobuf 绑定、Python client 与对应测试；三项前端产物保持。受影响期限接缝由
[实际 Go／Python 进程回归](model-deadline-2026-10-14.md)与当前准入回归验证。
保留 v13 的原运行 SHA、结果和收据，不将它改绑为新运行。
逐文件核验保存在 `.scratch/foundation-closeout-20261014/v13-scope-binding.json`。
因此 #5 的原问答／Context 接入范围为 VERIFIED；180 秒可选 profile 的整条产品运行仍 FAIL。
原 v13 第一问 PARTIAL、补问 FAIL 及其他原尝试继续保留，不宣称所有回答或调用均成功。

## #7 与 #8

#7 的范围是至少一个真实 Go 隔离修复、实际项目测试、绑定提案与查看不应用。
[已实现演示](model-proposal-budget-2026-10-13.md)使用受控 Go 错误示例，保留基线失败、
修改后成功及冻结复测，不冒充 #17 的公开仓库十轮任务。
#8 的确认／拒绝与单文件恰好一次应用按自己的验收实现，尚未据此声明完成。

## 验证与保留范围

当前启动 Go 采用 180 秒请求 profile／570 秒 RPC binding，反思父期限仍为 30 秒。
首次启动因原控制容器已停止而失败，未创建配置、子进程或付费调用；核对原 ID／镜像／
分组后重启该容器。准备响应的八秒辅助等待超时，随后公开 Inspect 和 Ledger
确认同一隔离副本已准备，98 文件与原基线保持，没有再次准备。
这些辅助失败保留在本轮分母，不改写为原操作脚本全程通过。

本轮完整发布、Memory 写回、后端 Trace 与十轮综合验收未运行。
浏览器复用明确标注的 fixture 身份，普通登录继续按对应产品范围记录。

v14 的七项独立场景为 6 PASS／1 FAIL；三项获准 run 为 0 completed／3 failed。
24 次真实调用为 21 foreground／3 reflection，23 settled／1 terminal unknown；
47 项工具结果为 43 成功／4 已知租约拒绝，没有 Shell、修改或子代理。
新增确认 389,346 tokens，当前批次累计 2,329,796；原预留估计 2,506,024,576，费用 unknown。
浏览器结束接口 HTTP 200，TaskID 已释放、ActiveCalls 为零；原仓库／index 和副本 98 文件
均保持。所属 Go／Python 进程停止后两个端口无监听，控制容器及原 pin／数据保留。
浏览器辅助 driver 在 finally 清理时仍等待；核对其 PID、创建时间、父进程和入口后
只停止该所属 driver，退出码 1。原子进程 stdout 缓冲未保存，不声称完整日志／Trace。
该清理诊断在 `receipt-v14-cleanup-note.json` 单列，原收据及全部计量／分母不改写。
产品失败收据：`deployments/docker/.local/receipts/repository-qa-20261014/receipt-v14.json`，
SHA-256 `d8fb1ebd0d0bd9031fe7ac95db26b2e5150e658759423fb177933d633aaec6b0`。

| 当前验证命令 | 完整分母／退出码 |
| --- | --- |
| `go test ./internal/admission -count=1 -json` | 133 pass／4 helper skip／0 fail；退出 0 |
| `go test ./internal/session -run 'Test(BuildContextEnvelope\|ContextEnvelopeP2\|ManagerContextEnvelope)' -count=1 -json` | 5 pass／0 skip／fail；退出 0 |
| `.venv/Scripts/python.exe -m pytest -q tests/test_model_gateway_client.py tests/test_context_memory.py tests/test_context_runner_events.py` | 135 pass／1 skip／1 warning；退出 0 |
| `go build -trimpath -o deployments/docker/.local/builds/repository-qa-20261014/server-v14.exe ./cmd/server` | Go 1.26.0；退出 0 |
| `audit_source.py` 的输入／政策回读核验 | 15 项最终完整回归运行输入指纹仍相同；三份 Issue 正文逐字核对；退出 0 |

本轮源码未变，不重新把[前轮完整回归](model-deadline-2026-10-14.md)计为本轮测试；
其原输入指纹经当前绑定核验保持。当前源范围不可变收据为
`.scratch/foundation-closeout-20261014/source-receipt.json`，SHA-256：
`de3d123fc4f9f0c37ce425a8fc97c9fc5d0983268216a9f1707569efd94c5703`。
审计辅助脚本首次以默认 GBK 读取 UTF-8 失败，未写收据或改变 Ledger；明确编码后通过，
原失败独立保留。各原运行收据的 SHA、分母、失败和 checksum 不改写。
