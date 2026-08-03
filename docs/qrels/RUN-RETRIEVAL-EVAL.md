# 任务说明：对 180 条 qrels 执行检索评测（交付给能力极强的 Agent）

> 本文件是给执行 Agent 的**自包含任务书**。执行 Agent 需读完全文后独立完成：读取查询与 qrels → 对 `knowledge_base_v2_bge_m3` 生成多路检索 predictions → 用官方 runner 计算指标 → 产出报告与验收。过程中不需要人类介入。

## 1. 任务目标

对 180 条人工 qrels 在 RAG v2 语料（`knowledge_base_v2_bge_m3`，BGE-M3 1024 维）上执行确定性检索评测，产出 **6 个命名 run** 的 predictions + 指标报告，作为 alias 切换"检索门槛"（Recall@5 / MRR@10 / nDCG@10）的证据。**不切换任何 alias**。

## 2. 输入

### 2.1 qrels（ground truth，已就绪）

- 路径：`D:\vscode\localcode\data\eval\techdocs\qrels.text.jsonl`（180 行）
- 格式（白名单字段）：`{"query_id","document_id","section_path","relevance","language","query_type","source_id"}`
- `document_id` = `{source_id}@{source_commit}:{source_path}`；`section_path` 是标题路径数组（可空）。

### 2.2 查询文本（已就绪）

- 路径：`D:\vscode\localcode\data\eval\techdocs\queries.text.jsonl`（180 行）
- 格式：`{"query_id":"go-q001","query":"..."}`，`query` 为该 query_id 的查询原文（英文或中文）。query_id 与 qrels 一一对应。

### 2.3 检索基础设施

- ES：`http://127.0.0.1:9200`，索引 `knowledge_base_v2_bge_m3`（`dense_vector` 1024 维 cosine，字段含 `text_content`/`embedding_text`/`document_id`/`source_path`/`section_path`/`corpus_generation`/`target_index`/`model_version` 等，**以实际 mapping 为准**，先 `GET /knowledge_base_v2_bge_m3/_mapping` 确认字段名）。
- Embedding：`http://127.0.0.1:8009`（OpenAI 兼容 `POST /embeddings`，`{"model":"BAAI/bge-m3","input":[...],"dimensions":1024}`，返回 `data[].embedding`）。
- Reranker（可选）：`http://127.0.0.1:8008`（可能未运行，不可用时该 run 标记 disabled 而非伪造）。
- 说明：语料字段若与上面假设不同，以实际 mapping/示例文档为准，并在报告中记录。

## 3. 输出（唯一交付物）

### 3.1 predictions（6 个命名 run，每 run 一个 JSONL）

路径：`results/retrieval/{run}-predictions.jsonl`，其中 run ∈ `bm25, bge_m3, hybrid_rrf, hybrid_rerank, bm25_v2, bge_m3_v2`（后两个与前三同源可省略，改为在报告里注明；**至少产出 bm25 / bge_m3 / hybrid_rrf 三个**，若 reranker 可用则加 hybrid_rerank）。

每行格式（**白名单**）：
```json
{"query_id":"go-q001","document_id":"go@5d29d80b6...:doc/go_spec.html","section_path":["..."],"score":0.87}
```
- 每 query 至少 top-10 命中（取 top-20 更稳），按 score 降序。
- 检索目标：`document_id` 与 qrels 同构（源级标识）；`section_path` 若有则填命中 chunk 的标题路径（可空 `[]`）。
- **不得**写入任何索引/alias；只写 JSONL 文件。

### 3.2 评测报告（每 run 一份）

用官方 runner 计算（命令见 §4），输出 `results/retrieval/{run}-report.json`，含总体 + per source + per language + per query_type 的 Recall@5 / MRR@10 / nDCG@10、worst queries、空结果、错误命中。

## 4. 执行步骤

1. **确认字段**：`GET /knowledge_base_v2_bge_m3/_mapping` + 拉 1 条示例 chunk，确认文本/向量/标识字段名。
2. **BM25 run**：对每 query 在 v2 索引做 BM25（`multi_match` 或 `query_string` on 文本字段，`size=20`），记录命中 `document_id`/`section_path`/`score`。
3. **BGE-M3 run**：对每 query `POST /embeddings` 得 1024 维向量 → ES `knn`（`dense_vector` cosine，`size=20`），记录命中。
4. **hybrid_rrf run**：BM25 + vector 各取 top-100，用 RRF（k=60）融合，取 top-20。可参考仓库 `internal/service/search_service.go` 的 RRF 语义（`rrf_k=60`）或自行实现标准 RRF。
5. **hybrid_rerank run（可选）**：对 hybrid top-50 用 reranker（8008）重排，取 top-20；8008 不可达则该 run 标 disabled。
6. **生成 predictions JSONL**（§3.1 格式）。
7. **跑 runner**（对每个 predictions）：
```powershell
python -m orchestrator.eval.runner --qrels-path data/eval/techdocs/qrels.text.jsonl --predictions-path results/retrieval/{run}-predictions.jsonl --corpus-generation techdocs-2026-07-30-v1 --index-alias knowledge_base_current --output-path results/retrieval/{run}-report.json --visual-disabled
```
（runner 只读白名单字段；`--index-alias` 仅写入报告元数据，不影响计算。）
8. **汇总**：把 6 个 report 汇总成 `results/retrieval/summary.md`（表格：run × 指标），标注 reranker 是否实际应用、空结果 query 列表。

## 5. 自校验（交付前必须全过）

```bash
# 1) predictions 与 qrels 的 query_id 一致性（每 run）
python - <<'PY'
import json
qrels={json.loads(l)['query_id'] for l in open(r'D:\vscode\localcode\data\eval\techdocs\qrels.text.jsonl',encoding='utf-8') if l.strip()}
pred=json.loads(open(r'D:\vscode\localcode\results\retrieval\{run}-predictions.jsonl',encoding='utf-8').read().splitlines()[0])
# 用 set 统计
import collections
pids=set()
for l in open(r'D:\vscode\localcode\results\retrieval\{run}-predictions.jsonl',encoding='utf-8'):
    if l.strip(): pids.add(json.loads(l)['query_id'])
assert pids==qrels, f"missing={qrels-pids} extra={pids-qrels}"
print("OK query_id match", len(pids))
PY
```

```bash
# 2) 每 run report 生成且指标有限值
python - <<'PY'
import json, pathlib
p=pathlib.Path(r'D:\vscode\localcode\results\retrieval\{run}-report.json')
d=json.loads(p.read_text(encoding='utf-8'))
for k in ('recall@5','mrr@10','ndcg@10'):
    assert k in str(d).lower(), f"missing {k}"
print("OK report")
PY
```

```bash
# 3) 中文查询有命中（跨语言验证）
python - <<'PY'
import json
queries=[json.loads(l) for l in open(r'D:\vscode\localcode\data\eval\techdocs\queries.text.jsonl',encoding='utf-8') if l.strip()]
zh=[q['query_id'] for q in queries if any('\u4e00'<=c<='\u9fff' for c in q['query'])]
preds=set()
for l in open(r'D:\vscode\localcode\results\retrieval\{run}-predictions.jsonl',encoding='utf-8'):
    if l.strip(): preds.add(json.loads(l)['query_id'])
assert all(q in preds for q in zh), "missing zh queries"
print("OK zh coverage", len(zh))
PY
```

## 6. 返回的验收报告

1. mapping 实际字段名记录（与你假设的差异）。
2. 每 run：predictions 行数、query 覆盖率、report 总体指标（Recall@5/MRR@10/nDCG@10）。
3. 6 个 run 的汇总表格（summary.md 内容）。
4. 中文查询命中率（跨语言表现）与 worst 5 query（按 nDCG 排序）。
5. 空结果 query 列表（若有）与原因分析。
6. 遇到的基础设施问题（reranker 不可用、ES 字段缺失等）与处理。

## 7. 边界

- 只写 `results/retrieval/` 下的 predictions/report/summary；**不修改**任何代码、配置、索引、alias、MySQL、staging。
- 不把查询或预测写入任何索引/alias。
- 若全量导入未完成（v2 只有 pilot 数据），明确在报告中标注"pilot 级评测"而非全量，并说明 v2 当前 chunk 数。
