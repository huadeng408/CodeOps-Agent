# Agent 工作约定

## 开始与推进

本文件是仓库工作指令唯一正文；`AGENT.md`、`CLAUDE.md` 仅作入口。
先读本文件、[当前目标](docs/GOAL.md)，执行 `git status --short`，再读任务相关源码、调用方和测试，保留已有改动。
多文件修改先写短计划及可检查的完成条件。按授权直接修复明显 bug 和冗余；继续使用当前 `main` checkout，不另建 worktree 或改写历史。

设计边界按需查 [CONTEXT.md](CONTEXT.md)、[设计导航](docs/DESIGN-MAP.md)和 ADR；正式任务、状态和依赖以 GitHub Issues 为准。
规格／拆票／分流技能分别加载 [Issue 配置](docs/agents/issue-tracker.md)、[标签配置](docs/agents/triage-labels.md)、[领域配置](docs/agents/domain.md)。
历史记录按具体证据链接打开；默认搜索排除 `docs/archive/`，追溯时用明确路径或 `rg --no-ignore`。避免递归加载文档、归档和评测运行树。

## 架构与事实

- Go Harness 强制鉴权、授权、Session Ledger、受约束的文件／Shell／Git／MCP、沙箱、进程生命周期和根 Trace。Python/LangGraph 负责模型策略、Context、计划、Memory、Subagent、Workflow 和评测编排，副作用经 Go 执行。
- 两侧经版本化 protobuf/gRPC、事件 schema 和 TraceContext 交互。协议先改 `proto/codeagent/orchestrator.proto`，用 `make proto` 或 `scripts/generate-proto.ps1` 同步 Go/Python 生成物及编译检查。
- Session Ledger 是唯一可变事实源；Surface、Checkpoint、fork、rewind、compaction 都是投影。旧 SQLite snapshot 只读导入；迁移走“旧调用方 → 单向 Adapter → 新实现”，禁止双写／静默覆盖。删除兼容 Adapter 前验证全部调用方的真实 E2E。
- 优先复用现有模块和接口，保持 Python 可从仓库根目录导入。无法确认权限、沙箱、信任根、pin、凭据或副作用结果时保留 `BLOCKED`／`unknown`，通过原请求／Run 对账。
- PDF 走 MinerU 显式 OCR；Tika 仅处理非 PDF Office 文档。
- 模型常规调用在用户批准、提供方设置的 1 亿 token 硬上限内直接推进，无需逐笔批准，也不检查账单。BeeAPI/OpenAI relay 所有本机进程及 shard 合计并发 1–10，429 仅有界退避。
- Go Ledger 保留确认用量、未知用量及原预留。**已结束调用的计量 unknown 不阻塞新的独立调用或已停止任务结束**；活动调用、防重复派发、工具结果和沙箱围栏仍须满足。用用户批准的真实 API 验证模型能力，绝不虚构零消耗／费用。
- `AI_REVIEWED`、`DISPUTED`、`HUMAN_REVIEWED` 分开记录，最后一项仅用于真实逐条人工复核。评测输入、prompt、receipt 不含答案、gold/qrels、patch 或修复提示；失败实例保留在分母中。

## 选择检查范围

| 修改范围 | 入口与验证 |
| --- | --- |
| Go Harness／Session／工具／权限／沙箱／PTY | 目标 `internal/` 或 `cmd/` 包、调用方及对应测试，核对授权、审计、持久化和副作用 |
| Python 编排／Context／Memory／Provider | 目标 `orchestrator/` 模块及 `tests/`，核对 Go Ledger 边界 |
| 前端 | 相关 `npm test`、`npm run build` 和浏览器交互；新布局或明显视觉改版先提供三个不同方案供选择，普通修复／已选方案直接实施 |
| 评测／性能／产品专项 | 当前票与相关入口，记录真实运行、完整分母和产物；需要 Trace 或模型能力的范围才要求对应真实证据 |
| 完整发布 | [七条 lane 契约](docs/release-gate.md)及 `python -m eval.release_gate --json`，缺必需 trust root／pin／凭据／服务则拒绝发布 |
| 普通文档 | 链接、当前事实、归档校验与 `git diff --check`；按内容影响选择其余检查 |

普通修复按自身范围交付，完整发布、其他专项和旧环境故障不自动成为它的门禁。
先定向回归，再执行受影响范围的完整检查一次；明确失败后保留记录、停止启动后续重检查，修复后重验受影响范围。新失败、输入变化或未解决疑点才扩大／重复验证。
常用入口由 `Makefile`、`pyproject.toml` 和目录配置定义；`go test ./... -count=1`、`go vet ./...`、`python -m pytest -q` 按任务范围使用。Windows 可复用[公共验证入口](docs/engineering-verification.md)。

每项验证记录源码／运行输入、命令、退出码、完整分母、必要产物和 checksum。分别标明单测、集成、产品、发布的通过／失败／未运行／受阻；fixture、mock、smoke、合成 receipt、HTTP200 和源码 CI 不代替真实产品或发布验收。
工程状态用 `DESIGNED`／`IMPLEMENTED`；`VERIFIED` 注明范围。失败与跳过保留，数据退化或静默丢弃失败时拒绝相应验收。
收据复用核对实际参与运行的源码、配置、指令、依赖和输入；指纹相同才保留原结果并追加绑定核验。加载的指令不是普通说明文档；相关内容变化或无法确认时重验受影响范围。旧 SHA、结果、分母和 checksum 不改写。

## 本机运行与秘密

- Docker 新资源统一由 [deployments/docker/](deployments/docker/README.md) 管理，Compose project 为 `localcode`，按 service／run identity 分层；创建前确定停止和回收方式。旧资源、数据、pin 和历史证据按原身份保留。
- Docker Desktop 可隐藏启动；不删除现有容器、volume、索引、业务数据或重拉全部镜像。Windows 测试先 dot-source `scripts/rag-agent-e2e-runtime.ps1` 并调用 `Wait-DockerDaemonReady`，复用 readiness 入口。
- 网络失败或很慢时，仅为该命令设置 `HTTP_PROXY`／`HTTPS_PROXY=http://127.0.0.1:7890` 重试；不修改系统代理。外部脚本先审阅再执行。
- 凭据仅来自进程环境或已批准 secret manager；日志、Trace、fixture、receipt 和测试输出脱敏。`.env`、本机配置、数据库、日志、缓存、运行树和机器路径不进入提交。

## 文档与交付

对外名称为 `CodeOps-Agent`；`code-agent` 仅保留于既有模块、二进制、包名和环境变量。
README 写稳定能力；Goal 只写当前目标、缺口和证据入口；设计导航负责路由。历史记录移入 `docs/archive/`，保留原文和 checksum 并更新索引，不再作为当前执行指令，也不授权同步到仓库外。
提交 README 前检查 `BLOCKED`、`VERIFIED`、`SMOKE_PASS`、run ID、receipt 路径和 prototype／unfinished 描述，将当前状态放回 Goal 或专门证据页。

手工修改使用 `apply_patch`，生成代码只用官方生成命令。提交前审阅精确 staged diff、执行不输出匹配值的秘密扫描、`git diff --cached --check` 和必要验证，排除未审核输入及运行产物。
完成必要验证和审查后，按已授权流程自动提交并推送 `origin/main`，核对远端 SHA、保留无关改动；只有超出授权范围或用户要求逐次审核时等待确认。
交付说明改动、验证命令及退出码、artifact/receipt、范围状态和剩余 blocker。受阻时记录具体边界，再推进同一目标内可执行工作；全部验收满足才结束目标。
删除、数据清空、覆盖用户改动及外部发布须有对应范围授权；优先可恢复归档。冻结运行中保持源码、profile、包和输入不变，已接受／未知请求先对账原 ID。
