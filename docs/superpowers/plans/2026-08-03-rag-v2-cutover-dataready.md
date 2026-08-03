# RAG v2 Cutover 数据层就绪 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `knowledge_base_v2_bge_m3` 从 pilot 状态推进到数据层就绪：锁定真实 BGE-M3 revision、preflight 接入 server、全量导入 3095 语料、contamination 扫描、一致性审计。alias 切换与 qrels 检索门槛是下一阶段。

**Architecture:** 三个动作：(1) 配置锁定——`configs/server.yaml` 与 embedding 服务 env 的 `model_revision` 从占位符改为已验证的 HF commit `5617a9f61b028005a4858fdac845db406aefb181`（本机权重 5 个关键文件 SHA-256 已与远程 MATCH）；(2) 代码——`cmd/server/main.go` 启动时调用现有 `pkg/embedding.Preflight`（bilingual 样本 + revision/dim 校验，fail-closed），失败不阻塞启动但 `/healthz` 暴露 degraded 状态；(3) 执行——importer 每源全量导入（幂等续跑），`eval/contamination/scanner.py` 对 v2 全量扫描，preflight-cutover.ps1 一致性审计。

**Tech Stack:** Go 1.25（gin/gorm）、Python 3.11（requests/PyYAML）、MySQL/ES 8/Kafka/MinIO、本机 GPU BGE-M3（8009）、PowerShell（preflight 脚本）。

## Global Constraints

1. 不删除 Docker volume、ES 索引（legacy/v2）、MinIO 对象、staging、MySQL 行；不切换任何 alias（`knowledge_base_current` 保持指向 `knowledge_base`）。
2. BGE-M3 revision 唯一值：`BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181`（下称 `PINNED_REVISION`）。禁止使用浮动态 tag（@main/@latest）。
3. 受保护工作树文件不提交：`AGENT.md`、`docs/HANDOFF-2026-08-02-REAL-CORPUS-IMPORT.md`、`scripts/embedding_server.py`、`CLAUDE.md`、`CLAUDE-CODE-RECOVER-*`、`.tmp-go-cache-*`、`import_docs.py.broken-*`。
4. worker 启动必须带 `PAISMART_INTERNAL_TOKEN=<internal-shared-secret>`（与 `configs/server.yaml` 的 `ai.orchestrator.shared_secret` 一致）、`PAISMART_EMBEDDING_BASE_URL=http://127.0.0.1:8009`、`PAISMART_EMBEDDING_MODEL=BAAI/bge-m3`、`PAISMART_EMBEDDING_DIMENSIONS=1024`（缺失会致 embed 500）。执行时从 `configs/server.yaml` 读取真实值，不写入文档/报告。
5. 提交前 `go build ./...`、`go vet ./...`、`go test ./... -count=1`、`python -m pytest -q`、`git diff --check` 全绿；提交信息英文 + `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`。
6. 每任务提交后同步 `docs/PROGRESS-2026-08-03.md` 与 Obsidian `PROGRESS-2026-08-03.md`（中文）。
7. qrels 检索门槛（Recall@5≥0.80 等）不在本计划；180 题 qrels 由独立 agent 按 `docs/qrels/BUILD-180-QRELS.md` 建设中，其产出 `data/eval/techdocs/qrels.text.jsonl` 是下一阶段输入。

---

### Task 1: 锁定 BGE-M3 真实 revision（配置）

**Files:**
- Modify: `configs/server.yaml:68-76`（embedding 段）
- Modify: `scripts/rag/preflight-cutover.ps1:48`（`-ExpectedModelRevision` 默认值）
- Verify: 本机 embedding 服务重启 + `/health`

**Interfaces:**
- Produces: 运行配置 revision = `BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181`；preflight 脚本期望 revision 同步。

- [ ] **Step 1: 改配置**

在 `configs/server.yaml` 中：
```yaml
embedding:
    model: "BAAI/bge-m3"
    model_revision: "BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181"
```
（仅改 `model_revision` 一行；其余不动。）

在 `scripts/rag/preflight-cutover.ps1` 中把参数默认值：
```powershell
[string]$ExpectedModelRevision = "BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181",
```
（原占位符值替换。）

- [ ] **Step 2: 重启 embedding 服务并验证**

停止本机 embedding（8009）进程，用以下 env 重启（Python 3.12）：
```powershell
$env:EMBEDDING_MODEL='BAAI/bge-m3'
$env:EMBEDDING_REVISION='BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181'
$env:EMBEDDING_LOCAL_DIR='D:\tools\mineru-models\models\BAAI--bge-m3'
$env:EMBEDDING_PRELOAD='true'
$env:EMBEDDING_DEVICE='cuda'
$env:EMBEDDING_OUTPUT_DIMENSIONS='1024'
$env:HF_HOME='D:\tools\mineru-models\hf-cache'
$env:HF_ENDPOINT='https://hf-mirror.com'
Start-Process -FilePath 'C:\Python312\python.exe' -ArgumentList '-m','uvicorn','embedding_server:app','--app-dir','scripts','--host','0.0.0.0','--port','8009' -WorkingDirectory 'D:\vscode\localcode' -WindowStyle Hidden
```
等待 `/health` 返回（最多 120s）：
```powershell
Invoke-RestMethod http://127.0.0.1:8009/health
```
必须满足：`ready=true`、`dimensions=1024`、`model_revision="BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181"`。若不满足，停止并报告（不要继续）。

- [ ] **Step 3: 验证 worker 仍可 embed**

```powershell
$body=@{task=@{file_md5='diag';file_name='d.md';user_id=1;stage='embed'};texts=@('hello')}|ConvertTo-Json -Compress
Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:8090/v1/ingestion/embed' -Method POST -Headers @{'X-Internal-Token'='<internal-shared-secret>'} -ContentType 'application/json' -Body $body -TimeoutSec 40
```
期望 200 且 vectors[0] 长度 1024。若 worker 未运行，按 GC4 env（token 从 `configs/server.yaml` 读取）重启后重试。

- [ ] **Step 4: 提交**

```bash
git add configs/server.yaml scripts/rag/preflight-cutover.ps1
git commit -m "chore(rag): pin BGE-M3 revision to verified HF commit 5617a9f6
...
Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```
（`git diff --stat` 确认只改这两个文件。）

### Task 2: Preflight 接入 server 启动（TDD）

**Files:**
- Modify: `cmd/server/main.go`（初始化阶段 + `/healthz` handler）
- Test: `cmd/server/main_test.go`（新建，若不存在则建；参考 `internal/middleware/logging_test.go` 的 gin 测试模式）

**Interfaces:**
- Consumes: `embedding.Preflight(ctx, serverconfig.EmbeddingConfig) error`（`pkg/embedding/preflight.go`，已存在）
- Produces: `/healthz` 响应体扩展为 `{"status":"ok","embedding_preflight":"ok"}` 或 `{"status":"ok","embedding_preflight":"degraded:<脱敏原因>"}`；server 启动时调用一次 Preflight。

- [ ] **Step 1: 写失败测试**

在 `cmd/server/main_test.go`（新建）：
```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHealthzReportsEmbeddingPreflightOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/healthz", healthzHandler(func() string { return "ok" }))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var body struct {
		Status            string `json:"status"`
		EmbeddingPreflight string `json:"embedding_preflight"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" || body.EmbeddingPreflight != "ok" {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestHealthzReportsEmbeddingPreflightDegraded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/healthz", healthzHandler(func() string { return "degraded: embedding preflight failed" }))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body struct {
		Status            string `json:"status"`
		EmbeddingPreflight string `json:"embedding_preflight"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.EmbeddingPreflight != "degraded: embedding preflight failed" {
		t.Fatalf("body=%s", rec.Body.String())
	}
}
```
> 设计说明：抽取 `healthzHandler(statusFn func() string) gin.HandlerFunc` 纯函数便于测试；`statusFn` 返回预检状态字符串（"ok" 或 "degraded: <原因>"）。

- [ ] **Step 2: 运行确认失败**

`go test ./cmd/server/ -run TestHealthz -count=1` → 编译失败（`healthzHandler` 未定义）。

- [ ] **Step 3: 实现**

在 `cmd/server/main.go`：
1. 定义 `healthzHandler(statusFn func() string) gin.HandlerFunc`，返回 `c.JSON(http.StatusOK, gin.H{"status": "ok", "embedding_preflight": statusFn()})`。
2. 在初始化阶段（`es.InitES` 之后、`kafka.InitProducer` 前后均可）执行：
```go
embeddingPreflightStatus := func() string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := embedding.Preflight(ctx, cfg.Embedding); err != nil {
		log.Warnf("embedding preflight degraded (non-blocking): %v", err)
		return "degraded: embedding preflight failed"
	}
	log.Info("embedding preflight passed")
	return "ok"
}()
```
> 说明：Preflight 失败只降级不阻塞启动（与现有"embedding 不可用则检索降级"语义一致）；`healthz` 暴露状态供运维/自动化判断。`embedding` 包已 import（main.go 已有 `embeddingClient := embedding.NewClient(cfg.Embedding)`），确认 `pkg/embedding` 的 Preflight 导出函数可用。
3. 将 `r.GET("/healthz", ...)` 替换为 `r.GET("/healthz", healthzHandler(func() string { return embeddingPreflightStatus }))`（闭包捕获）。

- [ ] **Step 4: 运行确认通过 + 全量回归**

`go test ./cmd/server/ -run TestHealthz -count=1` PASS；`go build ./...`、`go vet ./...`、`go test ./... -count=1` 全绿。

- [ ] **Step 5: 提交**

```bash
git add cmd/server/main.go cmd/server/main_test.go
git commit -m "feat(server): run embedding preflight at startup, expose in healthz
...
Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 3: 全量导入 3095（执行，非 TDD）

**Files:**
- 不新建/不修改代码文件。
- 产出：`results/corpus/full-<src>-<ts>.json`（每源一份 report）。

**Interfaces:**
- Consumes: importer（`scripts/corpus/import_docs.py`，Task 8 重建版）、专用入口 server（最新构建）、embedding 8009、worker 8090（GC4 env）。

- [ ] **Step 1: 重建并重启 server（含 Task 2 代码）**

```powershell
# 停旧 server → go build -a → 启动 → 等 healthz
Get-Process -Name code-server -ErrorAction SilentlyContinue | Stop-Process -Force
go build -a -o "$env:TEMP\code-server.exe" ./cmd/server
Start-Process -FilePath "$env:TEMP\code-server.exe" -WorkingDirectory 'D:\vscode\localcode' -WindowStyle Hidden
# 轮询 /healthz 直到 {"status":"ok","embedding_preflight":"ok"}
```
验证 `embedding_preflight` 字段为 `ok`（Task 2 产物）。

- [ ] **Step 2: 前置健康检查**

`curl :8081/healthz`、`:8090/healthz`、`:8009/health`（revision 为新值）、`docker compose ps`（es/mysql/kafka/minio/tika up）、ES `GET /_cluster/health` 非 red。

- [ ] **Step 3: 逐源全量导入（6 源串行，后台长跑）**

对 `go, python, git, docker, kubernetes, postgresql` 依次执行（**一次一源**，串行避免 GPU 争抢）：
```powershell
$ts = Get-Date -Format 'yyyyMMdd-HHmmss'
python scripts/corpus/import_docs.py --server http://127.0.0.1:8081 `
  --token-file "$env:TEMP\corpus-internal-token.txt" `
  --staging "$env:TEMP\corpus-pins" --source <src> `
  --report "D:\vscode\localcode\results\corpus\full-<src>-$ts.json"
```
- 每源完成后记录：report 的 `attempted/active/skipped/failed` 与 `EXIT` 码。
- 中断/超时可重跑同源（幂等：已 ACTIVE 跳过）。
- 全部 6 源结束后，再跑一次全部源（limit 0）确认 `skipped` 全量（无新增 active/failed）作为"导入完成"信号。

- [ ] **Step 4: 逐源数据核对**

对每源执行：
```powershell
docker compose exec -T mysql mysql -N -B -ucodeagent --password=codeagent codeagent -e "SELECT COUNT(*), status FROM knowledge_document WHERE corpus_generation='techdocs-2026-07-30-v1' GROUP BY status;"
```
记录 ACTIVE / FAILED / SKIPPED 计数。抽查 3 个 ACTIVE 文档的 `document_id` 与 `source_path` 合理性。

- [ ] **Step 5: 记录进度**

把每源计数、总 ACTIVE、总 FAILED（分类）、总 SKIPPED 写入 `docs/PROGRESS-2026-08-03.md` 与 Obsidian 副本。

### Task 4: Contamination 扫描（执行）

**Files:**
- 产出：`results/contamination/<date>.jsonl`

**Interfaces:**
- Consumes: `eval/contamination/scanner.py`（`scan(...)`，支持 `embedding_fn` 注入）、ES v2 全量 chunks（或 MySQL `document_vectors` 文本）。

- [ ] **Step 1: 写 contamination 扫描驱动脚本（TDD）**

`eval/contamination/scanner.py` 无 CLI（只有 `scan()` 函数，`embedding_fn` 可注入）。新建可重复运行的驱动脚本 `scripts/corpus/scan_contamination.py`：

先写失败测试 `tests/corpus/test_scan_contamination.py`（RED）：
```python
def test_driver_builds_report_with_embedding_layer(tmp_path, monkeypatch):
    # scan() 的 embedding 层：注入 fake embedding_fn（返回定长向量）
    # 驱动脚本必须：读 ES v2 chunks（fake ES 响应）→ 调 scan(chunks, embedding_fn=fake) → 写 JSONL
    # 断言 JSONL 行含 id/source/similarity/reviewed 字段
    ...
```
> 实现要点：驱动脚本从 ES `knowledge_base_v2_bge_m3` 拉取 chunks 文本（`_search` scroll 或 size 分页），`embedding_fn` 封装本机 8009（`POST /embeddings`，单文本返回 1024 维），调用 `scan()`，把每层命中写为 JSONL 行（格式见 runbook §2.4）。CLI 参数：`--es-url`（默认 `http://127.0.0.1:9200`）、`--index`（默认 `knowledge_base_v2_bge_m3`）、`--embedding-url`（默认 `http://127.0.0.1:8009`）、`--out`（必填 JSONL 路径）。

- [ ] **Step 2: 运行全量扫描**

从仓库根执行：
```bash
python scripts/corpus/scan_contamination.py \
  --es-url http://127.0.0.1:9200 --index knowledge_base_v2_bge_m3 \
  --embedding-url http://127.0.0.1:8009 \
  --out results/contamination/2026-08-03.jsonl
```
记录每层（exact/n-gram/minhash/embedding）命中数与耗时。

- [ ] **Step 3: 阻塞项评估**

按 runbook §2.4：统计 `similarity >= 0.85 && reviewed != true` 的行数。若为 0 → 记录 PASS；若 > 0 → 抽样人工审查，审查通过的标 `reviewed: true`（或记录为已知良性），在报告中说明每类高相似的原因（如同源文档间重复段落、front-matter 模板）。

- [ ] **Step 4: 记录 + 提交**

驱动脚本 + 测试 commit（英文 + Co-Authored-By）；报告写入 PROGRESS。

### Task 5: 一致性审计 + Preflight 证据（执行）

**Files:**
- 产出：`results/releases/cutover-dataready-<ts>/` 下的 preflight.log 与审计证据。

**Interfaces:**
- Consumes: `scripts/rag/preflight-cutover.ps1`、ES/MySQL/MinIO。

- [ ] **Step 1: Orphan 审计**

v2 distinct `document_id`（terms 聚合，size 20000）vs `knowledge_document` ACTIVE 计数（generation=techdocs-2026-07-30-v1）。二者应一致；不一致则列出差异来源（FAILED/SKIPPED 不算 orphan）。

- [ ] **Step 2: 跑 preflight-cutover.ps1（qrels 门槛除外）**

```powershell
New-Item -ItemType Directory -Force -Path "results/releases/cutover-dataready-$(Get-Date -Format 'yyyyMMdd-HHmmss')" | Out-Null
powershell -ExecutionPolicy Bypass -File scripts/rag/preflight-cutover.ps1 `
  -ContaminationReport results/contamination/2026-08-03.jsonl `
  -MinEsChunkCount 1000 -SkipMinio $false `
  | Tee-Object -FilePath "results/releases/cutover-dataready-<ts>/preflight.log"
```
> 说明：`-MinEsChunkCount 1000` 表示 v2 至少 1000 chunk（全量后应远超）；contamination 阻塞按 Task 4 结果。`knowledge_base_current` 仍指向 legacy（alias 检查应 PASS）。若某检查 FAIL 且属于本阶段范围（如 orphan、embedding revision），修复后重跑。

- [ ] **Step 3: 记录数据层就绪结论**

汇总：v2 总 chunk 数、ACTIVE 文档数、FAILED/SKIPPED 分类、contamination 阻塞项、orphan 结果、preflight 各检查 PASS/FAIL。写入 PROGRESS。明确标注：**alias 未切换，qrels 检索门槛待 180 题建设完成后执行**。

## Verification

- Task 1: embedding `/health` 上报 `BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181` + dims 1024。
- Task 2: `go test ./... -count=1` 全绿；server `/healthz` 含 `embedding_preflight: ok`。
- Task 3: 6 源全量 ACTIVE 计数接近 manifest 候选（go 27 / python 554 / git 910 / docker 928 / kubernetes 468 / postgresql 208 减去空文档 SKIPPED）；v2 chunk 数大幅增长；无新增 FAILED（除已知空文档/数据质量问题，分类记录）。
- Task 4: contamination JSONL 生成；`similarity>=0.85 && !reviewed` 计数报告。
- Task 5: orphan=0；preflight 除 qrels 门槛外 PASS 证据保存。

## 回滚边界

- 代码：Task 2 按 commit revert（healthz 恢复原状）。
- 配置：revision 改回占位符或保留真实值（双向兼容；真实值更安全）。
- 数据：不删任何 ES 索引/MySQL 行/MinIO/staging；FAILED 文档可新 run 重放；SKIPPED（空文档）不重试。
- alias：未动（`knowledge_base_current` → `knowledge_base`）。
- 全量中断：重跑 importer 幂等续跑。
