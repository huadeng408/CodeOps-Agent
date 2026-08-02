# Multimodal RAG and Agent Harness Platform Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建以 MinerU OCR、双索引检索、OpenTelemetry/Phoenix、权威 benchmark 和可复现自研 Agent Harness 为核心的多模态 RAG 平台。

**Architecture:** 文本主链与视觉 pilot 物理隔离，通过 document/page/element provenance 晚融合；Go 控制权限和执行，Python 负责编排、解析适配与评测。所有运行产出版本化 manifest、trace 和官方 scorer artifact，alias 只在质量门槛通过后原子切换。

**Tech Stack:** Go, Python 3.12, MinerU OCR, MySQL, MinIO, Elasticsearch, BGE-M3, optional ColPali/ColQwen, OpenTelemetry, Phoenix, Docker, pytest, Go testing, official benchmark harnesses.

---

## Phase 0：安全恢复与事实冻结

### Task 0.1：建立干净执行边界

**Files:**
- Read: `docs/HANDOFF-2026-08-02-MULTIMODAL-RAG-AGENT-PLATFORM.md`
- Read: `AGENT.md`
- Create or update: `docs/PROGRESS-2026-08-02.md`（跨日执行时改用实际执行日）

- [ ] 运行 `git status --short --branch`、`git log -1 --oneline`、`git diff --check`、`git diff --stat`，把输出摘要写入当天进展文档。
- [ ] 确认用户未提交的 `AGENT.md`、`CLAUDE.md` 和恢复文档不被暂存。
- [ ] 列出 Plan 3 WIP 的逐文件 diff；任何不能解释的修改标为 `BLOCKED`，不猜测。
- [ ] 创建隔离 worktree 后执行；若保留当前工作树，先由用户确认 WIP 归属。
- [ ] 提交纯文档基线：`docs: hand off multimodal RAG and agent harness architecture`。

验收：文档提交不包含任何 WIP 代码；`git show --stat HEAD` 只显示 docs。

## Phase 1：契约与 provenance

### Task 1.1：冻结 Document/Element/Chunk schema

**Files:**
- Create: `internal/model/document_contract.go`
- Create: `internal/model/document_contract_test.go`
- Modify: `internal/model/structured_chunk.go`
- Create: `orchestrator/rag/schemas/document_contract.py`
- Create: `tests/test_document_contract.py`

- [ ] 写失败测试：缺 `source_sha256/page_id/element_ids/parser_version/corpus_generation` 时拒绝。
- [ ] Go 运行：`go test ./internal/model -run Contract -count=1`，确认因新验证器不存在失败。
- [ ] Python 运行：`python -m pytest tests/test_document_contract.py -q`，确认 schema 未定义失败。
- [ ] 实现相同 JSON 字段和 validation；ACL 字段在 worker schema 中标为非权威。
- [ ] 加 Go↔Python golden JSON fixture，要求 round-trip 字段不丢失。
- [ ] 运行 `go test ./internal/model -count=1`、pytest 和 `go vet ./internal/model`。
- [ ] 提交：`feat(rag): freeze multimodal document contracts`。

### Task 1.2：全 PDF 路由守卫

**Files:**
- Modify: `pkg/documentparser/client.go`
- Modify: `pkg/mineru/client.go`
- Modify: `orchestrator/rag/ingestion.py`
- Modify: `internal/tools/read.go`
- Test: `tests/test_no_pdf_tika.py`
- Test: `internal/tools/pdf_test.go`

- [ ] 为扩展名伪装、MIME 错误、magic bytes PDF、无文本扫描件分别写失败测试。
- [ ] fake Tika server 记录 request count；所有 PDF case 必须为零。
- [ ] MinerU command assertion 必须包含 OCR 模式且不含 auto。
- [ ] 保持 DOCX/PPTX/XLSX 调 Tika 一次。
- [ ] 运行 focused suite 和真实 `CODE_AGENT_RUN_MINERU_E2E=1`。
- [ ] 提交：`test(rag): enforce MinerU OCR at every PDF boundary`。

## Phase 2：BGE-M3 文本索引

### Task 2.1：独立 embedding preflight

**Files:**
- Create: `pkg/embedding/preflight.go`
- Create: `pkg/embedding/preflight_test.go`
- Modify: `pkg/embedding/client.go`
- Modify: `internal/serverconfig/config.go`
- Modify: `configs/server.yaml`

- [ ] httptest 定义 `/health` 响应：`ready/model/revision/dimensions`。
- [ ] 写失败测试覆盖错误模型、错误 revision、512 维、NaN/Inf、只返回一个向量、dimensions 参数被 provider 忽略。
- [ ] 实现固定 UTF-8 中英样本请求，要求两个原生 1024 维向量。
- [ ] `RequireNativeDimensions=true` 时禁止不带 dimensions 重试。
- [ ] 模型 revision 使用不可变 commit；`main` 只允许开发，不允许 cutover。
- [ ] 运行 `go test ./pkg/embedding -count=1` 和 vet。
- [ ] 提交：`feat(rag): enforce native BGE-M3 embedding contract`。

### Task 2.2：显式 index manager 和 alias 原子操作

**Files:**
- Create: `pkg/es/knowledge_index.go`
- Create: `pkg/es/knowledge_index_test.go`
- Modify: `pkg/es/client.go`

- [ ] 使用 httptest ES server 写失败测试，禁止全局 `ESBaseURL`。
- [ ] `KnowledgeIndexManager` 显式持有 client；mapping 含 1024 dense vector、text/provenance/ACL 字段，无视觉 vector。
- [ ] `EnsurePhysicalIndex` 区分 alias 与 physical index，已有 mapping/model 不兼容时拒绝。
- [ ] `SwitchAlias` 只发送一个 `POST /_aliases`，同 body remove/add；测试断言没有 delete request。
- [ ] `RollbackAlias` 产生完全相反 actions；`ReadAlias` 返回排序后的 target。
- [ ] 运行 `go test ./pkg/es -run 'Mapping|Alias' -count=1` 和 vet。
- [ ] 提交：`feat(rag): add versioned text index manager`。

### Task 2.3：结构化向量写入

**Files:**
- Modify: `internal/pipeline/processor.go`
- Modify: `internal/model/es_document.go`
- Create: `internal/pipeline/structured_index_test.go`

- [ ] 用 fake repository、embedding 和 ES writer 写失败测试；512 维时零 ES writes。
- [ ] 1024 维时断言 source text 与 embedding text 分离，provenance 完整，model version 为锁定 revision。
- [ ] index name 从 `CorpusConfig.TextIndex` 注入，禁止硬编码；generation 非空时禁止写旧 index。
- [ ] external ingestion path 使用同一 validator。
- [ ] 运行 pipeline/pkg es/embedding tests 和 vet。
- [ ] 提交：`feat(rag): index structured BGE-M3 chunks`。

## Phase 3：高质量生产语料

### Task 3.1：来源 manifest 与许可证门

**Files:**
- Create: `corpus/manifest.schema.json`
- Create: `corpus/sources.yaml`
- Create: `scripts/corpus/validate_manifest.py`
- Create: `tests/corpus/test_manifest.py`

- [ ] schema 要求 source ID、official URL、commit、paths、license URL/hash、allowed formats、expected counts、loader user、generation。
- [ ] 首批只用许可证清楚的官方技术文档/仓库；不导入随机网页、Stack Overflow、benchmark query/qrels/answers。
- [ ] 写 path traversal、floating branch、missing license、hash mismatch 失败测试。
- [ ] 输出机器可读 license audit；任何失败阻止下载/导入。
- [ ] 提交：`feat(corpus): add pinned source and license manifests`。

### Task 3.2：staging、pilot、checkpoint loader

**Files:**
- Create: `cmd/corpus-loader/main.go`
- Create: `internal/corpus/loader.go`
- Create: `internal/corpus/checkpoint.go`
- Create: `internal/corpus/loader_integration_test.go`

- [ ] 每来源 20–50 文件 pilot；checkpoint key 为 source/commit/path/hash。
- [ ] 幂等重跑不创建重复 document/chunk；失败文件留 quarantine 和 reason。
- [ ] MySQL/MinIO/ES 写入后做四端一致性审计。
- [ ] 显式 Docker integration 只启动需要的 mysql/minio/es，不执行 compose down。
- [ ] pilot 全通过才允许 full import；每批保存 counts/duration/failures。
- [ ] 提交：`feat(corpus): add resumable official corpus loader`。

## Phase 4：检索、证据和 citation

### Task 4.1：ACL-first hybrid retrieval

**Files:**
- Modify: `internal/service/search_service.go`
- Create: `internal/service/search_service_v2_test.go`
- Modify: `internal/model/es_document.go`

- [ ] 测试 BM25/vector 各 top 100、RRF k=60、ACL 在召回 query 内。
- [ ] 测试同页/同 parent diversity，但不丢失不同页面。
- [ ] query embedding 维度不匹配在验收模式失败，不静默 BM25 fallback。
- [ ] 返回 source/document/page/element/bbox/asset/model/corpus 字段。
- [ ] 提交：`feat(rag): add ACL-first provenance-aware retrieval`。

### Task 4.2：真实 evidence expansion

**Files:**
- Create: `internal/service/evidence_expander.go`
- Create: `internal/service/evidence_expander_test.go`
- Modify: `internal/repository/document_vector_repository.go`

- [ ] 先写失败测试：命中 child 后加载 parent、必要邻块、精确 element；ACL 不允许跨租户 parent。
- [ ] 去重相同 element/bbox，按 token budget 截断，保留 citation key。
- [ ] 不存在 parent 时返回 child 并打 `expansion_status=partial`，不伪造成功。
- [ ] 提交：`feat(rag): expand retrievable evidence with citations`。

## Phase 5：视觉检索 pilot

### Task 5.1：page/crop artifact 与独立索引

**Files:**
- Create: `orchestrator/rag/visual/artifacts.py`
- Create: `orchestrator/rag/visual/encoder.py`
- Create: `pkg/es/visual_index.go`
- Test: `tests/test_visual_artifacts.py`
- Test: `pkg/es/visual_index_test.go`

- [ ] 只消费 MinerU rendered pages/assets，不打开源 PDF。
- [ ] page/crop 均带 source hash、page/element/bbox、model/revision。
- [ ] 映射和 alias 与 text index 分离；默认不创建生产 alias。
- [ ] GPU 不可用返回明确 disabled；unit tests 不下载模型。
- [ ] 提交：`feat(rag): add isolated visual retrieval pilot`。

### Task 5.2：ViDoRe 与自建多模态 bake-off

**Files:**
- Create: `eval/benchmarks/vidore.py`
- Create: `eval/retrieval/multimodal_metrics.py`
- Create: `scripts/eval/run-visual-pilot.ps1`

- [ ] manifest 锁定 ViDoRe snapshot/license/scorer。
- [ ] 比较 text-only、page visual、late-interaction 三路径。
- [ ] 报告 nDCG/Recall、bbox hit、P95、GPU peak、index bytes/page、OOM/timeout。
- [ ] 未达视觉门槛时保留 pilot artifacts，不创建 alias。
- [ ] 提交：`eval(rag): benchmark visual retrieval pilot`。

## Phase 6：可观测性

### Task 6.1：统一 span schema

**Files:**
- Modify: `internal/telemetry/genai/tracer.go`
- Create: `internal/telemetry/genai/schema_test.go`
- Modify: `orchestrator/rag/trace.py`
- Create: `tests/test_trace_schema.py`

- [ ] 定义 agent/retrieval/embedding/rerank/tool/scorer span 和必需属性。
- [ ] 默认只记录 hash/length；secret/header/raw document 必须脱敏。
- [ ] Go/Python golden tests 对属性名一致。
- [ ] 提交：`feat(observability): standardize agent and RAG spans`。

### Task 6.2：eval run 与 Phoenix join

**Files:**
- Modify: `scripts/trace-e2e.ps1`
- Create: `scripts/eval/trace_assert.py`
- Modify: `tests/test_trace_e2e.py`

- [ ] 每个 eval instance 注入 run_id/instance_id/traceparent。
- [ ] Phoenix REST 查询断言同 trace 有 Go root、Python LLM、retrieval、tool、scorer。
- [ ] 使用唯一 marker 排除历史 trace 假阳性。
- [ ] runner 不 stop 已运行 Phoenix、不删除 volume、不打印 key。
- [ ] 提交：`test(observability): join eval artifacts to Phoenix traces`。

## Phase 7：统一评测平台

### Task 7.1：dataset/run manifest

**Files:**
- Create: `eval/manifest.py`
- Create: `eval/datasets/manifest.yaml`
- Create: `tests/eval/test_manifest.py`

- [ ] 记录 dataset revision/hash/license/scorer、git SHA/dirty hash、model/prompt/tool policy/budget/image digest/corpus/index。
- [ ] floating revision、缺 hash、benchmark/production path 重叠时失败。
- [ ] 提交：`feat(eval): add reproducible benchmark manifests`。

### Task 7.2：retrieval 与 RAG scorer

**Files:**
- Create: `eval/retrieval/metrics.py`
- Create: `eval/rag/citations.py`
- Create: `eval/rag/faithfulness.py`
- Test: `tests/eval/test_retrieval_metrics.py`
- Test: `tests/eval/test_citations.py`

- [ ] 确定性实现 Recall/MRR/nDCG、page/element/bbox hit、citation precision/recall。
- [ ] faithfulness 优先 exact/evidence rule；LLM judge 必须固定模型/prompt，并报告 judge disagreement。
- [ ] 分 source/language/type 报告，不只总平均。
- [ ] 提交：`feat(eval): score retrieval grounding and citations`。

### Task 7.3：公开 benchmark adapters

**Files:**
- Create/Modify: `eval/benchmarks/beir.py`, `miracl.py`, `bright.py`, `vidore.py`
- Modify: `eval/run.py`

- [ ] adapter 只转换格式和调用官方 scorer。
- [ ] 每个 adapter 有 `--dry-run`、小样本 integration、offline cache 路径。
- [ ] 网络下载与评分分开；评分必须可离线重跑。
- [ ] 提交：`feat(eval): add authoritative retrieval adapters`。

## Phase 8：自研 Agent Harness

### Task 8.1：runner、budget、resume、artifact

**Files:**
- Create: `eval/harness/runner.py`
- Create: `eval/harness/budget.py`
- Create: `eval/harness/artifacts.py`
- Create: `tests/eval/test_harness_resume.py`

- [ ] 测试 timeout/OOM/infra/agent/scorer 分类、checkpoint resume、已完成实例不重复。
- [ ] 每实例独立 workspace/container，默认网络关闭。
- [ ] 生成规范 run artifact 树和原子 summary。
- [ ] 提交：`feat(eval): add reproducible agent harness runner`。

### Task 8.2：SWE-bench Verified adapter

**Files:**
- Modify: `eval/benchmarks/swebench.py`
- Create: `scripts/eval/run-swebench.ps1`

- [ ] 生成官方 predictions schema；调用官方 Docker harness，不改 tests/scorer。
- [ ] 先 1 instance，再固定分层 10，再批准 100；报告 resolved、infra failures、cost、trace IDs。
- [ ] 同模型/同预算比较 harness baseline 与新 harness。
- [ ] 提交：`eval(agent): run SWE-bench through official harness`。

### Task 8.3：Terminal-Bench 与 τ²-bench

**Files:**
- Create: `eval/benchmarks/terminalbench.py`
- Create: `eval/benchmarks/tau2bench.py`
- Test: `tests/eval/test_benchmark_contracts.py`

- [ ] 在线重新核验官方仓库、license、当前 runner API 和 dataset revision。
- [ ] Terminal-Bench 通过其容器 runner；τ²-bench 通过原生 domain runner。
- [ ] adapter 不伪装成统一 scorer；统一的是 run manifest/artifact/error taxonomy。
- [ ] 提交：`eval(agent): add terminal and tool-use benchmarks`。

## Phase 9：污染、发布门和 cutover

### Task 9.1：contamination scanner

**Files:**
- Create: `eval/contamination/scanner.py`
- Create: `tests/eval/test_contamination.py`

- [ ] exact/normalized n-gram/MinHash/embedding NN 四层扫描 benchmark 对 production chunks。
- [ ] 只隔离和报告，不自动删除数据。
- [ ] 高相似未审查项阻止发布。
- [ ] 提交：`feat(eval): detect benchmark corpus contamination`。

### Task 9.2：发布证据包和 alias 演练

**Files:**
- Create: `scripts/rag/preflight-cutover.ps1`
- Create: `scripts/rag/switch-alias.ps1`
- Create: `scripts/rag/rollback-alias.ps1`
- Create: `docs/releases/RAG-CUTOVER-techdocs-v2.md`（若 manifest generation 已更新，文件名必须与 manifest 精确一致）

- [ ] preflight 只读检查 MySQL/MinIO/ES counts、mapping/model、orphans、metrics、trace、contamination。
- [ ] 在测试 alias 演练 switch + rollback，保存完整请求/响应。
- [ ] 生产切换只执行一次 atomic aliases API；随后跑真实 Agent 中文查英文 E2E。
- [ ] 失败立即 inverse alias 回滚；不删除 v2/旧 index。
- [ ] 稳定观察期结束后仍需用户单独批准才能删除旧资产。
- [ ] 提交：`ops(rag): add evidence-gated alias cutover`。

## 全局验证矩阵

每阶段最少执行：

```powershell
git diff --check
go test ./... -count=1
go vet ./...
python -m pytest -q
```

大型/付费/下载测试使用显式开关：

```powershell
$env:CODE_AGENT_RUN_MINERU_E2E='1'
$env:CODE_AGENT_RUN_DB_INTEGRATION='1'
$env:CODE_AGENT_RUN_RAG_E2E='1'
$env:CODE_AGENT_RUN_TRACE_E2E='1'
$env:CODE_AGENT_RUN_PUBLIC_BENCHMARKS='1'
```

任何显式测试必须输出 artifact 路径、exit code、git SHA、数据/model revision、真实执行路径和降级状态。

## 回滚总边界

旧 `knowledge_base`、旧 alias target、MySQL/MinIO 备份、模型缓存、Docker volumes 和历史评测工件均保留。代码回滚按任务提交逆序；数据回滚只切 alias，不删物理索引。任何清理是独立破坏性任务，需要新批准。
