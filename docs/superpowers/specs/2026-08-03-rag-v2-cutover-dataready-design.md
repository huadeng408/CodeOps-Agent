# RAG v2 Cutover 数据层就绪设计（BGE-M3 revision + preflight + 全量导入 + contamination）

## Context

调研裁决（2026-08-03，多模态RAG/可观测性/评测集三线）：文本主链是唯一"代码+路径+资源全就绪"的线。v2 索引已有 30+ 真实 chunk（6 源 pilot），但全量 3095 未导入、alias 未切、BGE-M3 revision 是占位符、preflight 未接入 server 启动。

用户决策（brainstorming）：**本轮做到"数据层就绪"**——锁定真实 BGE-M3 revision → preflight 接入 → 全量导入 3095 → contamination 扫描 → 一致性审计。alias 切换与 qrels 检索门槛留到下一阶段（qrels 已由独立强 agent 建设，任务书 `docs/qrels/BUILD-180-QRELS.md`）。

## 已验证事实

- **BGE-M3 真实 revision 已锁定**：HF commit `5617a9f61b028005a4858fdac845db406aefb181`（BAAI/bge-m3 默认分支，2024-07-03）。本机 `D:\tools\mineru-models\models\BAAI--bge-m3` 与远程该 commit 的 5 个关键配置文件（config.json/modules.json/sentence_bert_config.json/config_sentence_transformers.json/special_tokens_map.json）SHA-256 **全部 MATCH**。当前 `configs/server.yaml` 与 embedding 服务 env 使用占位符 `8f1b7f9d4c2a6e5b0d9c8f7a6b5c4d3e2f1a0b9c`。
- **preflight 代码存在**（`pkg/embedding/preflight.go`：bilingual 样本 + `ValidateEmbeddingContract` revision/dim 校验 + health revision 比对，fail-closed），**未接入 `cmd/server/main.go`**（grep 无调用）。
- **全量导入**：importer（Task 8 重建）支持每源全量（`--limit 0`）、幂等（ACTIVE 预检 skip）、空文档 SKIPPED、断点续跑（新 run 每次重新触发未 ACTIVE 文档）。6 源候选 3095 份（go 27 / python 554 / git 910 / docker 928 / kubernetes 468 / postgresql 208）。
- **contamination scanner 就绪**：`eval/contamination/scanner.py` 4 层（exact hash / n-gram / MinHash+LSH / embedding NN），`embedding_fn` 可注入（本机 8009）。
- **一致性审计**：v2 distinct document_id vs `knowledge_document` ACTIVE 计数（preflight-cutover.ps1 已有 orphan check 逻辑）。

## 设计

### 1. BGE-M3 revision 锁定（真实 commit）

- `configs/server.yaml` `embedding.model_revision` → `BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181`。
- 本机 embedding 服务重启 env `EMBEDDING_REVISION` → 同值。
- `/health` 必须上报新 revision + dimensions=1024。
- `scripts/rag/preflight-cutover.ps1` 的 `-ExpectedModelRevision` 默认值同步更新（或运行时传参）。

### 2. Preflight 接入 server 启动

- `cmd/server/main.go`：初始化阶段调用 `embedding.Preflight(ctx, cfg.Embedding)`。
  - 语义：失败**不阻塞启动**（保持现有"embedding 不可用则检索降级"语义），记录 WARN 日志 + 设置 degraded 标志；成功记录 INFO。
  - `/healthz` 响应体扩展暴露 `"embedding_preflight": "ok"|"degraded:<原因>"`（脱敏，不含内部细节）。
- 测试：preflight 成功/失败路径的 healthz 状态 + 日志（TDD）。

### 3. 全量导入 3095

- 每源运行 importer 全量：`python scripts/corpus/import_docs.py --server http://127.0.0.1:8081 --token-file %TEMP%\corpus-internal-token.txt --staging %TEMP%\corpus-pins --source <src> --report results/corpus/full-<src>-<ts>.json`（6 源串行，避免 embedding GPU 争抢）。
- 后台长跑（预计 5-20 小时）；importer 自带幂等（已 ACTIVE 跳过）与断点续跑（重跑跳过已 ACTIVE）。
- 前置：embedding 服务（8009）保持在线；worker 带正确 embedding env；server 为最新构建。
- 每源完成后核对：该源 v2 chunk 计数、ACTIVE 文档数、FAILED/SKIPPED 分类。
- 全量结束条件：6 源均无待处理文档（重跑 importer 全 skip）。

### 4. Contamination 扫描

- 对 v2 全量 chunk 跑 `eval/contamination/scanner.py`：
  - 输入：ES v2 chunks（或 MySQL document_vectors 文本）；注入 `embedding_fn`（调本机 8009）开启 embedding 层。
  - 输出：`results/contamination/<date>.jsonl`（每行 `{id, source, similarity, reviewed}`）。
  - 阻塞规则（runbook §2.4）：`similarity >= 0.85 && reviewed != true` → preflight FAIL。
- 首轮扫描为**未审查基线**（reviewed=false）；若出现高相似项，人工审查后标 reviewed（或记录为已知良性，如同源文档片段重复）。

### 5. 一致性审计

- v2 distinct `document_id`（terms 聚合）== `knowledge_document` ACTIVE 数（同 generation）。
- 每源 v2 chunk 数 vs 该源 ACTIVE 文档数核对。
- 无孤儿（ES 有而 MySQL 无，或反之）。
- 用 `scripts/rag/preflight-cutover.ps1` 跑一次（除 contamination/qrels 门槛外的检查应全 PASS），保存证据。

### 6. 明确不做（本阶段）

- alias 切换（`knowledge_base_current` 保持指向 legacy）。
- qrels 检索门槛（180 题由独立 agent 建设，评测是下一阶段）。
- 视觉链路 / ColQwen2。
- 任何删除操作。

## 验证

1. `go build ./...` + `go test ./... -count=1`（preflight 接入测试）+ `python -m pytest -q`。
2. embedding `/health` 上报 `BAAI/bge-m3@5617a9f6...` + dims 1024。
3. server `/healthz` 暴露 `embedding_preflight: ok`。
4. 全量后：v2 count == 预期 chunk 总数；每源 ACTIVE 计数核对；FAILED/SKIPPED 分类报告。
5. contamination 报告生成 + 阻塞项评估。
6. preflight-cutover.ps1 除 qrels 外全 PASS 证据保存。
7. PROGRESS 同步（Obsidian + docs/）。

## 回滚边界

- 代码：preflight 接入按 commit revert；revision 改回占位符（或保留真实值，双向兼容）。
- 数据：不删 ES/MySQL/MinIO/staging；FAILED 文档可新 run 重放；SKIPPED（空文档）不重试。
- alias：未动。
- 全量导入中断：重跑 importer 幂等续跑。
