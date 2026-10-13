# 真实仓库问答续行：2026-10-14

运行源码：`c70f1049dd6002a34e9b7b8e3fa313cf1514cab6`，模型 `grok-4.6`。
使用与 [上一轮](repository-qa-product-2026-10-14.md)完全相同的两个原始问题、
chi 源码、镜像和持久批次；启动前冻结输入、894 项源码／指令、3 项前端产物和二进制。
普通登录使用既有 fixture identity，未作为真实登录验收。

本轮产品结果仍为部分通过。第一问完成；第二问及随后获准的续行失败，
没有第二条持久 assistant 回答。错误仍为 `agent continuation failed`，
不能仅凭发生时间或计量 unknown 推断提供商的具体失败原因。
[工具轮次修复](model-round-limit-2026-10-14.md)已通过公开协议回归与四项源码 CI；
本轮未取得工具轮次耗尽的实际运行样本，不能声称该分类已在真实调用中验证。

## 完整尝试分母

| 尝试 | 产品入口结果 | Go 终态 | Read／Grep |
| --- | --- | --- | --- |
| 第一问 | HTTP 202 | completed | 9 项，全部成功 |
| 第二问 | HTTP 202 | failed | 0 项新增工具结果 |
| 首次点击继续 | HTTP 409，expected 107／actual 108 | 未创建新 run | 0 |
| 刷新后点击继续 | HTTP 202，新 run 承接第二问，checkpoint 不变 | failed | 3 项，全部成功 |

两个 user message 与冻结问题一致；没有重发问题或增加修复提示。
续行的 `retryOfRunId`／`retryOfRunIds` 保留第二问 lineage。
浏览器计划面板与真实工具进展可见，原八项场景为 7 通过／1 失败；
第二问未完成这一失败保留，不能以 12 项成功工具结果替代完整回答。
创建观察器曾误断言 HTTP 201；真实入口为 200，按 GET 读回确认已创建，
没有再次创建会话。该观察器失败与产品失败分别保留。

## 计量、保护与停止

同一批次新增 12 项真实调用：9 项 foreground、3 项 memory_reflection；
7 项结算，5 项 terminal unknown，新增已确认 128,647 tokens。
三项反思均为 unknown，未声称 Go Memory/CAS 写回或跨会话召回通过。
结束后累计已确认 860,826 tokens，原预留估计 606,024,576；
预留是各未知调用的上界估计，不是实际消耗或服务商余额，费用仍未知。
旧调用事实保持原前缀，新独立调用没有清空旧预留或虚构零用量。

原仓库和 index、隔离副本的 98 个文件、源码／指令和前端产物指纹均保持一致，
Task Workspace 中没有 `.runtime` spill。模型只使用 5 项 Read／7 项 Grep。
活跃调用归零后，由浏览器调用 Go `task/finish` 返回 HTTP 200，释放任务槽；
确认计量和 unknown 不变。自建 Go／Python 进程已停止，18091／50061 无监听，
已有分组 Docker 控制容器、镜像 pin 和数据保留。

## 收据与范围

不可变产品收据：`deployments/docker/.local/receipts/repository-qa-20261014/receipt-v8.json`，
SHA-256：`f321f1a49021eb81cce1f932e28a9d4c3ae80afd275f326346ce190d2912471b`。
原始输入、每次派发、计划／工具画面、Go 验证的 Ledger 投影、保护与停止核验均由收据绑定。
`python .scratch/repository-qa-v8-20261014/audit_product.py` 退出 0；
该命令核对实际运行产物，没有调用模型或运行源码测试。

Python exporter 观察到本机 6006 连接拒绝，Trace 后端未读回验证。
真实第二问、错误归因、完整重试分母、反思写回、Memory 和完整发布仍按各自票验收；
不因这份部分产品证据关闭 #3／#5，也不把 Trace 前提作为八项文档／门禁清理的阻塞条件。
