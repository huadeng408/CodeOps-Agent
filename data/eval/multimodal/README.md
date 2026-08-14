# 多模态检索 qrels（人工复核工作流）

本目录预留多模态检索的人工复核 qrels 位置（design spec
`docs/superpowers/specs/2026-07-30-rag-technical-corpus-design.md` §8，plan
`docs/superpowers/plans/2026-07-30-retrieval-evaluation-cutover.md` Task 2）。

## 目标（spec §8）

- **至少 120 条已复核 qrels**，分层覆盖：
  - 正文（text）
  - 图片（image）
  - 表格（table）
  - 公式（equation）
  - 版面（layout）
  - 跨页关系（multi-page relation）
  - 中文查询英文资料（Chinese-to-English）
- 每条 qrel 使用稳定 ID 定位：`document_id/page_id/element_id/bbox_ref`
  （spec §4.3、§5.3），不依赖易变化的 chunk 序号，不包含可被生产检索的答案文本。
- 报告除检索指标外，还必须包含证据 bbox 命中率、P95、GPU 峰值显存、视觉索引
  每页字节数和失败页面列表；视觉链启用门槛见 spec §8。

## 当前状态：尚未创建

`qrels.multimodal.jsonl` **尚未创建**。多模态 qrels 依赖 MinerU 渲染证据先行落地：

1. MinerU 渲染页面的 `content_list.json` / `middle.json` provenance（稳定
   `page_id`、`element_id`、bbox、坐标系）；
2. 内部 Element Schema 长期契约（spec §5.3）与页面/元素/bbox 证据链打通；
3. 多模态 pilot 语料（每来源 20-50 个文件 + 500-2,000 页复杂 PDF，spec §7）就绪。

在上述证据存在之前创建多模态 qrels 会导致 ID 不稳定、无法回指页面/元素，因此本
目录只保留此 README 与目标定义。多模态 qrels 文件创建后需满足：

- 至少 120 条已复核记录，覆盖上方全部证据类型；
- 每条记录携带稳定 `document_id/page_id/element_id/bbox_ref` 与
  `reviewer_hash`（`sha256(instance_id + reviewer)[:16]`，未复核为空串）；
- 仅由人工复核生成，自动化不得直接写入。

## 工作流复用

多模态 qrels 生成后复用 `orchestrator/eval/annotation_export.py` 的同一套导出与
计数接口（`export_worksheet` / `validate_counts` / `count_reviewed`），确保文本与
多模态两套人工复核流程一致。
## 2026-08-14 Candidate Materialization

`orchestrator.eval.mineru_page_candidates.materialize_page_candidates()` is
the supported pre-review importer. It accepts existing MinerU
`content_list.json` / `middle.json`, rendered page assets and dimensions,
explicit OCR evidence, and pinned source/license metadata. MinerU 3.4.4 does
not write `ocr_mode` to `middle.json`; for that raw output, a hash-bound
`mineru-explicit-ocr-receipt/v1` is mandatory. The receipt must attest to an
explicit OCR run with exit code zero and bind the input PDF plus unmodified
content and middle JSON hashes. MinerU `content_list.json` geometry is already
`page_1000_xyxy`, so the importer preserves it rather than normalizing it a
second time. It hashes the rendered page image and validates PNG/JPEG page
dimensions before emitting only `AI_CANDIDATE` records.

Candidates are not Qrels and are not scoreable. They must flow through
`export_review_worksheet()` and `freeze_human_reviewed_evidence()` with a real
signed human decision before a separate Qrels-release process can be designed.
The current repository has no license-clear document-native
question/page/element/bbox source material, so no candidate file or Qrels has
been generated from the test fixtures.

## 2026-08-14 Real DUDE local candidate evidence

A local-only DUDE sample has now exercised this path with a genuine PDF:
`jordyvl/DUDE_loader@b3662175d3b2482d711f18559b7acc2a5bccc600` (dataset
declaration `CC-BY-4.0`). MinerU `3.4.4` ran `-m ocr -b pipeline` with exit
code `0` on all 12 pages of PDF
`3e823ecb634b9f1a76fb8fdad270f979.pdf`. Its immutable local receipt binds
the PDF, content JSON and middle JSON hashes; candidate materialization wrote
180 `AI_CANDIDATE` elements and a blank 180-row review worksheet.

These local artifacts are deliberately not tracked. Two native DUDE
question-to-page/bbox records are retained only as `UPSTREAM_PAGE_BBOX_UNVERIFIED`:
their source coordinate transform and element association have not received
human verification. In addition, the dataset-level declaration does not prove
redistribution rights for every underlying PDF. Therefore this work has not
created a Qrel, human-reviewed evidence, a scoreable metric, or a repository
PDF asset.
