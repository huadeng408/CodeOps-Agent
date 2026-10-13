# 真实仓库两轮只读问答：2026-10-14

运行源码：`355be0c192161c9e809bd07b34f66f36dac81c03`。这是新的冻结运行，
使用已批准的原问题、chi 仓库 pin、OpenSandbox pin 和原持久批次。
[旧失败分母](repository-spill-2026-10-14.md)及[源码修复证据](model-total-limit-2026-10-14.md)
保持原结果，不把旧上界违反、重试失败或 unknown 改成成功。

产品范围：实际 Chromium → 产品 HTTP → Go admission／Ledger → Python →
真实 provider → Go Read／Grep。使用现有测试身份登录浏览器；正常注册／登录未验。
没有运行提案测试、Shell、修改工具或子代理。完整代码任务、Memory 写回、
后端 Trace 和发布分别验收，本轮不关闭 #3／#5 整票。

## 输入与结果

会话：`38e1b97ca52f59e054808bfd65e8b35d`。
chi HEAD：`9b9fb55def404397748a9fc7e044efe9db1d618e`，98 个源文件。
模型仍为 grok-4.6，输入上界 500,000，请求回答 cap 2,048，
独立完整计量上界 100,000,000 在派发前冻结。
总量依据服务商强制的累计硬额度，不声称是模型固有单次输出上限，
也不是每次重新获得 1 亿额度。原输入及新的计量契约分别固定 checksum。

| 原八项场景 | 结果 |
| --- | --- |
| 工作区准备 | PASS；产品 HTTP 200，prepared |
| 第一问：中间件执行顺序 | PASS；真实回答已落入 Ledger，引用实际读取的 chain.go、chi.go、mux.go 及函数 |
| 第二问：Use／With 对比，新 user turn | FAIL；HTTP 202、同会话新 checkpoint，20 个读取结果成功，但运行 failed，没有持久 assistant 回答 |
| 浏览器计划与工具进展 | PASS；有限三步计划、右侧当前阶段和 Read／Grep 进展可见 |
| revision 与读取结果 | PASS；同一冻结 HEAD／基线，Go 的 26 个 Read／Grep 结果全部成功 |
| 同一 Ledger 的计量与失败 | PASS；原事实保留，新调用按相同批次结算或保留 unknown |
| 原仓库与任务文件保护 | PASS；98 项 SHA-256、原 HEAD／index／status 均一致，任务副本没有 .runtime 缓存 |
| 已停止任务结束与 unknown 保留 | PASS；界面 HTTP 200，任务槽释放，原用量和预留不变 |

完整分母为 8：7 PASS／1 FAIL。两问不能合并为“问答通过”。
第一问 `run:7e806595d72136a8fa622039173288f3`：6 个工具结果，completed。
第二问 `run:54f5e7a831a1fa115ca05205449f2935`：20 个工具结果，failed。
没有因观察等待而重复提交原问题；诊断的只读断言失败按 RED 保留，不算新模型尝试。

## 计量、诊断与验证边界

本轮 13 次真实模型预留：11 前台、2 反思；12 次结算、1 次终态 unknown。
已确认增加 258,336 tokens。缓存作为输入组成部分计入，未另加到总量。
第一轮反思用量未知，保留原预留；第二轮反思确认 5,519 tokens，
仅形成 reflection-proposed，不宣称已完成 Go Memory／CAS 写回。

批次结束累计已确认 732,179，保留预留估计 106,024,576，ActiveCalls=0，任务槽为空，
费用 unknown。预留估计含这次未知调用的保守上界，不是实际消耗或服务商余额；
已结束的计量 unknown 没有阻止第二轮的独立调用或停止任务结束。

第二轮的公开错误仍为 `agent continuation failed`，内部固定分类为
`continuation_runtime_error`。只读复制检查点后，确认最后状态为 tool_after／第 6 轮，
配置也是 6 轮；原数据库及 WAL 的 SHA-256 前后一致，没有模型调用。
工具轮次上限后的终稿处理和终态分类是下一步诊断线索，尚未将它们当作完整根因。
先前的 checkpoint head ValueError 和计费上界拒绝未在本轮重现。

| 验证范围／命令 | 本轮结果 | 退出码 |
| --- | --- | --- |
| 私有 audit_product.py | 原八项分母、唯一结算、原事实前缀、原仓库／任务 pin、停止任务保护核验一致 | 0 |
| 私有 inspect_checkpoint.py | 仅检查副本，原 DB／WAL 不变；1 个依赖弃用 warning | 0 |
| 驱动与进程结束 | 驱动正常结束，所管理的 Go／Python 及子进程停止；18091／50061 无监听；既有 Docker control pin 仍运行 | 0 |
| 单测／集成 | 本次运行未重跑；源码修复的当轮结果及四项 CI 见上方源码证据链接 | 未运行 |
| 完整产品／发布 | 第二问失败；后端 Trace 没有读回，其他专项与发布未运行 | 按范围记录 |

不可变产品收据：`deployments/docker/.local/receipts/repository-qa-20261014/receipt-v7.json`，
SHA-256：`7c6800e608e764ec2cf50e30f4d165c67f5ef7dd6af5cea1a9fb7b61f46eff65`。
冻结输入 SHA-256：`db93ff1f6eacb1a15676f9edd247339a7788cd2e4139b65368f3954e61447cb4`。
截图、原始运行投影、脱敏进程日志、诊断副本和原失败留在私有运行目录，
不进入公开提交。当前需要恢复第二问的完整回答，并按对应票补齐真实重试、
反思／子调用、重启、Memory、完整代码任务及 Trace 的声明分母。
