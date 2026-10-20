# CodeOps-Agent 全仓库开源复用与替代清单

调查日期：2026-10-08。源码基线：`58f42bb2b6d3529ef513bebad5ca81701cb3f531`。
状态：`DESIGNED`。这是源码调查、第一方选型和实施建议；没有安装这些候选，也没有
把测试、第三方得分或资料检索升级为本产品 runtime / 发布证据。

## 结论

按“没有本项目更优的有效证据就进入清单”的条件，Agent Loop、模型接入、Context、
记忆、工具执行、权限、会话、前端、启动交付、数据服务、可观测性和评测都需要进入
对标范围。现有验收记录未提供覆盖这些领域的当前源码、同任务、同模型预算的横向
实验，不能据此声称本项目胜过对应开源实现。领域也不存在可由星数证明的统一
“世界最佳”；下文选择有第一方源码、可用接口和明确许可的对标，给出适配性判断。

最快落地的组合是：保留 Go Harness、Session Ledger 与 LangGraph，把通用实现
交给成熟库，优先接官方 MCP Go SDK、ripgrep、官方模型 SDK、成熟 React 渲染和
编辑器部件、标准 trace/指标后端与官方 scorer。将旧执行/恢复/评测调用方收口到
同一个 Go 产品入口；先完成真实代码任务与浏览器验收，再考虑更换数据库或调度平台。

“进入清单”不等于强制替换：

| 方式 | 适用条件 | 交付动作 |
| --- | --- | --- |
| 直接用 | 已有工具/库提供所需功能 | 固定兼容版本，处理配置、许可与运行验证 |
| 薄适配 | 外部接口能承担通用实现 | 在现有 Interface 增加 Adapter，保留 Go 校验和事件提交 |
| 继续复用 | 本仓已经使用成熟开源依赖 | 升级或压薄 glue，补缺失调用方与测试，不再造一个同类模块 |
| 借行为/测试 | 上游采用 Rust/TypeScript 或事实模型不同 | 翻译事件、边界与故障契约，不搬整套执行器/存储 |
| 条件替换 | 迁移和运维成本较高或质量未比较 | 先做固定分母的对照；收益不明确则保留现有实现 |

清单的 P0/P1/P2 是实施优先级，与 Context 的 P0 目录 / P1 摘要 / P2 原文分层不同。
P0 表示先处理安全、许可、真实可用性和证据错误；P1 表示紧随其后的产品与代码任务
改造；P2 表示规模、算法或部署条件明确后再切换。

## 调查范围与证据口径

从 `git ls-files` 盘点 `cmd`、`internal`、`orchestrator`、`pkg`、`frontend/src` 与
`frontend/tests`、`proto`、`scripts`、`eval`、`deployments` 的跟踪代码/测试/配置，
按一级子模块归并为 87 个目录组、604 个文件。文件数包括对应测试；它只是范围说明，
不是代码质量指标。报告末尾给出每个目录组的清单 ID；根构建、CI、生成绑定、公开
配置、文档和许可证另列。只读取相关接口、关键调用方和测试，不声称做了逐行审计。

`eval/benchmark_data` 和语料清单只查入口/元信息，未读取答案、qrels、模型凭据或
归档/运行树。已有用户的 `AGENTS.md` 修改保留，不纳入本调查的提交。
本报告与 CSV 是静态研究产物，不构成新的 Session 或 checkpoint 事实源。

第一方核对包括 GitHub repository metadata、当前 HEAD、实际 LICENSE/NOTICE、
官方 API 文档和模型卡；`NOASSERTION` 不是无许可，也不自动等于 Apache-2.0。
当前 upstream HEAD 是调查定位，不是建议直接依赖滚动 main；落地时必须选兼容
release/tag/digest，并复核那个版本及其传递依赖许可。已有四类 blocker 的专项调查
见 [前一份组件调查](reuse-components.md)，本清单将其扩展到全仓库。

三份指定参考仓库也保留在对标范围：

| 对标 | 本地核对源码 | 当次上游 HEAD | 根许可与适配方式 |
| --- | --- | --- | --- |
| [Codex](https://github.com/openai/codex) | `624ccf794703e2d84e748fc3ef547d6191a8c0a4` | `9b738582b13c2cdbeff54af0afd04c50c3e7ba09` | [Apache-2.0](https://github.com/openai/codex/blob/624ccf794703e2d84e748fc3ef547d6191a8c0a4/LICENSE)，含独立第三方 NOTICE；借权限、工具事件、协议与恢复测试，Rust 模块不整体移植 |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | `cd5ef8148158c3a752a658978873241fdf8e2bbc` | `5badb15009ae1756c3afe0ae0cef1faafc290ccc` | [MIT](https://github.com/deepseek-ai/deepseek-harness/blob/cd5ef8148158c3a752a658978873241fdf8e2bbc/LICENSE)，vendor/native 部件独立核对；借插件、流式事件与会话投影契约 |
| [Pi](https://github.com/earendil-works/pi) | `28dcce2ba45ce4a9efeb0f5b686f0be830fd89b9` | `ce950d78f424dcaf9f5d6a03ce80ab141130eb1d` | [MIT](https://github.com/earendil-works/pi/blob/28dcce2ba45ce4a9efeb0f5b686f0be830fd89b9/LICENSE)；借 Loop、工具流、durable observation 和 compaction 测试，TypeScript 事实模型不替换 Ledger |

## 替换不改变的契约

- Go 掌管鉴权、授权、文件/Shell/Git/MCP 副作用、沙箱、进程生命周期、根 trace
  和 Ledger 的 owner/session/CAS/hash-chain；第三方 SDK 的 tools executor 不直接执行工具。
- Python 只生成策略、计划、摘要、Memory 提案和工作流控制；工具仍回调 Go。
  索引、缓存、前端 store 和 LangGraph checkpoint 是派生投影，不创建竞争事实源。
- 旧调用方先经过单向 Adapter，切换后只有一个副作用提交路径；不使用双写或
  成功子集证明迁移。不确认结果时保留 unknown/BLOCKED，重复执行不当作补偿。
- PDF 仍走 MinerU 显式 OCR，Tika 限定非 PDF Office。模型软件许可与权重许可
  分别核对；OS 库开源不使 NC 权重或商业扩展自动变成 Apache-2.0。
- 真实代码任务、200 assistant-turn、reconnect、8 Worker/200 三阶段任务/30 故障、
  40 Skills 与官方 scorer 使用现有完整门禁。组件安装和官方工具连通不替代这些收据。



## 清单总览

下表共有 **125 条**：Harness 40 条、编排与数据 40 条、产品与交付 40 条、跨接口 5 条。
同一个库可服务多个接缝；这是 125 个优化条目，不是要求安装 125 个依赖。
对应 CSV 为 [reuse-landscape.csv](reuse-landscape.csv)，包含路径、来源 URL、优先级和实施条件。

### Go Harness、工具、CLI 与协议

| ID | 优化部分与当前源码接缝 / 缺口 | 首选、备选与许可 / 平台 | 方式 / 优先级 | 落地、验收与回滚 |
| --- | --- | --- | --- | --- |
| H01 | HTTP 路由／中间件：`cmd/server/main.go`、`internal/handler`、`internal/middleware` 已用 Gin；SessionHandler 已是 canonical Workbench 的薄 transport。缺少替换路由框架能改善产品的证据。 | 继续 [Gin](https://github.com/gin-gonic/gin)，标准库 `net/http`；避免同时装 Chi/Echo。；许可/平台：Gin MIT；Go 跨平台，现有依赖。 | 继续复用、压薄 glue／P2 | 统一超时、错误映射、shutdown、路由 owner 检查；保持当前 HTTP 契约和回归。只有 router 成为测量瓶颈才比较替换，回滚不触及账本。 |
| H02 | 身份、JWT、密码：`internal/identity/actor.go`、`pkg/token/jwt.go`、`pkg/hash/bcrypt.go`、`internal/middleware/auth.go` 已有 Actor/session binding、JWT v5、bcrypt。签发 HS256，而验证只判断 HMAC 类；不应继续手写算法与 claims 校验。 | 继续 [golang-jwt/jwt](https://github.com/golang-jwt/jwt/blob/main/parser_option.go)、[x/crypto](https://github.com/golang/crypto)，用现有 `WithValidMethods` 和 claims options。；许可/平台：MIT／BSD-3-Clause；现有 Go 库。不是更换鉴权系统。 | 已装库能力复用／P0 | 先确认有效 token 契约，显式固定当前算法与必需字段；access/refresh、过期、撤销、owner、跨 Session 回归。旧 token 有迁移窗口，不静默扩大允许算法；未知身份仍拒绝。 |
| H03 | 登录扩展：`internal/handler/auth_handler.go`、`pkg/token/cookies.go`、Actor 已支撑本地账户；没有 OIDC/组织 SSO 的代码或对照证据。 | 组织部署时 [go-oidc](https://github.com/coreos/go-oidc)，已有 IdP 优先；确有自托管需求才 [Dex](https://github.com/dexidp/dex)。；许可/平台：两者 Apache-2.0；Go。IdP 是额外运维组件。 | 条件接入／P2 | Go 验证 issuer/audience/nonce/state/PKCE 后映射 Actor，不接受模型或前端自报角色。测账号注销、租户隔离、密钥轮换、IdP 故障；本地登录保留可控入口。面试本机演示不需要新增 Dex 服务。 |
| H04 | WebSocket 与一次性 ticket：`internal/handler/websocket_handler.go`、`internal/session/websocket_ticket.go` 已有 Gorilla、有限队列、cursor replay、ticket 原子消费。完整 reconnect runtime 门禁仍缺。 | 继续 [Gorilla WebSocket](https://github.com/gorilla/websocket)，复用其协议实现；本仓保留 ticket 与 Ledger replay 契约。；许可/平台：BSD-2-Clause；Go 跨平台。无需再引入消息事实库。 | 继续库复用、补运行矩阵／P0 | 保留 owner/session、同源和一次性 ticket；真实后端验证失效／重复 ticket、慢消费者、断线、游标缺口、进程重启。队列满断开并 replay，不能丢事件。回滚 transport 可保留账本全部事件。 |
| H05 | 授权与可配置规则：`internal/permission/controller.go`、`internal/permission/allowlist.go` 已有 deny 优先、actor scope、session approvals，当前规则是 tool+pattern。 | 当前规则够用就保留；复杂属性条件用 [CEL Go](https://github.com/cel-expr/cel-go)，组织 RBAC 用 [Apache Casbin](https://github.com/apache/casbin)；跨服务 policy bundle 才 [OPA](https://github.com/open-policy-agent/opa)。；许可/平台：均 Apache-2.0；CEL 当前 module 为 `cel.dev/cel-go`、Go1.23。不是三个引擎一起安装。 | 单一判定引擎条件替换／P2 | 新 evaluator 只返回允许／拒绝理由，Go 仍执行与审计。规则按版本固定、错误拒绝、deny 优先、批准按 actor/session 隔离；用现有矩阵比较全部历史调用。禁止策略引擎持有第二份可写 Session 或由 Python 直接放行。 |
| H06 | Shell/Git 风险解析：`internal/safety/analyzer.go` 用 `strings.Fields` 和字符串模式判定；`HardenedGitArgs` 已控制 hooks、pager、环境。多 shell 语法覆盖不可由字符串匹配证明。 | POSIX/Bash 解析复用 [mvdan/sh syntax](https://github.com/mvdan/sh/tree/v3.12.0/syntax)；PowerShell 用其 [原生 Parser](https://github.com/PowerShell/PowerShell)；借鉴 Codex execpolicy 的规则及测试。；许可/平台：mvdan BSD-3-Clause；v3.12.0 Go1.23 可配当前 Go1.25；当前 HEAD／v3.14.1 要 Go1.26。PowerShell MIT／.NET；Codex Apache-2.0。 | AST 解析适配，保留沙箱／P0 | 先覆盖嵌套引号、重定向、管道、subshell、env、Git `-c` 的拒绝分母；解析失败拒绝。先只导入 parser，不使用 shell interpreter 执行副作用；PowerShell 与 Bash 方言不能互相假装兼容。解析器不是沙箱，原 deny 和执行隔离保留。 |
| H07 | 工作区文件边界：`internal/tools/file_path.go` 做逐组件 Lstat/EvalSymlinks 后返回 pathname；读取/写入随后使用 pathname，检查与使用仍分两步。 | 当前 Go1.25 已可用标准库 [os.Root](https://go.dev/blog/osroot)；旧 Go 才考虑 `google/safeopen`，无需新增库。；许可/平台：Go BSD-3-Clause；`os.Root` 在 Go1.24 引入。允许根内 symlink，与当前“一切链接均拒绝”政策不同。 | 标准库直接复用／P0 | Read/Write/Edit/Notebook/Spill/restore 尽量改 root-relative handle 操作；保留既有更严格 no-symlink/reparse、ADS/设备名规则。测试 rename/link 竞态、junction、UNC、盘符、Unicode、缺失叶子；不能只换 sanitization 然后继续裸路径操作。 |
| H08 | 原子文件写入：`internal/tools/atomic_write.go`、`internal/tools/atomic_replace_windows.go` 已有 temp、Sync、替换、目录同步；其他恢复路径有独立 `os.WriteFile`。 | 复用本仓 helper＋[Go os APIs](https://pkg.go.dev/os)，参考 [Codex no-follow patch 测试](https://github.com/openai/codex/tree/main/codex-rs/apply-patch/tests/suite)。；许可/平台：Go BSD-3-Clause；Codex 测试 Apache-2.0，拷贝需保留 NOTICE／版权。 | 合并内部重复实现／P0 | 跨工具共享同一安全 root 与原子 replacement；测权限保留、空文件、进程中断、Windows 文件占用、temp 清理。不要为已有几十行 helper 引入新 framework；多文件事务仍必须用 Ledger intent/outcome，而非声称 rename 能解决所有崩溃。 |
| H09 | Undo 与 Git restore：CLI `/undo` 调 `internal/undo/manager.go` 的 ApplyEntry，其只做词法包含检查并写原内容；`internal/worktree/restore.go` 已有全量 after-content 预检与真实路径边界，`internal/session/workspace_restore.go` 记录 intent/outcome。两者仍把空 Before 与“不存在”混在一起。 | 首选复用已有 `RestoreCodeChanges`＋`RestoreFileTransitions`；对照 [Codex apply-patch no-follow](https://github.com/openai/codex/tree/main/codex-rs/apply-patch/tests/suite)，Git 工作树 API。；许可/平台：本仓 Apache-2.0；Codex Apache-2.0；Git 核心 GPL-2.0，以独立命令使用／分发时保留其许可证。 | 本仓收口＋契约补齐／P0 | 将 CLI undo 接入 owner/CAS/receipt 入口；新增明确 before/after existence 与 operation，不以空字符串猜“新文件”。先测空原文件、人工改动、链接、create/delete/rename、部分写回崩溃。旧不充分 receipt 标 unknown，不能覆盖用户新内容；回滚保留新事件不双写旧状态。 |
| H10 | 多文件 patch 与 edit：`internal/tools/edit.go` 仅 unique string replacement，`internal/tools/write.go` 单文件；没有标准化 Add/Update/Delete/Move patch batch 的生产能力。 | [Codex apply-patch](https://github.com/openai/codex/tree/main/codex-rs/apply-patch) 的 patch 语法、操作模型、场景测试；对照 Pi edit 的歧义与换行处理。；许可/平台：Codex Apache-2.0，Pi MIT；Rust／TS 原文件不直接变成 Go 可用包。 | 移植小契约／测试，Go 执行／P1 | 第一版只实现必要 patch grammar，先 parse+全量路径／前置内容校验再执行；mutation 经 Runner commit_pending/done、恢复、权限。保持现有 Edit 为单向兼容。完整代码任务比较 patch 成功率、错误修改率／token，未通过前不删原工具，也不移植全 Rust runtime。 |
| H11 | 代码文本检索：`internal/tools/grep.go` 自写 WalkDir、regexp、文件读取与 hardcoded skip list，没有 gitignore 实现。 | [ripgrep](https://github.com/BurntSushi/ripgrep) 固定二进制、argument vector、JSON 输出。；许可/平台：MIT 或 Unlicense 双许可；选 MIT。Windows/macOS/Linux 官方二进制；库主体 Rust，但无需改变 Go/Python 架构。 | 外部成熟二进制适配／P1 | 保留 Grep 请求／结果 schema，在 Go 校验 root、globs、pattern 和预算后调用；禁 `--pre` 等可执行外部 hook 参数，不让用户任意旗标。对同一仓库比较 ignore、hidden、binary、大小文件、取消、regex语义与p95；保留旧只读实现作为显式兼容路线。 |
| H12 | 目录与 glob：`internal/tools/glob.go` 对整个 workingDir WalkDir，自写递归 `**`，没有跳过 `.git`、node_modules 或 ignore；limit 在扫描结束后生效。 | 优先复用 H11 的 `rg --files`＋[doublestar](https://github.com/bmatcuk/doublestar) 匹配；备选 [fd](https://github.com/sharkdp/fd)，避免再打包第二个搜索程序。；许可/平台：doublestar MIT／pure Go；fd MIT／Apache-2.0 双许可、独立 Rust 二进制。 | 共享目录枚举，必要时替换 glob matcher／P1 | 先定义是否包含 ignored/hidden 的产品契约，再将所有目录枚举接同一预算。测试 Git ignore、嵌套模式、Windows slash、symlink、取消、限额；目录结果仍由 Go 限于工作区。回滚保留显式旧模式，不静默改变默认覆盖。 |
| H13 | 大文件 Read 与完整输出：`internal/tools/read.go` 先 `os.ReadFile` 再切行；`internal/tools/spill.go` 已有 content-addressed redacted SpillStore。有限模型 preview 不等于有限内存读取。 | 标准库 [bufio/io](https://pkg.go.dev/bufio)；继续本仓 SpillStore 和 ReadSpill。；许可/平台：Go BSD-3-Clause；无需外部库，image/PDF/notebook 仍走既有路径。 | 标准库 streaming／P1 | 文本按安全 root handle 和限额扫描需要范围，明确超长行／总字节数策略；大文件、取消、编码、CRLF、空文件、spill checksum／owner检查。image/PDF 分派保持；记录 truncated 而非默默截掉。测峰值内存／p95后再收口旧整文件路径。 |
| H14 | AST／符号摘要：`internal/session/context_envelope.go` 能提供 P0/P1/P2 接缝，现有 Go 工具只文本检索；没有树语法通用解析器生产接入。 | [tree-sitter](https://github.com/tree-sitter/tree-sitter)／[官方 Go binding](https://github.com/tree-sitter/go-tree-sitter)，先 Go+Python grammar；Go 单语言可先标准库 `go/parser`。；许可/平台：MIT；Go binding 使用 CGO，grammar 独立核验许可证，Parser/Tree/Query须 Close。 | 条件 parser adapter／P1 | 从 Go 受约束 Read 获取 pinned 内容，在摘要侧只产 P1 symbol span/引用，不执行文件。按内容 hash+grammar version 缓存投影；比较定位准确度／token与Windows编译体积。只对选定语言先接，CGO成本不值得时使用现有 Python 隔离解析、不能让 Python 任意读文件。 |
| H15 | LSP：`internal/extensions/registry.go`、`internal/tools/extension.go` 有 LSP Kind、audit和payload界限；cmd 与 internal 的生产入口没有 concrete adapter 注册，现有注册主要在测试。 | [gopls](https://github.com/golang/tools/tree/master/gopls)、[Pyright](https://github.com/microsoft/pyright)、[LSP规范](https://microsoft.github.io/language-server-protocol/)；DeepSeek `packages/lsp/tool-lsp` 有4种只读操作、UTF-16和cwd契约可复用。；许可/平台：gopls BSD-3-Clause、Pyright根 LICENSE MIT、DeepSeek MIT；LSP server依赖语言runtime。旧 `sourcegraph/go-lsp` 已归档，不作首选。 | 已有 Extension adapter 接入／P1 | 先 definition/references/hover/diagnostics，Go 管生命周期、工作区URI、位置编码、预算和审计。server发的 workspace edits/commands不能直接执行；变更仍走Go工具授权。真实Go/Python任务验证定位与修改后diagnostics，失败标 unavailable；回滚关闭adapter仍可Read/Grep。 |
| H16 | MCP协议、传输和生命周期：`internal/mcp/manager.go` 手写 stdio JSON-RPC、固定初始化 `2024-11-05`、超时 poisoned restart；`internal/mcp/protocol.go` 的内容只含 type/text。 | [官方 Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)，只替换协议/传输；保留 Manager 外部接口。；许可/平台：正在 MIT→Apache-2.0 迁移，旧未重许可贡献MIT、新贡献Apache2、文档CC-BY4；GH NOASSERTION不代表无许可。当前HEAD go.mod Go1.25；新协议具体feature按版本核对。 | SDK薄Adapter／P1 | 先stdio，再有需求才Streamable HTTP/OAuth。把已有process env scrub、cwd、schema/config pin、工具名冲突、restart drift、权限与Ledger审计围在SDK外；复跑所有MCP进程测试，增加通知/取消/分页/非text内容和旧协议协商。SDK不能自动可信任server或启用sampling副作用。 |
| H17 | 工具参数Schema：`mcp.normalizeToolDefinition` 校验schema JSON并计算pin，`callTool`直接发送arguments；没有在这层执行完整JSON Schema验证。built-in工具各自读`map[string]any`。；源码：`internal/mcp/manager.go` | H16 SDK使用的 [google/jsonschema-go](https://github.com/google/jsonschema-go)；备选 [santhosh-tekuri/jsonschema](https://github.com/santhosh-tekuri/jsonschema)。；许可/平台：MIT／Apache-2.0；google实现仅stdlib依赖；schema dialect与Go1.25兼容需按pin测试。 | 一个schema validator复用／P1 | 按 tool catalog pin 编译并缓存，不允许任意远程 `$ref` 网络请求。Go dispatch 前验证类型、必需字段、additionalProperties、尺寸；schema正确仍要业务权限与路径校验。用现有tool输入回归，未知schema拒绝／显示兼容状态，不以SDK推测通过。 |
| H18 | 插件/Extension 生命周期：`internal/extensions/registry.go` 有version、operations、audit先写、error脱敏，但生产装配与disposer/reload治理仍未展示完整运行证据。 | 继续本仓Go registry；借鉴 [Pi扩展事件契约](https://github.com/earendil-works/pi/tree/main/packages/coding-agent/src/core/extensions) 与DeepSeek工具scope／dispose；MCP优先做隔离第三方插件。；许可/平台：本仓Apache2，Pi／DeepSeek MIT；TS生态直接嵌入会增加runtime，不作为全量替换。 | 契约与场景测试复用／P1 | 明确register/version/operation/timeout/cancel/dispose，装配LSP等真adapter；emit只作Ledger投影或工具request。拒绝adapter直接使用Python/TS文件或shell sideeffect。测重复注册、关闭中调用、崩溃、权限变动、版本漂移；SDK不能替代Go审计和owner。 |
| H19 | 可执行hooks：`internal/hooks/engine.go` 的`expandCommand`把payload／metadata字符串替换进host shell，CLI `internal/cli/app.go`启动注册此路径；需明确可信hook配置与不可信参数。 | 首选复用现有Go `sandbox.Runner/StreamingRunner` 和 [Go os/exec](https://pkg.go.dev/os/exec) argument vector；Pi/DeepSeek只借鉴pre/post事件与取消契约。；许可/平台：Go BSD-3-Clause；本仓Apache2；上游参考MIT。源码风险待复现，不把它报成已验证远程漏洞。 | 本仓受控执行收口／P0 | 禁模型直接指定hook命令；可信配置命令固定，参数通过JSON stdin或明确argv，不插入shell模板。执行受workspace、timeout、env scrub、审计约束；测包含引号／换行／substitution的payload、cancel、secret输出。内存只读hook无需新执行库。 |
| H20 | Skills发现／加载：`internal/skills/manager.go`已有metadata catalog、precedence、lazy资源、invocation policy；`internal/skills/catalog.go`40个built-in主要短prompt。 | 继续现有manager；对齐 [Agent Skills规范](https://agentskills.io/specification)，移植明确允许的DeepSeek/Pi加载和资源边界测试。；许可/平台：AgentSkills代码/规范Apache2，文档CC-BY4；第三方skills逐包独立许可，不能按根repo全MIT认定。 | 标准格式与可许可技能复用／P1 | 先lint元数据／allowed-tools，再用实际工具执行40skill任务矩阵；入口有效不证明技能完成任务。保留resource root、pin、model/user invocation分离，禁止将规范validator当实际技能运行；失败分母保留。 |
| H21 | 可运行的代码任务沙箱：`internal/sandbox/docker.go`／`internal/sandbox/routing.go`已有trustRoot、只读rootfs、network none、capdrop、限制；默认`alpine:3.20`没有完整代码工具链，`/tmp noexec`影响编译后运行。 | 继续Docker/WSL Runner；复用 [devcontainers语言构建](https://github.com/devcontainers/images) 制作受约束Go/Python/Node任务镜像，而非复制其宽松运行参数。；许可/平台：images源MIT；镜像层、编译器、基础系统独立许可；官方上游不是已批准安全配置。 | 镜像配方与已有执行适配／P0 | 预装依赖／固定image digest，用隔离可写worktree及明确可执行scratch，保持network none、无host Docker socket／privileged。真实修代码→单元测试→diff→恢复链路至少覆盖三语言；容器/trace/ledger完整收据后才启用，不以“docker启动成功”算可用。 |
| H22 | 更强多租户隔离：目前Sandbox是Docker/WSL2，没有独立用户kernel隔离比较或陌生用户共享宿主部署证据。；源码：`internal/sandbox` | Linux多租户需求成立才 [gVisor](https://github.com/google/gvisor)；VM级场景再 [Firecracker](https://github.com/firecracker-microvm/firecracker)。；许可/平台：两者Apache2；gVisor Linux runtime兼容需测，Firecracker Linux/KVM与jailer；Windows本机不是drop-in。 | 条件后端／P2 | 通过既有Runner接入、不给Python宿主执行权；比较syscall/toolchain兼容、启动耗时、资源、逃逸测试。不是当前最快落地：先补H21真实任务证据。回滚到已批准Docker配置而不是native无隔离。 |
| H23 | Windows子进程树生命周期：`internal/jobs/process_windows.go`配置为空，killProcessTree／sandbox kill通过taskkill；Unix用process group。父进程崩溃后约束不由普通管道保证。 | 现有 [x/sys/windows Job Object APIs](https://pkg.go.dev/golang.org/x/sys/windows#CreateJobObject)，参考Codex windows sandbox生命周期测试；无需新process库。；许可/平台：BSD-3-Clause；当前go.mod实际replace x/sys v0.35.0，不直接升级当前Go1.26要求的HEAD。Windows Job Object不是权限沙箱。 | 原生API接入／P1 | 将子进程纳入Job、kill-on-close、句柄回收，考虑IDE/runner已有嵌套Job；容器进程终止要由Go处理对应sandbox，不仅杀docker客户端。真实双进程崩溃、取消、子孙泄漏矩阵，检查父repo不变；失败关闭能力不退回unconfined进程。 |
| H24 | 真PTY与交互：`internal/sandbox/process.go` 的 Process、`internal/jobs`目前标准stdin/stdout/stderr pipes；`Interactive`与live写入不等于TTY尺寸、SIGWINCH、terminal detection。 | Unix [creack/pty](https://github.com/creack/pty)，Windows [ConPTY](https://github.com/UserExistsError/conpty)／原生Windows API。；许可/平台：MIT；creack仅Unix，ConPTY库最近push2024、规模较小，Windows接口与toolchain先真实验证。 | 平台process adapter／P1 | PTY仅包住Go创建的受约束sandbox进程；默认pipe模式保留给批处理。测isatty、resize、Ctrl+C、Unicode、输出顺序、挂起输入、句柄/进程回收。不能用PTY库启动宿主shell绕开sandbox；未知能力报unavailable。 |
| H25 | Job输出／spill和backpressure：`internal/jobs/jobs.go`已有owner隔离、concurrency、有限输出与增量读取；SpillStore能保存脱敏完整输出。无实测并发下丢失/内存baseline。 | 继续本仓Registry和[Go io](https://pkg.go.dev/io)，参考Pi/Codex流式tool update契约；不引入第二个进程调度服务。；许可/平台：本仓Apache2，Go BSD；参考Pi MIT／CodexApache2。 | 共享bounded流＋测试复用／P1 | 保留owner、取消、limits；定义tail缓存与完整spill是否都保存、output cursor与字节数。对高吞吐／stderr交错／多job／断连／kill测试完整分母。只有存储与恢复需求达到现有接口极限才换backend，既有Ledger仍唯一事实。 |
| H26 | CLI输入、unicode、交互renderer：`internal/cli/input.go` raw x/term键盘/history/completion，`internal/cli/renderer.go`、`internal/cli/text.go`自写cell wrapping、sanitize、active row；已装graphemes/runewidth。 | 首选继续 [x/term](https://pkg.go.dev/golang.org/x/term)、[uax29](https://github.com/clipperhouse/uax29)、[go-runewidth](https://github.com/mattn/go-runewidth)；需要复杂TUI再 [Bubble Tea](https://github.com/charmbracelet/bubbletea)＋[Lip Gloss](https://github.com/charmbracelet/lipgloss)。；许可/平台：BSD／MIT；BubbleTea当前v2 import为charm.land，不能照搬v1教程。pipe transcript契约仍需保留。 | 已装库继续／TUI条件替换P2 | 只有功能要求多pane/menus时替换presentation，Command/Runner不动；reuse Pi virtual-terminal fixtures。比较窄宽窗口、CJK/emoji/组合字符、paste、interrupt、非TTY、screenreader；渲染器切换不能改变工具授权或输出sanitization。 |
| H27 | CLI Markdown和代码可读性：StreamRenderer当前PrintAssistant/raw文本与自写line wrapping，没有成熟Markdown renderer接入。；源码：`internal/cli/renderer.go` | [Glamour](https://github.com/charmbracelet/glamour)，仍用现有纯文本renderer作pipe路线；不要为了高亮迁移到TS terminal。；许可/平台：MIT；额外Go依赖，流式增量render成本需测。 | presentation library／P2 | 在明确终态／block边界渲染markdown，width/style受控，不执行HTML链接/命令；先sanitize不可信terminal controls，生成合法样式另处理。测fence、长行、中文、200轮输出性能／复制，非TTY保持稳定纯文本；回滚仅renderer。 |
| H28 | CLI命令启动契约：`cmd/agent/main.go`只是Load→Run，slash commands在`internal/cli/app.go`自写分支；help/completion/scripted subcommands未统一。 | 当前小入口可先Go [flag](https://pkg.go.dev/flag)；确需`run/session/doctor`一致命令树才 [Cobra](https://github.com/spf13/cobra)。；许可/平台：flag BSD、CobraApache2；都是Go跨平台。 | stdlib first／Cobra条件P2 | 保留slash交互；非交互入口复用同一Runner与Workbench，明确定义stdin、JSON、退出码、不TTY。脚本/launcher调用回归、help和参数错误验证；不能另造一套Agent Loop或Session。 |
| H29 | Ledger持久化driver：`internal/session/eventlog.go`已用SQL事务+BEGIN IMMEDIATE+CASCHECK，`internal/session/store.go`使用modernc；WindowsRows泄漏刚由v1.40.1修复。 | 继续 [modernc/sqlite v1.40.1](https://pkg.go.dev/modernc.org/sqlite@v1.40.1)；真实多主服务场景才 [pgx/Postgres](https://github.com/jackc/pgx)。；许可/平台：moderncBSD-3-Clause、无CGO；pgxMIT/pureGo＋Postgres服务。不能因vector扩展切换Ledger driver。 | 现有库继续／远程数据库条件P2 | 在EventLog接口内保持原子expectedSeq、hash chain、owner、fork/rewind、close/cancel。先测锁等待／IO瓶颈才决定Postgres；按一次导入切换，不双写。对旧DB校验全量序列与checksum，备份和可回放旧软件才进入迁移。 |
| H30 | Ledger投影与查询效率：`internal/session/eventstore.go`、`internal/session/ledger_snapshot.go`有一次immutable verified read；List/Load/runner等仍有全账本读取／Verify，历史变长需要测p95。 | 继续 [SQLite官方查询/索引能力](https://www.sqlite.org/queryplanner.html)、现有EventsAfter和LedgerSnapshot；借鉴Codex/Pi history projection tests，避免引入新event store。；许可/平台：SQLite公共领域，Go driverBSD；参考CodexAP2/PiMIT。 | 已有接口／投影优化P1 | 先profile真实200轮事件数、页大小、内存和重复SQL；物化索引/缓存必须带Ledger head checksum/seq，可完全重建。读投影滞后显示status，CAS仍取canonical头；compaction不删除原历史。回滚删缓存重放，不迁移事实到投影。 |
| H31 | Payload规范化与hash兼容：`internal/session/eventlog.go` 的 normalizeSnapshot unmarshal到any→marshal，checksumEvent hashGo存的payload字节；不可凭Python JSON等价假设跨语言canonical一致。 | 首选已有 [encoding/json Decoder.UseNumber](https://pkg.go.dev/encoding/json#Decoder.UseNumber) 与Go唯一规范化；需要正式跨语言signature时再指定RFC8785实现，不引入experimental JSON替换。；许可/平台：GoBSD；`go-json-experiment/json`也是BSD但experimental、不能默认换Ledgerhash规则。 | 标准库小修／版本化契约P1 | 比较大整数、Unicode、重复keys、float、NaN、metadata schema；hash只由Go生成，checkpoint引用不重算。hash变更新增schema版本／单向导入，新旧event算法并行读取而非重写历史；损坏拒绝。缺需求不做全仓JSON框架迁移。 |
| H32 | Go/Python协议与生成：`proto/codeagent/orchestrator.proto`、`internal/orchestrator/client.go`已有grpc-go/protobuf；legacyHTTP在`pkg/orchestrator`另有DTO。 | 继续 [grpc-go](https://github.com/grpc/grpc-go)、[protobuf-go](https://github.com/protocolbuffers/protobuf-go)；[Buf CLI](https://github.com/bufbuild/buf) lint/breaking/local generation。；许可/平台：grpc/BufApache2，protobufGoBSD；Buf CLI开源能力不要求付费BSR；没有必要重写成Connect。 | 已装库+schema工具P1 | 从唯一proto生成两侧，固定generator版本，CI做breaking对当前base。协议actor/schema/version/traceContext明确；remote transport配置TLS/mTLS，local insecure仅明示受限本地边界。旧客户端和正常turn/resume/stream取消回归，不用新RPC框架洗掉兼容责任。 |
| H33 | 恢复、重试、错误分类：`internal/recovery/error_recovery.go`按字符串permission/errorCount选策略；`internal/orchestrator/client.go`已识别grpc status但还fallback字符串；supervisor已有boundedbackoff。 | Go [errors.Is/As](https://pkg.go.dev/errors)、[grpc codes/status](https://github.com/grpc/grpc-go/tree/master/status)；重复backoff实现多了才 [cenkalti/backoff](https://github.com/cenkalti/backoff)。；许可/平台：GoBSD、gRPCApache2、backoffMIT；不因重试库默认值改变relay1–10或429上限。 | typed errors与已有重试复用／P0 | Provider auth/validation/integrity/cancel/transport/unknown区分。只有无副作用读或有幂等key请求可retry；mutating unknown保留对账，不能见errorCount就换model重跑。provider unavailable不伪装providererror；完整fault矩阵后再统一重复helper。 |
| H34 | 两套Go→Python桥接：`internal/orchestrator/client.go`是versionedgRPC的Harness回调；`pkg/orchestrator/client.go`、`pkg/orchestrator/memory_client.go`仍有HTTPDTO与legacyRAG桥。 | 首选复用现有versioned [gRPC](https://grpc.io/docs/languages/go/) 与本仓adapter，不引入第三套RPC；老HTTP only one-way compatibility。；许可/平台：gRPCApache2；产品状态不能由HTTPRAG快照绕开Ledger。 | 合并边界、保留一次向Adapter／P1 | 先全量列HTTP调用方与业务数据领域，确认哪个是知识库非Session。只将Session操作收口，RAG独立文档事实可保留专用API。CLI/HTTP/MCP每条调用真实E2E前不能删除旧入口；禁止双写‘暂兼容’。 |
| H35 | 配置与本地secret存取：`internal/config/loader.go`有settings/environment，`internal/serverconfig/config.go`已有Viper；部分模型缺省、超时、sandbox配置分散。 | 已装 [Viper](https://github.com/spf13/viper)＋stdlib typed validation；需要用户“保存密钥”时 [go-keyring](https://github.com/zalando/go-keyring)，secret默认仍环境/批准manager。；许可/平台：Viper/keyringMIT；keyringWindowsCredentialStore、macOSsecurity、Linux/BSDSecretService；无GUI/DBus运行需显式unavailable。 | 统一typed配置／keyring条件P1 | 一个已验证config snapshot投影到CLI/HTTP，固定优先级；keyring由用户显式导入，Go取出只注入必要provider进程环境，日志不含值。sharedsecretconstanttime比对、missingfailclosed、env scrub、reload/cancel回归；不自动扫桌面secret文件，不保存在session/fixture。 |
| H36 | 领域model与repository：`internal/model`、`internal/repository`及`pkg/database`已用GORM/MySQL；auth/knowledge/corpus不能因Ledger“唯一Session事实”而全删。Objectpath/tasks只是领域DTO与存储key。 | 继续 [GORM](https://github.com/go-gorm/gorm)、stdlib SQL；已有 [minio-go SDK](https://github.com/minio/minio-go)可继续，服务器license另核验。schema迁移交主报告Goose项。；许可/平台：GORMMIT，minio-goApache2（不等于MinIOserverApache2）；sqlxMIT未见必要，不为换ORM加一层。 | 已有库继续、重复glue收口P2 | 每个repository检查context、权限过滤、分页、transaction，首选补当前实现；数据库/对象key仍校验owner/path。性能计量表明ORM瓶颈才局部SQL/pgx，不整体改存储。对外DTO保持version，迁移备份校验，不直接删除业务表。 |
| H37 | WebFetch的SSRF与正文：`internal/tools/safe_url.go`先LookupIPAddr检查，再DialContext(host)重新解析，源码不支持注释所宣称完全防DNSrebind；`internal/tools/webfetch.go`只读64KiB原body，没有正文抽取。 | 首选Go [net/http Transport](https://pkg.go.dev/net/http#Transport)改为validatedIP拨号；确需正文抽取参考 [Mozilla Readability](https://github.com/mozilla/readability)。；许可/平台：GoBSD；ReadabilityApache2，JS需DOM/Node或Go移植；`go-shiori/go-readability`MIT但已归档，后继Readeck许可fetch受阻未核实，不推荐直接装。 | stdlib边界小修／抽取条件P0+P2 | 优先复现DNS两次解析、IPv4/6、redirect、公网转私网；使用已校验IP拨号保持Host/SNI，考虑多IP失败/代理策略。正文抽取仅解析Go已安全取回的字节，不让库FromURL另发请求；不执行页面script。受阻/截断标明，不能静默返回‘成功完整页面’。 |
| H38 | WebSearch真实搜索能力：`internal/tools/websearch.go`是DuckDuckGo instant-answer JSON格式，只取Abstract/RelatedTopics，不等同通用网页搜索结果。 | 可接 [SearXNG HTTP Search API](https://docs.searxng.org/dev/search_api.html)；保留Go WebSearch权限/出站边界，不把搜索服务当工具执行授权器。；许可/平台：SearXNGAGPL-3.0服务器、Python；源码分发/修改服务的许可边界独立，不能复制进Apache2代码声称全许可统一。 | 可选HTTPbackend adapter／P1 | 定义SearchHit(url,title,snippet,source)，owner/session审计；自托管JSON需settings显式启用，publicinstance可能403/限流；Go同时限制结果、timeout和来源。若服务在loopback/private，另设批准并固定的服务endpoint client，不能打开模型任意WebFetch的private访问。比较真实代码文档查找quality而非只API200；失败不伪装空正确答案。 |
| H39 | Trace、日志、成本指标：`internal/telemetry/genai`已有OTel与脱敏，`pkg/log`是zapwrapper；`internal/metrics/collector.go`只有4模型静态价格，未知modelmap零值算$0，没有price版本/usage对照。 | 继续 [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go)＋[zap](https://github.com/uber-go/zap)；复用编排modelregistry／provider usage与Pi modelmetadata契约，tracebackend由主报告确定。；许可/平台：OTelApache2、zapMIT、PiMIT；provider价格是需按版本核验的数据，不能从repoHEAD推断当前账单。 | 已装库继续／metric投影P1 | 一个roottrace传播Go/Python/tool，失败export/readback分级；统一typedredaction覆盖日志/adapter/error。price未知显示unknown／估算，并在预算需确认时failclosed，cachedtoken与modelalias有pin。真实计费用量/trace join再验收，不用静态$0绕过cost限额。 |
| H40 | Parser、事件、恢复边界测试：当前目标目录看到大量table/integration测试，未发现`Fuzz`/`Benchmark`函数；完整provider故障和200轮门禁仍在GOAL缺口。；源码：`internal/tools` | [Go原生fuzz](https://go.dev/doc/tutorial/fuzz)、race/benchmark；移植Codex patch no-follow、Pi loop／terminal／工具恢复、DeepSeek LSP／scope场景，复用现有进程E2E。；许可/平台：GoBSD；参考fixture代码CodexAP2，Pi/DeepSeekMIT、每文件依赖/NOTICE核验；不复制gold/评测答案。 | 测试轮子／P1 | 重点shell/path/schema/eventcodec/restore状态机不变量，保存counterexample；任意sequence必须拒绝owner逃逸、CAS倒退、unknown副作用重跑。加真实CLI/HTTP fault和Win/Linux matrix，分清fixture与provider-backed，测试/benchmark不能单独晋升releaseVERIFIED。 |

### Python 编排、Context、Memory、RAG 与数据

| ID | 优化部分与当前源码接缝 / 缺口 | 首选、备选与许可 / 平台 | 方式 / 优先级 | 落地、验收与回滚 |
| --- | --- | --- | --- | --- |
| O01 | `orchestrator/llm/client.py`、`orchestrator/llm/providers/openai.py`、`orchestrator/llm/providers/anthropic.py`、`orchestrator/llm/providers/local.py`：自写 urllib 请求、SSE、重试、取消和响应归一化；缺口：手写传输维护成本；取消时 caller 可返回而 daemon HTTP worker 等超时；无与 SDK 的中断/吞吐对照 | OpenAI Python SDK、Anthropic SDK；保留本地兼容 client；许可/平台：薄适配既有 `LLMClient/ChatRequest/StreamDelta`；Apache-2.0 / MIT；Python、Windows 可用；来源：[OD01](https://github.com/openai/openai-python)、[OD02](https://github.com/anthropics/anthropic-sdk-python) | P1；薄适配既有 `LLMClient/ChatRequest/StreamDelta` | 落地：先替换一个 provider 的 HTTP/SSE 部分，保持 tool IDs、thinking、多模态、identity 和脱敏；外层与 SDK 只允许一层重试；验收：`test_llm_providers.py` 全部协议边界，真实取消、半截 SSE、429、identity、Go/Python trace；禁止重复工具提交；回滚：按 provider 开关回旧 adapter，Ledger/工具协议不动 |
| O02 | `orchestrator/llm/providers/local.py` 已接受无 key 的 OpenAI-compatible endpoint；缺口：本机未证明模型能做工具、JSON 反思和长上下文；无需云 key 并不等于无需资源与身份 pin | 沿用 Ollama；llama.cpp 为资源受限候选，vLLM 为服务器吞吐候选；许可/平台：直接外部服务；MIT / MIT / Apache-2.0，权重另核；vLLM 需按支持矩阵选 Linux/WSL/容器，不能承诺 Windows 原生；来源：[OD03](https://github.com/ollama/ollama)、[OD04](https://github.com/ggml-org/llama.cpp)、[OD05](https://github.com/vllm-project/vllm) | P0；直接外部服务 | 落地：最快先用已有 `LocalClient` 加模型 digest 身份与 local E2E 路线；只选实际硬件可运行的一种服务；验收：真推理完成代码任务、工具调用、反思和重启召回；模型 digest、维度/工具能力、完整失败分母；回滚：选回云 provider；保留 local 失败收据，不能将分数归给云模型 |
| O03 | `orchestrator/llm/router.py` 已有 exact route、model catalog、PreparedRoute generation；缺口：不需要再写多 provider gateway；目录字段与能力仍依靠手工 catalog，无路由覆盖/故障对照 | 保留当前 router；LiteLLM OSS SDK 的 provider normalization/model catalog 作有条件候选；许可/平台：继续复用/选择性适配；LiteLLM 非 enterprise 部分 MIT，enterprise 另许可；不默认起代理服务；来源：[OD06](https://github.com/BerriAI/litellm/blob/d8c0e2c7153d82234ec46f0231bf9b9714e7d52a/LICENSE) | P2；继续复用/选择性适配 | 落地：仅当接入很多新 provider 才复用 catalog/归一化；prepared generation、credential 隔离留在项目；验收：provider/model 替换后旧 prepared route 不漂移，未知能力 fail-closed，错误/用量身份不丢；回滚：切回当前注册表，禁用新 catalog 源 |
| O04 | `orchestrator/llm/client.py:assess_complexity` 以英文正则和空格词数决定 fast/main；`orchestrator/recovery/error_recovery.py` 根据错误数切模型；缺口：中文代码任务、质量损失、重试成本无 held-out 对照；错误数量不是安全重试证据 | 现有精确 router + 官方 SDK 稳定错误类型；LiteLLM 路由统计仅作候选；许可/平台：缩减策略、继续复用；库许可见 O01/O03；不引入另一个 Agent 框架；来源：[OD01](https://github.com/openai/openai-python)、[OD02](https://github.com/anthropics/anthropic-sdk-python)、[OD06](https://github.com/BerriAI/litellm/blob/d8c0e2c7153d82234ec46f0231bf9b9714e7d52a/LICENSE) | P1；缩减策略、继续复用 | 落地：未验证任务保守选 main 或用户所选；把可恢复传输错误和未知副作用分开；切模型记录具体原因和身份；验收：中英同任务成功率/延迟/真实 token 与价格对照；未知 tool outcome 禁止盲重放；回滚：关闭自动 fast/fallback，用明确模型固定路由 |
| O05 | `orchestrator/context/budget.py`、`orchestrator/workflows/engine.py` 的并发上限是对象内状态；`orchestrator/llm/client.py` 已有有界重试；缺口：对所有进程、Worker、辅助压缩/反思的 relay 全局 1–10 并发缺新鲜证明；SDK 加外层可造成重试叠乘 | 先复用现有 Go jobs/任务门控、router、TokenBudget；需要跨进程时用已有 Redis/Valkey lease 或 Go 协调许可；许可/平台：继续复用；stdlib semaphore 本身不是全局门控；租约仅派生投影，授权仍 Go；来源：[OD01](https://github.com/openai/openai-python)、[OD02](https://github.com/anthropics/anthropic-sdk-python)、[OD07](https://github.com/redis/go-redis) | P0；继续复用 | 落地：统一模型调用入口取得 Go 发放的并发 permit；取消、超时释放；选唯一有界退避层，辅助调用同样计数；验收：同时启动多 Go/Python 进程+8 Worker+反思/compaction，aggregate peak≤10，429 可恢复或 BLOCKED；记录完整分母；回滚：恢复单进程/低并发运行，关闭额外 Worker；不放宽上限 |
| O06 | `orchestrator/memory/reflection.py` 手工严格 JSON 与 provenance；`orchestrator/rag/graph.py`、`orchestrator/rag/memory_tasks.py` 还有正则提取 JSON；缺口：结构化失败率/重试成本无对照；不同业务各写 JSON parsing | 首选已安装 Pydantic；需要模型级 typed output 时选 Instructor 或 PydanticAI 的输出解析部分；许可/平台：继续复用/薄适配；MIT；不直接用它们执行文件/Shell 或新增存储；来源：[OD08](https://github.com/pydantic/pydantic)、[OD09](https://github.com/567-labs/instructor)、[OD10](https://github.com/pydantic/pydantic-ai) | P1；继续复用/薄适配 | 落地：把一个现有 payload 验证集中到同一 schema，保持 Go source IDs/CAS 复核；严格禁止模型额外工具；验收：非法字段、长度、外源 IDs、credentials、cancel 均拒绝；真实 structured-output 成功/失败分母；回滚：恢复原解析器，保持 wire schema；不自动接受以前拒绝的结果 |
| O07 | `orchestrator/runtime/agent_loop.py`、`orchestrator/runtime/hooks.py`、`orchestrator/runtime/conversation.py` 已有 prepare_context/before_model/before_tool/finish_turn；缺口：Loop、Hook、Extension 多种 registry 可重复治理；没有证明整体换框架有收益 | 现有扩展点；借 DeepAgents / LangChain middleware 的规划、摘要、tool-result offload 策略；许可/平台：继续复用+选择性借鉴；MIT；最新依赖不兼容，先固定兼容版本/摘取明确许可策略；来源：[OD11](https://github.com/langchain-ai/deepagents)、[OD12](https://github.com/langchain-ai/langchain) | P1；继续复用+选择性借鉴 | 落地：每个策略只接现有 phase；工具参数/最终副作用仍 Go 审核；先合并重复 metadata/错误处理，避免第四 registry；验收：插件顺序、blocked、取消、边界、脱敏回归 + 真实代码任务；新策略不能产生 Go 未审计副作用；回滚：禁用一个策略注册项；既有 loop 继续运行 |
| O08 | `orchestrator/graph/main_graph.py`、`orchestrator/graph/sub_agent_graph.py` 已用 StateGraph/SqliteSaver；checkpoint 绑定 Ledger seq/checksum；缺口：已有成熟持久化依赖，缺当前版本完整重启/恢复证据；升级主版本并不能替代验收 | 继续 LangGraph/checkpoint-sqlite；多机需要才看 checkpoint-postgres；许可/平台：继续复用；MIT；checkpoint 仅工作流投影，不能把 LangGraph Store 当第二 Session 事实源；来源：[OD13](https://github.com/langchain-ai/langgraph) | P0；继续复用 | 落地：沿现有图完成暂停、恢复、取消、CAS；单独验证依赖升级/序列化兼容，不先换 scheduler；验收：正常新 turn 与 continuation 区分、跨进程恢复、异 owner/run/checksum 拒绝；8/200/30 分母；回滚：回旧 pinned 依赖与读兼容 adapter，保留旧投影/事实，不双写 |
| O09 | `orchestrator/workflows/engine.py`、`orchestrator/workflows/store.py`、`orchestrator/workflows/providers.py`、`orchestrator/workflows/models.py` 已有 DAG、SQLite lease/CAS、重试与 heartbeat；缺口：Worker 当前文字任务与代码工具 Worker 边界不同；无真实规模/租约失效矩阵；自写调度跨机器维护成本未测 | 首选现有 LangGraph/WorkflowEngine；Temporal 为跨机/长期调度触发候选；许可/平台：继续复用；Temporal server/SDK MIT；新服务条件，执行 history 不取代 Ledger；来源：[OD13](https://github.com/langchain-ai/langgraph)、[OD14](https://github.com/temporalio/temporal) | P1；继续复用 | 落地：先补现有 Worker 的取消、租约、模型总并发和真实规模；只有持久化跨机需求再把 Activity 接 Go 已有任务 ID；验收：8 Worker/200长任务/30故障完整分母，lease 丢失停止执行，重试不重复副作用，进程重启可对账；回滚：固定回单机 SQLite 调度，已提交 Ledger 事实继续有效 |
| O10 | `orchestrator/agents/deep_agent.py`、`orchestrator/agents/process.py`、`orchestrator/agents/worker.py` 独立兼容分支；生产 `SpawnAgent` 走 Go Harness；缺口：deterministic compatibility report 不能证明模型子 Agent；Python显式create-file逻辑不宜扩成第二执行器 | Go 子 Session/managed worktree + LangGraph subagent 图；借 DeepAgents isolated-context 策略；许可/平台：继续复用、退役冗余兼容能力；MIT 策略仅辅助，不能直接启用 filesystem/shell backend；来源：[OD11](https://github.com/langchain-ai/deepagents)、[OD13](https://github.com/langchain-ai/langgraph) | P1；继续复用、退役冗余兼容能力 | 落地：真实 child 运行同一模型/工具协议；兼容入口逐调用者改 Go RPC；旧接口单向 adapter，不新增 Python 本地动作；验收：父子 context/owner 隔离、worktree/artifact source pin、真实子代码任务、终态与重启回收；回滚：关新 subagent 策略，原 Go 子任务可继续；兼容入口明确范围而非冒充能力 |
| O11 | `orchestrator/context/memory.py:LayeredContext` 的 P1 只有 size/lines/hash；生产 Go ContextEnvelope 同样缺 AST 摘要；缺口：三层名字已有但 P1 不提供 symbol/relation，缺大仓库召回与 token 对照 | Aider repo-map 思路 + tree-sitter；语言语义用已有 LSP 扩展、python-lsp-server；许可/平台：借算法/薄适配；Apache-2.0 / MIT / MIT；grammar 和 language server 另核，Go 只交授权快照给 Python；来源：[OD15](https://aider.chat/docs/repomap.html)、[OD16](https://github.com/tree-sitter/tree-sitter)、[OD17](https://github.com/python-lsp/python-lsp-server) | P1；借算法/薄适配 | 落地：P0 Git文件清单，P1 symbol/signature/reference＋checksum cache，P2只请求Go Read；先做 Go/Python 两种语言；验收：多文件定位/改代码任务，对照成功率、输入tokens、索引延迟；symlink/跨工作区/删除文件拒绝；回滚：关闭 AST cache，退回 hash 概览与词法搜索；缓存可重建 |
| O12 | `orchestrator/context/compactor.py:estimate_tokens` bytes/4；`orchestrator/rag/chunking.py` whitespace-v1；`TokenBudget` 只累加输入指标；缺口：中文、代码、工具 schema token 偏差未知；预算/价格不是模型 tokenizer 能力 | tiktoken 对兼容 tokenizer；HF tokenizers 对本地模型；保留保守估算 fallback；许可/平台：直接库+model-aware 适配；MIT / Apache-2.0；tokenizer 文件/model revision 需固定；来源：[OD18](https://github.com/openai/tiktoken)、[OD19](https://github.com/huggingface/tokenizers) | P1；直接库+model-aware 适配 | 落地：统一 token counting seam，输出 `estimated`、`reported`、`tokenizer_id`；计数包含 tool JSON 与 provider framing，费用来自可靠价表/真实用量；验收：同内容中英/代码/schema计数误差对照；context overflow及压缩恢复；未支持模型不伪造精确账单；回滚：回保守估算，并明确标 estimated，保留原压力阈值 |
| O13 | `orchestrator/context/compactor.py`、`orchestrator/context/compaction.py` 现有 tool adjacency、range selection、LLM summary 与 deterministic fallback；缺口：200轮真实压缩语义保持无新鲜证据；完整summary成本未知 | 沿现有实现；借 LangChain trim/summarization middleware、DeepAgents offload 策略；LangMem 摘要提取可选；许可/平台：继续复用/局部策略；MIT；不可删除 Ledger原文或丢tool pairing/source events；来源：[OD11](https://github.com/langchain-ai/deepagents)、[OD12](https://github.com/langchain-ai/langchain)、[OD20](https://github.com/langchain-ai/langmem) | P0；继续复用/局部策略 | 落地：先同任务跑完整压缩链；只换一个摘要策略/输出 offload，内容经Go保存受控artifact，Python只持reference；验收：200assistant-turn，前后任务/决策/文件/失败保留率、真实token成本、pairing、重启surface一致；回滚：恢复原摘要策略；Ledger原文仍可重新构造surface |
| O14 | `orchestrator/prompts/system.py`、`orchestrator/prompts/project.py`、`orchestrator/config/env.py`、`orchestrator/config/provider_file.py`、RAG config 有 template/环境读取多条路径；缺口：没有证据复杂模板框架优于6个固定replace；手写env解析和profile规则可能漂移 | 首选现有模板/stdlib与已安装 Pydantic；Pydantic Settings 仅复杂配置增长时考虑；许可/平台：合并已有实现/有条件库；MIT；不能引入动态模板执行或导出秘密值；来源：[OD08](https://github.com/pydantic/pydantic)、[OD21](https://github.com/pydantic/pydantic-settings) | P2；合并已有实现/有条件库 | 落地：统一公开配置schema、优先级与校验；prompt保持稳定cacheable前缀，动态memory/session后置；先删重复parse而非加模板引擎；验收：env/profile优先级、未知字段、model routing、credentials脱敏；cache命中用provider reported指标；回滚：保留一次性旧config读adapter；当前publickeys保持兼容 |
| O15 | `orchestrator/skills/manager.py`、`orchestrator/skills/catalog.py`、`orchestrator/skills/init_skill.py`、`orchestrator/skills/review_skill.py`、`orchestrator/skills/security_skill.py` discovery/lazy load/manifest 已实现；缺口：40 Skills格式、选择质量、授权和运行矩阵不同；catalog短prompt不是成熟可执行skill证据 | Agent Skills标准与已核许可样例，沿现有 SkillManager；许可/平台：继续复用；规范/代码 Apache-2.0、文档CC-BY-4.0；格式 validator 仅开发，不换生产registry；来源：[OD22](https://agentskills.io/specification) | P1；继续复用 | 落地：少量同许可skills进入当前catalog，fixed source revision、metadata→body→resources；所有scripts经Go授权；验收：40Skills完整矩阵与选择分母、lazy budgets、symlink/path、错误恢复；格式pass不当runtime；回滚：退回catalog revision、停止新增skill注册 |
| O16 | `orchestrator/runtime/tools.py`、`orchestrator/runtime/extensions.py` Go manifest mirror；extensions支持attachment/code_runtime/lsp；缺口：不需要额外 Python MCP执行客户端；标准JSON schema可减少重复参数转换 | 继续版本化 protobuf/JSON schema 和 Go MCP执行；langchain-mcp-adapters仅归档参考，排除新依赖；许可/平台：继续复用/明确排除；旧adapter MIT且archived；LSP语义用现有扩展；来源：[OD23](https://github.com/langchain-ai/langchain-mcp-adapters)、[OD17](https://github.com/python-lsp/python-lsp-server) | P1；继续复用/明确排除 | 落地：Python仅catalog/参数建议，Go负责鉴权、server pin、路径、执行和审计；消除registry重复，协议先改proto；验收：实际Go MCP/LSP请求、恶意参数、预算、取消、manifest签名/版本、重启pin；回滚：关扩展注册；Go默认工具仍可用 |
| O17 | `orchestrator/memory/reflection.py` + `internal/memory/evolution.go` 提取候选再Go验证CAS；缺口：真实 `memory/commit-blocked` 原因尚需当前trace；不应因库替换隐藏失败 | 先完成现有链；LangMem无存储 `create_memory_manager` 提取策略候选；许可/平台：局部适配；MIT；禁止 `create_memory_store_manager` 直接写BaseStore作为事实库；来源：[OD20](https://github.com/langchain-ai/langmem) | P0；局部适配 | 落地：区分provider/schema/source/取消/GoCAS失败分类并脱敏；同约束比较提取质量，再决定是否接LangMem；验收：真代码轨迹反思写回、source IDs/checksum、重启召回、provider identity、无tools、当前SHA收据；回滚：回原extractor；保留Go已提交事实；失败不删除 |
| O18 | `internal/memory/ledger.go:RecallWithOptions` 当前词法投影；RAG另有ESdense检索；缺口：无同任务语义/预算对照，不能以“已有RAG向量”声称GoLedger记忆已向量化 | 先复用已运行ES的独立派生记忆索引；纯开放后端看Qdrant官方Go client；已有Postgres时pgvector；sqlite-vec小型候选；许可/平台：派生索引adapter；ES distribution ELv2，QdrantApache-2.0，pgvectorPostgreSQL；sqlite-vec有CGO/WASM路径不直接兼容modernc；新服务条件；来源：[OD24](https://github.com/qdrant/qdrant)、[OD25](https://github.com/qdrant/go-client)、[OD26](https://github.com/pgvector/pgvector/blob/f37c13f68b57d2c3472b2214fbcff699d6d34876/LICENSE)、[OD27](https://github.com/asg017/sqlite-vec) | P1；派生索引adapter | 落地：owner/workspace过滤＋memory ID/revision/source checksum、tombstone/expiry校验；index从Ledger重建；不改Ledger驱动；验收：同任务词法/语义/RRF召回与输入token、跨owner拒绝、失效缓存不召回、删除/重建/离线fallback；回滚：停用语义索引，回词法；保留权威Ledger，派生库可重建 |
| O19 | `orchestrator/context/memory.py`、`orchestrator/context/backends.py`、`orchestrator/memory/manager.py`；SQLite/Redis/MySQL兼容store与文件memory；缺口：已核Go托管禁用LayeredContext；独立入口是否仍需写事实要按调用者说明，不能用目录并存判双写 | Go Ledger单向兼容adapter＋已有LangGraph投影；无外部“Memory产品”能替代项目owner/CAS契约；许可/平台：收口/只读导入/退役候选；标准库/LangGraph；禁止双权威写入；来源：[OD13](https://github.com/langchain-ai/langgraph) | P1；收口/只读导入/退役候选 | 落地：列standalone/test/legacy调用者；公开产品统一到Go RPC；旧数据只读迁移，freeze独立写入口；不要盲删存量memory；验收：覆盖全部公开调用者的跨进程真E2E、可回读迁移记录、owner隔离、断电/取消，旧写入口不再被调用；回滚：保留只读旧库与adapter；恢复旧读路径，不恢复双写 |
| O20 | `orchestrator/identity.py`、`orchestrator/session_control.py`、`orchestrator/runtime/session_ops.py`、`orchestrator/todo/manager.py`、`orchestrator/todo/state.py` 自定义typed identity/状态/CAS/FIFO；缺口：它们承载本项目契约，不能用通用库取代Go授权；复杂状态解析可借已安装schema工具 | 保留stdlib dataclass/Condition与protobuf；重复wire校验可局部复用Pydantic；LangGraph interrupt/resume仅策略；许可/平台：继续复用/少量合并；MIT/stdlib；不新增独立session库或Redis权威锁；来源：[OD08](https://github.com/pydantic/pydantic)、[OD13](https://github.com/langchain-ai/langgraph) | P0；继续复用/少量合并 | 落地：actor/session最终校验Go；plan/todo只派生Go版本；Python错误分类稳定，取消/compact互斥保持；验收：现有actor/sessioncontrol/plan/todo tests＋真实并发RPC、过期revision/异Session/取消矩阵；回滚：保留原wire、关闭新策略；GoCAS继续兜底 |
| O21 | `orchestrator/security/injection.py` 5个英文正则给工具输出加warning；缺口：正则命中不是可靠防注入；中文/混淆/普通system文本误报未对照 | 现有untrusted framing＋Go约束为前置；NeMo Guardrails检测/策略仅选做；LLM Guard排除新依赖；许可/平台：候选校准、仅advisory；NeMoApache-2.0、模型另核；LLM GuardMIT已archived；来源：[OD28](https://github.com/NVIDIA-NeMo/Guardrails/blob/9f793de53e432c4c9c765975f5dd54df175fcb6e/LICENSE.md)、[OD29](https://github.com/protectai/llm-guard) | P2；候选校准、仅advisory | 落地：先用实际攻击/正常代码样本测误报漏报；必要再接detector，任何结果都不能提升权限或绕过Go边界；验收：held-out中英/混淆攻击与正常工具输出全分母，攻击无越权；记录detector模型/开销；回滚：禁用classifier，保留untrusted输出和Go权限；不删除现有边界 |
| O22 | `orchestrator/security/credentials.py`、Memory/RAG artifact红线；`orchestrator/rag/claude_mem.py` L3只读与privacy gate；缺口：多处同类redaction和privacypattern可漂移；本次未判定实际泄露；PII classifier不是秘密扫描器 | 先统一现有redaction helper；Presidio分析器为需要PII业务时的候选；L3不新增事实库；许可/平台：合并已有代码/可选external analyzer；PresidioMIT，NLP模型另核；离线与数据位置需验证；来源：[OD30](https://github.com/data-privacy-stack/presidio) | P1；合并已有代码/可选external analyzer | 落地：源码、trace、日志、memory、receipt同一边界脱敏；raw secret文件不进入审计输出；L3默认关闭且benchmark禁用；验收：credentials/PII及合法代码误报测试、trace与artifact无匹配值；currentsecret scan不打印值；回滚：关闭额外PII模型/L3，保留regex脱敏与默认限制 |
| O23 | `orchestrator/rag/main.py`、`orchestrator/rag/backend.py`、`orchestrator/rag/models.py`、`orchestrator/rag/schemas/document_contract.py` FastAPI/Pydantic＋GoBackendClient；已有Go内网token和actor契约；缺口：module import初始化service、HTTP与gRPC两条入口运维复杂；并存不是bug，需明确domain和health生命周期 | 继续FastAPI、Pydantic、HTTPX；类型与OpenAPI导出复用官方库，不加另一gateway；许可/平台：继续复用/收口生命周期；MIT/MIT/BSD-3-Clause；Go边界与固定schema保留；来源：[OD08](https://github.com/pydantic/pydantic)、[OD31](https://github.com/fastapi/fastapi)、[OD32](https://github.com/encode/httpx) | P1；继续复用/收口生命周期 | 落地：分清codeagent gRPC与knowledge HTTP能力；lifespan创建/close客户端，backend不可用fail-closed；复用tracecontext和actor校验；验收：真Go↔PyHTTP链、shutdown/timeout/cancel、schemagolden、internaltoken/actor、无越权；回滚：原HTTPAPI与gRPC各保持契约，切回旧生命周期adapter |
| O24 | `orchestrator/rag/graph.py`、`orchestrator/rag/retrievers.py`、`orchestrator/rag/memory_tasks.py` LangGraph、LangChain Retriever、ChatOpenAI，customplanner；缺口：RAG图已有框架；整体换框架无法直接改善source/ACL或证明质量；Planner/answer/embeddingclient有并发计数接缝 | 继续已装LangGraph/LangChain；Haystack仅数据pipeline可替换性对照；许可/平台：继续复用/只借pipeline组件；MIT、HaystackApache-2.0；不同时引入Haystack与LangChain生产图；来源：[OD12](https://github.com/langchain-ai/langchain)、[OD13](https://github.com/langchain-ai/langgraph)、[OD33](https://github.com/deepset-ai/haystack) | P2；继续复用/只借pipeline组件 | 落地：降重复model config/HTTP/JSON处理；一条RAG query端到端复用Go retrieval和persist；禁用默认外部memorystore；验收：独立held-out检索/答案质量、引用与成本，failures保留；GoACL、trace与事实持久性；回滚：回现有LangGraph，保持相同GoBackendClient契约 |
| O25 | `internal/service/search_service.go` GoBM25/vector/RRF；`orchestrator/rag/retrievers.py` PyRRF；缺口：Go单域检索与Py跨组fusion职责可能不同，先看调用，不据两份函数就删除；无重排名质量对照 | 先复用当前fusion；OpenSearchRRF searchpipeline/QdranthybridAPI作原生融合候选；许可/平台：继续复用/adapter；Apache-2.0候选；原生query语义、ACL、alias并非ES直接兼容；来源：[OD24](https://github.com/qdrant/qdrant)、[OD34](https://github.com/opensearch-project/OpenSearch) | P1；继续复用/adapter | 落地：一个真实domain选择一个fusion执行处；固定candidate IDs/ranks，统一去重和degraded状态；无收益不搬后端；验收：原始candidate/rank到最终结果可追溯，Recall/nDCG、P95、ACL、零向量/rerank超时完整分母；回滚：固定回当前Go/Py融合版本；检索索引不动 |
| O26 | `orchestrator/rag/chunking.py`、`orchestrator/rag/elements.py`、`orchestrator/rag/evidence.py`、`orchestrator/rag/modality_policies.py`、Go `pipeline`、`EvidenceExpander` 自定义多模态父子/邻居契约；缺口：部分whitespace与AST规则简化；没有不同chunker质量对照；source coordinates和ACL是项目约束 | 继续LangChain textsplitters；代码结构tree-sitter；借ParentDocumentRetriever思路，保留Go邻居ACL和citation；许可/平台：继续复用/薄算法替换；MIT；chunk ID/parser/version/tokenizer字段保持，不能用泛化Document丢来源；来源：[OD12](https://github.com/langchain-ai/langchain)、[OD16](https://github.com/tree-sitter/tree-sitter) | P1；继续复用/薄算法替换 | 落地：最先统一model tokenizer与boundary；每次只换一种模态chunker，保持header/table/formula/slide/page硬边界；验收：samecorpus held-out Recall/nDCG与准确引用，sourcehash/页/元素/bbox/ACL一致，embedding成本；回滚：新physicalindex与旧alias并存读对照，批准cutover才切alias，旧库不删 |
| O27 | `pkg/documentparser`、`pkg/mineru` 与Py `orchestrator/rag/ingestion.py` 显式 `-m ocr` PDF路由；缺口：部件已复用，不必再写PDFparser；GoCLI与Pyparserworker两入口配置/版本需要固定，模型和源码许可分开 | 继续MinerU；不可直接替换PDF为Tika/Docling；许可/平台：外部CLI/独立worker，不vendor重新许可；当前AP2+附加商业/署名条款，实际版本另核；Windows/GPU/Python依赖按版本预检；来源：[OD35](https://github.com/opendatalab/MinerU/blob/ed50cc15bc2c9bfb00520dadfe61979866e62236/LICENSE.md) | P0；外部CLI/独立worker，不vendor重新许可 | 落地：固定已验parser版本、weights/digests与backend，approvedrunner参数+超时/killtree；Pythonparser服务仅处理Go批准payload；验收：真实PDF OCR、损坏/magic-disguise、超时/cancel、sourcehash/readingorder/bbox；许可与modelpinreceipt；回滚：回已批准旧MinerU版本/服务，无法确认则BLOCKED，不路由Tika |
| O28 | MinerU OCR/backend与`orchestrator/rag/elements.py` 映射；缺口：OCR模型的文字/版式准确率无本项目更优证据；不能独立加第二PDF入口破坏契约 | PaddleOCR作为OCR/版式技术对照与MinerU允许backend内候选；Docling仅非PDF候选；许可/平台：借模型/算法或已有backend；PaddleOCRApache-2.0、weights逐款核；不要将默认OCR model能力当许可/质量保证；来源：[OD36](https://github.com/PaddlePaddle/PaddleOCR)、[OD37](https://github.com/docling-project/docling) | P2；借模型/算法或已有backend | 落地：在项目批准的MinerU输出契约内评测适用OCRbackend；先样本质量对照，Go仍控制input/执行；验收：独立人工ground truth，OCR CER/WER/layout/table准确率、GPU/CPU/延迟；源坐标保持；回滚：保留批准MinerUbackend；新OCR试验与生产分离 |
| O29 | `pkg/tika`非PDF；`orchestrator/rag/native_documents.py` python-docx/python-pptx直接保留heading/table/shape/assets；缺口：原生Office结构与Tikafallback各有作用，无Docling对照；移除现有lib可能丢notes/bbox | 首选已有Tika + python-docx/python-pptx；Docling统一Office representation候选；许可/平台：继续复用/非PDFadapter；Apache-2.0 / MIT / MIT / MIT；Doclingmodel+依赖许可另核，legacyformats需LibreOffice；来源：[OD37](https://github.com/docling-project/docling)、[OD38](https://github.com/apache/tika)、[OD39](https://github.com/python-openxml/python-docx)、[OD40](https://github.com/scanny/python-pptx) | P1；继续复用/非PDFadapter | 落地：做一个DOCX/PPTX corpus通过当前与Docling双路只读比较，document/Element/citation契约不变；PDF仍MinerU；验收：真实Office标题/表格/图片/notes/shape坐标、fallback错误状态、sourcehash、相同检索分母；回滚：保留native/Tika入口；新parser不改变旧来源版本和索引 |
| O30 | `orchestrator/rag/spreadsheet.py` 已用openpyxl双工作簿读取formula/cachedvalue，regexdependency；缺口：缺大表memory/公式依赖准确率对照；不能将cache存在解释成已重算正确 | 继续openpyxl（MIT）；按官方说明核对不可信XML的defusedxml防护，Docling仅表格结构补充；重算需经Go受控LibreOffice独立能力；许可/平台：继续复用/条件补足；MIT；公式执行/宏绝不在Python任意开启；先验证部署实际XML防护，LibreOffice许可和部署另审；来源：[OD37](https://github.com/docling-project/docling)、[OD57](https://openpyxl.readthedocs.io/en/stable/) | P2；继续复用/条件补足 | 落地：流式read_only读取避免全量rows，依赖引用保守解析；cache absent明确unknown；先不加公式执行器；验收：真实多sheet/namedrange/mergedcell/formula、missingcache、资源预算；deterministicvalidation+humanqrels；回滚：回旧parser，禁重算；新的公式证据不能覆盖旧来源hash |
| O31 | `pkg/embedding`、`scripts/embedding_server.py` 已FastEmbed/sentence-transformers，BGE-M3固定revision；缺口：不是自研模型，需保留已有native1024/finite/vectoridentity约束；cpu负载/吞吐与更轻模型无对照 | 继续FastEmbed、当前BGE-M3；FlagEmbedding仅需要dense+sparse/multivector时对照；Ollamaembed作轻运维备选；许可/平台：继续复用/HTTPadapter；FastEmbedAP2、FlagEmbeddingMIT、BGE-M3weightsMIT；不要truncate/pad尺寸；来源：[OD03](https://github.com/ollama/ollama)、[OD41](https://github.com/qdrant/fastembed)、[OD42](https://github.com/FlagOpen/FlagEmbedding) | P1；继续复用/HTTPadapter | 落地：优先固定部署依赖与loader实际revision，batch/warmup只按测量调整；传输保留现有EmbeddingClient；验收：query/doc同权重digest、native维度、NaN/错数量拒绝；冷热P95/CPU/RAM/召回质量同任务；回滚：退回旧embedding物理index+alias，旧weights/index保留，不双写事实 |
| O32 | `scripts/reranker_server.py` TextCrossEncoder，`pkg/reranker`；compose Jina模型浮动；缺口：JinaweightsNC，服务loader无revision参数；当前结果model字段不能证明权重identity | 最快先固定合法现有部署与用途；公开可商用分发候选 BGE-reranker-v2-m3 + FlagEmbedding/SentenceTransformers，保留现有HTTP契约；许可/平台：权重/adapter替换；JinaCC-BY-NC-4.0、BGEweightsAP2、FlagEmbeddingMIT；ONNXregistry是否支持选定model须实查；来源：[OD41](https://github.com/qdrant/fastembed)、[OD42](https://github.com/FlagOpen/FlagEmbedding) | P0；权重/adapter替换 | 落地：先核distribution/use-case许可，再固定revision/checksums；不做只换MODEL字符串的伪迁移；topN/score语义校准；验收：Jina/BGE同corpus qrels完整对照，rerank指标/P95/CPU/RAM、ordering、timeoutdegraded、trace报告revision；回滚：若许可允许保留已批准旧pin；否则明确关闭rerank并标degraded，保留失败分母 |
| O33 | `orchestrator/rag/audio.py`、音频验证脚本已有faster-whisper、时间片引用与独立WER；缺口：parser库已复用；ASR质量不是fixture/自比参考；模型revision参数尚需实际runtime证据 | 继续faster-whisper；不同Whisper模型只按实际CPU/GPU和语言对照；许可/平台：继续复用；代码MIT，CT2依赖与转换权重逐版本核；官方HFwhisper-small标AP2，不代替实际runtime模型许可；来源：[OD43](https://github.com/SYSTRAN/faster-whisper) | P2；继续复用 | 落地：固定实际Whisper/CT2modelrevision、时间边界/VAD，benchmark输入与reference分开；只改善真实需求场景；验收：独立transcriptWER/时间戳/检索，素材来源许可、非fakeASR、无原文trace、cold/warmresource；回滚：回旧模型/禁音频扩展；已有textRAG继续运行 |
| O34 | `orchestrator/rag/visual/encoder.py`、`orchestrator/rag/visual/artifacts.py` CLIPsinglevectorpilot；VisualEncoder抽象未实现ColPali/ColQwen；缺口：抽象存在不代表模型ready；文字dense与视觉索引已分开，multi-vector能力缺实现和质量分母 | 首选现有CLIPbaseline；ColQwen2 + colpali-engine作页面检索候选；ColPaliweights需追baseGemma许可；许可/平台：adapter/新实验lane；colpali-engineMIT，ColQwen2weightsAP2；ColPaliadapterMIT但baseGemma条款，非统一AP2；来源：[OD44](https://github.com/illuin-tech/colpali) | P2；adapter/新实验lane | 落地：保留baseline，Go下发授权imageartifact；新增lateinteractionmulti-vector存储/评分，禁止平均成singlevector冒充ColQwen；验收：官方queryformat、fixedrevision、GPU/RAM/index成本、独立pageqrels与baseline完整对照，错资产hash拒绝；回滚：保留text索引和CLIP旧visualalias，新visualindex独立可停用 |
| O35 | `pkg/es/client.go`、`pkg/es/knowledge_index.go`、`pkg/es/visual_index.go`、GoSearchService；当前ES8.10.4；缺口：SDKAP2不等于distributionAP2；OpenSearch不是drop-in，dense_vector/kNN/productcheck/alias/plugin皆不同 | 最快继续已部署ES及官方Go client；严格开放许可、维护/成本驱动时 OpenSearch+官方Go client；许可/平台：继续复用/有条件迁移；ESdistributionELv2，currentfreecode有AGPL/SSPL/ELv2选项；OpenSearchAP2；来源：[OD34](https://github.com/opensearch-project/OpenSearch)、[OD45](https://github.com/elastic/elasticsearch/blob/dd6423744a34ebef967580685f95ed1fd57cebfd/LICENSE.txt)、[OD46](https://www.elastic.co/pricing/faq/licensing)、[OD47](https://github.com/elastic/go-elasticsearch) | P1；继续复用/有条件迁移 | 落地：先固定当前服务digest；将IndexWriter/Searchbackend实际接缝收敛；独立建OpenSearchindex重建、只读对照后cutover；验收：mapping/native1024、text/visual分库、aliasCAS/rollback、ACL、bulk/neighborquery、qrels/P95/fullfailures；回滚：旧ES对象不删，切回原readalias/backend；不回滚Ledger事实 |
| O36 | `orchestrator/context/backends.py` Redisadapter，Gopipeline embeddingcache与tokenblacklist使用Redis7.2；缺口：7.2是BSD-3历史线，不能无约束升latest；维护与协议行为无本项目胜出证据 | 当前Redis7.2可继续；Valkey作为开放社区维护替代；现有go-redis可复用；许可/平台：外部server替换/库继续；Redis8 AGPL/RSAL/SSPL选一，ValkeyBSD-3，go-redisBSD-2；Windows用approvedDocker/WSL；来源：[OD07](https://github.com/redis/go-redis)、[OD48](https://github.com/redis/redis/blob/940a4d72fc7caaed6f853b65b39920ff3fb9ac1f/LICENSE.txt)、[OD49](https://github.com/valkey-io/valkey) | P1；外部server替换/库继续 | 落地：先盘点cmd/Lua/TTL/WATCH/blacklist；备份RDB/AOF并在独立Valkey验证，保持owner命名空间和单向cutover；验收：TTL、并发Lua/CAS、key隔离、缓存rebuild、blacklist及断网failclosed；7.4+数据不可直接套7.2迁移；回滚：保留原Redis数据volume，不删除；切回旧endpoint并对账期间新写入 |
| O37 | `pkg/kafka` + `internal/pipeline` parse/chunk/embed/index+DLQ；当前cp-kafka+ZooKeeper；缺口：CodeAgent本地核心是否需要Kafka与RAG批处理是不同scope；降低进程数有潜在收益，未做吞吐/恢复对照 | 已有kafka-go+Apache Kafka；优先官方KRaft单节点开发部署；小规模才考虑既有SQL任务队列；许可/平台：server部署调整/继续复用；KafkaAP2、kafka-goMIT；Confluent发行包额外组件逐份许可核；来源：[OD50](https://github.com/apache/kafka)、[OD51](https://github.com/segmentio/kafka-go) | P2；server部署调整/继续复用 | 落地：先消除新部署ZooKeeper依赖但不直接升级旧cluster；保持taskid、stagehash、retry/DLQ与Golifecycle；SQLqueue须单写owner；验收：真实重复/乱序/producer/consumercrash、DLQreplay、backpressure/denominator，业务幂等不依赖“exactlyonce”口号；回滚：保留旧broker/topics/offsetsnapshot，单消费者归属切回，禁止两消费者竞争事实 |
| O38 | pipeline `ObjectStore`、`pkg/storage`、upload/document服务用MinIO S3 client；缺口：MinIOserver已归档+AGPL，SDK仍AP2；改变存储不可丢原始文档/asset/sourcehash | SDK继续minio-go；SeaweedFS AP2 S3服务或已批准S3作server候选；许可/平台：server适配/迁移；MinIOAGPL、minio-goAP2、SeaweedFSAP2；Seaweed有Windowsweed.exe，但API覆盖需真测；来源：[OD52](https://github.com/minio/minio)、[OD53](https://github.com/minio/minio-go)、[OD54](https://github.com/seaweedfs/seaweedfs) | P0；server适配/迁移 | 落地：用已有ObjectStore端口read/write/presign/delete；盘点multipart/range/etag/url/ACL等用到的S3子集，备份与清点对象；验收：全对象分母＋bytes/SHA256，signedURL/权限/续传/取消/重启/原始数据读取，存量100%可回读；回滚：原volume/bucket保留，只切endpoint；迁移期间冻结写或单写转发并对账，不能删源对象 |
| O39 | `internal/corpus` manifest/sourcepin/licensehash/checkpoint；`scripts/rag/preflight-cutover.ps1`、`scripts/rag/switch-alias.ps1`、`scripts/rag/rollback-alias.ps1`；缺口：现有来源gate是必要项目逻辑；文件checkpoint是ingest索引而非Session事实；大对象版本/共享缓存有运维成本 | 继续manifest+GoStager；DVC仅大型corpus/artifact版本与远程cache候选，现有aliasrollback保留；许可/平台：继续复用/工具集成；DVCAP2（当前treeverse/dvc）；不把DVCstate当Ledger/评测事实源；来源：[OD55](https://github.com/treeverse/dvc) | P1；继续复用/工具集成 | 落地：依赖与模型同样pin源码/weights/licensehash；仅将合法大artifact交DVC，answer/qrels不进agent上下文；cutover保留原分母；验收：pinnedcommit/hash/许可拒绝、重复导入、changedsource版本、datareadback、全部输入无答案泄漏；回滚：保留旧manifest/objects/indexalias，一次只切一个generation，sourcepin可复核 |
| O40 | `internal/service/upload_service.go`、`internal/service/document_service.go`、`internal/service/conversation_service.go`、`internal/service/memory_service.go`、`internal/service/admin_service.go`、`internal/service/user_service.go`、`internal/service/corpus_ingest_service.go`、`internal/service/orchestrator_support_service.go` 同时知识聊天/组织/上传/代码Agent服务；缺口：它们是不同domain，不凭MySQLmemory与Ledger并存判双写；当前代码产品可能不需要暴露全部旧HTTP功能；5MBchunk/merge自写可借协议轮子 | GORM/Gin/既有Go业务边界继续；tusd用于实际需要断点上传时；旧RAG聊天迁移经GoSessionadapter；许可/平台：继续复用/收口/条件uploadadapter；tusdMIT，可embed Go并用S3store；不增Python副作用入口；来源：[OD56](https://github.com/tus/tusd) | P2；继续复用/收口/条件uploadadapter | 落地：先列router+公开调用者+domainauthority，再决定保留profile或readadapter；tusdembed前加现有GoACL/quota/objectprefix，completion触发现有pipeline；验收：所有公开caller契约真E2E、同owner/断点/并发merge/retry、sourcehash/cancel、旧数据只读可回查；不可删用户数据；回滚：保留旧HTTProute/readadapter与uploadrecords；新协议关闭，旧数据/对象可继续读 |

### 前端、发行、可观测性、测试与评测

| ID | 优化部分与当前源码接缝 / 缺口 | 首选、备选与许可 / 平台 | 方式 / 优先级 | 落地、验收与回滚 |
| --- | --- | --- | --- | --- |
| P01 | 基础控件、审批/恢复对话框：`frontend/src/App.tsx`、`frontend/src/CheckpointPanel.tsx`、`frontend/src/LoginPage.tsx`、`frontend/src/index.css` | [Radix Primitives](https://www.radix-ui.com/primitives/docs/overview/accessibility) MIT；[shadcn/ui](https://github.com/shadcn-ui/ui) MIT 是可选样式源码来源。直接依赖必要 primitive；不全包复制 | P1 | 落地：先替 Dialog/AlertDialog/Tabs/Dropdown 的键盘、焦点与状态，沿用现有 CSS；shadcn 的 Tailwind/模板不是当前 Vite/CSS 的免费适配，按需取用；审批内容无丢失、键盘和读屏可操作、取消回焦、暗亮主题及窄屏通过真实浏览器；回滚组件外观保留审批 API/seq |
| P02 | 消息壳、输入框、进展与工具卡片：`frontend/src/App.tsx`、`frontend/src/types.ts` | [assistant-ui ExternalStoreRuntime](https://www.assistant-ui.com/docs/runtimes/custom/external-store) MIT，薄 UI Adapter；备选继续 React 自有渲染 | P2 | 落地：仅在整组聊天交互节省维护量时接，映射 Ledger event 到 ThreadMessageLike，onNew 调现有 Go API；不启用它的云持久化或直接模型工具执行，不提供未经 Go 实现的 setMessages/edit/branch callback；保留 event IDs、seq、审批、rewind、compaction、同 requestId 重试；能力按钮由实际 API 能力控制，缺能力不展示。可回滚 UI Adapter，不动 session 存储 |
| P03 | Markdown：`renderInlineMarkdown`/`renderMessageMarkdown`；源码：`frontend/src/App.tsx` | [react-markdown](https://github.com/remarkjs/react-markdown) + [remark-gfm](https://github.com/remarkjs/remark-gfm)，均 MIT；[rehype-sanitize](https://github.com/rehypejs/rehype-sanitize) MIT 按启用插件需要加入 | P0 | 落地：用一个共享 MessageMarkdown 组件替当前解析器；默认关闭 raw HTML，URL 协议和外链按实际需求限制；sanitizer 放在产生 HTML 的插件后，不能认为加包自动安全；围栏代码、表格、列表、中文、长行、恶意链接/HTML、部分流式文本真实展示；日志原文和 tool details 仍可展开。回滚渲染实现即可，原始 Ledger 不改 |
| P04 | 代码显示和复制：`frontend/src/App.tsx` 的 `<pre>`/inline code | [Shiki](https://github.com/shikijs/shiki) MIT，直接依赖（只读高亮）；备选先用普通 `<pre>` | P1 | 落地：先做好围栏语义和复制按钮，再按实际 Go/Python/JS 语言集延迟加载高亮；不要为了几十行短输出加载全部语言或完整 IDE；escaped 未受信任代码、巨型输出、主题对比度和 bundle 影响可测；回滚高亮不丢代码内容 |
| P05 | 图标、跨平台视觉一致性：`frontend/src/App.tsx` 和 `frontend/src/CheckpointPanel.tsx` 使用 Unicode 符号 | [Lucide](https://github.com/lucide-icons/lucide) 主体 ISC，继承 Feather 图标 MIT；直接依赖所用图标 | P1 | 落地：替功能图标，统一尺寸/笔画/状态，保留中文 aria-label 和 tooltip，不用图标代替必要文字；保留 ISC 与 Feather MIT notices；Windows 字体差异、键盘焦点、禁用状态、暗亮主题截图合格；按组件回滚，不影响 action |
| P06 | 假搜索/导航入口：`frontend/src/App.tsx` 的 sidebar-search、tabs、Plan、rail | [cmdk](https://github.com/dip/cmdk) MIT，可选命令菜单；最小首选是现有 React/HTML 输入框筛选 sessions | P0 | 落地：先落实已有会话标题/项目搜索和导航，删除/禁用无实现入口；若确需 Ctrl/Cmd+K 再接 cmdk。归档/Plan/附件不能靠借 UI 自动获得后端能力；每个可点击入口有真实结果，列表空态和键盘可用，Ctrl 与 Cmd 都测试；仅展示已有授权 API，可回滚菜单不改 Ledger |
| P07 | 工作区布局：`frontend/src/App.tsx` 侧栏/详情、`frontend/src/index.css` 固定区域 | [react-resizable-panels](https://github.com/bvaughn/react-resizable-panels) MIT；条件直接依赖；备选现有 CSS grid/responsive | P1 | 落地：在三份预览选定后只接桌面可调宽分栏，保留移动端单栏；尺寸偏好只本地 hint，不存为 Session 事实；键盘调整、拖动、缩放、触屏、窄屏、详情关闭的布局与阅读不中断；回滚固定布局 |
| P08 | 服务端状态查询：`frontend/src/api.ts`、`frontend/src/App.tsx`、`frontend/src/CheckpointPanel.tsx` 重复 session/event 请求 | [TanStack Query](https://tanstack.com/query/latest/docs/framework/react/guides/important-defaults) MIT 条件接入；[Zustand](https://github.com/pmndrs/zustand) MIT 仅当跨组件共享确有需要；默认不同时引入 | P1 | 落地：先抽现有查询/取消与缓存去重；扩页面后可让 Query 管只读投影，mutation retry=false，401/403/409 禁自动重试，expectedSeq 发送前仍从 Go canonical head 取，不使用 staleTime 当事实有效性；两页同 session 与会话快速切换、logout 清缓存、断线、旧结果迟到不污染新页；数据库状态不进浏览器 store；回滚 Query 而不动服务 API |
| P09 | 长会话：`MessageList.visibleEvents.map` 与 MutationObserver；源码：`frontend/src/App.tsx` | [react-virtuoso](https://github.com/petyosi/react-virtuoso/tree/main/packages/react-virtuoso) 包 MIT；直接接入虚拟列表。排除商业 `@virtuoso.dev/message-list` | P0/P1 | 落地：保持完整事件投影，按 event.id computeItemKey；只虚拟化可见 DOM，使用 followOutput/atBottomStateChange 保留“读旧消息不跳尾”；不要任意裁掉前 200 轮；200 assistant-turn + 工具/审批大输出，reload/reconnect/rewind/compaction 后第一轮仍可读；量测 DOM 节点、峰值内存、滚动延迟。可换回全渲染，但不能删历史 |
| P10 | 代码变更审核：`event.codeModification` 只展示路径/哈希；源码：`frontend/src/App.tsx` | [Monaco Editor](https://github.com/microsoft/monaco-editor) MIT，首选只读 diff，ESM 按需加载；CodeMirror 6 为可选体积对照但需使用迁移后的官方包来源 | P1 | 落地：先在已有 Go 审计 artifact 有可读取原/新内容或 patch 的接口时接 read-only diff；没有接口先补受 owner/workspace/hash 约束的 Go 读取。不得让编辑器直接 fs 写入或装 VS Code 扩展；真修改/未修改/大文件/二进制/过期哈希，显示内容与 diffSha256 对账；手动编辑需求另走 Go Edit/approval，不把浏览器 buffer 当提交。回滚 diff 展示 |
| P11 | Shell/Job 输出可读性：当前工具详情 `<pre>`，Go 已管理进程/CLI；源码：`frontend/src/App.tsx` | [xterm.js](https://xtermjs.org/docs/guides/security/) MIT，条件直接依赖只读 ANSI 输出；备选保持 `<pre>` | P2 | 落地：仅当真实输出含 ANSI/交互终端产品需求明确再接；当前网页没有因此自动获得 PTY 输入通道。新 resize/input 必须经 Go session ownership、授权、审计与沙箱；ANSI/超长/恶意 escape/链接、disconnect 与 cancel，不得从 read-only 输出升为宿主 shell；回滚 `<pre>`，不改变 Go 进程边界 |
| P12 | 可访问性/React 组件测试：基础 TS helper tests 与 browser fixture；源码：`frontend/tests` | [axe-core/@axe-core-playwright](https://github.com/dequelabs/axe-core-npm) MPL-2.0；[React Testing Library](https://github.com/testing-library/react-testing-library) MIT 可选 | P1 | 落地：最快先在现有 Playwright 中扫描 login/任务/审批/恢复/错误态，并人工键盘走一次；组件复杂时才 RTL，不再写实现镜像测试。MPL 依赖保留文件级义务，不重标 Apache；axe 只覆盖可自动检查部分，键盘/焦点和读屏人工检查仍保留；记录实际 violation 分母和未修原因；回滚测试依赖不能放弃既有可访问性要求 |
| P13 | 前端测试底座：`frontend/tests/*.test.ts`、`frontend/tests/sendMessage.browser.mjs` | 当前 Node test + Playwright 继续复用；[Vitest](https://github.com/vitest-dev/vitest) MIT、[MSW](https://github.com/mswjs/msw) MIT 是组件/fixture 复用增长后的条件候选 | P2 | 落地：现有十余 helper 测试不必改框架；若组件 hooks/ESM mock 更难维护再迁 Vitest。若测试与预览需要共享接口 fixture，再用 MSW；现有 page.route 足够时不添加；不减少 current tests，fixture 清楚标 mock，测试不访问真 provider；单次迁移不保留两套重复 runner；回滚 runner/config |
| P14 | 真实浏览器 E2E：`frontend/tests/sendMessage.browser.mjs` 仅 fixture，Goal 的 200 轮/完整 ticket matrix 未完成 | 已安装 [Playwright](https://playwright.dev/docs/test-webserver) Apache-2.0，直接继续使用，不新增 Cypress | P0 | 落地：保留 fixture 快回归，新增独立真实 Harness/Python/provider 场景，通过正常登录、发送、批准、代码修改/测试、reload、ticket/reconnect、compaction；使用 webServer/fixtures 复用启动检查，credential 仅 env；每次真实 session 单次≥10轮，最终 200 assistant-turn；绑定当前 SHA、完整 turn/event 分母、trace backend readback、操作/断线矩阵、截图/trace/hash。没真实条件保留 BLOCKED；回滚脚本不删除失败收据 |
| P15 | 故障恢复：`eval/harness/fault_injection.py`、生产 CLI/HTTP、browser reconnect、Git restore | [Toxiproxy](https://github.com/Shopify/toxiproxy) MIT + 现有 Go/Python 真实进程 kill/restart；薄故障驱动 | P0/P1 | 落地：代理 REST/gRPC/trace/cache 端点施加延迟、timeout/reset；业务409/429用明确故障入口，SQLite crash/Go/Python kill/Git recovery用真实进程矩阵，不让网络 proxy假冒全部故障；每项故障记录发生/恢复时间、重试有界、重复副作用=0、Ledger链/CAS/worktree提交对账、30故障与所有失败；撤销本轮toxics并退出本轮helper，不清 DB/卷 |
| P16 | HTTP 多会话/队列性能：Goal 的 Workers/200 tasks 与 frontend 单用户不是同一测试；源码：`cmd/server/main.go` | [Locust](https://docs.locust.io/en/stable/what-is-locust.html) MIT，条件使用独立压测工具 | P2 | 落地：先用既有8/200/30脚本完成 durable workflow，真正出现并发用户目标再编 Python HTTP 客户端、cookie、seq、requestId；只对明确测试工作区压测，relay并发总和≤10；实际并发、成功/错误/超时分母、P95、服务 RSS、线程/句柄增长；HTTP load不能替200浏览器或代码正确率。停止工具即可回滚，无生产数据删除 |
| P17 | 可移植一键入口：`cmd/launcher/main.go`、`scripts/launch-codeops.ps1`、`scripts/start-interview.ps1`、Vite代理 | Go 标准库 [embed](https://pkg.go.dev/embed)、os/exec/net/http（BSD-3-Clause）；继续 Go 主体，比增桌面框架更快 | P0 | 落地：release build静态前端并由本Go服务器同源提供；入口基于exe所在目录/明确用户选择root，返回实际错误/退出码；预打包Go二进制与受控Python运行环境/依赖元数据，不要求面试机有npm/go源码；从另一目录、带空格/中文路径启动，干净Windows机器/新用户无Go/Node开发环境可用；缺Python/服务显示可恢复诊断，不静默失败；Python不因嵌frontend自动成为单exe。原开发入口可保留回滚 |
| P18 | 原生桌面壳：面试EXE当前只开浏览器，需原生菜单/文件选择才有新增需求；源码：`cmd/launcher/main.go` | [Wails v2](https://v2.wails.io/docs/introduction/) MIT，可选薄桌面壳；首选先完成PD17；v3在官方页面仍beta | P2 | 落地：只在确需原生window/dialog时保留React assets+Go；Wails binding只调用现有Harness服务方法，不对JS暴露任意os/file/exec。Windows依赖WebView2要进入安装/离线条件；普通用户权限、WebView2缺失、shutdown无孤儿子进程、cookie/origin/Go授权测试；不是Python自动打包方案。可回退浏览器发行，不迁移Ledger |
| P19 | 多平台构建、checksum、release archive：Makefile/当前手工EXE；源码：`Makefile` | [GoReleaser OSS](https://goreleaser.com/customization/) MIT，直接作为发布工具；实际安装器可独立使用 [NSIS](https://nsis.sourceforge.io/Docs/AppendixI.html)（主体 zlib/libpng；压缩模块与插件另核），不依赖 GoReleaser Pro 集成 | P1 | 落地：只配置本repo实际Go binaries、frontend静态build、Python wheel/runtime manifest和checksums；先 snapshot/check 不发布；按已存在release gate先取新鲜证据再发布。GoReleaser 的 [NSIS 自动集成](https://goreleaser.com/customization/package/nsis/) / [MSI 自动集成](https://goreleaser.com/customization/package/msi/) 属 Pro，OSS 可外置调用已审核的安装工具；具体签名工具、证书与收费边界分别核对；干净构建、license/notice/source revision/SBOM/artifact hashes、安装升级/回退不动Ledger；Windows Authenticode证书/可信CA另需真实配置，checksum签名不等于exe已签；回滚发行版本，不覆盖旧session数据库 |
| P20 | 启停编排/跨平台命令：Makefile + 多份PowerShell入口；源码：`scripts/launch-codeops.ps1` | [Task](https://github.com/go-task/task) MIT 条件直接CLI；[kardianos/service](https://github.com/kardianos/service) Zlib 仅将来明确后台service需求才考虑；首选当前Go ProcessManager/sharedPS helper | P2 | 落地：先合并重复启动/健康配置到现有入口，保留已核验进程身份再停止；平台命令确需统一再Task。展示应用无需管理员Windows service，不为自动启动提升权限；相同入口、退出码、bounded timeout、日志脱敏、foreign port拒绝停止、关闭应用清理本轮子进程；可回滚新CLI，不清容器/持久卷 |
| P21 | 部署就绪、镜像/插件供应链：`docker-compose.yml`、shared readiness helper | [Docker Compose](https://docs.docker.com/compose/how-tos/startup-order/) Apache-2.0，继续复用官方 healthcheck/service_healthy/service_completed_successfully/profiles | P0 | 落地：把sleep/吞错改健康/明确一次性初始化；`latest`与ES插件在线下载改构建期固定digest/hash；核心coding与RAG/trace分profile可选启动，不增加Kubernetes。Docker Desktop与镜像内软件许可证另核；冷启动/慢启动/依赖失败/重启皆有精确状态，init只有幂等exists可容忍而不是所有错误；127.0.0.1绑定适配本地产品；保留卷，回滚镜像需schema兼容判断，不能`down -v` |
| P22 | Trace接收/脱敏/批量导出：`internal/telemetry/genai/tracer.go`、Python trace、OTLP endpoint | 现有OTel SDK继续用；[OTel Collector](https://opentelemetry.io/docs/collector/configuration/) 与必要contrib组件 Apache-2.0，薄标准链路 | P1 | 落地：直连能满足当前小规模就先保留；需要两语言路由/统一scrub/export queue时加Collector，禁止prompt/tool秘密进入遥测；采样规则不能删验收instance根span；Go根 -> gRPC -> Python/model/tool/scorer真实parentage可后端读取；队列溢出或backend不可用记degraded/BLOCKED，flush不丢收据；回退直连须保留相同属性和审计契约 |
| P23 | Trace后端与readback：`eval/harness/phoenix.py`、trace_capture/contract，compose Phoenix | [Jaeger](https://www.jaegertracing.io/docs/latest/architecture/apis/) Apache-2.0为严格OSS候选；现有Phoenix server是ELv2（非OSI），OpenInference为Apache-2.0，不混为一体 | P1 | 落地：若Phoenix许可满足使用方式可先继续，固定镜像而非latest；若全栈OSI要求则OTLP->Jaeger并给现有CapturedSpan写只读query adapter；不因为换collector就认readback完成；同一真实run全span/页读回、ended/parent IDs/run IDs/git pins一致；后端空/错误不能通过。回滚后端不删除原trace，版本化readback契约 |
| P24 | 指标、错误、耗时与成本：`internal/metrics/collector.go`、HTTP/jobs/workflow | [Prometheus client_golang](https://github.com/prometheus/client_golang) 与server Apache-2.0；直接instrument现有Collector接口，不替Ledger | P1 | 落地：先让未知价格=unknown、provider usage与词估分开；再导出请求/queue/worker/error/latency/cache等低基数指标；价格要版本化source，不从硬编码旧价目宣称实际账单；用户内容和session IDs不做高基数label；已知/未知model、无usage、cacheusage、restarthydrate、error预算均测，metric汇总不成为持久化事实；trace降级与0cost区分；回滚exporter保留业务usage |
| P25 | Terminal代码任务评测被测接缝：`eval/adapter.py`、driver_headless、terminalbenchofficial、deepseek_tb_agent | [官方旧 Terminal-Bench](https://github.com/harbor-framework/terminal-bench-1) / [Harbor](https://docs.harborframework.com/agents/custom-agents) 均Apache-2.0；复用官方runner/verifier，写薄产品AgentAdapter | P0 | 落地：先接真实Go CLI/HTTP一次共享入口，再沿用pinned旧TB协议；目标是TB2.0才迁Harbor BaseAgent/BaseInstalledAgent，版本化结果schema；不把Python复制工具执行或上游Codex/pi成绩记为本产品；同Go权限/沙箱/Ledger/Python链，真实修改和测试、official raw output、trace/source/model/data/image pins、全失败分母；官方task容器不能为嵌套Docker挂宿主socket。回滚runner Adapter，不换scorer标签伪兼容 |
| P26 | 仓库修复评分：`eval/benchmarks/swebench.py`、`eval/benchmarks/swebench_predictions.py`、eval/run.py | [SWE-bench](https://www.swebench.com/SWE-bench/guides/evaluation/) MIT官方Docker scorer；继续三字段predictions Adapter | P0/P1 | 落地：生成补丁仍由产品Go工具完成，评分器读取Agent无权看到的测试；固定当前可用scorer版本，升级核对report输出路径（上轮已核最新路径不同）；不要先移植整个SWE-agent替本产品；currentSHA锁定20例/完整失败分母、baseline20例、官方18/20门槛、raw报告+checksums+trace；模型patch不是resolved，官方评分才是。WSL/Linux/Docker资源不足保留BLOCKED |
| P27 | 函数级代码正确性/执行安全：`eval/benchmarks/evalplus.py`、gen_samples、score_sequential、Dockerfile.evalplus | [EvalPlus](https://github.com/evalplus/evalplus) Apache-2.0，继续官方Linux sandbox/scorer | P1 | 落地：减少Windows inline/复制评分分叉，Agent生成samples，官方Docker评分；MockAgentAdapter返回canonical解答只能fixture smoke，不进正式model指标；generation应通过同产品Adapter或清楚区分模型函数评测；固定HumanEval+/MBPP+数据/scorer、原始样本、timeout/OOM/failure、完整分母；防生成代码逃逸，scorer环境与Agent隔离；保留旧输出供对账，不悄然缩小测试集合 |
| P28 | 工具交互benchmark：`eval/benchmarks/tau2bench.py`、`eval/benchmarks/tau2official.py`、run-tau2脚本 | [tau2-bench](https://github.com/sierra-research/tau2-bench) MIT，已有固定版本保留；条件适配，不是代码Agent主要验收 | P2 | 落地：当前mock域+上游llm_agent先仅标connectivity；真的评测本产品tool-use才适配其Agent protocol与Go许可链。官方当前主线已加入tau3知识/语音，旧v1.0.1不能自动继承兼容性；原始officialrun/fault分母、实际被测Agent identity；不把模拟CRM工具或tau语音成绩替代真实repo任务。回滚目标版本，不改变原门槛 |
| P29 | 通用评测编排/报告：`eval/harness/runner.py`、budget/artifacts/trace_contract、各benchmark | [Inspect AI](https://inspect.aisi.org.uk/agent-bridge.html) MIT条件外部评测执行器；不替本项目release receipt判定 | P2 | 落地：先复用它的eval viewer/limits/可选sandbox Agent Bridge做对照实验；以Go程序入口运行，不能使用Inspect自带bash/text_editor绕开Harness；桥接会改变model-call路由，要验证现有relay/modelidentity/budget契约；相同任务/预算/modelpin与官方scorer保留、错误分类不变、所有instances对账；Inspect .eval是派生artifact而非新session事实。没有确切省维护量先保留现runner |
| P30 | Skill选择/提示词/反思语义回归：`eval/harness/skill_selection_eval.py`、context_token_eval、prompts | [promptfoo](https://www.promptfoo.dev/docs/providers/python/) MIT OSS，条件评测工具；现有pytest/官方scorer仍主线 | P1/P2 | 落地：只接Python custom call_api到产品API，用于prompt/skill语义比较与注入拒绝用例；公开功能不依赖enterprise；限制所有并行≤10，默认避免缓存隐藏真实运行，gold只给assertion/judge；与已有固定1000cases/40Skills分母对齐、tokencost、falsepositive/failure保留，格式lint≠skill可用；不能把LLM judge标HUMAN_REVIEWED。回滚工具，无prodprompt自动发布 |
| P31 | 检索指标与official接通：`eval/retrieval/`、`orchestrator/eval/metrics.py`、`orchestrator/eval/runner.py`、`eval/benchmarks/` 内 BEIR/MIRACL/BRIGHT | [ranx](https://amenra.github.io/ranx/metrics/) MIT为本地标准指标/统计对照；[BEIR](https://github.com/beir-cellar/beir) Apache-2.0、[MIRACL](https://github.com/project-miracl/miracl) Apache-2.0、[BRIGHT](https://github.com/xlang-ai/BRIGHT) CC-BY-4.0为对应official paths | P0/P1 | 落地：先补真正runner/scorer结果采集，不停在predictions note；明确DCG版本、gain、tie/dedup/empty queries/fullquery分母；保留sectionprefix派生自定义指标，标准doc/page指标与official对照后才收敛重复代码；隐藏gold仅scorer可读；metric版本变更重跑原baseline，固定当前code/datapin；BRIGHT须保留CC署名且不是Apache2代码整体重标。回滚metricversion，保留老结果和失败 |
| P32 | 视觉/定位与多模态指标：`eval/benchmarks/vidore.py`、multimodal_metrics、screenspot_grounding、orchestrator/eval（多模态评测入口） | [官方 ViDoRe](https://github.com/illuin-tech/vidore-benchmark) MIT代码，条件thinpipeline scorer；ScreenSpot维持已pin的独立adapter，暂不承诺其所有数据许可 | P1/P2 | 落地：对已有text/page/lateinteraction预测接对应版本ViDoRepipeline evaluator；官方现在重点v3pipeline，旧retriever/v1v2处于deprecated保留状态。数据/模型/页面许可另核，PDF入口仍MinerU OCR；同样本对三路径nDCG/recall/boxhit+资源开销，官方与自定义指标标清；ScreenSpot单点GUIscore不是codingbenchmark。回滚version/视觉cache，不改原文/OCR入口 |
| P33 | 污染/相似文本候选生成：`eval/contamination/scanner.py` | [datasketch](https://ekzhu.com/datasketch/lshensemble.html) MIT，MinHash/LSH Ensemble为条件替代候选；保留当前exact+directional校验 | P2 | 落地：只在当前CPU/内存瓶颈实测后用库替候选index；LSH Ensemble支持containment而普通MinHashLSH是Jaccard；旧hash/seed/序列化不当然兼容，版本化重建derivedindex；长文包含短问题、近重复、不相关、短碎片、所有skippedlayers、召回/FN/峰值RSS/吞吐对照；不能删exactcontainment或将INCOMPLETE改CLEAN。回滚index实现，政策pin不变 |
| P34 | 大模型/数据/评测artifact缓存：`eval/datasets/loader.py`、manifest/source_pin/repo_cache、ignoredrun trees | [DVC](https://github.com/treeverse/dvc) Apache-2.0条件数据工件工具；现有Git+SHA256manifest优先 | P2 | 落地：多人共享大数据/modelcache才加DVCremote；只将非秘密、许可通过的数据或model artifact做内容寻址，source manifest/hash继续受现release gate校验；不要新增DVC session状态；checksum损坏/源失效/权限拒绝/offlinecache均failclosed，gold/questions隔离，缓存不发布未许可数据；回滚DVCmetadata但保留artifact和原manifest，不删除缓存/数据 |
| P35 | 人工标注/逐条qrel与bbox审核：`docs/manual-review-portal.html`、human_review_qrels、multimodal_human_review、holdout | [Label Studio Community](https://github.com/HumanSignal/label-studio) Apache-2.0，可选独立UI；小规模沿用现portal | P2 | 落地：只在大量reviewer/图像bbox工作时导入候选+导出人工decisions；其percentage bbox转本项目坐标，逐条reviewer身份、signature和exactsourcebytes由既有冻结入口核验；enterprise独立核；cancelled/未审核/AI预标注不升级HUMAN_REVIEWED，导出完整任务分母、身份/防重复、签名、sealedholdout对账；其annotationDB不是产品Session事实。回滚UI继续portal，保留原decisions |
| P36 | 源码与配置CI质量：`.github/workflows/ci.yml`、Makefile、pyproject、frontend config；源码：`.github/workflows/ci.yml` | 当前Actions测试继续；[Ruff](https://docs.astral.sh/ruff/configuration/) MIT、[Staticcheck](https://github.com/dominikh/go-tools) MIT、[actionlint](https://github.com/rhysd/actionlint) MIT为按需CLI | P1 | 落地：先最小no-unsafe-fix lint，仅对人工编辑活跃源码；excludegenerated/archives/runtrees。普通push快速Go/Py/frontend，Windows平台narrow/native跨进程回归按nightly/manual或合适独立job，真实provider/scorer继续manualeligible环境；cleancheckout重复成功、规则baseline可审核、无全量format churn、不删失败、不continue-on-error；网络服务不可用和codefail分开报告；回滚新增规则而不取消基础CI |
| P37 | 密钥/已知漏洞/发布供应链：依赖清单、镜像、license/NOTICE、CI现未含系统化扫描；源码：`go.mod` | [Gitleaks](https://github.com/gitleaks/gitleaks) MIT CLI；[govulncheck](https://go.dev/doc/tutorial/govulncheck) BSD-3；[pip-audit](https://github.com/pypa/pip-audit)、[Trivy](https://github.com/aquasecurity/trivy)、[Syft](https://github.com/anchore/syft)、[Cosign](https://docs.sigstore.dev/cosign/signing/signing_with_blobs/) Apache-2.0 | P1 | 落地：固定二进制校验，Gitleaks fullhistory/staged redact=100，source deps分别审，snapshotimages生成SBOM；release时cosign签checksum/SBOM并verifyidentity；无需复制工具源码。Gitleaks Action/GoReleaser Pro商业要求与CLI开源分开；secret scan输出只数量/paths并脱敏；未知扫描DB/网络失败记录unknown不报告clean；SBOM pin最终实际packaged Go/Py/frontend/images；Cosign签名≠runtime通过/WindowsAuthenticode。回滚CLI接入，不忽略真实泄漏/漏洞 |
| P38 | 更新依赖与pinnedactions：Go/Python/npm/compose，当前无dependabot config；源码：`.github/workflows/ci.yml` | GitHub [Dependabot配置](https://docs.github.com/en/code-security/dependabot/dependabot-version-updates/configuration-options-for-the-dependabot.yml-file) 为已有平台直接用；自托管 [Renovate](https://github.com/renovatebot/renovate) AGPL-3.0为可选独立工具 | P1 | 落地：每周分组版本PR、限制并发PR、固定actionsSHAs，并锁定实际Pythonextras/dependencies；只对既有文件更新，不自动major/auto-mergeprovider/langgraph/scorer。无源码优越证据不等于一律升latest；fullCI、跨语言proto、scorer/SDKbreakchange定向真实检查；依赖/许可证变化重新审核，失败PR保留；回滚对应lock/pin，data migration另判 |
| P39 | Go/Python协议演进/生成重复：`proto/codeagent/orchestrator.proto`、`scripts/generate-proto.ps1`、Makefile、CI | [Buf CLI](https://buf.build/docs/breaking/) Apache-2.0，直接开发/CI lint+breaking；继续官方protoc生成，不改runtime | P1 | 落地：对现schema设置最小兼容规则和明确baselineSHA，生成后git diff检查；不因使用Buf就依赖收费BSR或上传秘密schema，版本化RPC/事件语义仍本项目自己验证；删除/复用fieldnumber、wire/JSON/sourcebreakage被拦，Go/Pybindings同步编译+跨进程；events/CAS/permissions不是protobuflint能证明。回滚CLI规则不能擅自破坏wirecontract |
| P40 | 开源产品页与开发文档：README、CONTRIBUTING、provider configuration、release gate、design map；源码：`README.md` | [MkDocs](https://www.mkdocs.org/user-guide/configuration/) BSD-2 + [Material公开版](https://github.com/squidfunk/mkdocs-material) MIT首选；[Docusaurus](https://github.com/facebook/docusaurus) MIT只需React/MDX文档交互时备选 | P1/P2 | 落地：先完善现README快速开始/真实UI/功能入口，不再手写网站；文档多后按显式nav构建稳定docssite，excludearchive/runtime/privatepaths，Material特殊付费功能不默认承诺，文档不放modelkey或blockedrunpayload；quickstart新环境可复现、所有local/externallinks有效、移动端搜索、英文/中文概念一致；README稳定事实与Goal/currentevidence分离；回滚docssite保留原MD |

### 跨接口、迁移、部署与补充工具能力

| ID | 优化部分与当前源码接缝 / 缺口 | 首选、备选与许可 / 平台 | 方式 / 优先级 | 落地、验收与回滚 |
| --- | --- | --- | --- | --- |
| X01 | 编辑器接入：`cmd/agent/main.go`、`internal/cli`、`internal/session`、`proto/codeagent/orchestrator.proto` 目前是自有 CLI/HTTP/gRPC 接口，缺跨编辑器 ACP Adapter | [ACP](https://agentclientprotocol.com/protocol/overview)（Apache-2.0）；[coder/acp-go-sdk](https://github.com/coder/acp-go-sdk)（Apache-2.0，Go 1.21，官方社区目录列出的 SDK，不能称官方 Go SDK） | 薄适配 / P1 | 新 stdio surface 只把 initialize/new/load/prompt/update/cancel 映射到同一 Go SessionRunner；客户端给出的权限、文件/terminal 请求仍经 Go 校验。用真实兼容编辑器验证 diff、取消、重连和同 Ledger cursor；不可让编辑器或 SDK 的独立执行器绕过 Harness。回滚停用 ACP surface，已有 CLI/HTTP 和 Ledger 格式不变 |
| X02 | 业务库迁移：`pkg/database/migration.go` 与 `cmd/server/main.go` 同时有 AutoMigrate 和启动时 ALTER；不能据此提供版本化升级/失败恢复说明 | [Goose](https://github.com/pressly/goose)（实际 [MIT LICENSE](https://github.com/pressly/goose/blob/0e5c23df4b93cec4594830b21f66c9ae16d77f75/LICENSE)，支持嵌入 SQL/Go migration）；备选 [golang-migrate](https://github.com/golang-migrate/migrate)（MIT） | 薄适配 / P1 | 对现有用户/文档业务 schema 固定 baseline，后续用版本 migration、备份与可重复执行检查；先在空库/现有库/中途失败副本验证再替代旧启动 DDL。Ledger schema 的 CAS、投影与单向 snapshot import 是独立契约，不能让新 migrator 静默改 Session 语义。回滚用经验证的备份恢复或 forward repair，不在不可逆 DDL 后盲跑 down |
| X03 | 安装负担：`docker-compose.yml`、`scripts/start-interview.ps1`、`cmd/server/main.go` 把核心 Agent 与 MySQL/Redis/MinIO/RAG 启动链耦合；`cmd/agent` 已有本地 Ledger 路径，但这不证明 Web 可无这些服务启动 | [Docker Compose profiles](https://docs.docker.com/compose/how-tos/profiles/) 与 [startup conditions](https://docs.docker.com/compose/how-tos/startup-order/)（Compose 源码 Apache-2.0）；继续用现有 Go/Python 入口 | 继续复用并拆启动职责 / P1 | 分 core、knowledge、observability 启动 profile：第一步把知识模块依赖变为显式可选，使用现有 CLI 展示 core；第二步补 Web identity/元数据 Adapter 后再承诺轻量 Web。用无开发 PATH 的干净 Windows 安装、缺非核心服务、旧数据升级场景验收。回滚默认完整 profile，保留既有卷/数据，不以卸载数据库代替拆分 |
| X04 | 大文件上传与内容身份：`internal/service/upload_service.go`、`internal/repository/upload_repository.go`、`pkg/tasks/tasks.go` 存在自写分块和 MD5 业务键；MD5 的兼容查重不能代替发布/安全完整性 | Go 标准库 `crypto/sha256`、现有 MinIO Go SDK S3 multipart；若以后需要跨客户端续传协议，再评估 [tusd](https://github.com/tus/tusd)（MIT） | 继续复用；tus 条件替换 / P2 | 先用 SHA-256/revision 校验新的证据和对象数据，旧 MD5 只读兼容定位，不批量重写旧 ID；用现有 S3 multipart 减少手写分块 glue。共享对象必须核 owner/文档引用，取消不能删别人的对象。校验掉线续传、并发同内容、校验失败和恢复的完整分母；tusd 只有客户端明确需要 tus 时加，不平添另一个存储/认证服务。回滚保留旧读取 Adapter 与受控对象版本 |
| X05 | 浏览器与远端仓库能力：`internal/extensions`、`internal/mcp`、`internal/tools/executor.go` 的 Browser/LSP 类入口不能因有类型就称已接通真实 adapter；现有 WebFetch 也不等于浏览器 | [Playwright MCP](https://github.com/microsoft/playwright-mcp)（Apache-2.0；其 README 对代码 Agent 还建议评估 CLI + Skills 的 token 成本）、[GitHub MCP Server](https://github.com/github/github-mcp-server)（MIT，Go） | 受控工具薄适配 / P1 | 前端代码任务需要交互浏览器时接 pinned Playwright server；GitHub repo/PR/CI 首先选读能力，凭据只来自批准环境。MCP 目录只注入当次所需 schema；Go 校验 server pin、工具权限、目标 URL/owner/工作区和参数，外部 server 采用受约束进程/隔离 profile。原权限不够就 BLOCKED，不能以 MCP 转发掩盖未约束的本地文件或网络副作用。验证真正工具调用、取消、server crash、未授权目标拒绝和 trace；回滚撤掉对应 server，保留原生 Git/已有工具 |

## 应优先处理的源码问题

这些是源码中能定位的行为或缺口；本次没有运行漏洞利用、真实模型或质量对照。
在实施前先补最小复现，不把静态疑点直接称为已验证的生产漏洞。

1. `internal/tools/safe_url.go` 的预查 IP 与后续按 hostname 拨号不是同一次解析，
   需要复现 DNS 变化场景，改为对经校验的具体 IP 建连并保留 TLS hostname 校验。
2. 命令 Hook 把工具 payload 插入 host shell 模板；应区分受信任命令与不受信任数据，
   优先 argv/stdin，不以泛化自动授权规则掩盖注入或 workspace 问题。
3. CLI 旧 undo 与 canonical restore 的 preflight/hash/link 检查不同；先收口本仓
   已有更强入口，覆盖空原文件、人工修改、链接、半完成恢复。
4. 前端手写 Markdown 与全量事件渲染、占位操作、启动器固定机器路径和忽略退出码，
   都先于新主题、动画或另造客户端平台。
5. 自定义 token/成本/DCG 计算不能冒充真实 tokenizer/provider usage/官方 nDCG。
   修正指标时版本化口径，保留旧基线与全部失败，不偷偷重算成更好的成绩。
6. Headless/TB 独立 Python 执行器和只返回说明的 scorer 接口不能代表 Go 产品。
   为所有评测建立同一个真实 CLI/HTTP Agent Adapter，再收集官方结果。

## 许可、模型与维护状态先决项

本仓 Apache-2.0 并不要求每个依赖也叫 Apache-2.0；MIT、BSD、ISC 等依赖应按各自
许可保留署名与 NOTICE。库、服务端、商业功能、模型权重和基座模型分别核对。

- 当前默认 Jina reranker 权重标为 CC-BY-NC-4.0；先审实际使用和分发范围，
  商业场景选择许可合适的 BGE reranker 候选并重跑检索对照。embedding 库的
  Apache-2.0 不改变权重许可。
- MinIO 服务端上游已归档、为 AGPL-3.0；MinIO Go SDK 仍是独立许可的活跃库。
  优先设计 S3 兼容切换/备份验证，不能删除既有 bucket、卷和业务数据。
- 现有 Redis 7.2 与当前 Redis 8 的许可不同；升级时不要照搬旧 BSD 结论。
  Valkey 是保留 RESP 客户端的候选，数据和 Lua/TTL/事务语义仍需验证。
- Phoenix 的 ELv2、MinerU 当前附加条款、LiteLLM enterprise、Lucide 的继承
  NOTICE、axe 的 MPL-2.0、商业 Virtuoso Message List，均不能并入一张
  “所有依赖 Apache-2.0”的声明。
- 最新 LangGraph/DeepAgents 与本仓 `<1` LangChain/core 约束不同；先保留现有
  graph 接缝。`sqlite-vec` 也不是当前 modernc Ledger 驱动的直接插件。
- GitHub archived 还要区分停止维护与迁移托管地址：CodeMirror 官方迁站不能被
  误判为产品死亡；新增依赖使用当前官方发行源并核对版本。

具体许可源和 upstream 定位在各表及下文来源索引。调查时的维护状态不是 SLA。

## 最快的实施顺序

这是工程切片顺序，不是工期承诺。完成时间取决于已有服务、模型硬件、权限与数据量；
每一片结束都应能演示产品行为并独立回滚，只有通过的切片才进入下一次部署。

| 切片 | 首批改造 | 可检查的完成条件 | 暂不引入的复杂度 |
| --- | --- | --- | --- |
| 0：收口风险与计量 | SSRF/Hook/undo 最小复现与共享入口；reranker/服务许可与版本；统一真实产品评测 Adapter；版本化 token/成本/IR 口径 | Go/CLI/HTTP 不绕过授权；旧数据恢复拒绝不确定覆盖；评测实际启动 Go 产品；许可/模型 revision 清单可核对 | 不整体重写 Go Harness、Ledger、Python graph，不把全部候选安装进来 |
| 1：可展示的代码产品 | 现有 React 上替换 Markdown 和长列表部件，接 diff/editor；修实无响应操作；打包已有 Go/Python；去启动器机器路径和静默失败 | 干净 Windows 安装可启动，界面能选仓库、处理真实代码任务、查看工具/diff、确认权限、取消、恢复；先真实 10 轮，再完整 200-turn/reconnect 门禁 | Wails 仅在确实需要原生窗口时加入；优先沿用浏览器界面与 Go 静态资源 |
| 2：通用底层减负 | Go MCP SDK Adapter、ripgrep/ignore；官方 provider SDK；Aider/tree-sitter 摘要策略；LSP 与 ACP surface | 每个新 Adapter 覆盖原调用方、取消/超时/错误/畸形输入，路径/权限/pin 检查仍由 Go 执行；固定任务对照显示收益且无结果回退 | 不搬 Pi/DeepSeek/Codex 的 Node/Rust 执行拓扑，不让 Python MCP 接管副作用 |
| 3：记忆与可观测性 | 现有 Ledger reflection 提案闭环；派生向量 Adapter；精确 tokenizer；OTel trace join/readback 与标准指标 | 真实反思写回/重启召回/来源 revision 校验；同任务同模型预算比较质量与 token；实际 Go/Python/MCP/child 处于可核验 trace 树 | 已有 Elasticsearch 能满足派生索引时先复用；Qdrant/pgvector 为替代候选，不并行部署多套权威 Memory |
| 4：完整运行与发布证据 | Playwright 真实长会话、Toxiproxy 与真实 crash/Git restore；40 Skills、8/200/30 Workflow；pinned TB/Harbor/SWE/IR 官方 scorer；SBOM 与依赖扫描 | 收据绑定当前允许源码 SHA、完整失败分母、模型身份、命令/退出码、trace/run、artifact SHA-256；现有 release gate 真正通过后才允许发布 | 不用 fixture、第三方 agent 分数、connected/healthz 或成功子集填满门禁 |
| 5：按负载选择重服务 | 仅在前面测出瓶颈后替换 MinIO/S3、Redis/Valkey、ES/OpenSearch/Qdrant、Kafka KRaft、Temporal 或 PostgreSQL | 迁移副本对照、访问控制、兼容查询、吞吐/p95/p99、恢复和备份均通过，运维成本可接受 | 没有瓶颈和收益证据时不新增 Kubernetes、分布式 scheduler、另一套数据库或 Memory 框架 |

首批依赖从上表实际切片选取；每类只能有一个活跃写入/执行实现。对向量、全文、
桌面、UI 状态库和调度器的备选不要同时引入。大型服务替换不是快速落地的第一步。

## 每项迁移的共同验收与回滚

1. 找全 CLI、HTTP、后台 continuation、child/workflow、Hook/MCP 的调用方。
   修改者从现有 Interface 切入，补对应测试和配置，不新造横跨两侧的事实协议。
2. 固定依赖版本与许可、运行镜像 digest、模型/维度/分词器 revision；秘密只由
   授权环境提供，receipt/log/trace 不携带原值，跨进程并发与 SDK retry 不叠乘超限。
3. 先单向 Adapter：旧格式只读兼容，新事实只经 Go 提交。可并行比较派生查询，
   不允许两个 executor 对同一工作树执行，不允许两个 store 提交 Session/Memory。
4. 用同任务、同模型、同初始仓库、同预算、同硬件记录当前/候选两个完整实验臂。
   性能、语义、成本、代码测试结果和故障恢复分别比较；不把吞吐高解释为质量更好。
5. 定向测试、相关完整回归、真实跨进程/浏览器/代码任务、官方 scorer 逐级进行。
   未有新鲜证据就保持 IMPLEMENTED/BLOCKED；报告中的选型只保持 DESIGNED。
6. 首先回滚路由或禁用新 Adapter；派生索引可重建。权威账本、业务库与对象数据
   保留经验证备份，无法确定已执行副作用时进入对账，不重复执行或覆盖用户修改。

本次未修改生产源码，未执行这些迁移；最终依赖与软件分发清单以实际选中的兼容
版本为准，仍需真实产品和固定任务对照验证，不能从这份资料清单直接推导“生产级”。



## 活跃源码目录覆盖索引

每个目录组都有对应清单 ID。表中只统计该目录组的 Git 跟踪代码/测试/配置文件，
不统计其生成运行树，不用文件数量论证质量。`orchestrator` / `scripts` / `eval`
三行表示该目录直接包含的入口文件，子目录另列。

| 活跃目录组 | 跟踪文件数 | 清单 ID |
| --- | ---: | --- |
| `cmd/agent` | 1 | H02 / H09 / H19 / H28 / H33 / H35 / X01 |
| `cmd/cli-preview` | 2 | H26 / H27 / P04 / P11 |
| `cmd/corpus-loader` | 1 | O39 / O40 |
| `cmd/launcher` | 1 | P17 / P18 / P19 / P20 / X03 |
| `cmd/server` | 7 | H01 / H04 / H32 / H34 / H35 / O23 / O40 / X02 / X03 |
| `cmd/skills-manifest` | 4 | H20 / O15 / P30 |
| `deployments/embedding` | 1 | O31 / P21 |
| `deployments/reranker` | 1 | O32 / P21 |
| `eval` | 11 | P25 / P26 / P27 / P29 / P30 / P37 |
| `eval/benchmarks` | 16 | P25 / P26 / P27 / P28 / P31 / P32 |
| `eval/contamination` | 2 | P33 |
| `eval/datasets` | 2 | P34 / O39 |
| `eval/harness` | 24 | P14 / P15 / P22 / P23 / P25 / P29 / P30 / P33 / P34 / P37 |
| `eval/rag` | 3 | P31 / P32 |
| `eval/retrieval` | 3 | P31 / P32 |
| `eval/scripts` | 5 | P25 / P26 / P27 / P28 / P34 |
| `eval/swebench_work` | 19 | P25 / P26 / P27 |
| `frontend/src` | 16 | P01 / P02 / P03 / P04 / P05 / P06 / P07 / P08 / P09 / P10 / P11 / P12 / P14 |
| `frontend/tests` | 6 | P12 / P13 / P14 / P15 / P36 |
| `internal/cli` | 35 | H09 / H19 / H25 / H26 / H27 / H28 / H33 / H35 / H39 / X01 |
| `internal/config` | 4 | H05 / H21 / H35 / O14 / X03 |
| `internal/corpus` | 11 | O39 / O40 / P34 |
| `internal/extensions` | 2 | H15 / H18 / O16 / X05 |
| `internal/handler` | 23 | H01 / H02 / H03 / H04 / H34 / O23 / O40 |
| `internal/hooks` | 2 | H19 / H18 / O07 |
| `internal/identity` | 2 | H02 / H03 / H05 / O20 |
| `internal/jobs` | 4 | H21 / H23 / H24 / H25 / O05 |
| `internal/mcp` | 3 | H16 / H17 / O16 / X05 |
| `internal/memory` | 10 | O17 / O18 / O19 |
| `internal/metrics` | 1 | H39 / P24 / O12 |
| `internal/middleware` | 8 | H01 / H02 / H03 / H05 / O23 |
| `internal/model` | 21 | H02 / H31 / H36 / O40 |
| `internal/orchestrator` | 6 | H32 / H33 / H34 / O01 / O20 |
| `internal/permission` | 3 | H05 / H06 / H19 / H40 |
| `internal/pipeline` | 25 | O23 / O26 / O27 / O31 / O35 / O36 / O37 / O38 / O39 / X04 |
| `internal/prompts` | 1 | O07 / O14 |
| `internal/rag` | 3 | O23 / O24 / O25 / O26 / O35 / O40 |
| `internal/recovery` | 1 | H33 / O04 |
| `internal/repository` | 12 | H36 / O36 / O38 / O40 / X02 / X04 |
| `internal/safety` | 2 | H06 / H07 / H09 / H40 |
| `internal/sandbox` | 9 | H21 / H22 / H23 / H24 / H40 |
| `internal/serverconfig` | 3 | H35 / O14 / P21 / X03 |
| `internal/service` | 22 | H36 / O18 / O23 / O25 / O26 / O29 / O32 / O35 / O38 / O40 / X04 |
| `internal/session` | 36 | H04 / H09 / H10 / H29 / H30 / H31 / H33 / H39 / O08 / O10 / O11 / O13 / O17 / O20 |
| `internal/skills` | 7 | H20 / O15 / P30 |
| `internal/telemetry` | 4 | H39 / P22 / P23 |
| `internal/todo` | 1 | O20 / H28 |
| `internal/tools` | 32 | H06 / H07 / H08 / H10 / H11 / H12 / H13 / H15 / H16 / H17 / H21 / H37 / H38 / X05 |
| `internal/undo` | 1 | H09 |
| `internal/worktree` | 3 | H09 / H10 / H21 / H40 / O10 |
| `orchestrator` | 4 | O08 / O10 / O20 / O23 / H32 / H34 |
| `orchestrator/agents` | 5 | O09 / O10 |
| `orchestrator/config` | 3 | O01 / O02 / O14 / H35 |
| `orchestrator/context` | 7 | O08 / O11 / O12 / O13 / O19 |
| `orchestrator/eval` | 14 | P29 / P30 / P31 / P32 / P35 |
| `orchestrator/graph` | 4 | O07 / O08 / O10 |
| `orchestrator/llm` | 7 | O01 / O02 / O03 / O04 / O05 / O06 / O12 |
| `orchestrator/memory` | 3 | O17 / O19 |
| `orchestrator/prompts` | 3 | O07 / O14 / P30 |
| `orchestrator/rag` | 23 | O22 / O23 / O24 / O25 / O26 / O27 / O28 / O29 / O30 / O31 / O32 / O33 / O34 / O35 |
| `orchestrator/recovery` | 2 | O04 / H33 |
| `orchestrator/runtime` | 7 | O05 / O06 / O07 / O08 / O10 / O13 / O16 / O20 |
| `orchestrator/security` | 3 | O21 / O22 |
| `orchestrator/skills` | 6 | O15 / O16 / H20 |
| `orchestrator/todo` | 3 | O20 |
| `orchestrator/workflows` | 5 | O05 / O08 / O09 / P15 / P16 |
| `pkg/database` | 4 | H29 / H36 / O36 / X02 |
| `pkg/documentparser` | 2 | O27 / O29 |
| `pkg/embedding` | 3 | O31 |
| `pkg/es` | 5 | O18 / O25 / O35 |
| `pkg/hash` | 1 | H02 |
| `pkg/kafka` | 3 | O37 |
| `pkg/log` | 1 | H39 |
| `pkg/mineru` | 3 | O27 / O28 |
| `pkg/objectpath` | 1 | H36 / O38 / X04 |
| `pkg/orchestrator` | 7 | H32 / H34 / O23 / O24 |
| `pkg/reranker` | 3 | O32 |
| `pkg/storage` | 1 | H36 / O38 / X04 |
| `pkg/tasks` | 2 | O26 / O37 / X04 |
| `pkg/tika` | 2 | O29 |
| `pkg/token` | 3 | H02 / H03 / H04 |
| `proto/codeagent` | 1 | H32 / P39 / X01 |
| `scripts` | 21 | P14 / P17 / P20 / P21 / P25 / P28 / O27 / O31 / O32 / O39 |
| `scripts/corpus` | 4 | O39 |
| `scripts/eval` | 5 | P25 / P26 / P27 / P28 / P31 / P34 / P35 |
| `scripts/lib` | 2 | P15 / P17 / P20 / P21 |
| `scripts/rag` | 9 | O27 / O29 / O30 / O31 / O33 / O34 / O35 / O39 |

| 附加范围 | 清单 ID / 读取与处理边界 |
| --- | --- |
| `.github/workflows` / `Makefile` | P36 P37 P38 P39 P19 H40 |
| `go.mod` / `go.sum` / `pyproject.toml` / `uv.lock` / `frontend/package.json` / `frontend/package-lock.json` | H29 H32 H35 O01 O07 O08 O14 P13 P36 P37 P38 |
| `docker-compose.yml` / 公开 `configs` | X03 P21 O31 O32 O35 O36 O37 O38 H35 |
| `codeagent` / `gen/codeagentpb` 生成绑定 | H32 P39 O16；仅由协议生成入口更新，不手工替换 |
| `tests`（根 Go/Python、E2E、契约测试） | H40 P12 P13 P14 P15 P25 P30 P31 P35 P36；定向抽样读取，不声称逐个审计 |
| `eval/benchmark_data` / 语料清单 | P25 P26 P34 O39；只核入口与元信息，不读取答案/评测输入正文 |
| `README.md` / `CONTRIBUTING.md` / 活跃 `docs` / `LICENSE` / `NOTICE` | P40 P37；当前证据留在 GOAL，归档不递归读取 |

## 第一方来源与许可定位

来源 SHA 定位本次阅读版本；实际接入应选择兼容发行版并检查选中的文件、依赖、镜像与权重。
以下清单包含继续使用的依赖、替代候选和明确排除的新依赖。归档、缺许可字段或 API
连通性不足均显式说明；有不确定项就不作可分发或运行保证。

### Harness 来源

GitHub API核对repository、defaultbranch、archived、HEAD；下列hash用于调查引用，不代表
已下载／构建／审计整个上游。除明确写“归档”的两项，API `archived=false`。
MCP、Pyright、Goose、Git返回NOASSERTION后读取官方LICENSE/COPYING，避免据元数据猜许可。
Goose/schema迁移由主报告统一，故本表只作已核对参考。

| 上游 | 调查HEAD SHA | 实际许可／特殊状态 |
| --- | --- | --- |
| modelcontextprotocol/go-sdk | `71fd64651789a7bc76b69d84495ca92ab8d1b279` | MIT与Apache2迁移，文档CC-BY4，读取[LICENSE](https://github.com/modelcontextprotocol/go-sdk/blob/71fd64651789a7bc76b69d84495ca92ab8d1b279/LICENSE) |
| BurntSushi/ripgrep | `3fce3b5bb0236da2df6d99672afb8a719642eca7` | MIT或Unlicense双许可，README说明 |
| sharkdp/fd | `14dcd92fb76ca0ebc2e82671a275f67c790d25fc` | MIT／Apache2双许可，根LICENSE两文件 |
| tree-sitter/tree-sitter | `8261cea5e5098ad4b88f234dbaa224a916a34af3` | MIT |
| tree-sitter/go-tree-sitter | `c9492002f76ed75037e3fe6d3bbabb54ed3e1ff5` | MIT；CGO、grammar另审 |
| cel-expr/cel-go | `0d1261777903ef3ff419d0f23cd7de283040a93a` | Apache2，canonical module `cel.dev/cel-go`；旧google链接已移迁 |
| apache/casbin | `5506d7f7e4f0457f794cf8b1886818a385b885dd` | Apache2；GitHub旧casbin/casbinredirect |
| open-policy-agent/opa | `e5cd7da7a905e9acdc5f1dfdbb025b1846ce9eae` | Apache2 |
| mvdan/sh | `9a79a445faf5243da3c26be7d745c2cb17f27823` | BSD-3-Clause，HEADgo1.26；候选兼容v3.12.0 commit `8202166b7d1e3473a7c65eeac53ddbdb55d5b808` go1.23 |
| PowerShell/PowerShell | `163fa2e84a414be3f937f2e75084a9852304c64a` | MIT |
| spf13/cobra | `adbc8813901bba65827259daa8e22ff94ec1f30e` | Apache2 |
| charmbracelet/bubbletea | `96d69d2f7eb182bb311be08fc56ed2c208239aff` | MIT，当前major2 |
| charmbracelet/lipgloss | `6a419c6543d3475a369ef08f6252a2a6f33be733` | MIT |
| charmbracelet/glamour | `f71347d1b2f386d31f36392fc2c905ae338c968c` | MIT |
| creack/pty | `9246436fffe85773950f49dcc73de333da5212e6` | MIT，Unix |
| UserExistsError/conpty | `aff362cbe133d2e0818f6eeaffd66c84957a0cf1` | MIT，Windows，最近push2024-07-09 |
| gin-gonic/gin | `43fe48e8a0f44af783116cdb010725e6bb50255f` | MIT |
| golang-jwt/jwt | `73c870b18e68b6e654b2b03f485aa3c9fab32cea` | MIT |
| golang/crypto | `c3db4df58582058384d318c87f1c912848d8c464` | BSD-3-Clause |
| golang/sys | `b6b8557e8e2974925aec7a136ba0e323eedc5f43` | BSD-3-Clause；用本仓已选版本，不按HEAD升级 |
| golang/go | `3b98eddbcd66230a78c4893f32099b5d3045a334` | BSD-3-Clause；本仓toolchain1.25，标准库引用以当前版本验证 |
| golang/tools | `31c58979aa257a4ccd3da5b6102a5d77cfc8d41b` | BSD-3-Clause，gopls官方server |
| microsoft/pyright | `386cfa1161b63f048a9ea663d84189e69ced33b1` | 根[LICENSE.txt](https://github.com/microsoft/pyright/blob/386cfa1161b63f048a9ea663d84189e69ced33b1/LICENSE.txt) MIT；API NOASSERTION |
| coreos/go-oidc | `c914bd380327a5a3a81403774d1a5d5b73772ce7` | Apache2 |
| dexidp/dex | `05a275057112ba93e17db21f600b756f000f1c21` | Apache2 |
| google/gvisor | `254aeb360ee148130c24b62f0655bc832a647fe0` | Apache2 |
| firecracker-microvm/firecracker | `25c7a8a8699aeefe9ab12c63ab453bd72c6bee8c` | Apache2 |
| gorilla/websocket | `e064f32e3674d9d79a8fd417b5bc06fa5c6cad8f` | BSD-2-Clause |
| jackc/pgx | `288ef0884623dfddbea3848ebda9e61c5e8979d9` | MIT |
| go-gorm/gorm | `b3d3bf219f0283f8e2e985bac509cb643170f729` | MIT |
| spf13/viper | `528f7416c4b56a4948673984b190bf8713f0c3c4` | MIT |
| uber-go/zap | `4892335e05f14bce8a98a69a577fcf3844a42623` | MIT |
| grpc/grpc-go | `39ebc82c352a3302c3d7f748b4359e26689d9187` | Apache2 |
| protocolbuffers/protobuf-go | `0c373ff94f296da8645fbc892f211083b695709e` | BSD-3-Clause |
| bufbuild/buf | `5bf8dcea6c64dde83bdc49d79b66c756125dd19e` | Apache2，CLI与云BSR区分 |
| bmatcuk/doublestar | `a922627438914ff299434fb3410775421571b318` | MIT |
| google/jsonschema-go | `794ce5e429b22f05aee47a62f9151c1e7b54fd22` | MIT |
| santhosh-tekuri/jsonschema | `044d629dc1cb70d1d8ae72bcd650cd16e742946e` | Apache2 |
| cenkalti/backoff | `b1a30d947ca896d37518610255281904b439c619` | MIT；currentbranchv7，选择兼容pin |
| zalando/go-keyring | `66b55cc0c51cd3d8dabc0ac70da30f058b2fe54a` | MIT |
| minio/minio-go | `32e1f32cb176a611dbed3b37c15fda8c2d9ccf36` | Apache2，SDK不代表serverlicense |
| mozilla/readability | `ab4027a8b37669745016869a37a504727992b2ba` | Apache2，需DOM |
| searxng/searxng | `6671d89bede8c9fc108b17bb98916170f5657650` | AGPL-3.0 |
| open-telemetry/opentelemetry-go | `6b3ec1621285c44b321ba65b972f453213461fc6` | Apache2 |
| mattn/go-runewidth | `99da42750b5dcb798c068e8f1817cea66a220864` | MIT |
| clipperhouse/uax29 | `b03477d1fbba89df95a6b55da0a222b2e2228610` | MIT |
| sourcegraph/go-lsp | `f80c5dd31dfd8eddbb6fdf00c2696ecfe8f241fb` | MIT，**归档**，不作新接入首选 |
| go-shiori/go-readability | `5db1dc9836f0dda58b8112d3a93141b25eeeb454` | MIT，**归档**，README推荐Codeberg successor；后继license未核验 |
| git/git | `6de20f6092dcf9bdb1c8efe03db4b70c82b423dd` | [COPYING](https://github.com/git/git/blob/6de20f6092dcf9bdb1c8efe03db4b70c82b423dd/COPYING)以GPLv2为根，独立二进制使用/分发需独立许可清单 |

### 编排与数据来源

GH metadata与指定LICENSE/README/pyproject读取日期2026-10-08。下列 SHA 用于本次资料追溯，不建议直接安装主分支；实施选受支持release并核对其license/依赖。未写archived的条目本次API返回false，已归档条目明确写出。

| Source ID | 官方项目 / 已读资料 | 当前 SHA | 许可核对 / 限制 |
| --- | --- | --- | --- |
| OD01 | [OpenAI SDK](https://github.com/openai/openai-python)、[official Python reference](https://developers.openai.com/api/reference/python) | `9301e319ea33ef28fba380f39a289dedc14652c1` | Apache-2.0；官方异步/SSE/可配置重试，具体transport版本实施时固定 |
| OD02 | [Anthropic SDK](https://github.com/anthropics/anthropic-sdk-python) | `50b78d17a8a73bef97c3884102310344ac00f056` | MIT；SDK不替代Harnesstool授权 |
| OD03 | [Ollama](https://github.com/ollama/ollama)、[embed](https://docs.ollama.com/api/embed) | `e3cddc3e897d8414a60a46e23f5ef3a99be2eb81` | MIT；本地接口/identity适配见已完成`docs/reuse-components.md` |
| OD04 | [llama.cpp](https://github.com/ggml-org/llama.cpp) | `c35b66744f13cb0dcc476af063e112122eee9355` | MIT；具体模型weight/GGUF来源另核 |
| OD05 | [vLLM](https://github.com/vllm-project/vllm)、[installation](https://docs.vllm.ai/en/latest/getting_started/installation/) | `daa9085143d65896ba3fa77635b0b110aab3e585` | Apache-2.0；硬件/OS平台不得用SDK兼容性代替运行验证 |
| OD06 | [LiteLLM LICENSE](https://github.com/BerriAI/litellm/blob/d8c0e2c7153d82234ec46f0231bf9b9714e7d52a/LICENSE) | `d8c0e2c7153d82234ec46f0231bf9b9714e7d52a` | 非enterprise内容MIT；enterprise目录独立许可，GH总标签NOASSERTION |
| OD07 | [go-redis](https://github.com/redis/go-redis) | `760b98dc0b7d8621f09e1d91d7a6b32fe555bebe` | BSD-2-Clause；使用Redis协议不覆盖server许可 |
| OD08 | [Pydantic](https://github.com/pydantic/pydantic)、[model validation](https://docs.pydantic.dev/latest/concepts/models/) | `fcdefa7198c4fc94a22eecf1a5b7926e20b9fc8a` | MIT；本仓库已安装，strict/extra等业务规则仍要配置 |
| OD09 | [Instructor](https://github.com/567-labs/instructor) | `e12f8b49203b0c1f253d27c1e709d0a09b9fc5a8` | MIT；官方jxnl/instructor URL重定向到567-labs |
| OD10 | [PydanticAI](https://github.com/pydantic/pydantic-ai) | `e6eee68add2178c45d0e5603ae57f52b248d0d7d` | MIT；Python>=3.11，选slim特性，不默认fulltools/server |
| OD11 | [DeepAgents](https://github.com/langchain-ai/deepagents)、[customization](https://docs.langchain.com/oss/python/deepagents/customization) | `6a3a12bc5b3ac88aeb091e6c77ba81ad4d6732af` | MIT；最新core1.x，与现仓库core<1冲突，默认FS/shell/store禁止直接启用 |
| OD12 | [LangChain](https://github.com/langchain-ai/langchain) | `1f587e3f4e0b34d67ea83896f74f855ee6ec103f` | MIT；现有textsplitter/retriever已复用，当前主分支非直接升级目标 |
| OD13 | [LangGraph](https://github.com/langchain-ai/langgraph)、[persistence](https://docs.langchain.com/oss/python/langgraph/persistence) | `40a2e6d845054cc0cc17a6a169ca6e7394e5231c` | MIT；最新1.2.14与本项目<1不同，checkpoint与长期store角色不同 |
| OD14 | [Temporal](https://github.com/temporalio/temporal)、[Python developer guide](https://docs.temporal.io/develop/python/) | `d7f7d26196d21622bce2bb120b7d7d10998b9740` | MIT；服务增加运维面，activity结果需回Go权威审计 |
| OD15 | [Aider repomap](https://aider.chat/docs/repomap.html)、[Aider source](https://github.com/Aider-AI/aider) | `5dc9490bb35f9729ef2c95d00a19ccd30c26339c` | Apache-2.0；SHA/官方说明由root并行调查提供，不移植完整Python执行器 |
| OD16 | [tree-sitter](https://github.com/tree-sitter/tree-sitter) | `8261cea5e5098ad4b88f234dbaa224a916a34af3` | MIT；grammar/bindings逐份核，GoCGO或Pythonwheel按Windows目标检查 |
| OD17 | [python-lsp-server](https://github.com/python-lsp/python-lsp-server) | `a3620069a607a63ed52569fde188e9301feae93a` | MIT；LSPserver进程与workspace能力继续由Go限制 |
| OD18 | [tiktoken](https://github.com/openai/tiktoken) | `4e71bbe0c078468e00fefbf94b39849389f346e5` | MIT；不覆盖所有provider tokenizer/framing |
| OD19 | [HF tokenizers](https://github.com/huggingface/tokenizers) | `ff8ae1baabca0707b7fa9f22854652e231a933de` | Apache-2.0；tokenizer文件/权重另核与pin |
| OD20 | [LangMem](https://github.com/langchain-ai/langmem)、[no-store extraction](https://langchain-ai.github.io/langmem/guides/extract_semantic_memories/) | `48e3c11f5bb527282c7d5339c6a87a0b35abccfc` | MIT；前轮已核API，当前metadatafresh；自写adapter仍需取消/来源/预算 |
| OD21 | [Pydantic Settings](https://github.com/pydantic/pydantic-settings) | `5927b441848c330853eac74c82358bc283cd3640` | MIT；仅配置规模触发，避免为几个env字段加依赖 |
| OD22 | [Agent Skills](https://agentskills.io/specification)、[source](https://github.com/agentskills/agentskills/tree/69ef37e9424c0a7ea9dd2293b559e43ec8176379) | `69ef37e9424c0a7ea9dd2293b559e43ec8176379` | 继承前轮已核snapshot：代码AP2/文档CC-BY-4；skills-refdemo不是生产registry |
| OD23 | [LangChain MCP adapters](https://github.com/langchain-ai/langchain-mcp-adapters) | `52a4535f3eb4b98f386836e4d9b8c4cadf99afca` | MIT，**archived=true**；排除新依赖，Go官方MCP路线见Harness报告 |
| OD24 | [Qdrant](https://github.com/qdrant/qdrant)、[hybrid](https://qdrant.tech/documentation/search/hybrid-queries/) | `016542aa5deb6c66380bb137badf73d54f742bde` | Apache-2.0；索引派生，新增服务需明确收益/资源 |
| OD25 | [Qdrant Go client](https://github.com/qdrant/go-client) | `91c010ca97f153d3575d773881c70ff6186c0842` | Apache-2.0；Go过滤与checksum最终校验不交给vectorserver |
| OD26 | [pgvector LICENSE](https://github.com/pgvector/pgvector/blob/f37c13f68b57d2c3472b2214fbcff699d6d34876/LICENSE)、[README](https://github.com/pgvector/pgvector) | `f37c13f68b57d2c3472b2214fbcff699d6d34876` | PostgreSQL许可；GHNOASSERTION不等于无许可；Windows构建要C++工具/或容器 |
| OD27 | [sqlite-vec](https://github.com/asg017/sqlite-vec)、[Go integration](https://alexgarcia.xyz/sqlite-vec/go.html) | `04d28bd21773981e2d266bbf6aa4efbd011eb4f6` | Apache-2.0；CGO/mattn或ncrucesWASM，不直接嫁接modernc Ledger |
| OD28 | [NeMo Guardrails LICENSE.md](https://github.com/NVIDIA-NeMo/Guardrails/blob/9f793de53e432c4c9c765975f5dd54df175fcb6e/LICENSE.md)、[README](https://github.com/NVIDIA-NeMo/Guardrails) | `9f793de53e432c4c9c765975f5dd54df175fcb6e` | 明确SPDXApache-2.0；GHNOASSERTION；第三方models逐份核，两条官网deep-link获取失败不作为能力证明 |
| OD29 | [LLM Guard](https://github.com/protectai/llm-guard) | `168c1034ffdb33837e7ae6fd6a16b80567c1be03` | MIT，**archived=true**；排除为新生产依赖 |
| OD30 | [Presidio](https://github.com/data-privacy-stack/presidio) | `2523c7b74a469270c5c78bb253f140eafca21e31` | MIT；microsoft/presidio官方API重定向；NLPweights另核 |
| OD31 | [FastAPI](https://github.com/fastapi/fastapi) | `f5c6e9b4f9cadc1bf0f9a1fd672e621206b63007` | MIT；现有依赖继续用，不借框架跨越Go安全边界 |
| OD32 | [HTTPX](https://github.com/encode/httpx) | `b5addb64f0161ff6bfe94c124ef76f6a1fba5254` | BSD-3-Clause；明确timeout/trust_env/close，不能因此取消outbound校验 |
| OD33 | [Haystack](https://github.com/deepset-ai/haystack) | `6a76cdc3b3e3fe535745cb17fc389193ff50ea86` | Apache-2.0；只读资料候选，未安装/未与当前RAG对照 |
| OD34 | [OpenSearch](https://github.com/opensearch-project/OpenSearch)、[hybrid RRF](https://docs.opensearch.org/latest/vector-search/ai-search/hybrid-search/index/) | `d741f64531fdf28f854b8ccbe7f4079d49a06d53` | Apache-2.0；nativeRRF/searchpipeline/mapping语义需单独adapter |
| OD35 | [MinerU LICENSE](https://github.com/opendatalab/MinerU/blob/ed50cc15bc2c9bfb00520dadfe61979866e62236/LICENSE.md) | `ed50cc15bc2c9bfb00520dadfe61979866e62236` | **AP2加附加商业阈值、在线服务署名条款**；非纯AP2，实际部署版本/模型另核 |
| OD36 | [PaddleOCR](https://github.com/PaddlePaddle/PaddleOCR) | `dab3fe35379033fdcb2d0e9572fac0b36c9a9ebf` | Apache-2.0代码，weights逐款核；保留PDF MinerU契约 |
| OD37 | [Docling](https://github.com/docling-project/docling)、[formats](https://docling-project.github.io/docling/usage/supported_formats/) | `3181d9fcbb8b7568ceba14b7ed5cd21f66b221e7` | MIT，模型各自许可；Office可以对照，PDF不能按本任务直接换路由 |
| OD38 | [Tika](https://github.com/apache/tika) | `63284c17c0ee28223c4e8b736b49188fb2585ec7` | Apache-2.0；Tika和容器bundledparser各自NOTICE/依赖审计 |
| OD39 | [python-docx](https://github.com/python-openxml/python-docx) | `e45454602b53e8e572b179ccf1c91093ec9f4ed7` | MIT；现有依赖，不另买/复制sourceavailable文档skill |
| OD40 | [python-pptx](https://github.com/scanny/python-pptx) | `278b47b1dedd5b46ee84c286e77cdfb0bf4594be` | MIT；现有结构parser需保notes/coords/asset来源 |
| OD41 | [FastEmbed](https://github.com/qdrant/fastembed) | `539499b855478bcb6a810ac89a047de24c3b1866` | Apache-2.0；模型支持列表不等于权重自由分发 |
| OD42 | [FlagEmbedding](https://github.com/FlagOpen/FlagEmbedding) | `fd1a2bdf69488ffebe0327999d4400d8c8058a0b` | MIT；模型README/HFweights独立许可，BGE-reranker-v2-m3可比较 |
| OD43 | [faster-whisper](https://github.com/SYSTRAN/faster-whisper) | `9fa78645d7913ff9d94d0907a2156526b19b4556` | MIT代码；实际CT2模型revisions与许可另核 |
| OD44 | [colpali-engine](https://github.com/illuin-tech/colpali) | `97487f8871ff4d5d2284411fe61bdcd2cfe99894` | MIT代码；ColQwen与ColPaliweights不同，模型queryprefix随版本改变需固定 |
| OD45 | [Elasticsearch LICENSE](https://github.com/elastic/elasticsearch/blob/dd6423744a34ebef967580685f95ed1fd57cebfd/LICENSE.txt) | `dd6423744a34ebef967580685f95ed1fd57cebfd` | freecode三选AGPL/SSPL/ELv2、其他header另核；不是整个serverAP2 |
| OD46 | [Elastic licensing FAQ](https://www.elastic.co/pricing/faq/licensing) | 文档页面，2026-10-08读取 | defaultdistribution仍ELv2，AGPL新增选项只覆盖规定源码 |
| OD47 | [Elasticsearch Go client](https://github.com/elastic/go-elasticsearch)、[OpenSearch Go client](https://github.com/opensearch-project/opensearch-go) | `f13cda04963bfeb467279a4f1151dbe71d76a90d` / `d060743a2f9a07238f4438dc245f75a34f53db85` | 均Apache-2.0；backend不同，不能只换importpath |
| OD48 | [Redis LICENSE](https://github.com/redis/redis/blob/940a4d72fc7caaed6f853b65b39920ff3fb9ac1f/LICENSE.txt) | `940a4d72fc7caaed6f853b65b39920ff3fb9ac1f` | Redis8 AGPLv3/RSALv2/SSPLv1选一；7.2及更早BSD-3历史许可保留 |
| OD49 | [Valkey](https://github.com/valkey-io/valkey)、[Redis migration](https://valkey.io/topics/migration/) | `ff9481cfbee0ebb3237404705e3e8fc6cf122f8c` | BSD-3-Clause；与RedisOSS7.2兼容；CE7.4+文件格式不可直接搬 |
| OD50 | [Apache Kafka](https://github.com/apache/kafka) | `c553733515e35e636a1a5b3e1d45f773a1d64f02` | Apache-2.0；confluent发行包其他组件不继承此许可 |
| OD51 | [kafka-go](https://github.com/segmentio/kafka-go) | `2e0b3968aa51b16beb4e221876499a6ff816cd91` | MIT；现有SDK继续用，broker/runtime单独pin |
| OD52 | [MinIO](https://github.com/minio/minio)、[LICENSE](https://github.com/minio/minio/blob/7aac2a2c5b7c882e68c1ce017d8256be2feea27f/LICENSE) | `7aac2a2c5b7c882e68c1ce017d8256be2feea27f` | AGPL-3.0，**archived=true**；server能力/升级有维护边界 |
| OD53 | [minio-go](https://github.com/minio/minio-go) | `32e1f32cb176a611dbed3b37c15fda8c2d9ccf36` | Apache-2.0，活跃；SDK不是server许可 |
| OD54 | [SeaweedFS](https://github.com/seaweedfs/seaweedfs) | `0305e837fd2c0df21f622e564ad42638087d4155` | Apache-2.0；有单binary/Windowsrelease和S3mini，生产子集仍需验 |
| OD55 | [DVC](https://github.com/treeverse/dvc) | `56e59829512ff134aa269099a2099587b810b4dd` | Apache-2.0；iterative/dvc官方URL重定向到treeverse |
| OD56 | [tusd](https://github.com/tus/tusd)、[embedGo](https://tus.github.io/tusd/advanced-topics/usage-package/) | `78cc2291823e171b20d915570e3b68f554eab908` | MIT；可embed，默认store/handler不能越过已有ACL/quota |
| OD57 | [openpyxl 官方文档](https://openpyxl.readthedocs.io/en/stable/) | 文档页为3.1.3；本机distribution metadata为3.1.2，不作为生产pin | MIT/Expat；官方提示默认不防某些XML扩张攻击，需核对defusedxml与部署资源限制，未做攻击实验不宣称当前存在可利用缺陷 |

### 模型权重来源

本次从HF第一方 `/api/models` 读取 `sha`/`cardData.license`，并读取Jina与ColPali/base README相应许可说明。model-card标签不是对所有bundled artifact/转换权重的完整许可证审计；实施必须固定实际下载版本并保留LICENSE/NOTICE。

| Source ID | 模型 / 官方来源 | 当前 SHA | 许可/注意 |
| --- | --- | --- | --- |
| MW01 | [BAAI/bge-m3](https://huggingface.co/BAAI/bge-m3) | `5617a9f61b028005a4858fdac845db406aefb181` | MIT；当前已有pin；native1024不是随意重采样1024 |
| MW02 | [jinaai/jina-reranker-v2-base-multilingual](https://huggingface.co/jinaai/jina-reranker-v2-base-multilingual) | `9cfeff2df7d40d1b78e75e5e9cebec92a99813c9` | **CC-BY-NC-4.0**；README明确研究/评估用途，商业需按Jina许可/服务条款核对 |
| MW03 | [BAAI/bge-reranker-v2-m3](https://huggingface.co/BAAI/bge-reranker-v2-m3) | `953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e` | Apache-2.0；不能用同家族gemma/minicpm模型标签代替逐模型审计 |
| MW04 | [vidore/colqwen2-v1.0](https://huggingface.co/vidore/colqwen2-v1.0) | `2b6ac8fb37f46a49e4841e599583d00ae8a20117` | Apache-2.0；multi-vector/queryprefix需固定，GPU/索引成本另验 |
| MW05 | [vidore/colpali-v1.3](https://huggingface.co/vidore/colpali-v1.3)、[base model](https://huggingface.co/vidore/colpaligemma-3b-pt-448-base) | `b5c6dd62125326e6f0b540c1f7b36e901cdc0a11` | adaptercardMIT；basecardGemma，不能宣传整体纯MIT/AP2或忽略base许可 |
| MW06 | [openai/whisper-small](https://huggingface.co/openai/whisper-small) | `973afd24965f72e36ca33b3055d56a652f456b4d` | HF该仓cardApache-2.0；本产品实际faster-whisper转换模型须另核 |
| MW07 | [jinaai/jina-embeddings-v2-base-zh](https://huggingface.co/jinaai/jina-embeddings-v2-base-zh) | `c1ff9086a89a1123d7b5eff58055a665db4fb4b9` | Apache-2.0；不要从Jina品牌推断所有模型都是NC或都是AP2 |
| MW08 | [openai/clip-vit-base-patch32](https://huggingface.co/openai/clip-vit-base-patch32) | `3d74acf9a28c67741b2f4f2ea7635f0aaf6f0268` | 此APIcard无license字段，本次未完成该权重许可核对；不作可分发保证 |

### 产品与交付来源

下面是本次通过官方 GitHub API 查询到的默认分支 SHA、archived 字段，以及实际下载并阅读的 LICENSE 文件（不是只看 README badge）。除显式标注外 `archived=false`。这是来源调查 revision，不是推荐必须使用开发 HEAD；实施时应固定通过验证的发布版本、tarball/package integrity 和传递依赖许可。包/镜像/模型/数据/商业服务许可不由根 LICENSE 自动覆盖。

| 项目 | 源码 SHA | 实际许可文件与结论 |
| --- | --- | --- |
| radix-ui/primitives | `8f8d20ee4e4499c5c8a718fabebcb75203966711` | [LICENSE](https://github.com/radix-ui/primitives/blob/8f8d20ee4e4499c5c8a718fabebcb75203966711/LICENSE)，MIT |
| shadcn-ui/ui | `0132174664c07d41262fb51012d0cc782e458e6c` | [LICENSE.md](https://github.com/shadcn-ui/ui/blob/0132174664c07d41262fb51012d0cc782e458e6c/LICENSE.md)，MIT |
| assistant-ui/assistant-ui | `c46e1a2a321f5839403d9045dffc1867d5c7cb81` | [LICENSE](https://github.com/assistant-ui/assistant-ui/blob/c46e1a2a321f5839403d9045dffc1867d5c7cb81/LICENSE)，MIT；托管服务另计 |
| remarkjs/react-markdown | `f9ca66ec73d218127bff56b41a583bba1fb96125` | [license](https://github.com/remarkjs/react-markdown/blob/f9ca66ec73d218127bff56b41a583bba1fb96125/license)，MIT |
| remarkjs/remark-gfm | `109972e8a773bf5dac1d6d2da0776557f36971aa` | [license](https://github.com/remarkjs/remark-gfm/blob/109972e8a773bf5dac1d6d2da0776557f36971aa/license)，MIT |
| rehypejs/rehype-sanitize | `b3ee205aeda4e0fc276cb6664b2d6d339305fcd6` | [license](https://github.com/rehypejs/rehype-sanitize/blob/b3ee205aeda4e0fc276cb6664b2d6d339305fcd6/license)，MIT |
| shikijs/shiki | `f7d0167873fd676fe4e190bc9b53832fba9ee01d` | [LICENSE](https://github.com/shikijs/shiki/blob/f7d0167873fd676fe4e190bc9b53832fba9ee01d/LICENSE)，MIT；syntax grammar/theme来源仍需package审查 |
| TanStack/query | `aab352876a01f76fd0e00b0b500a85907ec7c8b4` | [LICENSE](https://github.com/TanStack/query/blob/aab352876a01f76fd0e00b0b500a85907ec7c8b4/LICENSE)，MIT |
| pmndrs/zustand | `d7a5583cffd80af515f7dfb69583c95cbdc9e2ce` | [LICENSE](https://github.com/pmndrs/zustand/blob/d7a5583cffd80af515f7dfb69583c95cbdc9e2ce/LICENSE)，MIT |
| petyosi/react-virtuoso | `b2a02d01de488dbf1e747c7a91151eeca6e13c72` | [packages/react-virtuoso/LICENSE](https://github.com/petyosi/react-virtuoso/blob/b2a02d01de488dbf1e747c7a91151eeca6e13c72/packages/react-virtuoso/LICENSE)，MIT；同包package.json声明React19兼容。根/license API返回404不代表包无许可；商业message-list见下文 |
| microsoft/monaco-editor | `fdf1ee75a63b85433a591db4e1184022cad8483b` | [LICENSE.txt](https://github.com/microsoft/monaco-editor/blob/fdf1ee75a63b85433a591db4e1184022cad8483b/LICENSE.txt)，MIT；不是VSCode全部扩展生态 |
| xtermjs/xterm.js | `c58ea3637f3968e0e6e79cd92cf9aace7ef89ee2` | [LICENSE](https://github.com/xtermjs/xterm.js/blob/c58ea3637f3968e0e6e79cd92cf9aace7ef89ee2/LICENSE)，MIT |
| lucide-icons/lucide | `a04f228cd01185e09c188b7227b9600c08c565ec` | [LICENSE](https://github.com/lucide-icons/lucide/blob/a04f228cd01185e09c188b7227b9600c08c565ec/LICENSE)，ISC+列名Feather衍生图标MIT；API SPDX为NOASSERTION，实际文本已核 |
| dip/cmdk | `dd2250ed608443e8f32bafc5fa2d1d07a3746aa3` | [LICENSE.md](https://github.com/dip/cmdk/blob/dd2250ed608443e8f32bafc5fa2d1d07a3746aa3/LICENSE.md)，MIT；pacocoursey/cmdk已重定向 |
| bvaughn/react-resizable-panels | `f4c06add8848836e65cbbd20a02f8de0d6be4618` | [LICENSE.md](https://github.com/bvaughn/react-resizable-panels/blob/f4c06add8848836e65cbbd20a02f8de0d6be4618/LICENSE.md)，MIT |
| dequelabs/axe-core | `cfd2ea6004e681a97eaca8bedd3add1c7219c8e9` | [LICENSE](https://github.com/dequelabs/axe-core/blob/cfd2ea6004e681a97eaca8bedd3add1c7219c8e9/LICENSE)，MPL-2.0 |
| dequelabs/axe-core-npm | `839553f276f251051509229146d6339a2a232214` | [LICENSE](https://github.com/dequelabs/axe-core-npm/blob/839553f276f251051509229146d6339a2a232214/LICENSE)，MPL-2.0，含playwright集成 |
| testing-library/react-testing-library | `20ce75f2907ca0e5c5a8ae595c0e9a4e368c7800` | [LICENSE](https://github.com/testing-library/react-testing-library/blob/20ce75f2907ca0e5c5a8ae595c0e9a4e368c7800/LICENSE)，MIT |
| mswjs/msw | `a32ee31cfbc4e2bd1a023ff0d8cd1f47f8510b31` | [LICENSE.md](https://github.com/mswjs/msw/blob/a32ee31cfbc4e2bd1a023ff0d8cd1f47f8510b31/LICENSE.md)，MIT |
| vitest-dev/vitest | `3e3624c5e5ecf77b20065da1aeb60f1182db0ccd` | [LICENSE](https://github.com/vitest-dev/vitest/blob/3e3624c5e5ecf77b20065da1aeb60f1182db0ccd/LICENSE)，MIT |
| locustio/locust | `5187530e5d3dc55fb3f6ddf451872035ada56481` | [LICENSE](https://github.com/locustio/locust/blob/5187530e5d3dc55fb3f6ddf451872035ada56481/LICENSE)，MIT |
| wailsapp/wails | `1a6053d859ed3f060a2bea2f1749ad579c19f88c` | [LICENSE](https://github.com/wailsapp/wails/blob/1a6053d859ed3f060a2bea2f1749ad579c19f88c/LICENSE)，MIT；Wailsv2文档说明Windows无CGO/外部DLL要求、使用WebView2；不要把Linux/macOS平台依赖推作同结论 |
| goreleaser/goreleaser | `1942d44355492f44cd3774c57bd3c27fe544dfe9` | [LICENSE.md](https://github.com/goreleaser/goreleaser/blob/1942d44355492f44cd3774c57bd3c27fe544dfe9/LICENSE.md)，MIT OSS；Pro独立 |
| go-task/task | `871da1618956bcd45bd1156294df2dcae2bd075b` | [LICENSE](https://github.com/go-task/task/blob/871da1618956bcd45bd1156294df2dcae2bd075b/LICENSE)，MIT |
| kardianos/service | `99070899946d7ab341109f83b7c9fb941a118be0` | [LICENSE](https://github.com/kardianos/service/blob/99070899946d7ab341109f83b7c9fb941a118be0/LICENSE)，Zlib |
| golang/go | `3b98eddbcd66230a78c4893f32099b5d3045a334` | [LICENSE](https://github.com/golang/go/blob/3b98eddbcd66230a78c4893f32099b5d3045a334/LICENSE)，BSD-3-Clause；项目已有Go1.25，优先现installedstd接口 |
| docker/compose | `7acb2b9f2587534e240cc707f67d5a0da679c363` | [LICENSE](https://github.com/docker/compose/blob/7acb2b9f2587534e240cc707f67d5a0da679c363/LICENSE)，Apache-2.0；DockerDesktop订阅条款另核 |
| open-telemetry/opentelemetry-collector | `b812929ad700144753c13c352549d62eb047aeb9` | [LICENSE](https://github.com/open-telemetry/opentelemetry-collector/blob/b812929ad700144753c13c352549d62eb047aeb9/LICENSE)，Apache-2.0 |
| open-telemetry/opentelemetry-collector-contrib | `9305b2d7a56f115b601e5672366594436ce5fe10` | [LICENSE](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/9305b2d7a56f115b601e5672366594436ce5fe10/LICENSE)，Apache-2.0；仅启需要的组件 |
| prometheus/prometheus | `21fd457f2b67fe417e181817d814dba812471e9f` | [LICENSE](https://github.com/prometheus/prometheus/blob/21fd457f2b67fe417e181817d814dba812471e9f/LICENSE)，Apache-2.0 |
| prometheus/client_golang | `a4329cfb379b666a2d895da149cb2c98df181e01` | [LICENSE](https://github.com/prometheus/client_golang/blob/a4329cfb379b666a2d895da149cb2c98df181e01/LICENSE)，Apache-2.0 |
| evalplus/evalplus | `26d6d00bb1fd0fa37f39c99d5290da67891d1c5e` | [LICENSE](https://github.com/evalplus/evalplus/blob/26d6d00bb1fd0fa37f39c99d5290da67891d1c5e/LICENSE)，Apache-2.0；评测数据/镜像传递内容另核 |
| sierra-research/tau2-bench | `4ce7c0397c1eb65c9bbe59aeacfe1ca44a1cd699` | [LICENSE](https://github.com/sierra-research/tau2-bench/blob/4ce7c0397c1eb65c9bbe59aeacfe1ca44a1cd699/LICENSE)，MIT；现repo已描述tau3，不等于旧pin可升 |
| UKGovernmentBEIS/inspect_ai | `69ee9ba020a6ecc8c2e76a5a3265ec80d32aab02` | [LICENSE](https://github.com/UKGovernmentBEIS/inspect_ai/blob/69ee9ba020a6ecc8c2e76a5a3265ec80d32aab02/LICENSE)，MIT |
| promptfoo/promptfoo | `421e7959642c5d4cc1c983259a268de1c6f847b9` | [LICENSE](https://github.com/promptfoo/promptfoo/blob/421e7959642c5d4cc1c983259a268de1c6f847b9/LICENSE)，MIT；企业服务另计 |
| AmenRa/ranx | `7363db0c35e92e90d6fa6fe73907b760678f765e` | [LICENSE](https://github.com/AmenRa/ranx/blob/7363db0c35e92e90d6fa6fe73907b760678f765e/LICENSE)，MIT；[实际ndcg源码](https://github.com/AmenRa/ranx/blob/7363db0c35e92e90d6fa6fe73907b760678f765e/ranx/metrics/ndcg.py)已核discount/gain |
| beir-cellar/beir | `ef83d29307061c65d04b035b4f4e7c18bd8374af` | [LICENSE](https://github.com/beir-cellar/beir/blob/ef83d29307061c65d04b035b4f4e7c18bd8374af/LICENSE)，Apache-2.0；各数据集独立许可 |
| project-miracl/miracl | `fa3a57c89ad8f61f0a02d8c27167d8141cfd77ca` | [LICENSE](https://github.com/project-miracl/miracl/blob/fa3a57c89ad8f61f0a02d8c27167d8141cfd77ca/LICENSE)，Apache-2.0；数据另核 |
| xlang-ai/BRIGHT | `d99e8391d967d4c2b3a74732530d2309e2fc92b6` | [LICENSE](https://github.com/xlang-ai/BRIGHT/blob/d99e8391d967d4c2b3a74732530d2309e2fc92b6/LICENSE)，CC-BY-4.0，保留署名，不能全仓改标Apache |
| illuin-tech/vidore-benchmark | `a70f23af8bb3b33efe8a4a6c6c15a6e2d978035e` | [LICENSE](https://github.com/illuin-tech/vidore-benchmark/blob/a70f23af8bb3b33efe8a4a6c6c15a6e2d978035e/LICENSE)，MIT；CohereLabs/vidore-benchmark请求404，已修正owner |
| ekzhu/datasketch | `ee60290e982be00b6f0a6aea8156e2be4d6921e6` | [LICENSE](https://github.com/ekzhu/datasketch/blob/ee60290e982be00b6f0a6aea8156e2be4d6921e6/LICENSE)，MIT |
| treeverse/dvc | `56e59829512ff134aa269099a2099587b810b4dd` | [LICENSE](https://github.com/treeverse/dvc/blob/56e59829512ff134aa269099a2099587b810b4dd/LICENSE)，Apache-2.0；iterative/dvc请求重定向，remote服务许可独立 |
| HumanSignal/label-studio | `f6895d41ce88ae59961d59f414a7bbef55253310` | [LICENSE](https://github.com/HumanSignal/label-studio/blob/f6895d41ce88ae59961d59f414a7bbef55253310/LICENSE)，Apache-2.0 Community；enterprise另核 |
| astral-sh/ruff | `a905ba48e0e5c43611aa1b7922f6f3727c38e823` | [LICENSE](https://github.com/astral-sh/ruff/blob/a905ba48e0e5c43611aa1b7922f6f3727c38e823/LICENSE)，MIT |
| dominikh/go-tools | `6cb65e58a558452b52f57cb43267ff9df669a77a` | [LICENSE](https://github.com/dominikh/go-tools/blob/6cb65e58a558452b52f57cb43267ff9df669a77a/LICENSE)，MIT |
| rhysd/actionlint | `011a6d15e749bb3f2d771eed9c7aa0e7e3e10ee7` | [LICENSE.txt](https://github.com/rhysd/actionlint/blob/011a6d15e749bb3f2d771eed9c7aa0e7e3e10ee7/LICENSE.txt)，MIT；reviewdog/actionlint请求404已修正 |
| gitleaks/gitleaks | `b58d3f102cf3a2c84cb7f923d05c25c9b1aed84b` | [LICENSE](https://github.com/gitleaks/gitleaks/blob/b58d3f102cf3a2c84cb7f923d05c25c9b1aed84b/LICENSE)，MIT CLI；[官方Action许可说明](https://gitleaks.io/)另核，优先直接二进制 |
| golang/vuln | `709015412431dd2b5b28a53c06c70bc02d49074c` | [LICENSE](https://github.com/golang/vuln/blob/709015412431dd2b5b28a53c06c70bc02d49074c/LICENSE)，BSD-3-Clause工具；Go漏洞DB为CC-BY4.0 |
| pypa/pip-audit | `828e77a4d4aa6bee8d315681db6928771d951a7c` | [LICENSE](https://github.com/pypa/pip-audit/blob/828e77a4d4aa6bee8d315681db6928771d951a7c/LICENSE)，Apache-2.0 |
| aquasecurity/trivy | `3f664e55b7b3f636f29d82f38340fd899c96fedc` | [LICENSE](https://github.com/aquasecurity/trivy/blob/3f664e55b7b3f636f29d82f38340fd899c96fedc/LICENSE)，Apache-2.0 |
| anchore/syft | `546c872c9d942216d9ddc0da4909cb06fcac356c` | [LICENSE](https://github.com/anchore/syft/blob/546c872c9d942216d9ddc0da4909cb06fcac356c/LICENSE)，Apache-2.0 |
| sigstore/cosign | `e2d25b54f768bdaa868318651e453b7bbc53c0ce` | [LICENSE](https://github.com/sigstore/cosign/blob/e2d25b54f768bdaa868318651e453b7bbc53c0ce/LICENSE)，Apache-2.0 |
| renovatebot/renovate | `d071998a8d43e500060f7506ad288840f8ea291b` | [license](https://github.com/renovatebot/renovate/blob/d071998a8d43e500060f7506ad288840f8ea291b/license)，AGPL-3.0；作为独立工具，不vendor为Apache代码 |
| bufbuild/buf | `5bf8dcea6c64dde83bdc49d79b66c756125dd19e` | [LICENSE](https://github.com/bufbuild/buf/blob/5bf8dcea6c64dde83bdc49d79b66c756125dd19e/LICENSE)，Apache-2.0 CLI；BSR服务另计 |
| mkdocs/mkdocs | `2862536793b3c67d9d83c33e0dd6d50a791928f8` | [LICENSE](https://github.com/mkdocs/mkdocs/blob/2862536793b3c67d9d83c33e0dd6d50a791928f8/LICENSE)，BSD-2-Clause |
| squidfunk/mkdocs-material | `6d3dc570d51064a3f55d189bd22c2390b07d46fe` | [LICENSE](https://github.com/squidfunk/mkdocs-material/blob/6d3dc570d51064a3f55d189bd22c2390b07d46fe/LICENSE)，MIT公开源码；不承诺特殊付费功能 |
| facebook/docusaurus | `021ee05058b2df769306dd717246bd5967f4845a` | [LICENSE](https://github.com/facebook/docusaurus/blob/021ee05058b2df769306dd717246bd5967f4845a/LICENSE)，MIT |

#### 已由上一轮同日研究核验，直接复用来源

- [Playwright LICENSE](https://github.com/microsoft/playwright/blob/4357c237cfde9135fb5b7894c22a45468321a973/LICENSE)，Apache-2.0。
- [Toxiproxy LICENSE](https://github.com/Shopify/toxiproxy/blob/f83c9865e568ddd795f00076b58b0ba0e7df1146/LICENSE)，MIT。
- [Harbor LICENSE](https://github.com/harbor-framework/harbor/blob/4b94505a91c5ddcb70b5740ac95718ddee13e5a0/LICENSE)，Apache-2.0；[旧 Terminal-Bench LICENSE](https://github.com/harbor-framework/terminal-bench-1/blob/d28711d0da2675d0bb1d56de45ae5df6082438a3/LICENSE)，Apache-2.0。
- [SWE-bench LICENSE](https://github.com/SWE-bench/SWE-bench/blob/02e7a74ffd0b707aab73d203fe87bdc7c76afc8e/LICENSE)，MIT；最新版scorer结果路径兼容问题沿用 `docs/reuse-components.md`。
- [Jaeger官方Apache-2.0许可](https://github.com/jaegertracing/jaeger/blob/main/LICENSE) 与 [API文档](https://www.jaegertracing.io/docs/latest/architecture/apis/)；[Phoenix许可说明](https://arize.com/docs/phoenix/self-hosting/license)是ELv2。[OpenInference仓库](https://github.com/Arize-ai/openinference) Apache-2.0，与Phoenix服务器分开看。本分支调查未另生成这三个项目的新HEAD快照，实施时再pin具体发行。

#### 排除或需要单独核实的边界

- [`@virtuoso.dev/message-list` 商业许可](https://virtuoso.dev/virtuoso-message-list/licensing/)与MIT `react-virtuoso` 分开，不能因同站点把前者放入免费开源依赖。
- `codemirror/dev` 在GitHub快照 `c010426d06689a7115aa9df08425126b6d3ead2f` 为 `archived=true`；`codemirror/view` `fbff59ba004d80d8c914f64c42586387b08706ac` 同样归档。官方[README](https://github.com/codemirror/dev/blob/c010426d06689a7115aa9df08425126b6d3ead2f/README.md)明确迁至 `code.haverbeke.berlin`，这是迁源，不是“项目无人维护”的证据。此处未核新站点HEAD，所以首选当前核验active的Monaco；如因bundle选择CodeMirror，应核新源包许可与锁定integrity，不clone归档开发脚本。
- 公开Apache-2.0仓库不能把CC-BY、MPL、AGPL或ELv2材料统一重标Apache。外部独立工具通常无需复制源文件，分发库/镜像仍要保留其适用许可；特定用法的义务应在引入前审核。
- Wails可解决WebView壳和assets；不能自动把整个Python环境、Docker后端、模型和OS权限一起变成独立单EXE。GoReleaser checksum/Cosign验证不等于Windows Authenticode签名，也不等于release gate通过。
- 本次未抓取ScreenSpot数据/annotation正文，没有依据GitHub `likaixin/ScreenSpot-Pro` 返回404就判定HF dataset不可用；该数据许可和scorer身份须走现manifest的独立审核。不能给新领域虚构“最佳开源GUI执行工具”替本编码Agent目标。

### 跨接口补充来源

| 项目 | 调查 HEAD | 许可 / 状态 |
| --- | --- | --- |
| ACP spec | `5ed386e81033918fe5e3a0503beaa6030280666e` | Apache-2.0，active |
| coder/acp-go-sdk | `0845a3bb9eddda5bfc22a94dd3598c90cb842451` | Apache-2.0，active，Go 1.21 |
| pressly/goose | `0e5c23df4b93cec4594830b21f66c9ae16d77f75` | MIT，active；GH 自动识别 NOASSERTION，已读 LICENSE |
| golang-migrate/migrate | `504568a3cbd23b8754760f55a3d89aec1b0c4963` | MIT，active；GH 自动识别 NOASSERTION，已读 LICENSE |
| Docker Compose | `7acb2b9f2587534e240cc707f67d5a0da679c363` | Apache-2.0，active |
| tusd | `78cc2291823e171b20d915570e3b68f554eab908` | MIT，active |
| Playwright MCP | `a6d7678b7bc10d9fb2ae828a103e9872cf75e483` | Apache-2.0，active |
| GitHub MCP Server | `eb47a99ddb866ca2b8a162920e6bda9521f33ebb` | MIT，active |

## 本次交付检查与状态

这份报告与 CSV 只交付调查和迁移设计，状态为 `DESIGNED`。路径、清单、目录覆盖、
来源定位、差异格式和秘密形状检查采用本次实际输出核对；产品源码未改，原有
`docs/GOAL.md`、发布门禁和用户改动保留。检查命令、退出码、文件哈希随交付说明记录。

未运行候选组件、权重、provider、服务迁移、真实浏览器/代码任务或官方 scorer；
也没有逐行审计全部源文件或完成全部传递依赖许可审核。Windows ABI、硬件成本、
语义质量、具体分发许可和存量数据迁移要在相应实施切片验证。来源调查成功不改变
现有 runtime 与发布 `BLOCKED`。
