# 本机模型工作区准入 — 2026-10-12

关联 [#3](https://github.com/huadeng408/CodeOps-Agent/issues/3)。本轮实现
local-core continuation 和逐次工作区准入，工程状态 `IMPLEMENTED`。
单测、集成与真实 OpenSandbox 接线在本记录范围为 `VERIFIED`；真实模型、完整产品及
发布验收未运行。#3 保持开放。

基点 `01af17e51bdde0ea418fa95a234987096f4aadd3`。复用既有
SessionRunner、ContinuationSlot／Supervisor、Gateway／Budget、Memory 和
Session Ledger。配置 provider、批准的 OpenSandbox 和健康 orchestrator 后，
local-core 可承接会话。Context 和工具进入 Go 准备的隔离 Task Workspace，
Memory namespace 继续使用原仓库。

## 准入与文件状态

每次模型调用在 token 预留和 HTTP dispatch 前重新核对 owner、准备租约、
副本、Git registration／index、批准依赖和实际 Runtime.Ready。缺前提时返回
`task_prerequisites_missing`。模型配置齐备但没有沙箱的生产 HTTP 检查返回
503；游标、模型 HTTP 次数和 token 预留均未变化。

Go 已完成的 Write／Edit／NotebookEdit 根据 Ledger 中的
`dispatch < code/modified < result`、before／after／diff 摘要和连续文件状态
重放验证。合法连续编辑与新文件可以继续；外部改动、凭据／生成路径、公开
fixture 改动、Git index 变化仍拒绝。原基线及其摘要保留。

任何工具的未知结果及缺结果的 commit／dispatch 均阻断后续模型调用。
当前 Go 工具的 `ToolWorkspace` 例外只接受唯一 matching call ID、已 dispatch、
仍运行且租约有效的调用；付费入口不使用该例外。预算 Ledger 读取失败时
没有 Tokens 投影，execution 保持 blocked。

## 现场接线与分母

测试使用真实生产 HTTP 进程、Go Gateway／Budget／Runner／工具／SQLite，
以及原批准 Go OpenSandbox manifest v7、任务镜像和冻结 chi 源码。模型 HTTP
与 orchestrator transport 是外部 fixture；本轮真实 API 调用为 **0**。
固定部署、源码及镜像身份见 [OpenSandbox Go 基线证据](opensandbox-product-2026-10-12.md)。

正常最终现场实例 v6-known 经过四次 fixture 模型请求、已审批 Write／Edit、
真实离线 `go test ./... -count=1` 和后续准入；chi 两包通过，退出 0，资源
停止已确认，原仓库／index 摘要未变。独立 fixture Ledger 结算 40 synthetic
tokens，预留 0、价格 unknown。随后注入 hash-valid 的矛盾预算事实，能力
入口保持 execution blocked；该故障只在独立测试 Ledger 中发生。

v5-unknown 使用真实 Shell 超时，保留 `outcome_unknown=true`、精确 runtime
身份和 stopped=true。第四次模型准入被拒绝，上游仍只有三次请求，结算
30 synthetic tokens、预留 0；未为拒绝的请求新增预留。

七次现场尝试全部保留：v1 使用错误健康字段而失败；v2／v3 的 Shell 默认
30 秒不足以完成完整测试，保留未知结果，v3 起保存不可变 Ledger 快照；
v4 指定 120 秒后通过；v5-known 复现不可读预算被误报 ready；修复后
v6-known 通过；v5-unknown 通过真实超时拒绝检查。成功不覆盖先前失败。

原首批 `02cfc8d9c93975b3ea63f27cc0a3331d` 通过 Go Budget.Inspect
读回：上限 100,000,000，已用／预留 0，任务槽空、usage 已知、价格 unknown。
该批次未创建替代身份、预留或重置；fixture 数字不计入它。

## 回归、审阅与收据

| 范围／命令 | 最终完整分母／退出码 |
| --- | --- |
| `go test ./... -json -count=1` | 1,441 pass、64 skip、0 fail；45 测试包、13 无测试包；0 |
| `go vet ./...` | 0 |
| `python -m pytest -q` | 2,473 pass、17 skip、31 warnings；0 |
| `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 go test ./tests/e2e -json -run '^TestProductionLocalCore' -count=1` | 10 pass、2 需批准现场环境的 opt-in skip、0 fail；0 |
| 批准现场的正常／Shell unknown 两项 HTTP 检查 | v6-known、v5-unknown 分别退出 0；在上一行的普通回归外单独执行 |
| `git diff --cached --check`、18 个精确 staged paths 的秘密／机器路径扫描 | 0；无匹配值输出，AGENTS 未暂存 |

Go 计数包含父测试／子测试 actions，不能写成独立产品场景数。

初轮完整 Go 回归 1,441 pass／64 skip／0 fail，45 测试包通过、13 无测试包，
退出 0；vet 退出 0。本机 HTTP 回归的一项旧 Trace 检查因客户端 1 秒期限在
并发编译期间失败，原日志保留。该检查仅将客户端期限调整为 10 秒，随后
重新运行完整 Go 和本机 HTTP，最终均退出 0。该未调用的 Trace 测试变更不影响前述现场
实例的生产源码、执行配置、场景输入或 OpenSandbox pin。

Python 首轮完整分母为 1 fail／2,472 pass／17 skip／31 warnings：原有
期限检查实测 0.309 秒超过 0.25 秒。该断言和实现未修改，单文件 27 项
通过后，独立重跑完整 Python，结果见表。首轮日志保留。

两轴 `AI_REVIEWED`：未知 Bash／缺结果的 P1 和错误 readiness 均经公开
RED／GREEN 修复；Standards 0、Spec 0 未解决发现。没有人工逐条复核声明。

收据路径：`.scratch/model-product-admission/local-continuation-checks-v1/receipt.json`。
SHA-256：`e20485c3dc7f5d092809088bd1d77b768ddbe292cbd479ea087165671d59973e`。
它保留 856 个源码／依赖摘要、命令、退出码、分母、七次现场尝试及全部失败。
五份已保存的 Ledger 快照扫描未发现运行时秘密值；其中十个精确物理资源
均核验不存在。原收据不改写，交付后的源码／staged blob 绑定另存补充。
用户私有 AGENTS footer 保持未暂存。

仍缺 HTTP 显式结束、未知 usage 对账、可结算的有界重试，以及真实 Python
编排／模型、auxiliary／child／重启、浏览器闭环和后端 Trace 证据。本轮没有
运行完整发布门禁，也不据工程接线关闭 #3。
