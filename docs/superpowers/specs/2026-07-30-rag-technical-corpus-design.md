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

### 4.3 ES 来源字段

每个 v2 chunk 除现有字段外增加：

- `document_id`
- `source_id`
- `source_path`
- `source_url`
- `source_commit`
- `section_path`
- `corpus_generation`

这些字段用于稳定 qrels、引用和一致性审计。qrels 不依赖易变化的 chunk 序号。

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

使用现有 Python `orchestrator/rag/ingestion.py` 的 Markdown header-aware splitter，而不是
Go pipeline 当前固定 1,000 rune 的简单切分。目标参数：

- chunk 大小 700 至 1,000 tokens。
- overlap 约 100 tokens。
- 标题路径随 chunk 保留。
- 代码块尽量保持完整；超长代码块按语法安全边界拆分。
- 每个 chunk 自包含来源标题和章节上下文，但不重复整份许可证或导航文本。

现有 ingestion chunk HTTP 契约只返回字符串列表，无法携带章节元数据。它需要升级为
结构化 chunk 列表，至少返回 `text`、`section_path` 和 `token_count`。Go pipeline 将
`section_path` 持久化到 `document_vectors`，并在索引阶段与 `knowledge_document` 的来源
字段合并后写入 ES。旧字符串响应仅在兼容测试中读取，不用于 corpus v2 导入。

Corpus pilot 必须证明实际执行的是外部 header-aware ingestion；若 orchestrator 未启用或
退回简单 splitter，则导入失败，不能静默继续。

所有 PDF 仍统一使用 MinerU OCR；Tika 仅处理 DOCX、PPTX、XLSX 等非 PDF 文档。本批
官方技术语料以源码文档格式为主，不通过 Tika 解析 Markdown/RST/AsciiDoc。

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
5. 每个来源选择 20 至 50 个文件执行 pilot。
6. Pilot 验证许可证、转换结构、代码块、来源 URL、1024 维向量和公开检索。
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

报告必须包含总体、按来源、按语言和按题型的 Recall@5、MRR@10、nDCG@10、延迟，
并列出最差查询、空结果、错误命中和 reranker 是否实际应用。

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
- Header-aware token chunking 的大小、overlap、章节路径和代码块边界。
- BGE-M3 健康检查和 1024 维强校验，拒绝 resize。
- Provenance 模型迁移、幂等重复导入和失败恢复。
- ES v2 mapping、物理索引/alias 区分和原子切换/回滚。
- MySQL、MinIO、pipeline、ES 四端一致性审计。
- 专用用户导入后，不同普通用户均能检索 `is_public=true` 文档。
- 评测器对 Recall、MRR、nDCG 和跨语言子集的确定性计算。
- 真实 Agent top-k 结果包含正确正文、来源 URL 和章节，不只命中 metadata。
- PDF 入口继续执行 MinerU OCR，并明确证明未调用 Tika。

普通单元测试不下载大型模型或官方仓库。模型、六来源 pilot、全量导入和 180 题评测使用
显式 integration 命令，输出脱敏报告并返回可靠退出码。

## 10. 失败处理与安全

- 模型、许可证、来源 commit、向量维度、mapping 或访问控制不符，在写入前失败。
- 下载和模型缓存允许断点续传；校验失败的文件隔离并重新获取，不使用损坏缓存。
- 日志不得包含 corpus-loader 密码、API key、Authorization header 或数据库备份凭据。
- 来源解析错误包含 `source_id/source_path`，但不把整份第三方内容写入日志。
- 任一质量门槛失败时保留 v2 供分析，但不切 alias。
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
- MySQL、MinIO、pipeline、ES 审计一致，备份和旧索引仍存在。
- `go test ./...`、`go vet ./...`、`python -m pytest -q` 和
  `git diff --check` 全部通过。
