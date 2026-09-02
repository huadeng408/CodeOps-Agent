# Phoenix Live Trace Runbook（可观测性线）

> 日期：2026-08-04
> 状态：**BLOCKED on environment** —— 真实语料导入进行中（3095 份 → `knowledge_base_v2_bge_m3`），Phoenix 容器未启动，本 runbook 只做核对与准备，**不启动任何服务**。
> 依据：`docs/GOAL.md`、`internal/telemetry/genai/{semconv.go, schema_test.go, semconv_test.go, tracer.go}`、`orchestrator/rag/trace.py`、`orchestrator/config/env.py`、`scripts/eval/trace_assert.py`、`tests/eval/test_trace_assert.py`、`tests/test_trace_schema.py`、`tests/integration/trace_e2e.py`、`scripts/test-trace-e2e.ps1`、`docker-compose.yml`。
> 当前边界：本文是历史运行记录，不再具有执行约束力。当前验收以 active Goal、当前仓库契约和新鲜端到端证据为准；旧指标和旧环境状态仅供追溯。

---

## 0. 当前状态速览

| 项 | 状态 | 证据 |
|---|---|---|
| tracer schema（Go/Python 11 键 golden） | ✅ VERIFIED（离线） | `schema_test.go` + `tests/test_trace_schema.py` 双端冻结清单一致 |
| `trace_assert.py` 单测 | ✅ 已有（`tests/eval/test_trace_assert.py`） | 覆盖 5 类 span + rag.* 三键断言 |
| Phoenix live trace | ❌ BLOCKED | 需 Docker Phoenix 容器 + 真实 DeepSeek key + 外网；导入进行中不可启动 |
| 导入服务 | 🔄 运行中 | ES/Kafka/embedding/worker 正在导入，**严禁触碰** |

---

## 1. 环境前置

### 1.1 Docker Phoenix 容器

`docker-compose.yml` 中已定义 `phoenix` 服务（无需任何配置改动）：

- 镜像：`arizephoenix/phoenix:latest`，容器名 `codeagent-phoenix`
- 端口：**6006**（UI + OTLP/HTTP）、**4317**（OTLP/gRPC，可选）
- 持久化：volume `codeagent-phoenix` → `/data`（`PHOENIX_WORKING_DIR=/data`）
- `restart: "no"`；首次拉镜像可能较慢（runner 给 300s 启动超时）

零配置接通的原理：
- Go 侧 `internal/telemetry/genai/tracer.go`：读 `OTEL_EXPORTER_OTLP_ENDPOINT`，**默认 `http://localhost:6006`**（服务根，OTLP HTTP 导出器自行追加 traces 路径）。若该 env 未显式设置且端口 TCP 探测不通 → 打日志并**静默降级为 NoopTracer**（不产 trace、不报错）。
- Python 侧 `orchestrator/config/env.py`：读 `OTEL_EXPORTER_OTLP_ENDPOINT`，**默认 `http://localhost:6006/v1/traces`**（完整 URL）；`OTEL_SERVICE_NAME` 默认 `code-agent-orchestrator`。
- ⚠️ **两端对同一 env 的语义解释不同**（Go=服务根，Python=完整 URL）。`test-trace-e2e.ps1` 因此**主动从子进程环境中移除 `OTEL_EXPORTER_OTLP_ENDPOINT`**，让双方各用自己的默认值指向同一 Phoenix。手工运行时也不要设置该变量，或必须分别为两端设置正确形式。

### 1.2 DeepSeek API key

- 通过批准的外部 secret manager 将凭据注入当前进程的 `OPENAI_API_KEY`。
- 当前运行说明不依赖 Markdown 密钥文件或本机固定路径；缺失或空凭据时 preflight 必须失败。
- key 只注入子进程环境块，不进命令行/临时文件/日志；诊断输出会先做 `<redacted>` 脱敏。

### 1.3 运行时与依赖

- `docker`（daemon 正常）、`docker compose`、`go`、`python` 均在 PATH。
- Python trace 依赖（runner 会 preflight 探测 `grpc` / `opentelemetry` / `OTLPSpanExporter` import），缺失时安装：
  ```powershell
  python -m pip install -e ".[trace-e2e]"
  ```
- 网络：可访问 `https://api.deepseek.com`（preflight 调 `/v1/models` 验证模型 `deepseek-v4-pro` 可用）。
- 端口空闲：6006（必需）、4317（可选）。

### 1.4 资源闸门（重要）

启动 Phoenix 前必须确认：
1. **导入已完成**（见 §3 Step 0 的检查命令）——导入占用 ES/Kafka/embedding/MySQL，Phoenix 与之争抢内存；历史上 Docker daemon 出现过内存压力 500。
2. Docker Desktop 内存余量充足（`docker stats --no-stream` 粗查）。
3. **导入进行期间禁止重启 Docker Desktop**（会打断 ES/Kafka/导入 worker）。

---

## 2. 离线核对结论（已 VERIFIED，无需 Phoenix）

### 2.1 rag.* 键清单（Go/Python 一致，11 键）

Go `semconv.go`（第 76–95 行）定义、`schema_test.go::TestRAGAttributeNamesFrozen` 冻结；Python `orchestrator/rag/trace.py::RAG_ATTRIBUTE_NAMES` 与 `tests/test_trace_schema.py::test_rag_attribute_names_match_go_golden` 断言**完全相同、顺序相同**的清单：

| # | 键 | 类型 | Go 构造器 |
|---|---|---|---|
| 1 | `rag.corpus_generation` | string | `CorpusGenerationKV` |
| 2 | `rag.index_alias` | string | `IndexAliasKV` |
| 3 | `rag.index_physical` | string | `IndexPhysicalKV` |
| 4 | `rag.mapping_version` | string | `MappingVersionKV` |
| 5 | `rag.query_hash` | string | `QueryHashKV`（配 `HashQuery` 隐私哈希） |
| 6 | `rag.top_n` | int | `TopNKV` |
| 7 | `rag.retrieval_mode` | string | `RetrievalModeKV` |
| 8 | `rag.reranker_applied` | bool | `RerankerAppliedKV` |
| 9 | `rag.visual_path` | string | `VisualPathKV` |
| 10 | `rag.document_hash` | string | `DocumentHashKV` |
| 11 | `rag.document_length` | int | `DocumentLengthKV` |

隐私约束：只存 hash/长度，绝不落原始 query/document 内容；`schema_test.go::TestQueryHashIsStableAndPrivacySafe` 保证哈希稳定且不含原文。

备注：`semconv_test.go` 覆盖的是操作名常量、基础属性、截断、NoopTracer、`parseOTLPEndpoint`；**rag.* 冻结清单在 `schema_test.go`**（两个测试文件分工不同，别混淆）。

### 2.2 `trace_assert.py` 覆盖情况

- **必需 span 类型**（`required_span_kinds()`，且被 `test_required_span_kinds_frozen` 冻结）：
  `["invoke_agent", "chat", "rag.retrieve", "execute_tool", "scorer"]` ✅ 五类齐全，按**子串**匹配 span 名（如 `invoke_agent code-agent`、`execute_tool Read`、`scorer evalplus` 均可命中）。
- **run 定位**：在 span 属性 `gen_ai.tool.call.result`（支持扁平或嵌套 dict）中找 `TRACE_E2E_FIXTURE:{run_id}` 标记，必须唯一命中一条 trace。
- **rag.\* 属性断言**（`assert_rag_schema`）：只检查首个 `rag.retrieve` span 的 **3 键**——`rag.query_hash`、`rag.retrieval_mode`、`rag.top_n`（扁平或嵌套均可）；**其余 8 键不在 live 断言范围**（由双端 golden 单测离线保障）。若无 `rag.retrieve` span 则**静默跳过、不报错**。
- **退出码**：0=通过（打印 JSON 报告，可 `--out` 落盘）；1=join/断言失败（`TRACE JOIN FAILED`）；2=传输等其他错误（`TRACE JOIN ERROR`）。
- **默认参数**：`--project` 默认 **`code-agent`**；`--phoenix-url`、`--run-id`、`--start-time`（ISO 时间）必填。
- 已知待 live 验证点（不阻塞，执行时留意）：
  1. `trace_assert.py` 查询 `{base}/v1/spans?project_name=...&start_time=...&limit=500`，而已验证的 E2E helper（`trace_e2e.py`）用的是 **`{base}/v1/projects/{project}/spans`**。若 live 返回 404/结构不符，优先换用 project 路径核对（不改代码，先用 `trace_e2e.py` 或手工 curl 定位）。
  2. E2E runner 的 Phoenix 项目默认是 **`default`**（OTLP 未带 `openinference.project.name` 时 Phoenix 路由到 default），跑 `trace_assert.py` 时记得显式 `--project default`（除非 eval 流程另行注入项目名）。
  3. 文档字符串说 scorer "when scoring spans exist"，但**代码无条件要求** 5 类齐全——被断言的 run 必须真实产生 scorer span，否则退出码 1。

### 2.3 `test-trace-e2e.ps1` 流程核对

阶段（`$stage` 标记，与源码一致）：

1. **preflight**：从当前进程的 `OPENAI_API_KEY` 取 key → 检查 `python`/`go`/`docker` 可用 → 探测 Python trace 依赖 → `check-model` 调 DeepSeek `/v1/models` 确认 `deepseek-v4-pro` 在列。支持 `-ValidateApiKeyOnly` 只验 key。
2. **docker-phoenix**：`docker compose ps --status running --services` 判断 phoenix 是否已在跑；**未跑才** `docker compose up -d phoenix`（300s 超时）；随后轮询 `GET {PhoenixUrl}/v1/projects?limit=1` 至就绪（60s）。
3. **temporary-workspace**：生成 run_id（GUID）→ 临时目录 `code-agent-trace-e2e-<runId>` → 写 `.agent/settings.json`（model、`model_fast=disabled`、orchestrator 自动启动 `python -m orchestrator.server`、空闲环回端口、Read 全放行权限）→ 写 fixture `trace-e2e-<runId>.txt`（内容 `TRACE_E2E_FIXTURE:<runId>`）。
4. **go-build**：`go build -o <tmp>/code-agent-e2e.exe ./cmd/agent`（120s）。
5. **agent-turn**：以隔离环境启动 agent（`LLM_PROVIDER=openai`、key、`OPENAI_BASE_URL`、`OPENAI_MODEL`、重试≤1、`THINKING_ENABLED=false`、`MODEL_FAST=disabled`、`OTEL_SERVICE_NAME=code-agent-orchestrator`、`PYTHONPATH=仓库根`；**移除继承的 `OTEL_EXPORTER_OTLP_ENDPOINT`**，原因见 §1.1）；stdin 发 prompt：用 Read 恰好一次读 fixture，然后只回复 `TRACE_E2E_OK:<runId>`；120s 内等标记。
6. **phoenix-verification**：调 `tests/integration/trace_e2e.py verify-phoenix`（轮询 `/v1/projects/default/spans`，带分页，45s）；断言：恰好一个 `invoke_agent code-agent` 根 span、≥1 个 `execute_tool Read`、≥2 个 `chat` span、Read span 无 ERROR、所有必需 span 的父链**可达根 span**（验证 W3C 跨 gRPC 传播，而非仅同名）。
7. **agent-shutdown**：关 stdin，15s 内正常退出且 exit 0。
8. **finally 清理**：必要时杀本测试的进程树、只删自己创建的临时目录、**仅当本次启动了 phoenix 才** `docker compose stop phoenix`；**绝不** `compose down`、绝不删 Phoenix volume、绝不停已存在的 Phoenix。

成功输出：`Trace E2E passed` + run_id / trace_id / model / elapsed_seconds + 必需 span 表格（name/runtime/span_id/parent_id）。

---

## 3. 逐步执行命令（解除阻塞后按序执行）

> 本节保留历史复现步骤，仅用于理解当时的环境和证据；当前工作不得把它当作执行授权或路径规范，须遵循 active Goal、当前仓库契约和 `main` 工作边界。

### Step 0：确认导入完成 + 内存充足（闸门）

```bash
# 导入进度：v2 索引 chunk 数趋于稳定且 importer 报告 active=预期数
curl -s http://127.0.0.1:9200/knowledge_base_v2_bge_m3/_count
# 无新增 DLQ、importer 进程已正常退出（exit=0）后再往下走

# Docker 内存余量
docker stats --no-stream
docker ps --format '{{.Names}}\t{{.Status}}'
```

判定标准：导入 worker 已结束或明确停止、ES `_count` 两次采样不再增长、Docker Desktop 无内存告警。**导入进行中不要执行下面任何 docker 命令改动容器状态。**

### Step 1：启动 Phoenix（仅 Step 0 闸门通过后）

```powershell
docker compose up -d phoenix
# 就绪检查（runner 同款探针）：
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:6006/v1/projects?limit=1
# UI：http://127.0.0.1:6006
```

注意：**不要设置** `OTEL_EXPORTER_OTLP_ENDPOINT`（两端默认值已指向此处，语义差异见 §1.1）。

### Step 2：真实 E2E（Go + Python + DeepSeek live trace）

```powershell
# 先由外部 secret manager 注入当前进程；不要把值写入脚本、命令历史或 Markdown 文件
$env:OPENAI_API_KEY = '<从安全存储加载>'

# 先只验证 key（可选，不发模型请求）
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-trace-e2e.ps1 `
  -Model deepseek-v4-pro -ValidateApiKeyOnly
# 期望输出：api_key=valid

# 完整 E2E
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-trace-e2e.ps1 `
  -Model deepseek-v4-pro
```

期望：exit 0，输出 `Trace E2E passed`、run_id、trace_id、model、elapsed_seconds，以及含 `invoke_agent code-agent`（go）、`execute_tool Read`（go）、≥2 个 `chat`（python）的 span 表。
失败时输出会带 `Trace E2E failed during stage: <阶段>` + 脱敏后的 agent/orchestrator 输出，按阶段定位（见 §4）。

### Step 3：`trace_assert.py` 验证（针对注入了 run_id 的 eval run）

```powershell
python scripts/eval/trace_assert.py `
  --phoenix-url http://127.0.0.1:6006 `
  --project default `
  --run-id <RUN_ID> `
  --start-time <ISO-UTC, 例如 2026-08-04T10:00:00+00:00> `
  --out .tmp/trace-assert-report.json
```

期望：exit 0，stdout JSON 报告 `"missing": []`、含 `trace_id` 与 `span_count`。
- exit 1（`TRACE JOIN FAILED`）：标记未命中 / span 类型缺失 / rag.* 三键缺失——stderr 给出缺什么、现场有哪些 span 名。
- exit 2（`TRACE JOIN ERROR`）：网络/HTTP/JSON 层问题；若 404，优先怀疑 §2.2 待验证点 1（`/v1/spans` vs `/v1/projects/{project}/spans` 路径差异），用 `trace_e2e.py` 或 curl 手工比对后再定。

### Step 4：清理

```powershell
# 仅当 Phoenix 是本次手动启动、且不再需要时：
docker compose stop phoenix
```

**绝不**：`docker compose down`、删除 `codeagent-phoenix` volume、停导入相关容器。

---

## 4. 故障排查（按代码实际行为）

| 现象 | 根因（代码依据） | 处置 |
|---|---|---|
| Phoenix 里没有任何 Go span | Go tracer 探测 6006 不通 → NoopTracer（`tracer.go` 日志 `Phoenix not reachable ... traces disabled`） | 确认容器已起、端口通；勿靠设 `OTEL_EXPORTER_OTLP_ENDPOINT` 绕过语义差异 |
| `Python trace dependencies are unavailable` | 缺 grpc/OTel 包 | `python -m pip install -e ".[trace-e2e]"` |
| `expected exactly one DeepSeek ...` | `OPENAI_API_KEY` 缺失、为空或格式无效 | 从批准的 secret manager 重新注入当前进程环境 |
| `model 'deepseek-v4-pro' is unavailable` | preflight 调 `/v1/models` 失败（鉴权/网络/模型名） | 按 stderr 列出的可用模型名核对 |
| `Phoenix did not become ready ... within 60s` | 容器慢/端口占用 | `docker compose ps`、查 6006 占用；首拉镜像给足 300s |
| Docker daemon 500/内存压力 | 与导入栈争资源 | **等导入完成**再试；导入期间不要重启 Docker Desktop |
| `trace_assert` 404/结构异常 | `/v1/spans` 路径待 live 验证（§2.2） | 用 `/v1/projects/{project}/spans` 手工核对 |

---

## 5. 阻塞声明

**BLOCKED on environment（2026-08-04）**：
- 真实语料导入进行中（目标 3095 份 → `knowledge_base_v2_bge_m3`，pilot 已于 2026-08-03 通过）；
- Phoenix 容器未启动（本 runbook 遵守约束未启动、未改任何代码/配置/服务）；
- live 验证还需外网至 `api.deepseek.com` 与有效 DeepSeek key。

解除阻塞的判定：§3 Step 0 闸门全部通过。schema 层面（11 键 golden、5 类 span 断言逻辑）已离线 VERIFIED，live 一通即可直接执行 Step 1–3。
