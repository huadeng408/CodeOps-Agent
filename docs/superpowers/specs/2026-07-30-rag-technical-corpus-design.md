# 高质量技术文档 RAG 语料库设计

## 1. 目标与现状

当前 RAG 数据只适合验证链路：MySQL 中有 4 个 E2E 私有测试文件、4 个 chunk 和
4 条向量记录；Elasticsearch `knowledge_base` 有 9 个 chunk，其中 5 个早期公开英文
烟测 chunk 没有对应 MySQL 文件记录。现有 embedding 是
`BAAI/bge-small-zh-v1.5`、向量维度 512，不适合稳定支持中文查询英文官方文档。

本项目要建立首批面向 Code Agent 的公开技术知识库：

- 覆盖 Go、Python、Git、Docker、Kubernetes 和 PostgreSQL 官方文档。
- 正式索引包含 10,000 至 20,000 个高质量 chunk。
- 所有生产文档具有可验证的来源、版本、许可证和内容哈希。
- 使用 BGE-M3 原生 1024 维 embedding，支持中文查询英文资料。
- 数据完整经过 MinIO、MySQL、pipeline 和 Elasticsearch 正式链路。
- 通过可重复的检索评测后原子切换索引，并能立即回滚。

本项目不导入 Stack Overflow、随机网页、问答答案、benchmark query/qrels 或来源不明的
第三方数据包。本项目也不删除现有 9 个 ES chunk 或旧索引。

## 2. 方案选择

采用版本化蓝绿索引方案：新语料写入 `knowledge_base_v2_bge_m3`，正式检索使用 alias
`knowledge_base_current`。旧 `knowledge_base` 在导入和验收期间继续服务；只有 v2
通过数据一致性、检索质量和真实 Agent E2E 后才原子切换 alias。

不采用以下方案：

- 不把 BGE-M3 向量简单截断为 512 维后追加到旧索引。当前 embedding 服务的 resize
  是开发兼容逻辑，不是可靠降维方法。
- 不离线 bulk 写 ES 并跳过上传流水线，否则会继续产生 MySQL 无来源记录的孤立数据。
- 不清空 Docker volume 或覆盖旧物理索引，避免破坏现有测试证据和回滚边界。

## 3. 官方语料来源

每个来源由仓库内 manifest 定义，至少包含 `source_id`、官方仓库 URL、锁定 commit、
文档 include/exclude 规则、预期许可证、许可证文件路径、上游 URL 模板和语言。

首批来源如下：

| source_id | 官方仓库 | 文档范围 | 预期许可证 |
|---|---|---|---|
| `go` | `https://github.com/golang/go` | `doc/`、关键包文档 | BSD-3-Clause |
| `python` | `https://github.com/python/cpython` | `Doc/` | PSF-2.0 |
| `git` | `https://github.com/git/git` | `Documentation/` | GPL-2.0-only |
| `docker` | `https://github.com/docker/docs` | 产品与命令文档 | Apache-2.0 |
| `kubernetes` | `https://github.com/kubernetes/website` | 英文内容树 | CC-BY-4.0 |
| `postgresql` | `https://github.com/postgres/postgres` | `doc/` | PostgreSQL |

表中的许可证是 manifest 的预期值，不替代下载后的验证。fetch 阶段必须在锁定 commit
中找到指定许可证文件，计算许可证哈希并匹配 allowlist；缺失或变化时整批拒绝导入，
等待人工审查。不得仅根据仓库首页标签推断许可证。

原始 checkout 和归档放在 gitignored staging 目录。仓库只提交 manifest、转换规则、
评测集和审计报告，不提交第三方文档全文或模型二进制。

## 4. 数据模型与可追溯性

新增来源和文档清单模型。

### 4.1 `knowledge_source`

- `source_version_id`：由 `source_id + source_commit + corpus_generation` 生成的主键。
- `source_id`：稳定逻辑标识，例如 `python`；同一来源可保留多个 commit 版本。
- `repository_url`：官方仓库 URL。
- `source_commit`：本批锁定 commit。
- `license_spdx`、`license_path`、`license_sha256`。
- `fetched_at`：抓取时间。
- `corpus_generation`：本批语料版本，例如 `techdocs-2026-07-30-v1`。
- `status`：`STAGED`、`PILOTED`、`ACTIVE`、`FAILED`。
- `last_error`：脱敏错误。

### 4.2 `knowledge_document`

- `document_id`：由 `source_id + source_commit + source_path` 生成的稳定 ID。
- `source_id`、`source_path`、`source_url`、`source_commit`。
- `title`、`document_language`、`content_sha256`。
- `file_md5`：连接现有 `file_upload`、`document_vectors` 和 ES 文档。
- `source_version_id`：连接对应的来源版本和许可证证据。
- `corpus_generation`、`target_index`。
- `status`：`STAGED`、`UPLOADED`、`PARSED`、`CHUNKED`、`EMBEDDED`、`INDEXED`、
  `ACTIVE`、`FAILED`。
- `retry_count`、`last_error`、`updated_at`。

文档只有在 MinIO、MySQL、parse/chunk/embed/index 四阶段和 ES 均成功后才能成为
`ACTIVE`。重复执行时，相同 `source_id/source_commit/source_path/content_sha256` 必须
幂等跳过；内容变化创建新文档版本，不覆盖历史审计记录。

### 4.3 ES 来源与多模态字段

每个 v2 chunk 除现有字段外增加：

- `document_id`
- `source_id`
- `source_path`
- `source_url`
- `source_commit`
- `section_path`
- `parent_chunk_id`
- `page_id`
- `page_span`
- `element_ids`
- `element_types`
- `bbox_refs`
- `asset_refs`
- `embedding_text`
- `token_count`
- `tokenizer_id`
- `parser_name`
- `parser_version`
- `corpus_generation`

这些字段用于稳定 qrels、父子检索、页面定位、引用和一致性审计。原始图片、页面截图和
表格资产保存在 MinIO，ES 只保存稳定引用，不内联大体积二进制。qrels 不依赖易变化的
chunk 序号。

所有首批语料使用专用 `corpus-loader` 系统账号，凭据仅来自环境变量，并统一写入
`is_public=true`。不复用 E2E 用户 3、4，不把凭据写入 manifest、配置、日志或 Git。

## 5. 获取、转换与分块

### 5.1 获取

fetcher 按 manifest 获取锁定 commit，支持本地已有 checkout、官方 Git URL和显式指定
的可信归档。下载失败时可重试和断点续传，但不得自动改写全局 Git/系统代理，不得自动
换到来源不明镜像。

每批获取后验证：

- checkout HEAD 等于 manifest commit。
- 许可证文件存在且哈希符合 allowlist。
- 文档路径只命中 include 规则且不命中 exclude 规则。
- 单文件大小、总文件数和总字节数在 manifest 上限内。
- 文档内容不是 symlink/junction 逃逸目标。

### 5.2 转换

使用结构化解析器处理 Markdown、reStructuredText 和 AsciiDoc，保留：

- 标题层级和章节路径。
- 围栏代码块、命令行、配置示例和表格文本。
- 原始相对路径、标题和上游 URL。
- 文档内部的必要交叉引用文本。

转换器移除导航、重复页脚、生成器标记和空章节，但不使用正则拼接代替格式解析器。
转换输出是规范化 Markdown，写入 staging 后再交给正式 ingestion。

### 5.3 分块

采用工业化的 `partition -> typed elements -> hierarchy-aware chunking -> contextual
serialization -> embedding` 流程，不再把整份文档压平后执行固定字符切分。

内部 Element Schema 是长期契约，至少包含：

- `document_id`、`element_id`、`parent_id`、`reading_order`。
- `type`、`sub_type`、`heading_path`、`page_index`、`bbox` 和坐标系。
- `text`、`html`、`latex`、`code_language`。
- `image_path`、`caption`、`footnote` 和 `caption_of/footnote_of/continuation_of` 关系。
- `parser_name`、`parser_version`、`backend`、`source_payload_ref`、`source_sha256`。

所有 PDF 统一使用 MinerU OCR。正式适配器读取稳定的 `content_list.json` 和需要的
`middle.json` provenance，将 `text/image/table/equation/code` 映射到内部 Element
Schema。不得把仍处于 development 状态的 `content_list_v2.json` 作为长期契约。
Docling、Unstructured 和 Marker 不得重新打开 PDF；Docling 只允许在 MinerU 之后接收
内部元素映射，用于 `HybridChunker` 的结构化、token-aware 合并。Unstructured 仅作为
分块对照实现，Marker 仅用于离线解析质量抽样。

正式 chunk 规则：

- 普通正文 child 目标为 256 至 512 tokens，只在同一 `heading_path` 内合并；父章节目标
  为 1,000 至 2,000 tokens。
- 标题、页面 provenance 和元素类型是硬边界，不跨章节合并，不把表格、代码、公式或
  图片与无关正文混成一个 chunk。
- 表格保持原子性；超长表按完整行组拆分，每个子块重复表头，并保留 caption、footnote
  和原表 ID。
- 代码、配置和日志按完整行及语言安全边界拆分；公式默认原子化，不在 LaTeX 中间切断。
- 图片和图表建立 parent element，caption、OCR 文本和 MinerU image analysis 描述形成
  可检索文本，原图或 crop 通过 `asset_refs` 关联。
- overlap 只用于拆分单个超长元素，不对正常相邻语义元素全局重复。
- embedding 使用 `contextual_text`，包含文档标题、祖先标题和必要 caption；展示与引用
  仍返回原始 `text` 和精确 element provenance。

现有 ingestion chunk HTTP 契约只返回字符串列表，必须升级为结构化 chunk 列表。Go
pipeline 需要持久化 parent、element、page、bbox、asset 和 tokenizer 元数据。旧字符串
响应仅在兼容测试中读取，不用于 corpus v2 导入。若 orchestrator 未启用、结构元数据
丢失或退回 1,000 rune splitter，则导入失败，不能静默继续。

MinerU 命令必须显式启用 OCR，不使用默认 `auto`。CPU 基线允许 `pipeline -m ocr`；包含
图片或图表理解的多模态 pilot 使用 `hybrid-engine -m ocr --effort high` 并显式开启
formula、table 和 image analysis。Tika 仅处理 DOCX、PPTX、XLSX 等非 PDF 文档。本批
官方技术语料以源码文档格式为主，不通过 Tika 解析 Markdown/RST/AsciiDoc。

### 5.4 多表示索引

多模态文档使用三个逻辑层级，并以 `document_id/page_id/element_id` 关联：

- `text_element`：段落、代码、表格 HTML/Markdown、公式 LaTeX、图注和图片描述，进入
  BM25 与 BGE-M3 1024 维文本索引。
- `page_visual`：整页截图的单向量视觉表示，写入独立物理索引；不得写入 BGE-M3
  `vector` 字段。
- `visual_asset`：图片、图表、表格和公式 crop，保存资产、OCR/描述、bbox 和父页引用；
  首轮以文本召回为主，crop 视觉检索是 pilot 项。

视觉模型采用可替换适配器。由于目前没有同时通过本地效果、显存、索引体积和商业许可
验证的单向量页面模型，corpus v2 的生产切换不依赖视觉索引；实现阶段先完成独立索引
契约和 500 至 2,000 页 bake-off。只有候选模型通过许可证审计和第 8 节多模态门槛后，
才锁定 revision 并启用 `knowledge_page_visual_current` alias。这是显式阶段门，不允许用
未经验证的视觉模型阻塞或污染文本索引。

ColQwen2 用作 top 20 至 50 页面的小候选 late-interaction 重排器，不把全库 patch
多向量写入 Elasticsearch。其代码、checkpoint 和基础模型许可证分别锁定；没有可用 GPU
时视觉链明确关闭，文本主链仍保持可用，评测报告必须标记实际执行的路径。

### 5.5 检索融合与证据展开

默认检索流程：

1. BM25 与 BGE-M3 并行召回 text child，各取 top 100。
2. 已通过门槛时并行召回 page visual top 50；未通过时不伪造视觉结果。
3. 以 `page_id` 和 parent 关系执行 RRF，避免同页大量 child 挤占候选。
4. 视觉链启用时对 top 20 至 50 页执行 ColQwen2 MaxSim 重排。
5. 将命中的 child 展开为必要 parent、前后邻接块、精确 element、页面图和 bbox，再交给
   生成模型；不得只返回整页截图而丢失可引用文本。

## 6. BGE-M3 与索引迁移

### 6.1 模型服务

embedding 服务使用 `BAAI/bge-m3` 的锁定 revision，输出原生 1024 维 dense vector。
生产路径禁止截断、补齐或循环填充向量；请求维度与模型维度不符时返回错误。

模型预检必须验证：

- 缓存文件和 revision 完整。
- `/health` 返回 ready 且模型名/revision 匹配。
- 固定中英文样本均返回有限数值的 1024 维向量。
- 相同输入的重复调用在容差内稳定。
- 容器重启后从独立 Docker volume 加载，不重新下载。

当前 FastEmbed 运行时是否原生支持锁定的 BGE-M3 revision，需要由实现阶段的可执行探针
确认。若不支持，服务改用 BGE-M3 官方兼容的 FlagEmbedding/SentenceTransformers
运行时；不得用 resize 伪造兼容。

reranker 优先使用 `BAAI/bge-reranker-v2-m3` 的锁定 revision。验收期间 reranker
不可用、超时或模型不匹配视为失败，不接受静默回退；普通运行是否允许降级保持现有
行为，但必须记录结构化指标。

### 6.2 物理索引与 alias

- 新物理索引：`knowledge_base_v2_bge_m3`，dense vector 为 1024 维 cosine。
- 检索 alias：`knowledge_base_current`。
- 当前旧物理索引：`knowledge_base`，保持只读回滚来源。
- 视觉 pilot 物理索引：`knowledge_page_visual_pilot_<model>_<revision>`。
- 视觉生产 alias：`knowledge_page_visual_current`，只在独立多模态门槛通过后创建或切换。

应用启动不能对 alias 使用“索引不存在则创建”的旧逻辑。初始化过程要区分物理索引和
alias，并在 mapping/model_version 不符合期望时拒绝写入。

导入完成前，线上检索继续指向旧索引。验收通过后使用一次 ES aliases API 原子移除旧
指向并添加 v2 指向。回滚执行相反的原子操作，不复制或重建数据。

`conversation_memory` 当前为空但 mapping 为 512 维。模型升级时创建 1024 维物理索引
和独立 alias，或使用独立 memory embedding 配置；不得让 1024 维 query 访问旧 512 维
memory index。

MySQL `document_vectors.model_version` 必须记录 `BAAI/bge-m3@<revision>`。来源清单记录
目标物理索引和 corpus generation，区分历史 E2E 文件与当前正式语料。

## 7. 导入流程与断点续跑

1. 创建专用 corpus loader 身份并验证公开访问语义。
2. 记录 MySQL 元数据备份位置、ES mapping/count/alias 快照和 MinIO 对象清单。
3. 下载并预检 embedding/reranker 模型。
4. 创建 provenance 表、v2 mapping 和 alias 迁移所需代码，但不切 alias。
5. 每个来源选择 20 至 50 个文件执行文本 pilot；另选 500 至 2,000 页许可清晰的复杂
   PDF 执行多模态 pilot。
6. Pilot 验证许可证、Element Schema、表格/代码/公式/图片边界、来源 URL、1024 维
   文本向量、页面证据和公开检索。
7. 六个来源 pilot 全部通过后按 source/commit/path 执行全量导入。
8. 每批保存 checkpoint、数量、失败列表和耗时；失败可从最后成功文档恢复。
9. 完成一致性审计、检索评测和真实 Agent E2E。
10. 所有门槛通过后原子切换 alias。

任何来源 pilot 失败都停止全量阶段。单文件失败记录状态和原因；超过 manifest 允许的
失败阈值时停止该来源，不将不完整 generation 标为 ACTIVE。

## 8. 检索评测

建立约 180 条人工复核题，每个技术域约 30 条：一半英文，一半中文查询英文官方文档。
题型覆盖概念、命令、配置、故障排查和代码 API。每条 qrel 指向稳定的
`document_id/section_path`，不包含可被生产检索的答案文本。

评测分别运行：

- BM25。
- BGE-M3 vector。
- BM25 + vector 的 hybrid RRF。
- hybrid RRF + BGE reranker。
- 多模态 pilot 的 text-only 基线。
- text + page visual RRF。
- text + page visual RRF + ColQwen2 小候选重排。

报告必须包含总体、按来源、按语言和按题型的 Recall@5、MRR@10、nDCG@10、延迟，
并列出最差查询、空结果、错误命中和 reranker 是否实际应用。

多模态 pilot 至少包含 120 条人工 qrels，分层覆盖正文、图片、表格、公式、版面、多页
关系和中文查询英文资料。除检索指标外，报告还必须包含证据 bbox 命中率、P95、GPU
峰值显存、视觉索引每页字节数和失败页面列表。视觉链启用门槛为：相对 text-only 的
nDCG@5 有统计可复现的提升、图片/表格子集 Recall@5 不下降、bbox 命中率不低于 0.85，
且当前机器资源预算内无 OOM 或超时降级。

Alias 切换门槛：

- v2 活跃 chunk 在 10,000 至 20,000 之间。
- 所有 v2 ES 文档均可追溯到 ACTIVE MySQL 来源记录，孤立数为 0。
- Recall@5 不低于 0.80。
- MRR@10 不低于 0.75。
- nDCG@10 不低于 0.75。
- 中文跨语言 Recall@5 不低于 0.75。
- 当前机器端到端检索 P95 不高于 5 秒。
- 评测期间 embedding/reranker 无维度错误、超时降级或不可用回退。

单独保存公开 benchmark，例如 MIRACL/BEIR 的 corpus/query/qrels；只有许可证证据完整的
数据才能用于离线评测。Benchmark query、qrels 和答案不得写入生产 alias。

## 9. 显式集成测试

自动化测试至少包括：

- Manifest schema、commit、路径 allowlist、许可证文件和哈希验证。
- RST/Markdown/AsciiDoc 转换，标题、代码块、表格和来源 URL 保留。
- MinerU `content_list.json/middle.json` 到内部 Element Schema 的契约、版本和 provenance。
- Hierarchy-aware/token-aware chunking 的父子关系、表格重复表头、代码/公式边界，以及
  overlap 只作用于单个超长元素。
- BGE-M3 健康检查和 1024 维强校验，拒绝 resize。
- Provenance 模型迁移、幂等重复导入和失败恢复。
- ES v2 mapping、物理索引/alias 区分和原子切换/回滚。
- MySQL、MinIO、pipeline、ES 四端一致性审计。
- 专用用户导入后，不同普通用户均能检索 `is_public=true` 文档。
- 评测器对 Recall、MRR、nDCG 和跨语言子集的确定性计算。
- 真实 Agent top-k 结果包含正确正文、来源 URL 和章节，不只命中 metadata。
- PDF 入口继续执行 MinerU OCR，并明确证明未调用 Tika。
- PDF 测试证明 Docling/Unstructured/Marker 未重新打开源 PDF。
- page visual 使用独立 mapping/alias；BGE-M3 文本字段拒绝视觉向量。
- ColQwen2 只重排受限候选集，GPU 不可用时返回显式 disabled 状态而非伪成功。
- 多模态评测器确定性计算 bbox 命中率、分层 Recall/nDCG、显存和索引体积门槛。

普通单元测试不下载大型模型或官方仓库。模型、六来源 pilot、全量导入和 180 题评测使用
显式 integration 命令，输出脱敏报告并返回可靠退出码。

## 10. 失败处理与安全

- 模型、许可证、来源 commit、向量维度、mapping 或访问控制不符，在写入前失败。
- 下载和模型缓存允许断点续传；校验失败的文件隔离并重新获取，不使用损坏缓存。
- 日志不得包含 corpus-loader 密码、API key、Authorization header 或数据库备份凭据。
- MinerU 使用基于 Apache 2.0 的自定义许可证，不能按普通 Apache-2.0 推断商业使用；
  上线前验证在线标识、规模门槛和所用模型权重条款。Marker 代码许可证不能替代其模型
  权重许可证，ColQwen2 代码、checkpoint 和基础模型条款分别审计。
- 来源解析错误包含 `source_id/source_path`，但不把整份第三方内容写入日志。
- 任一质量门槛失败时保留 v2 供分析，但不切 alias。
- 视觉模型许可证、显存或效果门槛失败时不创建视觉生产 alias，文本 v2 仍可独立验收。
- Alias 切换后的真实 Agent E2E 失败时立即切回旧索引。
- 稳定观察结束前不删除旧索引、旧模型缓存、备份或历史 9 条 ES 数据。
- 数据清理和旧索引删除是独立的破坏性任务，需要另行批准。

## 11. 最终验收

项目完成必须同时满足：

- 六个官方来源均有锁定 commit 和经验证的许可证证据。
- `knowledge_base_v2_bge_m3` 使用原生 1024 维 BGE-M3 向量。
- v2 中有 10,000 至 20,000 个公开、可追溯、非孤立 chunk。
- 180 题报告满足第 8 节全部门槛，且 reranker 确实执行。
- `knowledge_base_current` 已原子切到 v2，并保留已验证的回滚命令。
- 真实 Agent 可用中文查询英文官方文档，top-k 正文和来源引用正确。
- 多模态 pilot 的每个命中可追溯到 `document_id/page/element_id/bbox/asset_ref`；视觉链
  只有在第 8 节门槛通过后才启用生产 alias。
- MySQL、MinIO、pipeline、ES 审计一致，备份和旧索引仍存在。
- `go test ./...`、`go vet ./...`、`python -m pytest -q` 和
  `git diff --check` 全部通过。

## 12. 调研依据

本设计在 2026-07-30 通过官方文档、官方仓库和论文交叉核验：

- MinerU 输出与 CLI：<https://opendatalab.github.io/MinerU/reference/output_files/>、
  <https://opendatalab.github.io/MinerU/usage/cli_tools/>。
- Docling chunking：<https://docling-project.github.io/docling/concepts/chunking/>。
- Unstructured chunking：
  <https://docs.unstructured.io/open-source/core-functionality/chunking>。
- Marker：<https://github.com/datalab-to/marker>。
- NVIDIA NeMo Retriever：<https://github.com/NVIDIA/NeMo-Retriever>。
- AWS Bedrock chunking：
  <https://docs.aws.amazon.com/bedrock/latest/userguide/kb-chunking.html>。
- Azure 多模态索引：<https://learn.microsoft.com/en-us/azure/search/tutorial-multimodal>。
- Google layout-aware chunking：
  <https://docs.cloud.google.com/generative-ai-app-builder/docs/parse-chunk-documents>。
- ColPali/ColQwen2：<https://arxiv.org/abs/2407.01449>、
  <https://github.com/illuin-tech/colpali>。
- VisRAG 与 DSE：<https://arxiv.org/abs/2410.10594>、
  <https://arxiv.org/abs/2406.11251>。
