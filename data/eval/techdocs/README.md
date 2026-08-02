# 文本检索 qrels（人工复核工作流）

本目录承载文本检索的人工复核 qrels（design spec
`docs/superpowers/specs/2026-07-30-rag-technical-corpus-design.md` §8，plan
`docs/superpowers/plans/2026-07-30-retrieval-evaluation-cutover.md` Task 2）。

## qrels 格式

`qrels.text.jsonl` 每行一个 JSON 对象，字段如下：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `query_id` | string | 查询唯一标识，形如 `go-0001` |
| `source_id` | string | 语料来源标识（见下方来源表） |
| `source_commit` | string | 来源仓库锁定的不可变 commit（40 位 hex） |
| `document_id` | string | 稳定文档 ID，由 `source_id + source_commit + source_path` 生成 |
| `section_path` | array[string] | 稳定章节路径（标题层级），不使用易变化的 chunk 序号 |
| `relevance` | int | 相关性等级（1 相关 / 0 不相关；计划允许 2 级部分相关） |
| `language` | string | 查询语言：`zh`（中文查询英文文档）或 `en` |
| `query_type` | string | 题型：`concept` / `command` / `config` / `troubleshooting` / `api` |
| `evidence_type` | string | 证据类型：文本集固定为 `text` |
| `reviewer_hash` | string | 人工复核后的脱敏审查者哈希；未复核为空串 |

每条 qrel 只指向稳定的 `document_id/section_path`，**不包含可被生产检索的答案文本**。
`relevance` 属于金标准答案，`annotation_export.export_worksheet()` 导出的盲审工作表
永远不会携带该字段。

## 来源覆盖（corpus/sources.yaml，spec §3）

首批 6 个来源全部为许可证清晰的官方技术仓库：

| source_id | 官方仓库 | include 范围 | 许可证 | 预期文档数 |
| --- | --- | --- | --- | --- |
| `go` | https://github.com/golang/go | `doc/` | BSD-3-Clause | 50 |
| `python` | https://github.com/python/cpython | `Doc/` | PSF-2.0 | 100 |
| `git` | https://github.com/git/git | `Documentation/` | GPL-2.0-only | 60 |
| `docker` | https://github.com/docker/docs | `content/` | Apache-2.0 | 80 |
| `kubernetes` | https://github.com/kubernetes/website | `content/en/docs/` | CC-BY-4.0 | 150 |
| `postgresql` | https://github.com/postgres/postgres | `doc/src/sgml/` | PostgreSQL | 40 |

来源锁定 commit 由 corpus loader 在导入前替换为验证过的真实 commit；本文件中的
`0000...` 全部为占位值，任何真实评测运行前必须替换。

## 复核数量目标（spec §8）

- **恰好 180 条已复核查询**：每个来源恰好 30 条。
- **一半为中文查询英文官方文档**（`language=zh`，共 90 条）。
- 题型覆盖概念、命令、配置、故障排查、代码 API 五类。
- 每条 qrel 使用稳定的 `document_id/section_path` 定位，不依赖 chunk 序号。

## 当前状态：仅含种子行（SEED-ONLY）

`qrels.text.jsonl` 目前**只有 6 行占位种子**（每个来源 1 行），用于打通导出、复核、
计数工作流。它们满足：

- `relevance` 均为 1，`reviewer_hash` 均为空（等待人工复核）。
- `document_id`/`section_path` 为占位稳定 ID，非真实锁定语料。

**180 条真实已复核集合必须由人工复核生成**，任何自动化不得直接写入本文件。人工
复核流程：

1. 运行 `python -c "from orchestrator.eval.annotation_export import export_worksheet; export_worksheet('data/eval/techdocs/qrels.text.jsonl', 'data/eval/techdocs/worksheet.jsonl')"`
   生成盲审工作表（seed=0，可复现；不含答案）。
2. 审查者对每条记录给出 `relevance` 并回填 `reviewer_hash`
   （`sha256(instance_id + reviewer)[:16]`）。
3. 用 `validate_counts()` 核对总数/每来源/中文化，用 `count_reviewed()` 核对已复核数，
   达标（180 条、每来源 30、90 条中文）后才允许进入评测 runner（plan Task 3）。

## 常用命令

```bash
# 结构化校验与计数（total / per_source / zh_count）
python -c "from orchestrator.eval.annotation_export import validate_counts; print(validate_counts('data/eval/techdocs/qrels.text.jsonl'))"

# 已复核行数（reviewer_hash 非空）
python -c "from orchestrator.eval.annotation_export import count_reviewed; print(count_reviewed('data/eval/techdocs/qrels.text.jsonl'))"
```
