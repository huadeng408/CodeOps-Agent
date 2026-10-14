# #11 仓库 MCP 产品范围：2026-10-15

工程状态 IMPLEMENTED；VERIFIED 限于单个批准 MCP 工具的单测、集成与 Windows 产品调用。
Memory、完整 Trace 后端、多轮代码任务和完整发布分别验收，不作为本票门禁。

## 原要求与结果

| 原要求 | 实现与验证 |
| --- | --- |
| 固定身份、版本、许可和范围 | `codeops-repository` 1.0.0、Apache-2.0；同一 pinned Go EXE 的 stdio 入口，只允许 RepositoryManifest 读取三个固定依赖清单 |
| 执行侧检查参数、owner、Session、工作区与 trust | 复用 Go Task Workspace 准入、审批、Executor 和 Manager；固定 executable/schema/config checksum，拒绝额外参数、链接、其他 owner 和 pin 漂移 |
| 成功可核对，失败分类且不重复 unknown | canonical tool/call 保存 MCP binding；tool/result 保留结果。公开接缝覆盖畸形协议、参数拒绝、超时、派发后取消、断连、未知调用恢复及父 catalog 停用 |
| Registry 与受控生命周期、调用方兼容 | Go 通过正式 protobuf 的 MCPToolCatalog schema 1 传声明；Python 保留 ASK_SESSION。CLI、常规 HTTP 与子调用复用 Manager；旧 manifest adapter 保留 |
| 真实演示与 deterministic 拒绝各自标明 | 最终生产 Go／Python、真实 provider、分组 OpenSandbox 与 Chromium 演示；协议及故障 fixture 单独记录，未将 fixture 升级为真实模型证据 |

参数仅接受 `go.mod`、`pyproject.toml`、`package.json`，返回最多 1 MiB UTF-8 内容与原始字节 SHA-256。
MCP 没有命令、网络或工作区选择接口；Python 不执行本地副作用。
父服务停用或绑定变化后，工作区副本也拒绝新调用并回收进程。
未知已派发结果沿用 Go Ledger 对账边界，不恢复执行同一副作用。
稳定契约、停用方式及许可证见 [mcp-repository.md](../mcp-repository.md)。

官方 Go MCP SDK 固定 v1.7.0，tag object `25cb00203c6b693780f602ab4041c06f7f4b9570`，
commit `bc72835f62eb94d0fb484439f886b6885b075f36`。
保留其 MIT／Apache-2.0 迁移许可及文档 CC-BY-4.0 原文，不能简写为单一许可。
新链接依赖许可和 NOTICE 同时保留；SDK 的依赖解析将既有 JWT 升级到 v5.3.1，完整 Go 回归包含鉴权。

## 最终真实产品分母

执行前冻结十项场景：未鉴权拒绝、浏览器准备工作区、pinned catalog、真实模型工具请求及批准前无结果、
浏览器批准与 Go 执行、界面结果与回答来源、canonical Ledger／预算及旧记录前缀、原仓库／任务文件保护、
停止任务释放、owned 进程停止。最终 v4 为 10／10 通过。

使用已批准的现有身份 token helper；真实鉴权 middleware 返回未登录 401，不宣称注册／登录产品验收。
原 chi 仓库固定 `9b9fb55def404397748a9fc7e044efe9db1d618e`。
实际模型只请求一次 RepositoryManifest 读取隔离副本的 go.mod；批准前没有工具结果，批准 HTTP 200。
结果及最终回答的 module、Go 版本与完整 SHA-256 一致：
`1f512b7849eed7acb188046b4de09a8de6dc45edad91ea4f1e6637f83c4e0826`。
原仓库 HEAD、index、status 和 98 项文件不变，任务副本 98 项文件亦不变，没有额外 `.agent` catalog。
任务结束 HTTP 200，ActiveCalls 为 0，任务槽释放；浏览器 page errors 为 0。
owned Go／Python／MCP 进程明确终止，driver 退出 0；被终止的 Windows 子进程退出值为 1，不能当作自然成功退出。
本轮启动的既有 Docker 控制容器停回原状态，数据、volume、pin 和历史不删除。

原始产物在 `deployments/docker/.local/receipts/mcp-product-20261015/`，测试日志在
`.scratch/mcp-product-20261015/`。它们是只读审计证据；运行事实仍只写 Session Ledger。

## 失败与计量保留

四轮各保留原十项分母及全部 browser command 尝试。
v1 的 catalog 文件污染任务清单，提交 409；没有模型预留，未到达的模型／审批场景不计通过。
该生成目录按原文可恢复归档，改用 Go protobuf catalog；没有放宽 Task Inventory。
v2 产品调用和回答完成，但 driver 等待了错误的结束 URL；保留 timeout，另读回实际任务释放。
v3 三个观察命令因错误响应解包／UI locator 失败，修正后完成观察，未重复模型请求。
v4 创建会话返回实际 200，driver 错断言为 201；沿用已创建会话继续，没有重复创建或重复提交。
v1–v3 未保存未鉴权场景收据，记 not_recorded；最终 v4 保存实际 401。

三轮成功演示各有 3 次模型调用预留：2 次结算、1 次已结束反思计量 unknown。
合计 9 次预留、6 次结算、3 次 unknown，已确认增加 69,901 tokens；未知用量与原预留保留，费用未知。
原 driver 将六条 call accounting events 命名为六次 model calls；本收据以 canonical Ledger 纠正计数，原产物不改写。
所有旧 call 记录前缀保持相同，unknown 不阻塞已停止任务结束，预算仍由服务商 100,000,000-token 硬上限约束。

## 验证与审查

| 命令／范围 | 当轮结果 |
| --- | --- |
| `go test ./... -count=1 -json`，最终 sealed | 1,673 pass／78 skip／0 fail test events；45 包 pass／13 无测试；退出 0 |
| `go test ./cmd/server ./tests/go -run 'TestContinuationMCP\|TestMCP' -count=1 -json` | 42 pass／1 skip／0 fail；退出 0 |
| `go vet ./...` | 退出 0 |
| `python -m pytest -q`，本机 Python 3.12 环境 | 2,508 pass／17 skip／31 warnings；退出 0；产品 Python 使用项目 .venv |
| 后加 Registry 负向测试：`python -m pytest -q tests/test_tools.py` | 21 pass／1 warning；退出 0；仅增测试，Python 实现未再改动 |
| frontend `npm test`／`npm run build` | 12 pass／0 fail；测试、类型检查及构建退出 0 |
| 最终真实 provider／Go MCP／浏览器 v4 | 10／10 场景；driver 退出 0；不是 mock／scorer connectivity |

保留 RED／GREEN 与所有中间完整 Go 日志。父 catalog 停用回归最初确实允许 bound／unbound 旧工具调用，
修复后两项拒绝通过。Task 回归最初用相同参数碰到既有 pending-commit 对账边界，保持该规则，
改为另一 manifest 参数验证独立新调用；最终 canonical 保存一成功、一拒绝。
其中一次完整 Go 有该测试的 1 条失败事件，最终完整 Go 重新通过，不用成功子集替代它。

最终产品 base SHA `cf99cc31b0316b989fb9eedaf111776638df8fab` 加本轮实现；495 项参与运行的
源码／配置／指令／依赖及全部前端 assets 当前指纹一致，二进制 checksum 一致。
冻结后只调整 `cmd/server/mcp_test.go` 的测试场景，原指纹与当前测试指纹均保留；最终全量回归覆盖它。
纯证据、Goal 与契约说明的后续更新不参与上述运行。
完整分母、失败、命令退出码和 checksum 见 [脱敏收据](mcp-product-2026-10-15.receipt.json)。

Standards：AI_REVIEWED，0 项剩余硬问题、0 项需要调整的 smell；保留名与父 catalog 停用发现已修复。
Spec：AI_REVIEWED，0 项剩余问题；text union、派发后取消及停用回归已补齐。
两轴独立审查不等于 HUMAN_REVIEWED；本票声明范围无未解决 blocker。
任意远程 MCP、非文本结果、非 Windows bundled pin 和完整发布未验收。
