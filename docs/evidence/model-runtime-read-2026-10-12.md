# 默认入口的真实模型读取与运行边界

范围：#3 的失败反思期限、只读 Git Context 和默认浏览器读取路径。
源码基点 `0fae735b160dbbca34a2f8a8eefb489026152b6b`，继续当前 main。
工程 `IMPLEMENTED`；下列定向／集成和前台读取范围 `VERIFIED`。
完整代码修改、提案应用、Memory 成功写回与完整发布尚未验收，#3 保持开放。

## 修复与公开边界

失败落盘使用五秒 context；此前该父期限也截断 Memory 的三十秒反思。
现在 fail 仅把反思调用接到 Runner context，沿用已有三十秒上限并随
Runner 关闭取消。Memory API、显式取消与成功运行的既有策略不变。
最初的统一 context 分离方案改变这些策略，被目标回归拒绝并移除。

生产 Python 构造 Git Context 时，普通 status／diff 刷新了隔离 Git index，
Go 随后以 `task Git index does not match prepared lease` 拒绝模型准入。
共享读取入口现在仅在该命令关闭 optional locks 和 index 自动刷新。
Go 的 index pin、基线、授权和失败副本均未改写；两处调用方共用修复。
Git diff 的默认 index 刷新行为见 [官方文档](https://git-scm.com/docs/git-diff)。

新测试只通过已确认的 SessionRunner／Run、TaskWorkspaces Prepare／Inspect、
Ledger、Gateway 和真实 Python 进程观察结果。外部 HTTP／gRPC 传输是 fixture，
自身模块没有替身。Task Workspace 原生准备检查明确为 Windows 范围。

| 检查 | 结果及完整边界 |
| --- | --- |
| 六秒反思 | 两次原行为 RED；修复后正常结算 5 fixture tokens，失败终态保留 |
| 关闭反思 | 去掉生命周期绑定时 RED；最终取消真实 HTTP 请求并保留 103 unknown 预留 |
| Git Context | 真实 Go 准备后，Python 读取使工作区 blocked；仅关闭 optional locks 仍 RED，另关闭 diff 自动刷新后 GREEN |
| 原 diff 输出 | `python -m pytest -q tests/test_gitdiff.py`：1 pass，exit 0 |
| 目标 Memory／反思回归 | 32 pass／3 skip／0 fail，exit 0；先前被拒绝的方案失败日志保留 |
| 完整 Go 最终源码 | `go test ./... -count=1 -json`：1495 pass／71 skip／0 fail，45 测试包／13 无测试包，exit 0 |
| Go vet | `go vet ./...`，exit 0 |
| 完整 Python | `python -m pytest -q`：2483 pass／17 skip／31 warnings，540.60 秒，exit 0 |

编译／夹具准备失败、未读取 HTTP 请求体的断连观察失败、宽 context 方案
失败、仅 optional locks 的失败和实际 v7 产品失败都保留，不计为行为成功。
调试探针已移除；测试没有真实 API 调用。frontend 源码未修改，本轮未重跑
frontend tests／build；真实浏览器使用冻结且运行后核对的既有构建。

## 真实产品与计量

沿用批准的 grok-4.6／Anthropic Messages、固定 OpenSandbox 镜像和原
100,000,000-token 批次 `02cfc8d9c93975b3ea63f27cc0a3331d`。
仅 Go 获得 provider 凭据；Python 与任务容器没有该凭据。未修改用户
Codex 配置或 auth.json。执行前冻结源码、输入、二进制、前端产物和镜像 pin。

v7：实际启动 Go／Python／鉴权浏览器，准备 98 文件副本；前台在模型派发前
失败，新增模型预留为 0。该失败定位到 Context 刷新 index；原副本保留。
v8：新准备副本运行同一冻结的只读请求，前台 completed，Go 执行一次 Read，
模型回答 module `github.com/go-chi/chi/v5`、Go `1.20`。
被入口接受的运行分母为 2：v7 失败，v8 成功；不把 v7 隐去。

本轮真实 HTTP 模型尝试分母为 **3**：前台 2 次已结算、反思 1 次 unknown。

| purpose／Go call ID | 结果 |
| --- | --- |
| foreground／07e00a12f53e4cc292f802eb1ec56776 | HTTP 200；11099 input＋144 output＝11243，含 128 cached input |
| foreground／28f04a8d33824a8b969d87386c760fd6 | HTTP 200；11215 input＋68 output＝11283，含 128 cached input |
| memory_reflection／8e120bf1c8fa4984a4b1720b55a4c3d9 | 北京时间 22:36:10–22:36:38；用量 unknown，502048 预留保留 |

前台累计 22526 tokens，缓存按 provider 契约已包含在 input，不再次累加。
反思受已有三十秒期限约束，未取得可结算结果，不能推断零消耗或未扣费。
自动费用仍 unknown。上一笔用户逐笔确认的零结算只适用于原 call ID，
原 reserved／unknown／人工 settled 事实全部保留，不用于本轮反思。

实际浏览器覆盖未认证 401、未准备 409／草稿与事件不变、原旧任务显式结束、
准备成功、前台读取成功、unknown 后新消息 409／草稿与事件不变，以及
finish 409。测试源仓库的 HEAD、index 和全部 98 个文件指纹保持不变。
最终运行输入和前端产物另按冻结清单核对；不是将提交 SHA 相同视作证据。
实际 Go／Python 重启后 completed 回复仍在，原批次仍为 used 22526／
reserved 502048，finish 仍返回 409。重启不清除未知用量或替换批次。

## Trace、状态与交付

从真实 Phoenix GraphQL 后端读回前台 Trace
`1cb0dac539e843686142fac9d78d311f`：Go agent.main 为根，Python 两次 chat、
Go execute_tool Read 和失败的 memory.reflect 都关联同一根 span。
模型与工具正常 span 为 UNSET，Memory 为 ERROR；不伪造全链成功状态。
同时间窗其他 Session／fixture spans 保留在原响应中，验收仅按 Session
身份与 Trace ID 选择，未把无关 fixture spans 当作产品证据。

当前完整代码任务仍需实际编辑、测试、提案、批准应用及恢复证据。
未结算反思阻断新增模型调用；普通修复的声明范围可交付，完整 #3 不关闭。
实际浏览器在 unknown 拒绝后正确保留草稿，但消息栏仍给出笼统的会话冲突
重试提示；额度卡刷新后显示精确的 unknown／预留。这一入口恢复说明仍需完善。
Standards／Spec 源码复核均 0 未解决发现，状态 AI_REVIEWED；未声明
HUMAN_REVIEWED。重启结果、不可变收据与提交／CI 绑定按原始产物核验。

原始产物：`.scratch/reflection-runtime/` 与 `.scratch/model-live-product/`
的 v7／v8 文件；当前源码、命令、退出码、全部失败、分母和 checksum
汇入 `.scratch/reflection-runtime/receipt.json`。旧文件与旧收据保留。
