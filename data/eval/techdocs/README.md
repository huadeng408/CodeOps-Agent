# 文本检索 qrels（人工复核工作流）

本目录承载文本检索的人工复核 qrels。当前验收边界以 `docs/GOAL.md`、
`qrels.schema.json`、版本化 release policy 和评测代码为准。

## 文件清单

| 文件 | 内容 |
| --- | --- |
| `qrels.text.jsonl` | 180 条 query-document 相关性判定（每源 30 条，90 条中文查询） |
| `queries.text.jsonl` | 180 条查询原文（`query_id` 与 qrels 一一对应，中英双语） |
| `qrels.schema.json` | qrels 行 JSON Schema（稳定 ID / section_path 前缀匹配 / reviewer_hash 16-hex 或空=未审） |
| `reviews/beeapi-openai-relay-recovery-20260813-01/qrels.human-reviewed.jsonl` | 180 条复核后投影；143 条 `HUMAN_REVIEWED`，37 条仍为 `AI_REVIEWED` |
| `reviews/beeapi-openai-relay-recovery-20260813-01/human-review-worksheet.input.jsonl` | promotion receipt 绑定的原始盲审输入，保留 143 条完整分母 |
| `reviews/beeapi-openai-relay-recovery-20260813-01/human-review-worksheet.jsonl` | 真人逐条填写的 worksheet；其 SHA-256 由 promotion receipt 绑定 |
| `reviews/beeapi-openai-relay-recovery-20260813-01/human-qrels-promotion-receipt.json` | 绑定真人 worksheet、输入/输出 hash 与 143/37 分母的 promotion receipt |

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
永远不会携带该字段。查询原文单独存于 `queries.text.jsonl`，同样不写入 qrels。

## 来源覆盖（corpus/sources.yaml）

首批 6 个来源全部为许可证清晰的官方技术仓库：

| source_id | 官方仓库 | include 范围 | 许可证 | 预期文档数 |
| --- | --- | --- | --- | --- |
| `go` | https://github.com/golang/go | `doc/` | BSD-3-Clause | 50 |
| `python` | https://github.com/python/cpython | `Doc/` | PSF-2.0 | 100 |
| `git` | https://github.com/git/git | `Documentation/` | GPL-2.0-only | 60 |
| `docker` | https://github.com/docker/docs | `content/` | Apache-2.0 | 80 |
| `kubernetes` | https://github.com/kubernetes/website | `content/en/docs/` | CC-BY-4.0 | 150 |
| `postgresql` | https://github.com/postgres/postgres | `doc/src/sgml/` | PostgreSQL | 40 |

6 个来源的锁定 commit 与 license hash 已写入 `corpus/sources.yaml` 与
`corpus/pins/locked.json`（均为验证过的真实 40 位 hex，非占位值）；本目录
`document_id` 中的 commit 与之一致。staging 克隆是可重建的本机临时输入，位置由
运行者选择，不属于版本化评测证据。

## 复核数量目标

- **恰好 180 条已复核查询**：每个来源恰好 30 条。
- **一半为中文查询英文官方文档**（`language=zh`，共 90 条）。
- 题型覆盖概念、命令、配置、故障排查、代码 API 五类。
- 每条 qrel 使用稳定的 `document_id/section_path` 定位，不依赖 chunk 序号。

## 当前状态：143 条真人逐项复核，发布仍 BLOCKED

`qrels.text.jsonl` 现有 **180 行真实条目**（每来源 30 行）。仓库中的复核投影与
promotion receipt 给出以下可审计状态：

- `document_id` 全部指向 staging 中真实存在的文件，commit 为锁定的真实 40 位 hex；
- `relevance` 已赋初值：163 条 `1.0` 正样本 + 17 条 `0.0` 负样本（每域负样本 ≤ 3）；
- `language`/`query_type` 已标注：中文查询 90 条，五类题型全覆盖
  （concept 46 / troubleshooting 36 / code_api 34 / config 34 / command 30）；
- 查询原文 180 条存放于 `queries.text.jsonl`（`query_id` 与 qrels 一一对应）。
- 143 条争议项由真人逐条完成 verdict 与 notes，promotion 后标为
  `HUMAN_REVIEWED`；对应 worksheet、reviewer hash 和输入/输出 SHA-256 均已保留；
- 其余 37 条没有逐行真人 evidence，继续标为 `AI_REVIEWED`，不得因真人完成了
  争议批次就自动提升为 `HUMAN_REVIEWED`。

因此当前证据不是 180/180 真人复核，且全部 180 条均已进入开发集，holdout 为 0。
它们不能支撑正式泛化指标或 release。若补齐剩余 37 条，必须继续使用相同盲审与
promotion 流程：

1. 运行 `python -c "from orchestrator.eval.annotation_export import export_worksheet; export_worksheet('data/eval/techdocs/qrels.text.jsonl', 'data/eval/techdocs/worksheet.jsonl')"`
   生成盲审工作表（seed=0，可复现；不含答案）。
2. 审查者对每条记录确认 `relevance` 并回填 `reviewer_hash`
   （`sha256(instance_id + reviewer)[:16]`）。
3. 用 `validate_counts()` 核对总数/每来源/中文化，用 `count_reviewed()` 核对已复核数，
   并生成新的 hash-bound promotion receipt；在新 holdout 冻结前保持 release
   `BLOCKED`。

## 常用命令

```bash
# 结构化校验与计数（total / per_source / zh_count）
python -c "from orchestrator.eval.annotation_export import validate_counts; print(validate_counts('data/eval/techdocs/qrels.text.jsonl'))"

# 已复核行数（reviewer_hash 非空）
python -c "from orchestrator.eval.annotation_export import count_reviewed; print(count_reviewed('data/eval/techdocs/qrels.text.jsonl'))"
```
