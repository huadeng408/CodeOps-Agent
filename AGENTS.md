# Agent 工作约定

## 启动协议

本文件是仓库工作指令的唯一权威正文；`AGENT.md` 和 `CLAUDE.md` 只保留入口链接。
每次仓库任务先按以下顺序读取：

1. 本文件；
2. [`docs/GOAL.md`](docs/GOAL.md)（当前目标、缺口和证据链接）；
3. 与任务直接相关的源码、测试和直接依赖。

需要设计边界时再读取 [`docs/DESIGN-MAP.md`](docs/DESIGN-MAP.md) 及其任务卡；按任务需要扩大读取范围，不设固定文件数量上限。需要历史证据时只打开任务点名的 `docs/archive/` 文件。不要在启动时递归读取 `docs/`、归档目录、评测运行树或生成产物。

开始前执行 `git status --short`，确认工作树中的既有改动并保留它们。多文件改动先写一个短计划，声明验证范围；每个步骤都要有可检查的完成条件。结束时报告修改路径、验证命令及退出码、artifact/receipt、声明范围的当前状态和未解决 blocker。

## 工程技能入口

使用 Matt Pocock 的规格、拆票、分流或设计技能时，按以下分支加载配置：

- **Issue tracker**：发布、读取或拆分工程任务时，使用 GitHub Issues 和本地 Markdown，先读 [`issue-tracker.md`](docs/agents/issue-tracker.md)。
- **Triage labels**：分流任务时，先读 [`triage-labels.md`](docs/agents/triage-labels.md)。
- **Domain docs**：探索领域或设计模块时，使用根目录 `CONTEXT.md` 和 `docs/adr/`，先读 [`domain.md`](docs/agents/domain.md)。

## 事实源与归档

- 本文件是架构、安全、验证和交付的权威指令；当前对话目标与 `docs/GOAL.md` 是验收权威，不能用旧日志覆盖它们。
- `docs/GOAL.md` 只保留当前目标、当前缺口和证据链接。旧测试数量、旧 blocker、实施切片与改造过程移入历史记录，保留原文并更新归档索引；追溯时按证据链接读取。
- `docs/DESIGN-MAP.md` 是当前设计控制面；只有任务卡声明需要时才读取对应的 `read_set`。`docs/archive/` 及 `docs/archive/INDEX.md` 只保存历史参考，归档必须保留原文并更新索引；历史记录不作为执行指令，也不授权同步到仓库外路径。
- Session Ledger 是唯一可写事实源；不要新增与它竞争的 session、checkpoint、状态或评测事实文件。
- 项目对外名称是 `CodeOps-Agent`；`code-agent` 仅在已有模块、二进制、包名或环境变量标识中保留。README 只写稳定的已实现能力，当前缺口和运行证据写 `docs/GOAL.md` 或专门文档。
- 提交 README 前扫描 `BLOCKED`、`VERIFIED`、`SMOKE_PASS`、run ID、receipt 路径及 prototype／unfinished 描述，保持产品页只说明稳定能力。
- 用户要求在当前 `main` 工作时直接使用该 checkout，不创建隔离 worktree 或改写历史。

## 任务分流

- **Go Harness、工具、权限、Session、沙箱、PTY**：读取 `internal/` 或 `cmd/` 的目标包及对应测试；确认副作用仍经 Harness 授权、审计和持久化。
- **Python 编排、Context、Memory、Workflow、Provider**：读取 `orchestrator/` 的目标模块及对应 `tests/`；不得让 Python 绕过 Go Session Ledger 或直接执行本地副作用。
- **协议变更**：先改 `proto/codeagent/orchestrator.proto`，再运行 `make proto` 或 `scripts/generate-proto.ps1`，检查 Go/Python 生成物和编译测试均同步。
- **前端变更**：新布局或明显视觉改版先提供至少三个基于现有参考的明显不同的截图/样式方案，经用户选择后实施。修按钮、改文案、恢复既有行为以及实施已选方案，沿用现有选择直接推进；仍运行相关 frontend tests、build 和适当的浏览器交互检查。
- **评测、发布、性能**：读取 `docs/GOAL.md`、`docs/release-gate.md` 和相关评测入口；区分单元/集成/真实 runtime E2E/scorer connectivity，不把 smoke、fixture、mock 或合成 receipt 升级为生产证据。
- **文档或 README**：稳定的已实现能力写 README；当前缺口、运行收据、指标和限制写 `docs/GOAL.md` 或专门开发文档；过时材料归档并更新 `docs/archive/INDEX.md`。

## 架构不变量

- 优先复用已有模块和接口；仅在既有模块边界需要时新增依赖，保持变更窄而可审查。
- Python 模块保持可从仓库根目录导入；版本化协议的生成物与实现同步。
- Go Harness 是信任边界：鉴权、授权、Session Ledger、受约束的文件/Shell/Git/MCP、沙箱、进程生命周期和根 Trace 必须由 Go 强制。
- Python/LangGraph 只负责模型策略、Context、计划、Memory、Subagent、Worker/Workflow 和评测编排；两侧只通过版本化 protobuf/gRPC、事件 schema 和 TraceContext 交互。
- Session Ledger 是唯一可变事实源；Surface、Checkpoint、fork、rewind、compaction 都是投影。旧 SQLite snapshot 只读导入，禁止双写或静默覆盖。
- 新数据迁移采用“旧调用方 -> 单向兼容 Adapter -> 新实现”；新旧实现不得双写。删除 Adapter 前必须有覆盖全部调用方的真实 E2E 证据。
- 失败路径优先 fail-closed：无法确认权限、沙箱、信任根、pin、凭据、外部服务或副作用结果时，保留 `BLOCKED`/`unknown` 并要求恢复或人工对账。

## 领域契约

- PDF 入口统一走 MinerU 的显式 OCR；Tika 只处理 DOCX、PPTX、XLSX 等非 PDF Office 文档。
- BeeAPI/OpenAI relay 的所有本地进程和 shard 合计并发保持在 1–10；HTTP 429 只允许有界退避，无法恢复就保持 `BLOCKED`。
- `AI_REVIEWED`、`DISPUTED` 与 `HUMAN_REVIEWED` 不可互换；只有真实逐条人工复核才能产生 `HUMAN_REVIEWED`。
- 评测输入、prompt 和 receipt 不得包含答案、gold/qrels、patch 或修复提示；失败实例及其分母必须保留。
- 生产发布缺少 trust root、pin、凭据或外部服务时拒绝发布，并在内部记录中明确标为 `BLOCKED`。

## 证据与状态

按声明范围分别记录单测、集成、产品验证和发布的通过、失败、未运行或受阻状态。普通修复完成其声明范围后可以交付；未涉及的产品或发布验证记为未运行，不能据此把修复标为 `BLOCKED`。`BLOCKED` 只用于声明范围内必需条件缺失。工程状态继续使用 `DESIGNED`／`IMPLEMENTED`；`VERIFIED` 必须注明验证范围。

每项验证记录源码及运行输入、命令、退出码、完整分母和必要产物／checksum。需要验证 Trace 或模型能力的范围才要求真实 trace／模型证据；单测和集成不代替产品验证，源码 CI 不代替发布合格。任何失败与跳过都保留，不能用成功子集替代分母，也不能把 fixture、mock 或 smoke 升级为生产证据。

保留已验证的数据、checksum 和完整分母；数据退化或静默丢弃失败，即使测试通过，也拒绝相应范围的验收。

Git SHA 用于追溯；收据是否仍适用还须核对其范围内实际参与运行的源码、配置、指令、依赖和任务输入。纯说明文档变化且相关内容及产物指纹相同时，可以保留原运行收据并记录当前绑定核验。相关运行内容变化或无法确认时，重新验证受影响范围；旧收据的原始 SHA、结果、分母和 checksum 不改写。运行中加载的指令不能简单作为 Markdown 说明文档豁免，发布收据仍满足完整发布契约。

执行验证时按风险递进：先跑目标包定向测试，再跑相关完整 Go/Python 测试，随后 `git diff --check`、需要的真实跨进程/runtime E2E，最后运行 `python -m eval.release_gate --json`（发布任务）。外部依赖不可用时记录精确错误和阻塞边界，不伪造 receipt。

常用新鲜回归命令是 `go test ./... -count=1`、`go vet ./...` 和 `python -m pytest -q`；改动 `frontend/` 时在 `frontend/` 目录运行 `npm test` 与 `npm run build`。这些命令按任务范围取舍，输出中的通过数量、跳过数量、退出码和外部依赖状态都要记录，不能把上一次运行的结果当作本轮证据。

## 本地运行约定

- Docker Desktop 可以隐藏启动以准备 Docker 测试；不删除已有容器、volume、索引或业务数据。
- Go/Python 的常用入口由 `Makefile`、`pyproject.toml` 和各目录测试配置定义；优先使用这些入口，不在文档中复制易漂移的依赖版本。
- Windows Docker 测试先 dot-source `scripts/rag-agent-e2e-runtime.ps1` 并调用 `Wait-DockerDaemonReady`；不要在单个脚本中另写 readiness 探针，也不要删除容器、volume、索引或业务数据。
- 网络命令失败或很慢时，仅为该命令设置 `HTTP_PROXY`/`HTTPS_PROXY=http://127.0.0.1:7890` 后重试；不修改系统代理。外部仓库脚本先审阅再执行。
- 凭据只来自进程环境或批准的 secret manager；日志、trace、fixture、receipt 和测试输出都必须脱敏。`.env`、本地配置、数据库、日志、缓存、运行树和机器路径不进入提交。

## 编辑与交付

编辑前先读目标文件和相关测试，使用 `apply_patch` 做手工修改，保持变更窄而可审查；生成代码只由官方生成命令更新。不要重置、覆盖或清理用户既有改动。

提交前检查精确 staged paths，运行不输出匹配值的 secret scan、`git diff --cached --check` 和必要测试；确认没有 `.env`、凭据、运行产物或未审核的评测输入。只有验收证据完整且用户/项目流程允许时才提交或推送；否则留下清晰的 `BLOCKED` 说明和可复现下一步。

沿用原有交付约定：验证和人工审阅 staged diff 后，有意义的改动可按已授权流程自动提交并推送至 `origin/main`，核对推送 SHA，保留无关的用户改动。

## 持续推进与切换

长期 goal 不因单一路径受阻而缩小。遇到真实 blocker 时，先记录证据和边界，再在同一 goal 内切换到仍可验证的测试、文档、UI、可观测性或恢复工作；切换不得绕过 fail-closed 验收、秘密扫描、许可证边界、用户确认或安全约束。只有审计证明全部验收项满足，才能结束为 `complete`；声明范围内缺必需证据时，该范围保持 `BLOCKED`，其他范围分别如实记录。

破坏性删除、清空数据、覆盖用户改动和外部发布都需要明确的范围与授权；可恢复的归档移动优先于不可恢复删除。
