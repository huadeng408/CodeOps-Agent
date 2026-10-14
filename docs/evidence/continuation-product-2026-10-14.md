# 有界续行的真实仓库验证：2026-10-14

运行源码：`5febece930145a8893d04d437df9c64aa49d6c99`；
[源码修复与验证范围](continuation-rounds-2026-10-14.md)。
工程状态：`IMPLEMENTED`；源码／公开协议范围已验证，当前模型产品范围为部分完成。
沿用前轮两个原问题、`grok-4.6` 配置和同一持久批次，未添加答案、修复提示或改写失败。

## 真实运行分母

| 运行 | 终态 | 成功工具结果 |
| --- | --- | ---: |
| 第一问初次发送 | provider_timeout_unknown | 0 |
| 第一问第一次续行 | continuation_runtime_error | 0 |
| 第一问第二次续行 | provider_timeout_unknown | 15 |
| 第一问第三次续行 | completed，回答已写入 Go Ledger | 11 |
| 第二问新用户消息 | provider_timeout_unknown | 10 |
| 第二问第一次续行 | provider_timeout_unknown | 9 |
| 第二问第二次续行 | continuation_runtime_error | 0 |

七次 POST 均获准（HTTP 202），终态一项完成、六项失败。
45 项工具结果全部成功：33 Read、10 Grep、2 Skill；没有 Shell、写文件或子代理。
两个 Skill 请求通过实际浏览器逐项批准，只加载受限文档，不扩大 Shell／写权限。
实际计划最多三步；计划卡片、工具进展和最终任务结束均通过真实浏览器操作。

第一问第三次续行实际完成 11 项新工具操作后返回回答。
只读检查既有 Python checkpoint 投影显示该 lineage 的累计轮次达到 10，
不再被截回第 6 轮；该诊断不替代 Go Ledger 的事实与完整性校验。
本轮没有真实杀进程后恢复待执行工具；该部分仍只有前述公开协议 fixture 范围证据。

原冻结的八项场景保留全部分母：六项 PASS、一项 PARTIAL、一项 FAIL。
第一问虽有持久完整响应，源码核对发现 `r.Use(A).Use(B)` 示例不符合
`mux.go` 中 `(*Mux).Use` 的无返回值签名，因此回答质量记 PARTIAL，保留原回答。
第二问仍没有持久完整回答，记 FAIL。该核对不是人工逐条复核，不标 HUMAN_REVIEWED。

## 计量、保护与准备失败

25 次真实调用包含 19 次前台、6 次反思；16 次已结算、9 次终止后用量 unknown。
新增已确认 352,363 tokens；批次累计已确认 1,505,863。
原预留估计累计 1,906,024,576，属于保留的上界估计，不是实际消耗或服务商余额；费用未知。
旧调用事实逐项保持，停止任务与关闭进程没有释放或改写 unknown。
六次反思中出现一项 reflection-proposed；完整 Go Memory／CAS 写回和重启召回仍未验收。

停止后通过浏览器结束代码任务（HTTP 200），确认活跃调用为零、任务槽释放。
原仓库、HEAD、index 与 98 个原文件保持；隔离副本 98 个文件保持，未出现 `.runtime` spill。
894 项源码／指令／配置输入和三项前端产物指纹保持。
仅停止本轮自有 Go／Python 进程，驱动退出码 0；批准的 Docker 控制服务、镜像和数据保留。

v11 的启动准备失败单独保留：Windows venv 启动器的监听端口归属其直属 Python 子进程，
旧准备检查只接受启动器 PID；同时普通管道未保留交互 stdin。
Go／浏览器尚未启动，新增付费调用为零，旧 Ledger 保持；v12 修正准备入口并用交互终端运行。
初次请求实际继承 Clash，执行后才发现并记录，不能声称预先冻结了直连。
后续单进程直连配置在 Go 重启及新派发前冻结；不修改系统代理。

## 当前边界与证据

两项 continuation_runtime_error 均未产生对应的前台付费请求。
精确时间戳核对显示：第一项与后台反思活跃期重合；第二项不重合，发生在
工作区准备租约过期后，HTTP 核验明确报告副本保留、需要对账。
内部错误的精确归因仍须定向复现，不能将时间相关性写成已确认根因。
现有源码期限为 HTTP 60 秒、模型 RPC 65 秒、反思提交 30 秒。
[xAI 当前最佳实践](https://docs.x.ai/developers/model-capabilities/text/best-practices)
对原生推理请求建议更长的期限和流式处理；这不能直接确认 BeeAPI Anthropic relay／4.6 的契约。
下一步复现前台准入与后台反思的协调边界、处理仍在使用的工作区租约恢复，
再核对 Go 控制的请求 profile 和对应 RPC 期限。

不可变产品收据：`deployments/docker/.local/receipts/repository-qa-20261014/receipt-v12.json`，
SHA-256：`a642cf9f249751c7c2b42b820a76a329b0c25d63d168231ab39e23a1af433f63`。
原收据曾将两项准入失败都关联到反思，该元数据观察已被后续时间戳审计否定。
只更正该字段的补充绑定为同目录 `receipt-v12-correction.json`，SHA-256：
`ec89500a7b7b8adc372422b6c0a7ffbfd36d6744208ff860d983297643065774`；原收据、
运行结果、分母、计量、输入和 checksum 保持，补充文件记录原错误和失败的审计断言。
完整原始输入、网络配置、浏览器操作、Go 验证快照、准备失败与保护记录保存在
`deployments/docker/.local/profiles/repository-qa-20261014/`；脱敏日志在对应 `.local/logs/`。
源码收据和四项通过的 CI 绑定保存在 `.scratch/continuation-rounds-20261014/`。
本轮不关闭 #3／#5，不宣称普通登录、完整产品故障矩阵、Trace 后端读回或完整发布通过。
