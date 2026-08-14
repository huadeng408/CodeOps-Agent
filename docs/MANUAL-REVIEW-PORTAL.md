# 人工审核工作台使用说明

打开 [`manual-review-portal.html`](manual-review-portal.html)。它是一个独立
的本地 HTML 文件：不需要启动服务，不会发送网络请求，不会改写 qrels，也
不会要求私钥。

## 1. 文本仲裁

当前唯一可用的权威输入是：

- `data/eval/techdocs/reviews/beeapi-openai-relay-recovery-20260813-01/qrels.sol-review-arbitrated.jsonl`
- `data/eval/techdocs/queries.text.jsonl`

不要使用 `data/eval/techdocs/review/disputed-worksheet.md` 或其 JSONL；它们
是旧的 157 行预览，不能代表当前恢复后的 143 条争议。

在仓库根目录、并确保 Elasticsearch 可用后，运行：

```powershell
python orchestrator/eval/dispute_worksheet.py `
  --qrels data/eval/techdocs/reviews/beeapi-openai-relay-recovery-20260813-01/qrels.sol-review-arbitrated.jsonl `
  --queries data/eval/techdocs/queries.text.jsonl `
  --out data/eval/techdocs/reviews/beeapi-openai-relay-recovery-20260813-01/human-review-worksheet.jsonl `
  --markdown data/eval/techdocs/reviews/beeapi-openai-relay-recovery-20260813-01/human-review-worksheet.md `
  --reviewer local-human-1
```

不要添加 `--no-evidence`。没有真实 `document_sections` 证据的工作包无法
用于盲审，页面会拒绝导入它。

将生成的 `human-review-worksheet.jsonl` 选入页面。每题：

1. 阅读查询、文档身份和实际检索到的证据段。
2. 在“相关 / 不相关”中选择一项。这是唯一必填项。
3. 按需要填写可回答性、语言、问题类型、证据充分性和备注。
4. 使用上一条/下一条继续。页面会在浏览器本地保存当前工作包的草稿。
5. 所有条目完成后，点击“下载未提交草稿”。

下载文件的类型是 `UNSUBMITTED_REVIEW_DRAFT`。它不是 qrels，不能直接写回
原始 JSONL，不能标记为 `HUMAN_REVIEWED`，也不能作为发布或指标证据。

## 2. 语料来源声明

填写来源路径或 URL、锁定版本、许可证，以及五项明确许可：OCR、页面渲染、
向量化、内部评测和公开展示。所有字段填完后可下载
`UNSUBMITTED_SOURCE_DECLARATION.json`。

该文件只是后续人工 intake 的声明；它不证明权利，也不会把来源加入允许列表。

## 3. 多模态证据复核

此项目前被阻塞：仓库没有候选、PDF 页面图、页面级证据或多模态 qrels。不要
制造测试候选来绕过该状态。

后续的真实候选必须由 MinerU 加显式 OCR 处理 PDF，不能使用 Tika。受控人工
流程需要每条候选的 `ACCEPT`、`CORRECT` 或 `REJECT` 决定；只有 `CORRECT`
填写 `page_1000_xyxy` 坐标框，且其四个有限坐标须位于 0 到 1000、面积为正。

## 4. Holdout 声明

当前 split 为 180 条开发集、0 条 holdout、状态 `BLOCKED`。不能将现有 180
条重新命名为 holdout。页面只收集未来独立来源的许可与规划信息，并导出
`UNSUBMITTED_HOLDOUT_INTAKE.json`；它不会创建 split、qrels 或评测指标。

## 5. 签名与最终导入

此页面从不采集、保存或上传私钥。最终决定导入和 Ed25519 回执由
`orchestrator/eval/multimodal_human_review.py` 所代表的受控本地流程处理。回执
必须绑定精确候选字节、决定字节和已配置的审核者密钥 ID；将草稿交给该流程前
必须经过项目既有的人工审核和签名规范。
