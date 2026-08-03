# 任务说明：建设 180 条文本检索人工 qrels（交付给能力极强的 Agent）

> 本文件是给执行 Agent 的**自包含任务书**。执行 Agent 需要读完全文后，独立完成语料阅读、题目设计、文档定位、JSONL 产出与自校验，最后返回一份验收报告。过程中不需要人类介入。
> 产出物将用于 RAG v2 检索评测（Recall@5 / MRR@10 / nDCG@10）与 alias 切换门槛。

## 1. 任务目标

为已导入的官方技术语料构建 **180 条人工 qrels**（query-document relevance judgments），覆盖 6 个技术域，每条查询标注语言与题型。这是 alias 切换前"检索门槛"的数据基础（设计目标：每域约 30 条；一半英文查询、一半中文查询英文文档；覆盖概念、命令、配置、故障排查、代码 API 五类题型）。

## 2. 背景与语料

- 语料来源：6 个官方开源仓库的文档，已按 manifest 锁定 commit 并 staging 到本机：
  - `go`：`C:\Users\ieeep\AppData\Local\Temp\corpus-pins\go`（commit `5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed`）
  - `python`：`...\corpus-pins\python`（commit `96ebb20fc2f0542d9387091e49626f9a1132de82`）
  - `git`：`...\corpus-pins\git`（commit `a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7`）
  - `docker`：`...\corpus-pins\docker`（commit `bb6ca1cb679394b4e9f7f44cc84a1288f8966247`）
  - `kubernetes`：`...\corpus-pins\kubernetes`（commit `b035ea80a2f666e0a60923560984458806788104`）
  - `postgresql`：`...\corpus-pins\postgresql`（commit `7a0299a1348b563c72a57a2a40462e90af9dfbac`）
- 每源只取 manifest 选中的文档（include/exclude/格式规则见 `D:\vscode\localcode\corpus\sources.yaml`）。**只允许选有实质正文内容的文档**；空文档（如纯 Hugo front matter 的 `_index.md`）已被导入流水线标为 SKIPPED，不得作为 qrels 目标。
- 文档在导入后以 chunk 形式进入 ES 索引 `knowledge_base_v2_bge_m3`，每条 chunk 的 `document_id` 是稳定的源级标识。

## 3. document_id 与 section_path 约定（必须严格遵循）

- `document_id` 格式：`{source_id}@{source_commit}:{source_path}`，其中 `source_path` 是相对 staging 根、用 `/` 分隔的仓库内路径（与 manifest include_paths 一致）。
  - 示例：`go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/asm.html`
  - 生成规则必须与代码一致：`{source_id}@{source_commit}:{source_path}`（无多余空格）。
- `section_path`：可选的标题路径数组（如 `["A Quick Guide to Go's Assembler", "Constants"]`）。标注精确到小节时填；只到文档级可填 `[]`。**不要填 chunk 序号**（评测按稳定 document_id+section_path 匹配，不依赖 chunk 序号）。
- 你可以用 `grep`/`rg` 在 staging 里确认文档存在与标题层级；`source_commit` 固定取上面的值，不要用 staging 的 HEAD（理论上 staging 已被 checkout 到锁定 commit，但以本文件值为准）。

## 4. 输出格式（唯一交付物）

一个 JSONL 文件，每行一条 qrel，UTF-8 编码，字段**只允许**以下白名单：

```json
{"query_id": "go-q001", "document_id": "go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/asm.html", "section_path": ["A Quick Guide to Go's Assembler"], "relevance": 1.0, "language": "en", "query_type": "concept", "source_id": "go"}
```

字段规范：

| 字段 | 类型 | 规则 |
|---|---|---|
| `query_id` | string | 唯一；格式 `{source_id}-q{3位序号}`（如 `go-q001`）。全文件唯一。 |
| `document_id` | string | 严格按第 3 节格式；必须能解析到 staging 真实文件。 |
| `section_path` | array of string | 标题路径（可空数组 `[]`）。 |
| `relevance` | number | 二值 `1.0`（文档确实回答该查询）或 `0.0`（文档存在但回答不了，仅用于负样本控制；**每域负样本不超过 3 条**）。 |
| `language` | string | 查询语言：`en`（英文查询）或 `zh`（中文查询）。 |
| `query_type` | string | 枚举：`concept` / `command` / `config` / `troubleshooting` / `code_api`。 |
| `source_id` | string | 六源之一：`go` / `python` / `git` / `docker` / `kubernetes` / `postgresql`。 |

## 5. 建设规则（硬性要求）

1. **配额**：每源恰好 30 条（含负样本 ≤3），总计 180 条。
2. **双语**：每源 15 条英文查询 + 15 条中文查询。中文查询是"用中文提问、答案在英文官方文档里"（如「Go 的汇编里 SB 伪寄存器是什么」），**不是翻译题**——提问要符合中文技术用户真实表达。
3. **题型覆盖**：每源五类题型（concept/command/config/troubleshooting/code_api）各至少 4 条（30 条里其余 10 条自由分配）。
4. **防污染（最高优先级）**：查询文本**不得包含**答案文档里的连续短语/代码片段/命令原文（这是 benchmark 泄漏，会让评测虚高）。查询是自然语言提问，不是文档摘录。**qrels 永远不得写入生产索引/alias**，只作为评测数据文件存在。
5. **文档真实性**：每条 `document_id` 必须满足：
   - staging 里该文件存在；
   - 文件有实质正文（非空、非纯 front matter）；
   - 文档内容确实能支撑该查询的答案（正样本）。
6. **去重**：同一查询只出现一次；同一文档可被多条不同查询引用（合理），但避免同一文档在同一域出现超过 5 次。
7. **query_id 有序**：按源内序号递增，便于审查。

## 6. 执行方法建议（不强制，但推荐）

1. 先读 `D:\vscode\localcode\corpus\sources.yaml` 了解每源 include/exclude/格式。
2. 对每源：列出 manifest 选中文档（可用 `python scripts/corpus/import_docs.py --manifest corpus/sources.yaml --staging <staging> --source <src> --limit 0` 的**选择逻辑**，或直接按 sources.yaml 规则用 `rg` 列出）。注意不要真的导入。
3. 浏览每源文档目录结构，识别常见主题（概念页、CLI 命令、配置文件、FAQ/故障排查、API 参考）。
4. 每源设计 30 条查询（先写查询文本，再定位文档+标题），对照第 5 节配额。
5. 生成 JSONL，按第 7 节自校验。

## 7. 自校验清单（交付前必须全过）

在 `D:\vscode\localcode` 仓库根目录执行（Python 3.11 + 依赖已装）：

```bash
# 1) 行数与配额
python - <<'PY'
import json, collections
rows=[json.loads(l) for l in open(r'<你的输出路径>', encoding='utf-8') if l.strip()]
assert len(rows)==180, f"total={len(rows)}"
by_src=collections.Counter(r['source_id'] for r in rows)
assert all(v==30 for v in by_src.values()), by_src
by_lang=collections.Counter((r['source_id'],r['language']) for r in rows)
assert all(v==15 for v in by_lang.values()), by_lang
qids=[r['query_id'] for r in rows]
assert len(set(qids))==len(qids), "duplicate query_id"
print("OK quotas")
PY
```

```bash
# 2) 文档真实性与路径
python - <<'PY'
import json, pathlib
staging=pathlib.Path(r'C:\Users\ieeep\AppData\Local\Temp\corpus-pins')
COMMITS={'go':'5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed','python':'96ebb20fc2f0542d9387091e49626f9a1132de82','git':'a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7','docker':'bb6ca1cb679394b4e9f7f44cc84a1288f8966247','kubernetes':'b035ea80a2f666e0a60923560984458806788104','postgresql':'7a0299a1348b563c72a57a2a40462e90af9dfbac'}
rows=[json.loads(l) for l in open(r'<你的输出路径>', encoding='utf-8') if l.strip()]
for r in rows:
    sid, rest = r['document_id'].split('@',1)
    commit, path = rest.split(':',1)
    assert commit==COMMITS[sid], (r['document_id'], commit)
    p=staging/sid/pathlib.Path(path)
    assert p.is_file(), f"missing {p}"
    text=p.read_text(encoding='utf-8', errors='ignore')
    assert len(text.strip())>200, f"too short {p}"
print("OK documents")
PY
```

```bash
# 3) 题型覆盖
python - <<'PY'
import json, collections
rows=[json.loads(l) for l in open(r'<你的输出路径>', encoding='utf-8') if l.strip()]
for sid in ('go','python','git','docker','kubernetes','postgresql'):
    types=collections.Counter(r['query_type'] for r in rows if r['source_id']==sid)
    assert all(types.get(t,0)>=4 for t in ('concept','command','config','troubleshooting','code_api')), (sid,types)
print("OK types")
PY
```

## 8. 交付

- 输出文件写到：`D:\vscode\localcode\data\eval\techdocs\qrels.text.jsonl`（**覆盖**该文件；当前是 6 行 seed，最终应为 180 行）。
- 同时在仓库 `results/qrels/` 下保存一份带校验日志的副本（可选）。
- 返回验收报告（见第 9 节）。

## 9. 执行 Agent 返回的验收报告（必须包含）

1. 每源统计：query_id 范围、语言分布、题型分布、正/负样本数。
2. 自校验三条命令的输出（OK quotas / OK documents / OK types）。
3. 说明你如何防污染（抽查 3 条查询，给出查询原文 + 对应文档，证明不是摘录）。
4. 列出 3 条你判断为最难的查询（对检索系统有区分度的）。
5. 遇到的异常（文档缺失、路径不一致、歧义）与你的处理。

## 10. 回滚与边界

- 只写 `data/eval/techdocs/qrels.text.jsonl`（和可选的 results 副本）；**不得**修改仓库代码、配置、ES 索引、MySQL、MinIO、staging。
- 不得把查询/答案写入生产索引或任何 alias。
- 如果发现某源 staging 文档数不足 30 条可支撑题目的实质内容，允许该域少于 30 条但**必须在报告中说明**，并保证总条数尽量接近 180。
