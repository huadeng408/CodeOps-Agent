# 专用官方 Corpus 导入恢复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 严格执行 TDD：先写失败测试（RED）→ 确认失败 �?最小实现（GREEN）→ 全量回归 �?提交�?
**Goal:** 用受内部 token 保护的专用导入路径恢复官�?corpus 导入：修�?`go/doc/asm.html` 这类「parse SUCCESS �?chunk FAILED」文档，�?v2 索引获得真实数据，并完成单文档门禁、六�?pilot 门禁。全量导入与 alias 切换不在本计划内（后续门禁）�?
**Architecture:** 普�?`/api/v1/upload/*` 保持兼容不动；扩展现�?`/internal/orchestrator/knowledge-ingest` 入口接收完整 corpus provenance（source_id/source_path/source_commit/corpus_generation/content_sha256/target_index），服务端持久化 `KnowledgeSource`/`KnowledgeDocument` 生命周期（STAGED→ACTIVE/FAILED），并用显式 `RunID` 维度重构 pipeline task 幂等键，使受�?replay 不再被历�?parse SUCCESS 跳过。导入脚�?`scripts/corpus/import_docs.py` 安全重建为专用入口客户端 + 状态轮�?报告�?
**Tech Stack:** Go 1.25（gin、gorm/mysql、kafka-go、elasticsearch/v8、segmentio）、Python 3.11（pytest、requests、PyYAML）、MySQL、MinIO、Kafka、ES 8、Tika、本�?BGE-M3�?009）�?
## Global Constraints

1. 保留 `docs/HANDOFF-2026-08-02-REAL-CORPUS-IMPORT.md`、`AGENT.md`、`scripts/embedding_server.py`、`CLAUDE.md`、`CLAUDE-CODE-RECOVER-*` 的用户工作树修改，不覆盖、不提交。脚本重建在临时路径进行（见 Task 8）�?2. 普�?`/api/v1/upload/*`（check/chunk/merge/fast-upload）不接受、不解析 corpus 字段；corpus metadata 只能经内部入口进入。`upload_service.go` �?`MergeChunks` 不写 `CorpusGeneration`�?3. 禁止：删�?Docker volume、ES 索引（legacy/v2）、MinIO 对象、staging、MySQL 行、Kafka offset、DLQ 消息；禁�?`DELETE FROM pipeline_task`；禁�?alias 切换（`allow_alias_switch` 保持 false）。回滚只允许代码回退 + 受控 replay + alias 逆操作（本计划不�?alias 操作）�?4. 每次 Kafka produce 失败必须把错误返回给调用方（handler→非 2xx），不再吞掉只打日志；`documents/`、搜索、`initSeedFiles` 行为不变�?5. merge HTTP 2xx �?ACTIVE；ACTIVE 只能由「index 阶段成功后对 ES v2 的确认」决定（ES 轮询�?importer 侧做，server 侧只维护 document 状态）�?6. 所有服务端错误消息�?`knowledge_document.last_error` 脱敏：只�?source_id/source_path/stage/短摘要，绝不�?token、header、完整文件内容�?7. 每次提交前：`go build ./...`、`go vet ./...`、`go test ./... -count=1`、`python -m pytest -q`、`git diff --check` 全绿；提交信息英�?+ `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`�?8. 每任务提交后�?`docs/PROGRESS-2026-08-02.md` �?`D:\Obsidian\code-autogrowth\私人\localcode\PROGRESS-2026-08-02.md` 记录（中文，含分�?提交、事实、验证、数据快照、回滚边界）�?9. 所有环境验证（单文�?pilot/full）发生在主工作树部署�?server（`go build -a -o /tmp/code-server.exe ./cmd/server` 强制重建�? worker（`PAISMART_INTERNAL_TOKEN=<internal-shared-secret>`�? 本机 embedding�?009）上；`go build` 缓存坑见 HANDOFF §4.1�?10. 文本语料允许格式�?`md/rst/adoc/html/txt/sgml/xml`（manifest 枚举）。PDF 永远�?MinerU OCR（现有路径，本计划不涉及 PDF 变更）�?
## 推荐最小设计（架构决策�?
1. **专用 corpus request metadata**：扩�?`/internal/orchestrator/knowledge-ingest` �?multipart 表单（信任链 = InternalAuthMiddleware + 服务端强校验，普通用户不可达）。服务端是权威：`corpus_generation` 必须等于 `cfg.Corpus.Generation`、`loader_user` 必须等于 `cfg.Corpus.LoaderUser`、`target_index` 必须等于 `cfg.Corpus.TextIndex`（客户端提交值仅用于一致性校验，不一�?400）、`content_sha256` 必须等于服务端对 raw staging bytes 的计算值（�?handler 读流时同步计算）。缺任何字段 �?400 且零 service 调用�?2. **持久�?source/document record**：入口在 merge �?`CreateOrGetSource`（STAGED�? `CreateOrGetDocument`（STAGED，`document_id=source_id@commit:path`、`content_sha256`、`file_md5`、`target_index=cfg.Corpus.TextIndex`）。失�?�?`MarkDocumentStatus(FAILED, 脱敏错误)` + 返回�?2xx�?3. **受控 replay 从最早未成功 stage 开�?*：admin replay 扩展�?`ReplayPipelineTask(fileMD5, stage, runID)`；`runID`（如 `replay-<unix-nano>` �?`import-<unix-nano>`）写�?task �?`IdempotencyKey` �?Kafka 消息（`FileProcessingTask.RunID`）。消费者去重键改为 **run 感知**：带 RunID 的消息跳�?SUCCESS 检查（�?run 必须执行），不带 RunID 的消息保持旧语义（向后兼容）。对 `asm.html` �?`stage=chunk` replay：读取已保存�?`parsed/<md5>.json` artifact �?成功�?embed→index→ACTIVE�?4. **documents 状态推�?*：在 pipeline �?index 成功路径（`processIndexExternal`/`processIndex` 成功后）�?`task.DocumentID`（新增字段）更新 `knowledge_document.status=ACTIVE`；processChunkExternal �?DB 持久化失败处 `MarkDocumentStatus(FAILED)`。`processor` 通过新增 `documentRepo repository.KnowledgeDocumentRepository` 依赖（注入，nil 容忍以兼容旧测试）�?5. **import_docs.py 重建为专用入口客户端**：走 `/internal/orchestrator/knowledge-ingest`（X-Internal-Token �?`--token-file` 读取），上传后按 `source_id/commit/source_path` 轮询 `GET /internal/orchestrator/knowledge-documents?generation=...&status=ACTIVE` 直到 ACTIVE/FAILED/超时，输出脱�?JSON report �?exit code�?=全部 ACTIVE/合法 skip�?=任一失败�?=参数/manifest/preflight）。本轮轮询报告实现为脚本侧（server 只提供只读查询端点）�?6. **明确不做**：不�?alias 切换、不删数据、不做污染扫�?评测/Agent E2E（后续门禁）、不接管视觉链路、不修改 worker �?chunk 硬编�?generation 之外的契约（server �?chunk 阶段�?task.CorpusGeneration 为准覆盖——现�?`processor.go:554-557` 已保证）�?
## 关键风险（执行者确认后再动手）

- **R1（最高）**：消费者去重键变更的兼容性。现�?`pipeline_task` 已有 legacy 数据；`idempotency_key` 是唯一索引 `varchar(96)`。新 run 感知键（`run:<runID>:md5:stage:chunk`）与旧键共存：旧键行不受影响；同 runID 的重复消息去重；`MarkProcessing` �?upsert 用「新键查、旧键兼容」而不是整体换键�?- **R2**：历�?SUCCESS parse �?artifact 可能不可用（`parsed/<md5>.json` �?index 成功后被删除，但 asm.html �?index 从未成功，MinIO 中应�?artifact�?*必须现场验证**）。若 artifact 缺失，`stage=chunk` replay 会失败并进入 FAILED→DLQ——正确的处理是改�?`stage=parse`（新 run）整段重跑，而不是「修改历�?SUCCESS 行」�?- **R3**：worker �?`_text_to_elements` 不填 `source_sha256`/`source_url`，导�?v2 文档�?`source_sha256` 目前实际�?**artifact JSON �?hash**（`processor.go:536` �?`textBytes`）而非 raw 文件 hash——与 `knowledge_document.content_sha256`（raw 字节）不一致。最小修复：Go 侧在 chunk 填充处用「任务携带的 `SourceSHA256`（专用入口传入的 raw hash）」优先于 artifact hash（见 Task 6）�?- **R4**：`initSeedFiles`/`DocumentService` 与搜索路径不感知 corpus——保持不动；�?`MergeChunks` �?produce 错误上抛后，`initSeedFiles`（main.go:379）会因错误停止该文件——可接受（fail-closed）�?- **R5**：`EmbeddingCfg.ModelRevision` 仍是占位符（runbook 明确）。本计划不把它当�?cutover 前置（v2 写入�?`ValidateEmbeddingContract` 校验 revision 为不可变 commit 即可，占�?hash �?40-hex 不可变形式，现有测试全部通过）�?- **R6**：Windows 路径/编码。`import_docs.py` �?staging 路径来自 `--staging`（HANDOFF 明确 `$TEMP` 而非 `/tmp`）；报告文件�?UTF-8；脚本内所有相对路径用 `/` 规范化�?- **R7**：`knowledge_document` 幂等键冲突——`CreateOrGetDocument` �?content 变化时生�?`document_id#<sha256>` 新版本（`knowledge_document_repository.go:41`）。专用入口必须保持这个语义（同一 source 路径内容变化 �?�?document 版本，不覆盖审计历史）�?
## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/model/pipeline_task.go` | 修改 | �?`RunID` 字段（varchar 限制内） |
| `internal/model/task_provenance.go` | 新建 | `CorpusProvenance{SourceID, SourcePath, SourceURL, SourceCommit, SourceSHA256, TargetIndex, CorpusGeneration}` + `Validate()` |
| `pkg/tasks/tasks.go` | 修改 | `FileProcessingTask` �?`RunID`、`CorpusProvenance`、`DocumentID`（json 标签�?worker 契约一致） |
| `internal/repository/knowledge_source_repository.go` | 修改 | �?`MarkSourceStatus(sourceVersionID, status, lastError)` |
| `internal/repository/knowledge_document_repository.go` | 修改 | �?`GetDocumentByFileMD5(fileMD5)`、`ListDocumentsByGenerationAndStatus` |
| `internal/repository/pipeline_task_repository.go` | 修改 | �?`GetByRunKey`、`MarkProcessingRun`（run 前缀键）；`buildPipelineKey` 扩展 |
| `internal/service/corpus_ingest_service.go` | 新建 | 专用入口业务逻辑（校�?持久�?task 构建+produce 上抛�?|
| `internal/service/upload_service.go` | 修改 | `MergeChunks` produce 错误上抛 |
| `internal/handler/knowledge_ingest_handler.go` | 修改 | �?provenance 字段、算 raw sha256、调 `CorpusIngestService`、返�?4xx/5xx 语义 |
| `internal/handler/knowledge_document_handler.go` | 新建 | `GET /internal/orchestrator/knowledge-documents`（只读状态查询，InternalAuth�?|
| `internal/service/admin_service.go` | 修改 | `ReplayPipelineTask(fileMD5, stage, runID)` 支持 corpus �?run 语义 |
| `internal/handler/admin_handler.go` | 修改 | replay 请求�?`runId` |
| `internal/pipeline/processor.go` | 修改 | index 成功→document ACTIVE；chunk 失败→document FAILED；`SourceSHA256` 优先�?task 携带�?|
| `pkg/kafka/client.go` | 修改 | `consumeStage` �?RunID 决定去重策略 |
| `cmd/server/main.go` | 修改 | 装配�?service/repo/handler；内部组�?documents 查询路由 |
| `pkg/database/migration.go` | 修改 | `EnsureRuntimeSchema` �?`ALTER pipeline_task ADD COLUMN run_id VARCHAR(96) NULL`（容错风格同现有�?|
| `scripts/corpus/import_docs.py` | 重建 | 专用入口客户�?+ 状态轮�?+ JSON report + exit code |
| `tests/corpus/test_import_docs.py` | 修改 | 全部重写为新契约测试 |
| `internal/corpus/import_client.go` | 新建 | Go 侧导入客户端（`internal/rag` 风格，httptest 可测�?|

---

### Task 1: pipeline task 与消息契约加 run/provenance 维度（纯模型�?TDD�?
**Files:**
- Create: `internal/model/task_provenance.go`、`internal/model/task_provenance_test.go`、`internal/model/pipeline_task_test.go`、`pkg/tasks/tasks_test.go`
- Modify: `internal/model/pipeline_task.go`、`pkg/tasks/tasks.go`

**Interfaces:**
- Produces: `model.CorpusProvenance`（字段见上）+ `Validate() error`；`tasks.FileProcessingTask.RunID string`（json `run_id,omitempty`）、`DocumentID string`（json `document_id,omitempty`）、`Provenance *model.CorpusProvenance`（json `provenance,omitempty`）；`model.PipelineTask.RunID string`（`gorm:"type:varchar(96)"`）�?
- [ ] **Step 1: 写失败测�?* `internal/model/task_provenance_test.go`：合�?provenance 通过 `Validate()`；缺 source_id / source_path / source_commit（非 40-hex�? source_sha256（非 64-hex�? corpus_generation / target_index 各自返回明确错误；`SourceURL` 可选�?- [ ] **Step 2: 运行确认失败**：`go test ./internal/model/... -run TaskProvenance -count=1` �?编译失败（类型不存在）�?- [ ] **Step 3: 实现** `task_provenance.go`（沿�?`knowledge_contract.go` �?errRequired 风格�? `pipeline_task.go` �?`RunID` + `tasks.go` 加三字段�?- [ ] **Step 4: 回归**：`go test ./internal/model/... ./pkg/tasks/... -count=1` + 全量 `go test ./... -count=1`�?- [ ] **Step 5: 提交**：`feat(rag): add run/provenance dimensions to pipeline task contract`�?
### Task 2: 专用 corpus 入口服务（校�?+ source/document 持久�?+ task 构建 + produce 上抛�?
**Files:**
- Create: `internal/service/corpus_ingest_service.go`、`internal/service/corpus_ingest_service_test.go`
- Modify: `internal/service/upload_service.go:261-263`（produce 错误上抛�?
**Interfaces:**
- Consumes: `model.CorpusProvenance`、`repository.KnowledgeSourceRepository`、`repository.KnowledgeDocumentRepository`、`repository.UploadRepository`、`serverconfig.CorpusConfig`、`pkg/kafka.ProduceFileTask`（可注入 seam `producer func(tasks.FileProcessingTask) error`）�?- Produces:
  - `type CorpusIngestService interface { Ingest(ctx context.Context, req CorpusIngestRequest) (*CorpusIngestResult, error) }`
  - `type CorpusIngestRequest struct { UserID uint; OrgTag string; IsPublic bool; FileMD5, FileName string; TotalSize int64; ContentSHA256 string; Provenance model.CorpusProvenance; SourceVersionID, DocumentID string }`
  - `type CorpusIngestResult struct { FileMD5, FileName, ObjectURL, DocumentID string }`
  - 语义：校验失败→`ErrCorpusValidation`（handler 映射 400）；storage/DB 失败→原错误�?00）；**Kafka produce 失败→返回错�?*（不吞）�?
- [ ] **Step 1: 写失败测�?*（fake repos + 注入 producer 记录调用）：
  1. 合法请求：source �?STAGED 创建、document �?STAGED 创建、task 携带 `CorpusGeneration=cfg.Generation`、`Provenance`、`DocumentID`、`UserID=cfg.LoaderUser`（若 req.UserID≠cfg.LoaderUser �?校验错误）、producer 恰好被调用一次�?  2. `generation != cfg.Corpus.Generation`、`target_index != cfg.Corpus.TextIndex`、`content_sha256 != 服务端计算值` �?各自 `ErrCorpusValidation` 且零持久�?�?produce�?  3. producer 返回错误 �?`Ingest` 返回该错误�?  4. document 创建失败 �?返回错误且不 produce�?  5. 幂等：相�?document_id 二次 Ingest �?`CreateOrGetDocument` 返回既有行（fake 模拟 repo 语义），仍只 produce 一次�?- [ ] **Step 2: 运行确认失败**：`go test ./internal/service/ -run CorpusIngest -count=1`�?- [ ] **Step 3: 实现**。`MergeChunks` �?produce 错误处理改为 `return "", fmt.Errorf("merge: enqueue parse task failed: %w", err)`�?- [ ] **Step 4: 回归**：`go test ./internal/service/... ./internal/handler/... ./cmd/server/... -count=1` + 全量�?- [ ] **Step 5: 提交**：`feat(rag): add corpus ingest service with provenance validation and lifecycle rows`�?
### Task 3: 专用入口 handler 接线（provenance 表单 + raw sha256 + 语义错误码）

**Files:**
- Modify: `internal/handler/knowledge_ingest_handler.go`、`internal/handler/knowledge_ingest_handler_test.go`

**Interfaces:**
- Consumes: `service.CorpusIngestService`；`handler.KnowledgeIngestService` 接口扩展为含 `CorpusIngest`（保持普�?UploadChunk/MergeChunks 兼容）�?- Produces: 表单字段 `sourceId/sourcePath/sourceCommit/sourceUrl/corpusGeneration/sourceSha256/targetIndex`；响�?202 �?`documentId`�?00=校验/缺字段，500=服务�?持久�?produce 失败�?
- [ ] **Step 1: 写失败测�?*（复�?`serveKnowledgeIngest` 风格，新增字�?map）：
  1. 合法请求 �?202，recorded service 调用含完�?provenance �?raw sha256（测试文�?bytes 计算）�?  2. �?`sourceId`/`sourcePath`/`sourceCommit`/`corpusGeneration` �?400 且零调用�?  3. `sourceSha256` �?handler 计算�?raw bytes hash 不符 �?400 且零调用�?*防伪造路由的关键测试**）�?  4. `corpusGeneration != cfg.Corpus.Generation`（handler 侧先�?`serverconfig.Conf.Corpus`）→ 400�?  5. service 返回错误 �?500 且响应体不含内部细节�?  6. `userId` �?`cfg.Corpus.LoaderUser` 不符 �?400�?- [ ] **Step 2: 运行确认失败**�?- [ ] **Step 3: 实现**：handler 在现有流式读文件中同步计�?sha256（`crypto/sha256` 流式），构�?`CorpusIngestRequest`；错误映�?`errors.Is(err, service.ErrCorpusValidation)` �?400�?*注意**：handler �?`KnowledgeIngestService` 接口在测试里�?recording fake——扩展接口后必须同步更新 `recordingKnowledgeIngestService`，否则旧测试编译失败（这是预期的 RED 阶段）�?- [ ] **Step 4: 回归**：`go test ./internal/handler/... -count=1` + 全量�?- [ ] **Step 5: 提交**：`feat(rag): wire corpus provenance through internal knowledge-ingest handler`�?
### Task 4: 消费�?run 感知去重 + repository run �?
**Files:**
- Modify: `internal/repository/pipeline_task_repository.go`、`pkg/kafka/client.go`
- Test: `internal/repository/pipeline_task_repository_test.go`（新建，gorm sqlite in-memory——`go.mod` 已有 `modernc.org/sqlite`）、`pkg/kafka/client_test.go`（新建：�?`consumeStage` 抽取可测的决策函数）

**Interfaces:**
- Consumes: `model.PipelineTask.RunID`、`tasks.FileProcessingTask.RunID`�?- Produces:
  - `repository.PipelineTaskRepository` 增加：`GetByRunKey(runID, fileMD5, stage string, chunkID int)`、`MarkProcessingRun(...)`、`MarkSuccessRun(...)`、`MarkRetryRun(...)`、`MarkFailedRun(...)`（run 版本全部�?`run:<runID>:md5:stage:chunk` �?idempotency_key）�?  - `pkg/kafka` 增加纯函�?`func shouldSkipByStatus(previous *model.PipelineTask, hasRunID bool) bool`（无 runID→旧语义 SUCCESS skip；有 runID→false）�?- 语义：带 RunID 的消�?*永远执行**（不 skip SUCCESS），重复消息（同 runID+stage+chunk �?SUCCESS/PROCESSING）由 `MarkProcessingRun` �?upsert 幂等去重；无 RunID 的消息走旧键旧语义。`RetryCount`/`LastError` 记录�?run 行�?
- [ ] **Step 1: 写失败测�?*�?  - repository：`MarkProcessingRun` 创建�?`idempotency_key == "run:r1:md5:chunk:-1"`；重复调用返回同一行不报唯一冲突；`MarkSuccessRun` 更新同键；无 runID �?`MarkProcessing` 仍用旧键�?  - kafka：`shouldSkipByStatus(SUCCESS �? hasRunID=false) == true`；`shouldSkipByStatus(SUCCESS �? hasRunID=true) == false`；`FAILED 行` 两种情况�?false�?- [ ] **Step 2: 运行确认失败**�?- [ ] **Step 3: 实现**：repo 新方法（复用 `MarkProcessing` 的实现骨架，键含 run 前缀）；`consumeStage` �?`hasRunID := task.RunID != ""`，查行用 `GetByRunKey`（有 run）或 `GetByKey`（无），skip 判定�?`shouldSkipByStatus`，后�?Mark 调用对应 run 版本。`MarkProcessing` 语义保持兼容�?- [ ] **Step 4: 回归**：全�?Go 测试�?- [ ] **Step 5: 提交**：`fix(rag): run-aware pipeline dedup so controlled replay bypasses stale SUCCESS`�?
### Task 5: admin replay 支持 stage 恢复 + runID + corpus metadata

**Files:**
- Modify: `internal/service/admin_service.go`、`internal/handler/admin_handler.go`、`internal/service/admin_service_test.go`（新建）

**Interfaces:**
- Consumes: `tasks.FileProcessingTask.RunID/CorpusGeneration/Provenance/DocumentID`、`repository.KnowledgeDocumentRepository`�?- Produces: `ReplayPipelineTask(fileMD5 string, stage tasks.Stage, runID string) error`；当 `runID==""` 时服务端生成 `replay-<unixnano>`；corpus 文档（`KnowledgeDocument` 存在�?`FileMD5` 匹配）replay �?*自动携带该文档的 provenance**（source_id/source_path/source_commit/source_sha256/corpus_generation/target_index/document_id），`ObjectURL=""` 保留（外部路径会 presigned 回退）；�?corpus 文档 replay 行为与现在完全一致（普通上传不�?replay 获得 corpus generation）�?
- [ ] **Step 1: 写失败测�?*�?  1. corpus 文档 `stage=chunk` + runID �?产生 task �?`RunID`、`Provenance`、`DocumentID`、`CorpusGeneration`（fake producer 记录）�?  2. �?corpus 文档 �?task �?provenance 字段�?*回归：普通上传不会被 replay 抬升�?v2**）�?  3. `runID` 为空 �?自动生成且非空�?  4. 未知 stage �?错误（沿用现有校验）�?  5. `stage=chunk` �?`KnowledgeDocument` 状�?FAILED（如 asm.html）→ replay 直接允许（不检查历�?SUCCESS——run 语义）�?- [ ] **Step 2: 运行确认失败**�?- [ ] **Step 3: 实现**（`admin_service.go:337-365` 扩展）�?- [ ] **Step 4: 回归**�?- [ ] **Step 5: 提交**：`feat(rag): stage-resumable corpus replay with run identity`�?
### Task 6: pipeline 生命周期推进 + raw source_sha256 优先

**Files:**
- Modify: `internal/pipeline/processor.go`（`NewProcessor` �?`documentRepo repository.KnowledgeDocumentRepository` 参数—�?*所有现有测试调用点需同步**，或提供 `NewProcessorWithDocumentRepo` 兼容构造）、`internal/pipeline/processor_structured_test.go`、`processor_index_test.go`、`structured_index_test.go`

**Interfaces:**
- Consumes: `task.DocumentID`、`task.Provenance`、`repository.KnowledgeDocumentRepository`�?- Produces: index 成功（`processIndexExternal` �?`processIndex` �?ES 写入成功后）�?`documentRepo.MarkDocumentStatus(task.DocumentID, DocumentActive, "")`；`processChunkExternal` �?`docVectorRepo.BatchCreate` 失败 �?`MarkDocumentStatus(FAILED, 摘要)`；`processChunkExternal` 填充 `SourceSHA256` 时，�?`task.Provenance != nil && task.Provenance.SourceSHA256 != ""` 则用它（**raw 文件 hash 优先**），否则保持 artifact hash 兜底�?
- [ ] **Step 1: 写失败测�?*�?  1. `processIndexExternal` 成功（fake ingestion client + fake esWriter 返回 0 错误）→ `MarkDocumentStatus(ACTIVE)` 被调用且参数�?`task.DocumentID`�?  2. ES 写入失败 �?不调�?ACTIVE�?  3. `processChunkExternal` BatchCreate 失败 �?`MarkDocumentStatus(FAILED)`�?  4. `SourceSHA256` 填充：task �?`Provenance.SourceSHA256=rawhash` �?chunk �?`SourceSHA256 == rawhash`（现�?`client_fill_test` 语义不破坏：�?provenance 时仍�?artifact hash）�?  5. �?`task.DocumentID` �?不调�?documentRepo（nil 安全）�?- [ ] **Step 2: 运行确认失败**�?- [ ] **Step 3: 实现**。注�?`NewProcessor` 签名变更波及 `cmd/server/main.go:140-151` 与所�?`processor_*_test.go` 构造点——测试文件用注入�?fake documentRepo（可新增 `internal/pipeline/fake_document_repo_test.go`）�?- [ ] **Step 4: 回归**：全量�?- [ ] **Step 5: 提交**：`feat(rag): promote knowledge_document lifecycle on pipeline success/failure`�?
### Task 7: 只读状态查询端点（importer 轮询用）

**Files:**
- Create: `internal/handler/knowledge_document_handler.go`、`internal/handler/knowledge_document_handler_test.go`
- Modify: `internal/repository/knowledge_document_repository.go`（加 `ListDocumentsByGenerationAndStatus(generation string, statuses []string)`）、`cmd/server/main.go`（内部组加路由）

**Interfaces:**
- Consumes: `repository.KnowledgeDocumentRepository`、`serverconfig.CorpusConfig`�?- Produces: `GET /internal/orchestrator/knowledge-documents?generation=...&status=ACTIVE`（InternalAuth）→ 200 `{documents:[{documentId,sourceId,sourcePath,sourceCommit,contentSha256,status,lastError,targetIndex,corpusGeneration}]}`；`generation` 必须匹配 `cfg.Corpus.Generation` 否则 400；`status` 白名�?`STAGED/ACTIVE/FAILED`；`lastError` 只回脱敏摘要（模型已存摘要）�?
- [ ] **Step 1: 写失败测�?*：合法查询返回记录；generation 不匹�?400；未�?status 400；无 InternalAuth 401（沿�?`serveAuthenticatedKnowledgeIngest` 模式）�?- [ ] **Step 2: 运行确认失败**�?- [ ] **Step 3: 实现**�?- [ ] **Step 4: 回归**�?- [ ] **Step 5: 提交**：`feat(rag): read-only corpus document status endpoint for importer polling`�?
### Task 8: import_docs.py 安全重建（专用入口客户端 + 状态轮�?+ report�?
> 重建纪律�?*不原地修�?*。把半成�?`scripts/corpus/import_docs.py` 移到 `scripts/corpus/import_docs.py.broken-<date>`（保留现场），从 HEAD 版本重建为完整实现；`tests/corpus/test_import_docs.py` 全量重写为新契约测试。用户文件约束：HANDOFF 只保�?AGENT.md/embedding_server.py/CLAUDE.md——`scripts/corpus/import_docs.py` 的当前工作树修改是「部分编辑且不一致」，属于本计划重建对象，但删除现场前需 `git diff` 复核并记录旧内容到任务提交说明�?
**Files:**
- Create(重建): `scripts/corpus/import_docs.py`
- Modify: `tests/corpus/test_import_docs.py`

**Interfaces:**
- Consumes: `POST /internal/orchestrator/knowledge-ingest`（multipart：file + `userId=cfg.loader_user` + `orgTag=corpus` + `isPublic=true` + 六个 provenance 字段 + `sourceUrl=<repo_url>/blob/<commit>/<path>` 由脚本按 manifest 构造）、`GET /internal/orchestrator/knowledge-documents?generation=...&status=ACTIVE`�?- Produces: CLI（参�?`--server/--token-file/--manifest/--staging/--source/--limit/--file`，`--limit` �?attempted）、exit code 0/1/2、`--report <path>` JSON：`{runId, source, generation, commit, selected, attempted, queued, active, skipped, failed, durationSec, files:[{path,sha256,md5,status,failure}]}`（token 永不�?report/日志）�?
- [ ] **Step 1: 写失败测�?*（全部离线：monkeypatch `requests.post`/`requests.get`，临�?manifest/staging/token 文件）：
  1. 选择策略：include/exclude/allowed_formats 与现�?`test_manifest_document_paths_apply_excludes_and_allowed_formats` 等价（重建后保留该断言）�?  2. `--file` 只能�?manifest 内路径（沿用现有拒绝测试）�?  3. `--limit` �?attempted 计数（沿用现有测试语义）�?  4. 上传请求断言：调�?`knowledge-ingest` 端点、`X-Internal-Token` 来自 token 文件、form 含全部六�?provenance 字段、`sourceUrl` 格式正确、`isPublic=true`�?  5. **fast-upload 不再用于 corpus**：脚本不得调�?`fast-upload`；重复导入由「ACTIVE 轮询结果」判断为 skipped（合�?skip），失败文档必须 requeue（新 run），不得误报完成�?  6. 轮询：上传后 GET 状态端点，`ACTIVE` �?queued/active 计数；`FAILED` �?failed �?exit 1；超时（可注�?clock）→ failed�?  7. 失败退出：任一文档 FAILED �?exit 1；参�?manifest/preflight 错误 �?exit 2�?  8. report 写入含规定字段且不含 token 字符串�?- [ ] **Step 2: 运行确认失败**（重建文件不存在 �?收集失败）�?- [ ] **Step 3: 实现**（完整重建；脚本保持�?`python scripts/corpus/import_docs.py` 直跑；路径用 `Path` + `/` 规范化）�?- [ ] **Step 4: 回归**：`python -m pytest tests/corpus/ -q` + 全量 pytest；`go test ./...` 不受影响�?- [ ] **Step 5: 提交**：`feat(rag): rebuild corpus importer as dedicated provenance client with ACTIVE polling`�?
### Task 9: Go 侧导入客户端（可选但推荐；为 pilot/full 自动化铺路）

**Files:**
- Create: `internal/corpus/import_client.go`、`internal/corpus/import_client_test.go`

**Interfaces:**
- Consumes: `internal/rag` �?client 风格（httptest）�?- Produces: `corpus.NewImportClient(baseURL, internalToken string, httpClient *http.Client) *ImportClient`；方�?`Ingest(ctx, req IngestRequest) (*IngestResult, error)`（multipart �?Task 8 契约）、`ListActiveDocuments(ctx, generation string) ([]model.KnowledgeDocument, error)`�?- 本任务交付纯库（CLI 包装可后置）；测试断言 multipart 字段/路径/token/错误传播�?
- [ ] **Step 1: 写失败测�?*（httptest server 断言请求形状，参�?`internal/rag/client_test.go:296-362`）�?- [ ] **Step 2: 运行确认失败**�?- [ ] **Step 3: 实现**�?- [ ] **Step 4: 回归**�?- [ ] **Step 5: 提交**：`feat(rag): add Go corpus import client for pilot automation`�?
### Task 10: 环境验证——单文档门禁（真实验收，�?TDD 步骤，但必须全绿才算本计划完成）

**前置（只�?启动，无破坏）：** `docker compose ps` �?Up；ES health �?red；`curl :8081/healthz`；worker 8090 `/healthz=ok`；embedding `:8009/health` 报告 `dimensions=1024` + revision；`go build -a -o /tmp/code-server.exe ./cmd/server` 重启 server；记录快照（legacy count、v2 count=0、alias 指向、MinIO 对象数、`knowledge_document`/`knowledge_source`/`pipeline_task` 行数）到 PROGRESS 文档�?
**步骤�?*
1. 用重建后�?importer 单文档跑 `go/doc/asm.html`（manifest-selected）：`python scripts/corpus/import_docs.py --server http://127.0.0.1:8081 --token-file $TEMP/corpus-token.txt --staging $TEMP/corpus-pins --source go --file doc/asm.html --report results/corpus/asm.html.json`�?2. 验证（`verification-before-completion` 全部断言）：
   - report 中该文档 `active=true`（或明确 `failed` 带脱敏原因）�?   - MySQL：`pipeline_task` 出现 run 键行（`run:*:796c9a98...:*`）且 parse/chunk/embed/index �?SUCCESS；`knowledge_document` �?status=ACTIVE、`content_sha256` �?`source_sha256` 关系正确、`target_index=knowledge_base_v2_bge_m3`；`document_vectors` 有该 md5 的行�?`source_sha256`/`parser_version`/`corpus_generation`/`model_version` 完整、vector 1024 维�?   - ES v2：`_count` > 0；`_search` 命中�?`document_id` �?chunk，`vector` 1024 维、provenance 字段齐全�?   - legacy/alias 未变（`knowledge_base_current` 仍指�?`knowledge_base`，count=44 不变）�?   - �?DLQ 新增消息�?3. **R2 验证**：若 `stage=chunk` replay �?artifact 缺失失败 �?执行受控 `stage=parse` �?run 重放（admin API �?runId），确认整段成功——该结果记录�?R2 的最终处理证据�?
### Task 11: pilot 门禁（本计划范围的终点；full/cutover 是后续计划）

**步骤�?*
1. 每源 `--limit 20`（确定�?`PilotSelector` 已存在；�?`internal/corpus` �?`PilotSelector` 语义�?importer 的确定�?hash 排序——二者已一致，�?`checkpoint.go:101-137`）�?2. 逐源验证：report �?ACTIVE/合法 skip；MySQL/`document_vectors`/ES v2 计数逐源核对；失败文�?�?manifest 阈值（当前 manifest 无显式阈值字段—�?*默认 0 失败才通过**，任何失败停止该源并记录）；无孤�?vector（v2 distinct `document_id` == `knowledge_document` ACTIVE 数）�?3. 任一源失�?�?停止 full（按设计 §7：pilot 失败停止全量阶段）�?4. 输出：脱敏报告落 `results/corpus/pilot-<ts>.json`（含每源 selected/attempted/active/failed、耗时、失败原因摘要、`run_id` 列表——供后续恢复续跑）�?
### Task 12: 收尾与回滚边界固�?
- 更新 `docs/HANDOFF-2026-08-02-REAL-CORPUS-IMPORT.md` 为「专用路径已落地 + 单文�?pilot 结果 + 下一�?full」状态；同步 Obsidian PROGRESS�?- 回滚边界（写入文档并遵守）：
  - 代码回滚：本计划提交按序 `git revert`/`git reset`；`import_docs.py.broken-<date>` 保留供恢复�?  - 数据回滚：不删任何行；重放失败只追加 FAILED run 行；ACTIVE 文档如需重做 �?�?run 受控 replay（幂等重�?document_vectors/ES �?`vector_id`，`vector_id=file_md5_chunkid` 稳定）�?  - alias 未动；`knowledge_base` �?`knowledge_base_v2_bge_m3` 双保留�?- 完成定义（本计划）：Task 1-9 全部 TDD �?+ Task 10 单文档门�?PASS + Task 11 pilot 通过（或明确记录 BLOCKED 原因）。full 3095 �?alias cutover 明确标记为下一阶段�?
## Verification

- `python -m pytest tests/corpus/ -q`；全�?`go test ./... -count=1`�?- 环境：`docker compose ps` �?Up；ES health �?red；worker/embedding health 符合预期；embedding `/health` dimensions=1024�?- 单文档后核对 pipeline_task（run 键行�?SUCCESS）、knowledge_document=ACTIVE、document_vectors、ES v2 文档�?1024 维、legacy/alias 未变、无 DLQ�?- pilot 每源 report �?ACTIVE/合法 skip，失败停�?full�?