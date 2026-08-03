# 真实语料导入恢复与专用导入路径设�?
## Context

依据 `docs/HANDOFF-2026-08-02-REAL-CORPUS-IMPORT.md`，本阶段目标是让已锁定的六个官方仓库进入 `knowledge_base_v2_bge_m3`，先完成单文档门禁，再执行六来源 pilot 和全量导入；`knowledge_base_current` 在所有门禁通过前继续指向旧索引�?
已验证的环境恢复事实�?
- Docker Desktop �?WSL 重置后恢复，MySQL、Redis、MinIO、Tika、ZooKeeper、Kafka、ES 已启动；不启�?Docker embedding，保留本�?BGE-M3 GPU 服务�?- Go server 已通过 `go build -a` 重建并健康；Python worker 已带 `PAISMART_INTERNAL_TOKEN=<internal-shared-secret>` 重启并健康；本机 embedding `/health` 已报�?1024 维�?- ES v2 mapping 存在但当前计数为 0，legacy �?44，alias 仍指�?legacy�?- 同一 Go 文档 `go/doc/asm.html` 的真实上传返�?merge 200，但 v2 仍为 0。MySQL 中该 MD5 �?`parse=-1=SUCCESS`、`chunk=-1=FAILED`，重投相�?MD5 �?parse 消息会被 `pkg/kafka/client.go` �?SUCCESS 去重分支跳过；因此失败的 chunk 不会恢复�?- 正常上传创建�?`FileProcessingTask` 当前没有 corpus generation；`CorpusGeneration` 虽在 task 结构中存在，但上�?handler/service 没有接收、持久化或写入它。因此即使重新处理，官方 corpus 也缺少受控的 v2 路由来源�?- 当前 `scripts/corpus/import_docs.py` 已补�?manifest include/exclude/format 过滤、显�?`--file`、严格尝试数和失败非零退出测试；这部分应继续作为专用导入入口，不把普通用户上传自动标记为 corpus�?
本轮边界经用户确认：**采用专用 corpus 导入路径**，不�?corpus metadata 无条件扩展为普通用户上传契约；普通上传保持兼容，官方导入使用显式受控 metadata 和专用恢复语义�?
## Recommended design

### 1. 专用 corpus ingestion contract

保留现有普�?`/api/v1/upload/*` API 作为 legacy/通用上传。扩展受内部 token 保护的专用入�?`/internal/orchestrator/knowledge-ingest`，使其接收以下字段：

- `source_id`
- `source_path`
- `source_url`（由 repository URL、locked commit �?source path 构造）
- `source_commit`
- `corpus_generation`
- `content_sha256`（必须是 staging 原始文件 bytes �?SHA-256�?- `target_index`（由服务端按 generation 配置解析，客户端只提交期望值用于一致性校验）
- `file_name`/bytes、`loader_user`、`is_public=true`

服务端在入口校验 generation、loader user、target index �?content hash；客户端不能通过任意 multipart 字段把普通上传路由到 v2。所�?provenance 进入专用 upload request/context，并在生�?`FileProcessingTask` 时写�?`CorpusGeneration`，同时保�?source identity 的传递载体（扩展 task 或持久化 document record）�?
利用已有 `KnowledgeSource`/`KnowledgeDocument` 模型�?repository�?
- `source_version_id = source_id + source_commit + corpus_generation`
- `document_id = source_id + source_commit + source_path`
- `content_sha256 = SHA-256(raw staged bytes)`
- `target_index = cfg.Corpus.TextIndex`
- 初始状态为 `STAGED`，只�?pipeline parse/chunk/embed/index、向量写入和 ES v2 完整校验均成功后才为 `ACTIVE`；任何失败为 `FAILED`，保�?retry count 和脱敏错误�?
本阶段将完成 source/document lifecycle 的最小接线：专用入口创建或复�?source/document 记录，失败标�?`FAILED`，index 成功后标�?`ACTIVE`。这�?single-document �?pilot 门禁能以持久化状态而非 HTTP merge 2xx 判定完成�?
### 2. Failed-stage replay

�?pipeline task identity 中加�?run/generation 维度，或提供等价的显式新-run key，避免历�?`SUCCESS(parse)` 遮蔽新的恢复运行。最小可行方案：

- replay 请求读取�?upload/document provenance�?- 对失败文档默认从最早未成功 stage 开始，而不是固定从 parse 开始；本例�?`chunk`�?- 允许 `chunk` replay 读取已保�?parsed artifact，成功后产生 embed/index�?- SUCCESS skip 只针对同一 run/generation/task identity，有�?run 时不得跳过；
- replay 成功只在 downstream ACTIVE/ES 一致性检查通过后更�?checkpoint�?
扩展现有 admin replay API 以显式支�?`stage=chunk` 和受�?corpus metadata；不手工删除 task 行，不重�?Kafka offset，不清空数据�?
### 3. Importer policy and reporting

`import_docs.py` 作为 transport/selection layer�?
- manifest 选择同时执行 include、exclude、allowed_formats；未�?source、路径穿越、symlink/junction 逃逸和�?policy 失败�?- `--file` 只能�?manifest-selected relative path；`--limit` 限制 attempted 数，不以成功数计数�?- merge 200 只表�?queued/upload transport 成功，不能报�?ACTIVE；fast-upload 命中时必须明确是 skip 还是受控 requeue，不能误把已有失败文档当完成�?- multipart/merge 传�?`corpus_generation`、`source_commit` 和完�?provenance；token 只从指定本地文件读取，不写日志�?- 退出码：所有目�?ACTIVE/合法 skip �?0；任一处理失败�?1；参数、manifest、环�?preflight 失败�?2。当�?pilot 前先实现失败非零和选择策略，ACTIVE 状态轮�?报告作为下一阶段实现�?- 结构�?report 至少记录 run id、source/commit/generation、selected/attempted/queued/active/skipped/failed、每文件相对路径�?hash、失败原因、耗时、checkpoint；脱敏，不含 token/header�?
### 4. Implementation sequence

1. **Contract tests first (RED)**�?   - `tests/corpus/test_import_docs.py`：manifest exclude/format、explicit file、limit attempted、failed exit、fast-upload/reenqueue、metadata fields�?   - Go handler/service tests：专用入口拒绝缺�?不匹�?provenance；合�?corpus request 进入 task；普�?upload 不自动获�?corpus generation�?   - pipeline tests：`parse SUCCESS + chunk FAILED` 的受�?chunk replay 不被�?parse SUCCESS 阻塞；new run identity 不跳过；v2 generation/target index/vector dimensions 全链路保留�?2. **Implement minimal dedicated path**：扩展内�?ingestion request/context、task metadata 和服务端任务创建；复用现�?`CorpusConfig`、`KnowledgeDocumentRepository`、`documentVectorFromStructuredChunk`、`indexNameFor`�?3. **Implement replay semantics**：扩�?`PipelineTaskRepository`/consumer identity 或新增受�?replay method，最小先支持从失�?stage replay；禁止通过普�?merge �?parse 重投恢复�?4. **Importer integration**：把 source metadata �?raw SHA256 传入专用入口；增�?report �?ACTIVE polling/checkpoint only after verification；保留当前通用 upload fallback 禁止用于 corpus�?5. **Single-document gate**：固�?`go/doc/asm.html`（或另一�?manifest-selected file），确认 parse/chunk/embed/index SUCCESS、v2 文档存在、vector 1024、source_sha256/parser_version/corpus_generation/model_version/target_index 完整，并确认 legacy/alias 未改变�?6. **Pilot gate**：每源选择 20�?0 个稳定文件；逐源核查 failure/DLQ、MySQL/MinIO/document_vectors/ES/v2 计数；任�?source 失败停止 full�?7. **Full and cutover**：六�?pilot 全通过后再全量 3095；运行污染扫描、真�?qrels 检索评测、release preflight；只有满足门槛才原子�?alias，失败保�?v2 与旧 alias�?
## Error handling and rollback

- Docker/Redis/Kafka/ES/worker/embedding 任一依赖不健康，阻断导入�?- merge HTTP 2xx 不等�?ACTIVE；Kafka produce 错误必须对调用方可见或进入明�?FAILED 状态�?- 任一 parse/chunk/embed/index/ES 失败，文档保�?FAILED，不�?checkpoint；错误只保留 source/path 和脱敏摘要�?- 不删�?Docker volume、ES index、MinIO、staging 或历�?task；回滚只使用代码回退、受�?replay �?alias 原子回滚�?
## Verification

静�?单元�?
- `python -m pytest tests/corpus/test_import_docs.py -q`
- `go test ./pkg/orchestrator ./internal/pipeline ./pkg/es ./internal/handler ./internal/repository`
- 全量 `go test ./...` 和相�?Python contract tests�?
环境�?
- `docker info`、`docker compose ps`；ES cluster health yellow/�?red；MySQL/Redis/Kafka/MinIO/Tika 可达�?- worker `/healthz=ok` 且内�?token 路由返回 200；embedding `/health` ready、revision 匹配�?dimensions=1024�?- 导入前记�?legacy/v2/alias/MinIO/MySQL 快照�?- 单文档完成后查询 pipeline_task、knowledge_document/document_vectors、ES v2 精确文档�?vector 维度；确认无 DLQ、孤�?vector 或错�?alias�?- pilot/full report 保存�?`docs/`/results 脱敏路径，并同步 `D:\Obsidian\code-autogrowth\私人\localcode\PROGRESS-2026-08-02.md`�?
## Out of scope

- 本轮不切�?`knowledge_base_current` alias�?- 不删除旧索引、Docker volume、MinIO/staging 数据�?- 不将 benchmark/qrels/答案写入生产 corpus�?- 不启�?Docker embedding 抢占本机 8009�?