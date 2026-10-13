# 真实仓库问答失败分类与恢复：2026-10-14

运行源码：`f6c9833b56ea7ae95fead132de5d3651f05712b9`，模型 `grok-4.6`。
沿用[前轮](repository-qa-continuation-2026-10-14.md)两个原始问题、chi 源码、镜像和持久批次，
运行前冻结输入、894 项源码／指令、3 项前端产物及二进制。
登录使用既有 fixture identity，不作为真实登录验收。

本轮四个获准 run 全部失败，没有持久 assistant 回答。
原八项场景为 6 通过／2 失败，两个问题均未完成。
[工具轮次分类](model-round-limit-2026-10-14.md)与
[typed timeout 分类](model-gateway-classification-2026-10-14.md)
已由真实 Go／Python／模型／浏览器链路观察到；分类正确不代表任务完成。

## 完整尝试分母

| 尝试 | 入口／准备结果 | Go 终态／分类 | 新增 Read／Grep |
| --- | --- | --- | --- |
| v9 准备 | 二进制构建尚未结束，启动器读文件失败；未启动运行进程，未调用模型 | 未创建 run | 0 |
| v10 初次发送 | Python 端口绑定失败，HTTP 503；草稿保留，恢复前无付费调用 | 未创建 run | 0 |
| 恢复后重试发送第一问 | HTTP 202 | failed／tool_round_limit | 16 |
| 第一次点击继续 | HTTP 409，expected 138／actual 139 | 未创建新 run | 0 |
| 刷新后继续第一问 | HTTP 202，同 checkpoint，承接第一问 | failed／tool_round_limit | 0 |
| 原第二问 | HTTP 202 | failed／provider_timeout_unknown | 16 |
| 代理 profile 下继续第二问 | HTTP 202，同 checkpoint，承接第二问 | failed／provider_timeout_unknown | 3 |

最初 Go 的临时 Gateway 监听占用了 Python 固定端口 50061，Python 退出 1；
Go 健康检查未附着 continuation，首发拒绝后保留了草稿。
仅重启本轮自建进程，先确认 Python 监听，再启动 Go，浏览器“重试发送”获准。
这是私有验证启动器的顺序恢复，没有声称已修复便携产品启动器。

第二问发生实际 timeout 后，先冻结代理 profile，再仅为本轮 Go 进程设置
`HTTP_PROXY`／`HTTPS_PROXY=http://127.0.0.1:7890`，本机地址绕过代理。
重启前后已验证 Ledger 的调用及批次完全一致，随后从浏览器继续原任务。
该尝试再次 timeout；不能由此推断提供商根因、零消耗或超时应改为某个数值。
两个 user message 与冻结问题一致，没有重发问题或添加修复提示；续行 lineage 保留。

## 计量、保护与停止

同一批次新增 22 项真实调用：18 项 foreground、4 项 memory_reflection；
18 项结算、4 项 terminal unknown，新增已确认 292,674 tokens。
四项反思中两项已结算并形成 proposal，两项 unknown；
Go Memory／CAS 写回及跨会话召回未验证。
结束后累计已确认 1,153,500 tokens，原预留估计 1,006,024,576。
预留是未知调用的上界估计，不是实际消耗或服务商余额；费用未知。
所有旧调用保持原前缀，没有清空旧预留或为未知调用虚构零用量。

35 项工具结果全部成功，17 项 Read／18 项 Grep；没有 Shell、Write 或子代理。
原仓库及 index、隔离副本 98 个文件、894 项源码／指令和 3 项前端产物指纹保持一致，
Task Workspace 没有 `.runtime` spill。
活跃调用归零后，浏览器调用 Go `task/finish` 返回 HTTP 200，释放任务槽，计量与 unknown 不变。
自建 Go／Python 进程已停止，18091／50061 无监听；
已有分组 Docker 控制容器、镜像 pin 和数据保留。

## 收据与范围

不可变产品收据：`deployments/docker/.local/receipts/repository-qa-20261014/receipt-v10.json`，
SHA-256：`a4467f9c0c07ee7deadcdd8157d1e0694aa1aeb6324cca9d79bfd37b03014a83`。
收据绑定原输入、派发、浏览器画面、Go 验证的 Ledger 投影、启动／代理恢复、保护和停止核验。
`python .scratch/repository-qa-v10-20261014/audit_product.py` 退出 0；
它核对实际运行产物，未运行源码测试，也没有把失败任务改记为通过。
源码验证与四项 CI 另见[分类修复证据](model-gateway-classification-2026-10-14.md)。

Trace 后端未读回，完整产品、Memory 和发布未通过；#3／#5 保持开放。
下一步核对续行的剩余工具轮次与实际请求期限，使原问题获得完整回答，
保留既有失败和 unknown；不能仅提高限制或以成功工具结果代替任务验收。
