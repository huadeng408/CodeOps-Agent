# Go 提案与服务商预算续行：2026-10-13

工程状态：`IMPLEMENTED`。`VERIFIED` 范围是受控 Go 错误示例的真实模型、
Go/Python、OpenSandbox、浏览器提案，以及预算／停止进程恢复边界。
公开仓库的完整十轮任务、反思写回和完整发布仍分别验收；#3、#5、#7 保持 OPEN。

## 结果与实现范围

Go 在同一 Session Ledger 中记录提案 intent/outcome，绑定 owner、Session、
隔离标识、原基线、来源事件、完整 diff 和最终文件清单。冻结归档包含路径、
存在状态、内容及原模式；测试使用实际消耗的封闭输入，记录归档和输入 SHA-256。
可信 bootstrap 校验归档，再由 UID/GID 65534 离线执行完整 Go 测试。
查看或刷新提案不会应用原仓库。

选定的右侧卡片展示 diff、文件状态、命令、退出码及绑定证据。
已确认停止的 stale／failed／blocked 提案允许新请求重新生成；历史请求重试
不会再次执行或替换最新提案。重放核对 intent 的内容，拒绝伪造 outcome。
diff 计算不依赖时限；大输入确定性地使用整文件替换，超过完整审查大小则拒绝。

用户已在服务商批准 100,000,000-token 硬上限。本地账本保留已确认消耗、
未知用量和原预留，终态计量缺口允许新的独立调用及已停止任务结束，不再逐笔询问。
活动调用、防重复派发、权限、工具未知结果和沙箱仍由 Go 拒绝。
新预留保存 scope 的进程身份；确认原进程死亡后，结束操作可将中断调用记录为
terminal-unknown，保持原预留。即使 scope 已先释放也可恢复；缺失或不匹配的
进程身份继续受阻。不调用自动结算，不重置批次，不虚构零消耗或费用。

## 真实产品演示与完整分母

输入是预先提交的受控 Go 错误示例，**不是官方评测或冻结的公开仓库任务**。
使用 `grok-4.6`、BeeAPI Anthropic adapter、真实 Go/Python 进程、批准的
OpenSandbox 镜像和真实 Chromium。模型自行读取源码／测试并提出修改，
已有产品工具审批由本轮自动化按用户授权范围执行，不声明人工逐条复核。

- 首轮前台任务失败，读取结果和失败终态保留；第二轮独立继续，修改实现并运行真实项目测试成功。
- 原始基线 `go test ./... -count=1` 退出码 **1**；模型修改后的 Bash 测试退出码 **0**；冻结提案复测退出码 **0**，完整输出和停止状态保留。
- 浏览器提案 diff、刷新保留、最终输入绑定及原文件／HEAD／index 不变均通过。8 项场景检查通过；前台对话完整分母 **2：1 失败、1 完成**。
- 真实 provider 尝试完整分母 **8：5 已结算、3 unknown**。前台 6 次中 1 次 unknown；后台反思 2 次均 unknown，不能据此声明反思写回通过。
- 本轮新增已确认用量 **60,477 tokens**，批次从 **22,526** 增至 **83,003**。原 unknown 预留 **502,048** 保留，新 unknown 预留另加 **1,506,144**，合计 **2,008,192**；全部费用仍 unknown。
- 旧任务和演示任务均经公开 Go 结束接口完成。最后活动调用 **0**、任务槽已释放、原用量和预留保留。没有执行此前取消的逐笔 `Budget.Settle` 操作。

两项自动化观察错误保留：v1 driver 的 stdin 在执行场景前关闭，未调用模型；
v2 准备请求的响应匹配器超时，但随后公开 Inspect 确认原准备已完成，未另建请求。
它们不隐藏为产品成功请求，也不与模型失败合并。

## 收据与绑定

[脱敏收据](model-proposal-budget-2026-10-13.receipt.json)，SHA-256：
`8d3791387c270dc208889bb04fcd4ea22e15af9a463e117768206f1eb25df857`。

原运行追溯 SHA 为 `61ff61fde3709b58726f65658e056da5bdd87874`，运行包含本轮
尚未提交的实现；收据保留实际源码、二进制、前端产物和运行输入指纹，不能只凭
该 Git SHA 复现。最终核验参与运行的 394 项源码／配置／依赖指纹不变；后补的
回归断言与本文不改变原运行内容。原收据 SHA、结果和 checksum 不改写。

Session `8e0c9521ed572c3e45ba48224773676b`；提案
`2755e501773e69d408a5710688b523f05ac92b2532d39872502d32cd03578c59`。
Go 公开 `ReadVerifiedSnapshot` 与 `Budget.Inspect` 验证规范 Ledger；HTTP
刷新同时核对提案语义。私有原始事件／日志只保留在忽略的本地 artifact 中。

批准 manifest SHA-256 为
`63b635fcdb66f5ac4859a960054534d2020641c90dfd6ca6c38c75ef2fcc2f2f`，
任务镜像为
`sha256:c35ffc50ff2d4e97ea90f973b29836945912397204592a0b06a0b6fbbba59b58`。
冻结输入和归档 checksum 见收据；没有更换 pin 或使用本机执行回退。

## 工程验证

以下命令的最终退出码均为 **0**；通过数包括 Go 子测试事件，helper／外部前提
造成的跳过单独保留。

| 范围 | 命令 | 当轮结果与 artifact |
| --- | --- | --- |
| 完整 Go | `go test ./... -count=1 -json` | 测试事件 1,516 pass、72 skip、0 fail；58 包：45 pass、13 无测试 skip；`.scratch/cloud-resume-20261013/go-full-complete.jsonl` |
| Go 静态检查 | `go vet ./...` | `.scratch/cloud-resume-20261013/go-vet-final.log` |
| 完整 Python | `.venv/Scripts/python.exe -m pytest -q` | 2,483 pass、17 skip、3 warnings；`python-full-final.log` |
| 前端单测／构建 | `npm test`、`npm run build`（frontend） | 12 pass、0 fail／skip，构建通过；`frontend-tests-final.log`、`frontend-build-final.log` |
| 前端浏览器 fixture | `node tests/taskProposal.browser.mjs`（frontend） | 6 检查通过；`output/playwright/task-proposal-1791894422313/receipt.json` |
| 真实生产 HTTP 核心进程 | 启用 `CODE_AGENT_RUN_LOCAL_CORE_E2E` 后运行启动、能力、身份、origin、结束及恢复的 10 个目标测试 | 12 pass test events、0 skip／fail；`http-core-final.jsonl` |
| 真实 OpenSandbox 提案／外部模型与 gRPC fixture | `python .scratch/code-change-proposal/run-runtime-check.py http-historic-green-v1` | 1 个顶层测试通过、0 skip，真实 provider 0 次；保留原仓库、历史请求幂等及伪造 diff 拒绝；日志与原始事实位于 `.scratch/code-change-proposal/` |
| 已释放 scope 的崩溃恢复 | `go test ./internal/admission -run '^TestStoppedScopeHolderRetainsCrashReservationAsUnknown$' -count=1 -json` | retained／released 两个真实子进程场景通过；`released-scope-green.jsonl` |
| 归档篡改拒绝 | `go test ./internal/sandbox -run '^TestOpenSandboxRunnerReturnsCompleteCommandOutcomeAndStopsOwnedSandbox/changed_frozen_input$' -count=1 -json` | 批准环境 Ready 后篡改归档，在创建／执行前拒绝；`frozen-input-negative-final.jsonl`。该 native pin 场景仅 Windows，Linux 明确跳过 |

除明确列出的私有目录外，工程日志位于 `.scratch/cloud-resume-20261013/`。
真实模型浏览器启动使用本地审阅过的 `model-proposal-live-v1/driver.mjs 2`，
公开收据保存其输入、截图、浏览器操作记录和日志 checksum。

失败记录保留：预算策略 RED、原旧断言回归、已释放 scope RED、历史提案
请求 RED、语义伪造 diff RED，以及前期编译／CAS／浏览器观察错误。
完整 Go 首轮暴露依赖准备仍按旧 unknown 规则判断的断言，定向修正后重新跑完整
回归通过。不得以最后的成功替代这些分母。

## Standards

两轴审查基线固定为云端 `61ff61fd`，覆盖工作 diff 和新增源码。
最初的重放时限、历史请求重复执行和 Windows 专用测试缺平台 guard 均修正；
最终无剩余 Standards 发现。审查是 AI_REVIEWED，不声明 HUMAN_REVIEWED。

## Spec

owner／Session／基线、隔离修改、实际测试、查看不应用，以及服务商预算
授权均符合当前声明范围；停止后的旧预留保留。最终无剩余 Spec 发现。
两轴分别为 0 项剩余发现，运行证据与审查结论分别记录。

## 未验证范围

公开仓库 Go／Python 的最终十轮分母、#5 完整问答、真实反思写回、完整 Trace
读回及冻结提案 cleanup-unknown 负向场景未在本轮完成。Trace 导出超时保留在
真实日志中。受批准应用仍属于后续票，本轮不会自动写回原仓库。
完整发布门禁未运行，不能以本轮演示或源码回归声明发布合格。
