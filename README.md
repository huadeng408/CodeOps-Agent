# CodeOps-Agent

`CodeOps-Agent` 是一个本地运行的 CLI 代码开发协作工具。项目采用 Go Harness + Python Orchestrator 的双层架构：Go 侧负责终端交互、权限控制、工具执行和本地状态管理，Python 侧负责 LLM 调用、上下文组装、计划管理、记忆检索、子 Agent 编排和错误恢复。

目标是把“模型决策”和“本地执行”隔离开来：LLM 可以提出工具调用请求，但真正的文件、命令、网络、Git 和 MCP 操作都由 Go Harness 校验后执行。

## 核心特点

- 本地优先：CLI、会话库、记忆文件、工具执行和 MCP 进程都在本机运行。
- 双语言分层：Go 负责稳定的系统操作面，Python 负责灵活的 LLM 编排面。
- gRPC 流式协议：Harness 和 Orchestrator 通过双向流传递用户输入、工具请求、工具结果、计划更新、Todo 更新、子 Agent 事件和 token/cost 元数据。
- 安全执行模型：内置权限分级、allow/deny 规则、命令风险分析、Git 危险操作拦截、工具输出不可信包装和 prompt injection 防护。
- 隔离执行路由：按平台在 Docker 与 WSL2 Docker 后端之间选择，工作区受信根和容器网络、权限、资源限制统一由 Harness 强制；没有可用隔离后端时拒绝执行。
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
| `JobStart` | 会话确认 | 启动受 Harness 管理的后台进程，支持超时、有界输出和交互输入 |
| `JobOutput` | 自动允许 | 增量读取后台进程输出与生命周期状态，可选择有界等待 |
| `JobWait` | 自动允许 | 等待后台进程结束并返回确定性状态 |
| `JobList` | 自动允许 | 列出当前会话可见的后台任务 |
| `JobKill` | 会话确认 | 取消后台进程并回收其进程树，重复调用幂等 |
| `JobWrite` | 会话确认 | 向交互式后台进程写入 stdin |
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
  "sandbox": {
    "enabled": true,
    "backend": "auto",
    "image": "alpine:3.20",
    "wsl_distro": "Ubuntu-24.04",
    "trust_root": "."
  },
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
- Shell 子进程使用受控工作目录、超时和有界输出；宿主执行路径会清理凭据形环境变量，后台任务沿用相同的命令风险与环境策略。
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

格式化和基础编译检查：

```bash
make fmt
```

启动 PostgreSQL 依赖：

```bash
docker compose up -d
```

`docker-compose.yml` 提供本地 PostgreSQL 依赖。

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

Windows 环境可以使用：

```powershell
.\scripts\generate-proto.ps1
```
