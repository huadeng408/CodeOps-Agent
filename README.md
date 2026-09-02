# code-agent

`code-agent` 是一个本地运行的 CLI 代码代理原型。项目采用 Go Harness + Python Orchestrator 的双层架构：Go 侧负责终端交互、权限控制、工具执行和本地状态管理，Python 侧负责 LLM 调用、上下文组装、计划管理、记忆检索、子 Agent 编排和错误恢复。

目标是把“模型决策”和“本地执行”隔离开来：LLM 可以提出工具调用请求，但真正的文件、命令、网络、Git 和 MCP 操作都由 Go Harness 校验后执行。

## 核心特点

- 本地优先：CLI、会话库、记忆文件、工具执行和 MCP 进程都在本机运行。
- 双语言分层：Go 负责稳定的系统操作面，Python 负责灵活的 LLM 编排面。
- gRPC 流式协议：Harness 和 Orchestrator 通过双向流传递用户输入、工具请求、工具结果、计划更新、Todo 更新、子 Agent 事件和 token/cost 元数据。
- 安全执行模型：内置权限分级、allow/deny 规则、命令风险分析、Git 危险操作拦截、工具输出不可信包装和 prompt injection 防护。
- 可恢复会话：使用 SQLite 持久化会话，支持恢复历史消息、工作目录、预算指标、已批准工具、Todo、计划、Undo 和 worktree 状态。
- 工具闭环：支持读写编辑文件、Shell、搜索、Git、网络抓取、网络搜索，以及通过 MCP 动态扩展工具。
- 任务编排：支持 PlanWrite、TodoWrite、AskUser 和 SpawnAgent，适合多步骤代码任务、探索任务和人工确认流程。
- 成本与上下文控制：跟踪 token 和成本，支持会话预算、历史压缩、上下文裁剪和只读工具结果缓存。
- 项目指令与记忆：自动加载 AGENT.md 指令，并支持持久化 markdown 记忆。
- 内置技能：提供初始化、代码审查、安全审查等技能入口。

## 架构概览

```text
User
  |
  v
Go CLI / Harness
  |-- slash commands, status line, Ctrl+C handling
  |-- permissions, hooks, safety analyzer
  |-- local tools, MCP tools, undo, worktrees
  |-- SQLite sessions, memory files, metrics
  |
  |  gRPC bidirectional stream
  v
Python Orchestrator
  |-- LLM provider adapters
  |-- prompt/context assembly
  |-- plan/todo/ask-user/sub-agent events
  |-- memory, compaction, token budget
  |-- error recovery and injection protection
```

主要目录：

| 路径 | 说明 |
| --- | --- |
| `cmd/agent` | CLI 程序入口 |
| `internal/cli` | 终端交互、斜杠命令、渲染、状态行 |
| `internal/tools` | Go Harness 执行的本地工具 |
| `internal/permission` | 工具权限和 allow/deny 规则 |
| `internal/safety` | Shell/Git 风险分析 |
| `internal/session` | SQLite 会话存储与恢复 |
| `internal/mcp` | MCP stdio 服务生命周期和工具调用 |
| `internal/worktree` | Git worktree 管理 |
| `internal/undo` | 文件变更撤销记录 |
| `orchestrator` | Python gRPC 服务与 LLM 编排 |
| `orchestrator/runtime` | 对话循环、工具协议、预算和恢复逻辑 |
| `orchestrator/llm/providers` | OpenAI-compatible、Anthropic、本地模型适配 |
| `proto/codeagent` | Harness 与 Orchestrator 的 protobuf 协议 |
| `gen/codeagentpb` / `codeagent` | Go/Python 生成的 protobuf 绑定 |
| `tests` | Python 测试和 Go 测试 |

## 功能清单

### CLI 与会话

- 交互式 CLI 对话循环。
- Ctrl+C 中断当前轮，连续中断退出。
- 自动启动 Python Orchestrator。
- 自动加载 `AGENT.md` 项目指令。
- 状态行展示模型、token、成本、轮次、工具调用等信息。
- 会话自动保存到 `.agent/sessions/sessions.sqlite`。
- 支持 `/resume` 恢复最近或指定会话。
- 支持 `/compact` 压缩当前会话历史。

### 内置工具

| 工具 | 权限级别 | 能力 |
| --- | --- | --- |
| `Read` | 自动允许 | 读取工作区文件与行范围；图片和 MinerU OCR 解析后的 PDF 通过原生多模态内容块送达模型 |
| `Glob` | 自动允许 | 按 glob 模式查找文件 |
| `Grep` | 自动允许 | 正则搜索，支持文件列表、内容、计数、上下文、忽略大小写、glob 过滤 |
| `Write` | 会话确认 | 创建或覆盖工作区文件，并记录 undo |
| `Edit` | 会话确认 | 精确替换文本，支持唯一性校验和变更记录 |
| `Bash` | 每次确认 | 执行 shell 命令，支持超时和持久工作目录 |
| `Git` | 会话确认 | 执行受控 Git 子命令，并阻止危险参数 |
| `WebFetch` | 会话确认 | 拉取 URL 内容，带输出限制 |
| `WebSearch` | 会话确认 | 通过 DuckDuckGo-compatible JSON 接口搜索网页 |
| `SearchKnowledge` | 自动允许 | 通过内部 RAG 服务检索当前用户和组织可见的知识片段 |

编排层还注册了这些控制型工具：

- `TodoWrite`：更新当前任务列表。
- `PlanWrite`：更新当前计划。
- `AskUser`：在需要人工输入时向用户提问。
- `SpawnAgent`：启动子 Agent 执行独立探索或辅助任务。

### 扩展能力

- MCP：读取 `.mcp.json`，启动配置的 MCP stdio server，发现工具后写入 `.agent/mcp-tools.json`，再暴露给 Orchestrator 使用。
- Hooks：支持 pre/post tool 命令钩子，可用于审计、格式化、阻断或额外校验。
- Skills：Go Harness 提供 40+ 个可运行的内置 Skill，支持模型通过 `Skill`
  工具自主调用，也支持用户通过斜杠命令显式调用；Python 编排层按需镜像
  `.agent/skills.json` 元数据，不加载提示词正文。
- Memory：持久化项目记忆，支持新增、列表、查找、展示和删除。
- Worktree：支持创建、切换、清理本地 Git worktree。
- Undo：对 `Write` 和 `Edit` 产生的文件变更做撤销记录。

## 快速开始

### 环境要求

- Go 1.24+
- Python 3.11+
- `grpcio` / `grpcio-tools`，仅在运行或重新生成 Python protobuf 时需要
- `protoc`，仅在重新生成 protobuf 时需要
- MinerU CLI（命令名默认 `mineru`），读取和入库 PDF 时必需；所有 PDF 入口默认使用 MinerU `pipeline` 后端的 OCR 模式，不使用 Tika 或 `pdftotext`。Tika 仅处理 DOCX、PPTX、XLSX 等非 PDF 文档。可通过 `CODE_AGENT_MINERU_COMMAND`、`CODE_AGENT_MINERU_BACKEND` 和 `CODE_AGENT_MINERU_TIMEOUT_SECONDS` 覆盖命令、后端与超时

### 配置模型

复制示例环境文件：

```bash
cp .env.example .env.local
```

OpenAI-compatible 配置：

```env
LLM_PROVIDER=openai
OPENAI_API_KEY=<your-api-key>
OPENAI_BASE_URL=https://api.openai.com
OPENAI_MODEL=gpt-4o
```

Anthropic 配置：

```env
LLM_PROVIDER=anthropic
ANTHROPIC_API_KEY=<your-api-key>
ANTHROPIC_BASE_URL=https://api.anthropic.com
ANTHROPIC_MODEL=claude-sonnet-4-6
```

本地 OpenAI-compatible 服务配置：

```env
LLM_PROVIDER=local
LOCAL_LLM_BASE_URL=http://127.0.0.1:11434/v1
LOCAL_LLM_MODEL=local
```

如果没有可用模型配置，Orchestrator 会进入 fallback 模式，只做最小工作区检查。

### 运行

```bash
go run ./cmd/agent
```

或使用 Makefile：

```bash
make run
```

CLI 默认会自动启动：

```bash
python -m orchestrator.server
```

默认 gRPC 地址为 `127.0.0.1:50051`。

## 配置

配置会按以下顺序合并，后者覆盖前者：

1. 内置默认值
2. `.env.local` 中的模型名称
3. `~/.agent/settings.json`
4. `.agent/settings.json`
5. `.agent/settings.local.json`

常用 JSON 配置项：

```json
{
  "model": "gpt-4o",
  "model_fast": "gpt-4o-mini",
  "context_window": 256000,
  "max_tokens_per_session": 1000000,
  "max_cost_per_session": 5.0,
  "orchestrator_addr": "127.0.0.1:50051",
  "orchestrator_auto_start": true,
  "orchestrator_command": "python",
  "orchestrator_args": ["-m", "orchestrator.server"],
  "orchestrator_conversation_timeout_seconds": 300,
  "session_db_path": ".agent/sessions/sessions.sqlite",
  "memory_dir": ".agent/memory",
  "mcp_config": ".mcp.json",
  "worktree_base_ref": "fresh",
  "rag_enabled": true,
  "rag_server_url": "http://127.0.0.1:8081",
  "rag_user_id": 1,
  "rag_org_tag": "engineering",
  "rag_ingest_public": false,
  "permissions": {
    "allow": [
      {"tool": "Read", "pattern": ".*"}
    ],
    "deny": [
      {"tool": "Bash", "pattern": ".*rm\\s+-rf.*"}
    ]
  },
  "hooks": [
    {
      "type": "pre_tool",
      "matcher": ".*",
      "command": "echo checking",
      "timeout": 10
    }
  ]
}
```

`CODE_AGENT_RAG_INTERNAL_SECRET` is read only from the process environment.
Do not put the internal RAG secret in settings JSON, dotenv files, fixtures,
command-line arguments, logs, or committed artifacts.

`rag_org_tag` 只接受一个组织标签，不支持逗号分隔的多值。`/ingest` 仅接受工作区内的普通文件；PDF 入库统一使用 MinerU OCR，Tika 只处理 DOCX、PPTX、XLSX 等非 PDF 文档。

## 斜杠命令

| 命令 | 说明 |
| --- | --- |
| `/help` | 显示命令帮助 |
| `/plan` | 切换规划模式 |
| `/compact` | 压缩当前会话历史 |
| `/clear` | 清空当前会话 |
| `/config` | 显示已加载配置 |
| `/budget` | 显示 token 和成本预算 |
| `/memory` | 查看记忆概览 |
| `/memory add <content> [#tag...]` | 新增记忆 |
| `/memory list` | 列出记忆 |
| `/memory find <query>` | 查找相关记忆 |
| `/memory show <name>` | 展示记忆详情 |
| `/memory delete <name>` | 删除记忆 |
| `/sessions [limit]` | 列出最近会话 |
| `/tasks` | 展示当前 Todo |
| `/undo` | 撤销最近一次记录的文件变更 |
| `/diff` | 显示当前 Git diff 摘要 |
| `/worktree list` | 列出 worktree |
| `/worktree create <name>` | 创建 worktree |
| `/worktree switch <name>` | 切换 worktree |
| `/worktree cleanup <name> [--discard]` | 清理 worktree |
| `/resume [session-id]` | 恢复最近或指定会话 |
| `/skills` | 列出内置技能 |
| `/init [instructions]` | 运行初始化技能 |
| `/review [focus]` | 运行代码审查技能 |
| `/security-review [focus]` | 运行安全审查技能 |
| `/ingest <path>` | 将工作区内的文件提交到 RAG 入库 |

## 安全模型

默认权限分级：

- 自动允许：`Read`、`Glob`、`Grep`。
- 会话确认：`Edit`、`Write`、`Git`、`WebFetch`、`WebSearch`。
- 每次确认：`Bash`。

额外安全控制：

- 工具路径限制在工作区内，防止越界读写。
- Shell 风险分析会阻止 fork bomb、递归删除、磁盘格式化、curl/wget 管道执行、递归权限变更和广泛进程终止等危险模式。
- Git 安全规则默认允许 `status`、`diff`、`log`、`show`、`branch`、`fetch`、`worktree list` 等低风险操作，并阻止 `push`、危险 `reset`、`clean`、`rebase`、强制参数和删除分支等操作。
- 工具输出会被编排层视为不可信内容，避免文件内容或命令输出中的提示注入覆盖系统指令。
- Hooks 可以在工具执行前后做额外检查或阻断。

## 开发与测试

Python 3.12+ 可运行包含官方 Terminal-Bench 适配器的完整测试环境：

```bash
uv sync --locked --extra rag --extra eval --extra trace-e2e --extra audio --extra test
```

运行全部测试：

```bash
make test
```

分别运行 Go 和 Python 测试：

```bash
go test ./...
pytest -q
```

运行可审计的真实进程故障恢复验收（默认 8 Worker、200 个任务、30 次进程终止）：

```powershell
python -m eval.harness.fault_injection `
  --run-id fault-<timestamp> `
  --artifact-root .tmp/fault-injection
```

runner 会启动真实 Python 子进程，复用 `WorkflowEngine` 和 SQLite checkpoint，
按固定间隔终止正在执行的 worker 并重启其 assignment。receipt、任务清单、
进程退出码、故障事件、SQLite 事件摘要和 `checksums.sha256` 写入
`.tmp/fault-injection/<run_id>/`；运行目录被 `.gitignore` 排除，不应提交。
只有故障次数、任务分母、最终状态和 checksum 全部满足约束时才会输出
`VERIFIED`，夹具或 mock 不会被计为该验收证据。

最新 canonical 批次 `fault-20260902-canonical-2` 在 clean commit `e6f176af`
上以 8 Worker 执行 200 个 deterministic 80ms 任务，向 30 个不同 PID 注入
30 次真实进程终止，最终恢复 `200/200`（100%），SQLite integrity 与 artifact
checksum 均通过。该 receipt 只验证固定进程故障恢复 lane；由于任务不调用外部模型，
不能外推为真实模型长任务的端到端成功率。

运行 Context/Memory 的固定双臂输入 Token 验收：

```powershell
$env:OPENAI_API_KEY = '<从安全存储加载>'
$env:OPENAI_BASE_URL = '<OpenAI-compatible HTTPS endpoint>'
$env:OPENAI_MODEL = '<locked model>'
python -m eval.harness.context_token_eval `
  --task data/eval/context-token/tasks/workflow-provider-contract-v1.json `
  --project-root . `
  --artifact-root eval_results `
  --run-id context-token-<timestamp> `
  --model $env:OPENAI_MODEL `
  --max-output-tokens 512 `
  --minimum-reduction 0.60
```

runner 对同一锁定任务、模型和预算分别发送完整语料 baseline 与生产
`LayeredContext` 生成的 P0/P1/P3 上下文。正式降幅只读取 provider 返回的
`usage.input_tokens`；两臂任一结果回退、usage 缺失、模型身份不一致或降幅低于
60% 都会输出 `BLOCKED`。回环地址只产生 `SMOKE_PASS`，远端 HTTPS provider
才可产生该单项验收的 `VERIFIED`。这条 lane 验证 Context A/B，不代表完整产品
E2E。原始回答和运行数据库只保留在已忽略的 `eval_results/<run_id>/`，精简
receipt 不含 gold、prompt 正文或回答正文。任务文件预先 pin 住 P3 路径，因此该
lane 不评测自动上下文选择准确率。

运行 Skill 自主选型的固定 1,000 案例验收：

```powershell
go run ./cmd/skills-manifest `
  --output .agent/skills.json `
  --project-dir .agent/skills
$env:OPENAI_API_KEY = '<从安全存储加载>'
$env:OPENAI_BASE_URL = '<OpenAI-compatible HTTPS endpoint>'
$env:OPENAI_MODEL = '<locked model>'
python -m eval.harness.skill_selection_eval `
  --manifest .agent/skills.json `
  --dataset data/eval/skills/skill-selection-v1.json `
  --artifact-root eval_results `
  --run-id skill-selection-<timestamp> `
  --model $env:OPENAI_MODEL `
  --max-output-tokens 64 `
  --max-concurrency 10 `
  --minimum-accuracy 0.948 `
  --required-case-count 1000
```

manifest 由 Go Harness 的真实 Skill registry 导出，只含名称、描述和工具元数据，
不加载 Skill 正文。runner 对每个案例发起一次独立 `Skill` tool-call，gold 标签不进
模型输入、运行结果或精简 receipt；少于 1,000 个案例、catalog 不完全覆盖、数据
sidecar 不匹配、并发超过 10、provider usage 缺失或超预算、模型名/fingerprint
无法锁定，或低于 `948/1000` 都 fail-closed。回环 provider 仍只产生
`SMOKE_PASS`。`skill-selection-v1` 是明确标为
CC0-1.0 的仓库原创 catalog-routing 数据，不代表外部真实任务的泛化准确率。

最新真实 provider 批次 `skill-selection-20260902-sol-1` 在 clean commit
`9447e390` 上以 `gpt-5.6-sol`、10 并发和单例 256 output-token 预算完成
`960/1000`（96.0%），无传输、usage、预算或 tool-call 完整性失败。BeeAPI 未返回
`system_fingerprint`，因此精简 receipt 保持 `BLOCKED`，该结果证明锁定数据集上的
选型准确率，不构成不可变模型 revision 已验证的正式发布结论。

### Phoenix 跨语言 Trace 显式集成测试

该测试会启动 Docker Phoenix，调用真实 DeepSeek OpenAI 兼容接口，并运行真实 Go agent 与 Python orchestrator。它不会被 `go test ./...` 或默认 `pytest` 自动执行。

先安装该显式测试所需的 gRPC 与 OpenTelemetry 依赖：

```powershell
python -m pip install -e ".[trace-e2e]"
```

```powershell
$env:OPENAI_API_KEY = '<从安全存储加载>'
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-trace-e2e.ps1 -Model deepseek-v4-pro
```

凭据必须由外部 secret manager 加载到当前进程的 `OPENAI_API_KEY`；仓库文档和脚本调用示例不依赖 Markdown 密钥文件。测试不会输出 API key，也不会把它写入仓库、临时文件或 Phoenix span。

格式化和基础编译检查：

```bash
make fmt
```

启动 PostgreSQL 依赖：

```bash
docker compose up -d
```

当前仓库主要使用 SQLite 保存会话；`docker-compose.yml` 中的 PostgreSQL 服务用于后续 checkpointer 或外部存储集成。

## Protobuf 生成

修改 `proto/codeagent/orchestrator.proto` 后，需要同时生成 Go 和 Python 绑定：

```bash
make proto
```

等价命令：

```bash
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  proto/codeagent/orchestrator.proto
mv proto/codeagent/orchestrator.pb.go gen/codeagentpb/orchestrator.pb.go
mv proto/codeagent/orchestrator_grpc.pb.go gen/codeagentpb/orchestrator_grpc.pb.go
python -m grpc_tools.protoc -Iproto --python_out=. --grpc_python_out=. proto/codeagent/orchestrator.proto
```

Windows 环境也可以使用：

```powershell
.\scripts\generate-proto.ps1
```

## 当前状态

本项目按 [Goal](docs/GOAL.md) 持续改造中。现有仓库已经提供 Go Harness +
Python Orchestrator 的可运行原型，但目标描述中的每项能力都必须以当前
代码、测试和真实 runtime receipt 重新验收；历史文档中的完成度或旧指标不
自动构成发布结论。

### 已有代码边界

- **LLM 推理**：Extended Thinking / 深度思考（Anthropic thinking block + OpenAI reasoning_effort 模型门控）——复杂任务、多轮、计划模式自动触发。
- **Prompt 缓存感知**：Anthropic 显式 ephemeral 缓存标记（cacheable 段：identity/capabilities/tools/project），缓存命中率在状态栏实时可见。
- **多模型路由**：基于任务复杂度的 fast↔main 自动选举，带中段升级（错误累积 / plan_mode 二阶段回归主模型）。
- **增量流式输出**：Anthropic/OpenAI SSE 流式解析——LLM 文本逐块渲染（Go harness `OnTextDelta`→`AppendAssistantText`），而非等整段完成。工具调用结果统一后在文本后发出。
- **用户中断**：Ctrl+C 全链路传播（Go `turnCtx`→gRPC `context.add_callback`→Python `threading.Event`→`http_call_with_retry` 可中断）+ idle 提示符 3 次快速 Ctrl+C 强退。
- **进程崩溃恢复**：Go `ProcessManager.Monitor` 监督协程，编排器意外退出时自动重启（CAS 守护、指数退避），在途对话重放一次、会话不丢失。
- **并行工具调用**：`ToolRequestBatch`→Go `sync.WaitGroup` 扇出，gRPC 批量协议支持。
- **输出截断合规**：行数优先（250 行）+ 字节上限（50KB），信息性提示（`[Output truncated: N lines total, showing first M]`），`.truncated` 结构标志传递到 LLM。
- **多模态**：图片二进制经 protobuf 内容块传递，不受文本输出 50KB 上限截断；PDF 由 MinerU OCR 提取 Markdown 与图像资产，再转换为 Anthropic 原生 `image` / OpenAI `image_url` 内容。该链路不使用 Tika 或 `pdftotext`。
- **NotebookEdit**：Jupyter `.ipynb` cell 级增/删/改；`.ipynb` 读取渲染 cell 摘要而非原始 JSON。
- **自动提交建议**：`/commit` 斜杠命令，流式生成 conventional-commit 建议（不回退自动化）。
- **Allowlist 学习**：批准历史记录 + 规则建议器（重复匹配的命令/文件模式→候选 `AllowRule`），持久化跨会话。
- **工具安全加固**：符号链接路径穿越防护；关闭所有 8 个工具中的硬编码输出限制。

### 基础设施

- **可观测性**：SessionMeta 随每轮上报 token/成本/缓存命中/模型；Go metrics `Collector` + session 持久化（SQLite）+ 状态栏实时渲染。
- **会话持久化**：SQLite store + 确定性时间戳排序（`Résumé` 后强制单调递增，Windows 时钟分辨率健壮）。

### 验收边界

以下门槛是发布前必须由新鲜、可复现证据满足的最低值：

- 故障恢复 `>=98.5%`（恢复任务数 / 全部任务数）。
- 固定任务的输入 Token 降幅 `>=60%`，且任务结果不回退。
- 锁定 1,000 个案例的 Skill 选型准确率 `>=948/1000`。
- 固定预算和官方 scorer 下的 SWE 子集 `>=18/20`。

`go test ./...`、`pytest -q`、集成测试和真实 E2E 是不同门禁；夹具、mock、
合成 receipt 或开发 smoke 不得替代真实 E2E。当前未满足的门槛保持
`BLOCKED`，不会用文档措辞升级为 `VERIFIED`。
