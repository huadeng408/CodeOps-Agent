# 新消息准入预检与本机运行观察

范围：#3 的 local-core 自然语言消息入口。基点 `311afc31`；本次修复
在新 `user/message` 写入前核对 Task Workspace 与当前预算，沿用已选择的
右侧卡片布局和 Matt Pocock implement/TDD/code-review 工作流。

工程状态 `IMPLEMENTED`；工作区／既有 unknown 用量拒绝及幂等交付按声明范围
`VERIFIED`。真实前台模型成功、完整代码任务和发布均未验收，#3 保持开放。

## 行为与回归

原浏览器在工作区尚未准备时收到 HTTP 202，随后运行失败并清空输入。
现在 SessionRunner 在新消息 Append 前调用已有工作区边界；local-core 同一
resolver 还只读核对 Budget，拒绝 unknown、未结算预留和其他任务占用。
拒绝返回 HTTP 409，已有前端错误处理保留草稿和 requestId。
已接受请求的幂等读取先于新增预检；部分写入的恢复顺序保持原契约。

这是提交前的快照检查；并发提交的最终付费准入仍由 Gateway 的预留 CAS
强制，不能据此声称全局原子提交。每次模型尝试的原检查仍保留。

公开 HTTP 检查使用真实 Go、SQLite、批准的 Go OpenSandbox；模型 HTTP 和
orchestrator gRPC 是外部 fixture，自身模块没有替身。这些测试未调用真实模型。

| 检查 | 原结果与最终结果 |
| --- | --- |
| 未准备工作区 | RED：202 != 409；GREEN：拒绝且 cursor/run/模型请求数不变 |
| 已存在 unknown 用量 | RED：202 != 409；GREEN：拒绝新消息，旧请求仍 202，103 synthetic 预留不清除 |
| 已准备的正常模型工具路径 | 既有隔离 Write/Edit／完整 chi 测试与四次 fixture 计量继续通过 |
| 目标包 | `go test ./internal/session ./cmd/server -count=1`，exit 0 |
| 完整 Go | `go test ./... -count=1 -json`：1492 pass／71 skip／0 fail，45 测试包／13 无测试包，exit 0 |
| Go vet | `go vet ./...`，exit 0 |
| 生产 local-core HTTP | `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 go test ./tests/e2e -run '^TestProductionLocalCore' -count=1 -json`：14 pass／2 现场 opt-in skip／0 fail，exit 0 |
| 前端构建 | `npm run build`，exit 0；本次前端源码未修改 |

现场定向命令为 `python .scratch/model-live-product/run-live-check.py <attempt>`；
它仅注入已批准的运行配置。两次 RED 和两次 GREEN 的原始日志均保留；
第一项 GREEN 106.51 秒，最终含 unknown／幂等检查的 GREEN 112.21 秒。
Python 全套和 frontend tests 本轮未运行；不引用旧数量作为本轮证据。
修复后的浏览器草稿保留尚未重跑；桌面截图属于修复前的真实观察，
本次修复由 HTTP 409 与 Ledger／计量不变的公开接口检查验证。

## 真实运行与人工对账

本轮确实启动生产 Go 和 Python，并通过鉴权浏览器创建 Session、准备固定
chi 工作区。原仓库 HEAD、index 和全部文件指纹未变；固定基线仍为
`2bb85eb0c031c9996db9ef0a3f3afd177aae1a0b48cce5305cfeef8c61682594`。

自动化启动失败保留：REPL 绑定／模块加载、Windows 宿主缺正常环境字段、
配置占位符以及 Python 解释器选取。只调整验证进程配置，未修改系统代理
或系统环境。最终的前台请求被验证脚本过窄的 Python project_root 拒绝；
前台模型派发为 0，不能报告真实代码任务成功。

原批次 `02cfc8d9c93975b3ea63f27cc0a3331d` 的完整调用分母为 **1 个真实网关
尝试**，purpose 为 `memory_reflection`，Go call ID
`10c303901c964ce68f67e9e2ff55964c`。先预留 502048，随后 unknown；实际
Go／Python 进程重启后同批次仍保留预留，显式 finish 返回 409。
真实 Phoenix 读回保留 agent／memory 错误及 workspace.prepare，未证明
完整 Go／Python／工具成功链。

用户提供对应时段商家日志：grok-4.6、Anthropic Messages、Xai-Heavy、
official、非流式、已中断，token/cache/cost 为 `-`。随后用户针对该笔明确
确认“0 消耗，没扣费”。经既有 Go `Budget.Settle` 追加人工零用量结算：
原 reserved／unknown 事件不删除、不改写，只增加一个 settled 事件。
绑定结算 checksum：
`a2ebb5e40875cc57e59be4294bf5adf3f94b47a887a964431792096bf69983a7`。
结算后仍为原批次，used 0／reserved 0／unknown false；任务槽未擅自结束。
零用量依据为逐笔 operator attestation，不能升级为自动 provider usage 证据。
自动费用字段仍为 unknown；用户的未扣费确认单独记录。

官方 [API 文档](https://beeapi.ai/docs/03-api/overview)声明 `/v1/models` 为
无上游生成、无收费的目录读取。本轮 Go 目录检查 HTTP 200、目标模型存在，
生成请求 0；目录可用不代替模型生成、完整用量契约或发布验证。

源码核对发现 `SessionRunner.fail` 用五秒 context 调用 `commitSessionMemory`；
后者的三十秒内部期限不能扩大父期限。这与本次约五秒的反思失败时间吻合，
尚待独立跨进程复现，不能把中断归因于 provider 自身。
后续优先跑通正确启动配置下的一条真实代码任务，保留所有
失败和副作用边界；不把大量模块测试当作默认产品路径的成功。

## 交付证据

Standards 与 Spec 两轴当前源码复核均 0 未解决发现，状态 AI_REVIEWED；
没有源代码 HUMAN_REVIEWED 声明。不可变 receipt 和提交／CI 绑定见本轮
交付记录；原始产物位于忽略的 `.scratch/model-live-product/`。
