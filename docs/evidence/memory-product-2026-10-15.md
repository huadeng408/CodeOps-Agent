# #14 真实反思与重启召回

范围：[GitHub #14](https://github.com/huadeng408/CodeOps-Agent/issues/14) 的五项验收。
工程 `IMPLEMENTED`；该票单测、集成和下述产品范围 `VERIFIED`；完整发布未运行。
Go 实现与回归见[源码证据](memory-scope-2026-10-15.md)；本记录补充实际 Windows
浏览器、Go/Python、BeeAPI、分组 OpenSandbox 和持久 Ledger 的产品验证。
完整 Trace、语义向量、跨仓库晋升 UI 和发布资格分别验收。

## 验收映射

| 验收项 | 证据与边界 |
| --- | --- |
| 真实反思、Go 校验及单一写入者 | 源代码任务的三次真实 tools-disabled 反思分别返回 0、2、5 个候选；Go 校验不可变来源后追加 proposal 与 Experience catalog。两次失败运行保留，后续完成没有覆盖它们 |
| provenance、CAS 与去重 | 主 Experience revision 1，关联源 Session、原 trajectory checksum 及精确事件引用；该 source identity 只有一次 catalog update。实际进程重启前后 catalog 完全一致；并发重试的公开 Go 回归只晋升一个持久 proposal/revision |
| 实际重启后的空新会话 | 停止并重新启动本次拥有的 Go/Python 进程后，新 Session 的原生 Go 审计起点只有一条 session/created。新隔离基线没有源任务新增文件；模型实际读取 Experience 并在最终回答引用其 ID，没有注入旧 transcript |
| 默认 owner/repository 范围 | 同一用户另一个仓库的轨迹检索为空；cases 检索只读到该仓库首次空查询新产生的经验，来源正是新仓库 Session，未读到 chi 的 Experience。异 owner、损坏来源、TTL/forget 由公开 Go Runner/Memory 接缝回归验证；实际匿名 HTTP 返回 401 |
| 原预算及完整分母 | 所有源码任务、续行、反思和召回进入原持久批次。已知消耗、原未知计量和预留分别保留；结束后核对 active calls 与任务槽。费用没有服务商数据时仍为 unknown |

## 实际任务与召回

源 Session：`79dafd2e0a6c56a09f3b876628b29461`。原 chi 基线为
`9b9fb55def404397748a9fc7e044efe9db1d618e`，98 个受控文件及原 index 保留。
模型在隔离工作区新增 `context_memory_regression_test.go`，SHA-256
`81440b849265880a4f86fe889eb59e793a8543f238725433945acfb870a118f7`。

实际浏览器生成的 proposal
`3b91a607e181ca7356e73630122329b073e02a79c47b6b8bea6d8e81a98ca5c8`
在批准的离线 Go 镜像运行 `go test ./... -count=1`，退出 0，两个包通过，
输出未截断，runtime 已停止。任务通过现有“结束代码任务”入口结束；原仓库没有应用修改。
反思发生在提案测试前，其内容只称需求／助手陈述的覆盖案例，不能冒充测试通过证据。

主 Experience：
`experience-fe5f2f3b625f3eab891cb2dac5059dd67e8e0c4e5de76addac09b56bc236c303`。
revision 1，source checksum
`e448f3a3217eca28d808f17a3834383e5cf256366357dea8e632b40c6f0d7c0d`，
来源事件为源 Session 的 #505 与 #542；proposal 为 #547。
重启后消费者 Session `85e42368609fb7ecde46ae6417309449` 的 Go 工具结果 #63
实际返回同一 Experience、revision 与来源；最终回答也引用了它，并保留“未实测”的限制。

跨仓库消费者 Session `eb6f01ce6387d868f71f77121b6cadf6` 使用独立两文件 Go 仓库，
没有显式选择或晋升 chi 经验。首次 RecallMemory 的 entries 为空；第二轮 cases
返回 `experience-46cf5a58b1f323142e276a702c3682d39cef91527470544d4120c5f018783ff2`，
它记录的是该新仓库首次空检索，source URI 指向该消费者本身。这个合法的本仓库新记忆
不应被误判为跨仓库泄漏，也不能把第二次结果记成空集。

本机身份入口拒绝另建用户：两次请求返回 409，未创建账户或绕过身份校验。
因此不声明“两位已认证用户的真实模型 E2E”；异 owner 拒绝的证据来自 Go 集成边界。

## 源码、失败与交付边界

源任务二进制 v2 对应 `9ee7a6bba2aed8e11c5bcf79d03d66857d710631` 的运行内容；
消费者 v3 对应 `819216856e4eb80a2c55931afeb6e82172b4d588`，二者之间只有已验证的
[Gateway scope 释放修复](model-scope-release-2026-10-15.md)影响运行源码，Memory 实现相同。
消费者派发前已核对全部 923 个运行文件及浏览器产物指纹。
[81921685 的源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38666102285)
四个 job 全部通过；原 CI 的 Go 竞争失败没有删除或改写。

保留的产品失败包括两次工具轮次上限、续行游标 409、后台反思占用时的两次发送 409，
以及消费者把 trajectory ID 交给 Experience 管理接口导致的真实未找到结果。
首次反思零候选不算写回成功。浏览器自动化的选择器／响应观察超时另有原始记录；
提案请求沿用原 request identity 读回真实结果，没有另发重复操作或伪造成功响应。

本机原始证据位于 `deployments/docker/.local/receipts/memory-product-20261015/v1/`。
初始冻结 inputs SHA-256：
`8d06f2a78a18d93d39990c92884c036e36d6bf45681c92eeb60cee5ac7337c9b`。
结构化结果、完整场景分母和封存 checksum 见[产品收据](memory-product-2026-10-15.receipt.json)。

本轮 30 次模型调用（前台 24、反思 6）全部结算，新增 unknown 为 0；已确认用量从
2,515,632 增至 3,098,371，差额 582,739。历史原预留 3,606,024,576 及 unknown
保持不变，费用仍未知。所有任务通过产品入口结束，最终 active calls 为 0、任务槽为空。
本次拥有的进程全部停止，精确核对后的控制容器恢复原先 exited 状态。

原生 Go Ledger 审计、109 项产物封存及只读断言均退出 0；封存 manifest SHA-256
`8e4edc057d85a818b4faa09c263d3a58af95a12c6293b538fb5b34ef98cfdc1f`。
源码阶段的全量测试、RED 和跳过仍保留原绑定；本次交付仅修改说明及证据，重新核对
923 个运行文件和浏览器产物一致后复用相应收据，没有重写历史源码 SHA 或运行结果。

最终证据的双轴审阅：Standards 硬违规 0／Smell 0；Spec 缺失、错误和范围扩张均为 0。
分类为 `AI_REVIEWED`，不宣称人工复核。精确 staged paths 为本轮五份说明／证据文件，
secret scan、本地链接／checksum 核验及 `git diff --cached --check` 均退出 0。
