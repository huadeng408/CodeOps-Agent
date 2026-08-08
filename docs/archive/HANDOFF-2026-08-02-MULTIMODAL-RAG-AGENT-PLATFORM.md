# 多模态 RAG、可观测性与 Agent Harness 交接文档

日期：2026-08-02

仓库：`D:\vscode\localcode`

目标分支：`feature/complete-design-implementation`
可信远端基线：`5ccfee2 feat(rag): complete structured MinerU chunking provenance`

## 1. 本文用途

本文交给新的执行 Agent。它不是完成声明，而是当前事实、未提交工作、顶层边界、执行顺序、验收证据和回滚约束的统一入口。执行前还必须阅读：

- `AGENT.md`
- `docs/superpowers/specs/2026-08-02-multimodal-rag-agent-platform-design.md`
- `docs/superpowers/plans/2026-08-02-multimodal-rag-agent-platform.md`
- `docs/superpowers/specs/2026-07-30-rag-technical-corpus-design.md`
- `docs/superpowers/specs/2026-07-29-phoenix-trace-e2e-design.md`
- `docs/PROGRESS-2026-07-30.md`
- `docs/PROGRESS-2026-07-31.md`

## 2. 不可违反的决策

1. 所有 PDF 入口统一使用 MinerU，命令必须显式 OCR；不得使用 Tika、`pdftotext`、Docling、Marker 或 Unstructured 重新打开 PDF。
2. Tika 只处理 DOCX、PPTX、XLSX 等非 PDF Office 文档。
3. PDF 的长期内部契约是 Element Schema，不是扁平 Markdown。必须保留 document/page/element、reading order、bbox、资产、parser/version/source hash。
4. Go task 中的 `file_md5/user_id/org_tag/is_public` 是权限与租户权威来源，不能信任 Python worker 返回的同名字段。
5. 生产语料、benchmark corpus/query/qrels/answers 必须物理隔离。benchmark 答案不得进入生产 RAG。
6. BGE-M3 文本向量必须是锁定 revision 的原生 1024 维有限数值；禁止 resize、padding、truncation 或循环填充。
7. 文本索引与视觉索引物理分离。视觉模型未通过质量、许可、显存和延迟门槛前，不创建生产视觉 alias。
8. `knowledge_base` 是只读回滚来源。新索引通过物理 index + alias 原子切换；稳定观察结束前不删除旧 index。
9. 不删除 Docker volume、模型缓存、旧 ES index、历史 E2E 数据、数据库业务行或备份，除非用户单独批准破坏性清理。
10. API key 只从环境变量或用户指定的仓库外私密文件读取；不得打印、写入配置、测试 fixture、trace、日志或提交。
11. 有意义进展同步到 `D:\Obsidian\code-autogrowth\私人\localcode`，使用中文，明确区分 `DESIGNED/IMPLEMENTED/VERIFIED/BLOCKED`。
12. 用户已授权所有项目在需要时直接隐藏启动 Docker Desktop；授权不包含删除容器、volume、索引或数据。

## 3. 已验证事实

### 3.1 Plan 2：结构化 MinerU ingestion

状态：`VERIFIED`，已提交并推送到 `5ccfee2`。

- MinerU 3.4.4 `content_list.json`/`middle.json` 已映射到 typed Element Schema。
- PDF 使用显式 OCR；重命名为非 PDF 后仍通过 magic bytes 识别并走 MinerU。
- Python 可生成包含 page/element/bbox/asset/parser/corpus provenance 的结构化 child chunks。
- 分块支持同章节小元素合并、超长正文 overlap、代码完整行、表格/公式/图片硬边界、caption contextual embedding、parent token 分组。
- Go 向 Python 传递 `elements`，接收 `structuredChunks`，持久化 provenance；ACL 字段由 Go task 覆盖 worker 字段。
- 真实 MinerU OCR E2E：`CODE_AGENT_RUN_MINERU_E2E=1 go test ./pkg/mineru -run TestRealMinerUOCR -count=1 -v` 通过。
- Python focused suite 14 项通过；相关 Go tests/vet 通过；MySQL 显式 integration 通过。

### 3.2 可观测性

状态：核心跨语言 trace 设计和显式 E2E runner 已实现过并记录为已验证，接手 Agent仍需在当前 HEAD 重新运行确认。

- Go 与 Python 使用 W3C `traceparent` 连接。
- Go agent、工具执行、Python orchestrator/model span 使用 OpenTelemetry/OpenInference 语义。
- Phoenix Docker 服务和显式真实 DeepSeek E2E runner 已存在。
- 现有测试入口见 `README.md` 的“Phoenix 跨语言 Trace 显式集成测试”。

### 3.3 Eval/Harness 资产

状态：`PARTIAL`。

- `eval/adapter.py`、`eval/driver_headless.py`、`eval/run.py` 已存在。
- EvalPlus 数据、生成样本、判分脚本和多个历史结果存在。
- SWE-bench flask-5014 单实例工作目录、patch 和官方 harness 产物存在。
- 这些历史结果不能代表当前 HEAD 的完整 harness 质量；统一 CLI 契约、可重复 manifest、污染检查、成本/trace join、Terminal-Bench/τ²-bench 等仍需完成。

## 4. 当前未提交工作树：必须先审查

本交接生成时，除本文档外，存在 Plan 3 半成品。不得一次性提交。执行 Agent应从 `git status --short` 获取最新事实，并按下面拆分审查。

### 4.1 用户侧文件，不得覆盖

- `AGENT.md`：有用户未提交的中文记录要求变更；保留。
- `CLAUDE.md`：用户未跟踪文件；保留，不纳入本功能提交。
- `CLAUDE-CODE-RECOVER-PARENT-THREADS-20260801.md`：独立恢复文档；除非用户要求，不纳入本功能提交。

### 4.2 Plan 3 WIP 文件

- `configs/server.yaml`
- `internal/serverconfig/config.go`
- `pkg/embedding/client.go`
- `pkg/embedding/preflight_test.go`
- `pkg/es/client.go`
- `pkg/es/knowledge_index.go`
- `internal/pipeline/processor.go`
- `internal/model/es_document.go`
- `internal/service/search_service.go`
- `internal/service/search_service_v2_test.go`

已知风险：

1. `pkg/embedding/client.go` 的 preflight 写在 client 文件中，固定中英样本曾出现终端编码乱码；应移到 `pkg/embedding/preflight.go` 并用 UTF-8 测试保证文本正确。
2. `Client` 接口没有统一暴露 preflight contract；当前 top-level wrapper 需要重新设计和测试。
3. health 响应模型/revision 字段不是任何现有服务已验证的正式契约；必须先定义服务契约和 httptest，再下载/启动模型。
4. `pkg/es/knowledge_index.go` 尚无 httptest，alias 操作未完成 mapping/model compatibility 校验，不能视为安全切换实现。
5. `ESBaseURL` 全局变量会破坏多 client/测试隔离；应改成显式 `KnowledgeIndexManager`，持有 client/base URL。
6. `internal/pipeline/processor.go` 将 v2 index 名硬编码为 `knowledge_base_v2_bge_m3`；应注入 `CorpusConfig.TextIndex`，并在写入前校验 corpus generation、target index、model version 和 dimensions。
7. structured dimension guard 只覆盖部分路径；external index 路径也必须 fail closed。
8. `SearchResponseDTO` 扩字段后需要 API contract 测试，不能仅靠编译。
9. `fuseAndExpand` 当前只做每页去重，不是真正的 parent/neighbor evidence expansion；必须读取 parent/邻接 chunks，并保留 ACL filter。
10. 尚未创建或切换 v2 index，尚未下载/验证 BGE-M3，尚未导入正式 corpus；不得标记 Plan 3 完成。

### 4.3 临时目录

`.tmp-go-cache-plan3-*` 是工作区专用 Go cache。确认路径严格位于 `D:\vscode\localcode` 后可删除；不得递归删除工作区根目录。

## 5. 建议的恢复步骤

```powershell
cd D:\vscode\localcode
git status --short --branch
git log -1 --oneline
git diff --check
git diff --stat
```
预期：HEAD 为 `5ccfee2` 或其后仅包含交接文档提交；Plan 3 WIP 仍未提交。

然后：

1. 先提交本交接、设计、计划文档；不要混入 WIP 代码。
2. 用 `git diff -- <file>` 逐文件审查 WIP。
3. 按实施计划 Task 0 建立 worktree/备份边界；不能用 `git reset --hard` 或 `git checkout --` 覆盖用户修改。
4. 将每一功能重新走 TDD：测试先失败、最小实现、测试通过、规格审查、质量审查、独立提交。
5. 只有 Task 1–3 全部通过后才启动/下载 BGE-M3 服务。
6. 只有离线评测和真实 Agent E2E 达门槛后才允许 alias 切换。

## 6. 总体执行顺序

1. 基线与契约冻结。
2. 结构化 ingestion 完整性审计。
3. BGE-M3 原生 1024 维服务、preflight、v2 index。
4. 官方高质量技术语料 manifest/pilot/full import。
5. 文本检索、RRF、parent/neighbor evidence expansion、引用。
6. 视觉 page/crop pilot，与文本索引完全隔离。
7. OpenTelemetry/Phoenix 全链路、eval-run join、成本和失败分类。
8. 统一评测平台：解析、检索、回答、Agent、系统性能。
9. 自研 Agent Harness：可复现实验、容器隔离、budget、resume、artifact、official scorer adapter。
10. 切换演练、原子 alias cutover、稳定观察与回滚。

## 7. 完成定义

只有同时满足以下条件，平台才可标记 `VERIFIED`：

- 所有 PDF 入口真实证明 MinerU OCR，Tika request count 为零。
- 生产文本索引使用锁定 BGE-M3 revision 的原生 1024 维向量。
- 每个检索结果可追溯到 source URL、document/page/element/bbox/asset。
- benchmark 数据与生产语料隔离，有 snapshot hash、license evidence、污染报告。
- BEIR/MIRACL/BRIGHT/ViDoRe 或选定子集使用官方 scorer；自建集有双人复核 qrels。
- 检索、答案忠实度、citation、Agent resolution、成本、P95、错误分类均有版本化报告。
- SWE-bench Verified/Terminal-Bench/τ²-bench 通过薄 adapter 调官方 harness；不自行篡改 scorer。
- 每个 eval run 可由 manifest 复现，关联 git SHA、model/revision、prompt hash、corpus generation、index mapping、trace ID、预算和环境摘要。
- Phoenix 中同一 run 可看到 Go root、tool、Python orchestration、retrieval、embedding/rerank、LLM 和 scorer spans。
- alias 切换使用一次原子 API；回滚命令已演练；旧 index、备份和历史报告仍保留。

## 8. 阻塞时的报告格式

不要写“基本完成”。使用以下格式：

```text
Status: BLOCKED | IMPLEMENTED_NOT_VERIFIED | VERIFIED
Task: <计划任务编号>
Evidence: <命令、exit code、关键数量、artifact 路径>
Blocker: <具体错误与重复次数>
Data changed: <表/索引/对象/容器；无则写 none>
Rollback boundary: <如何恢复且不删旧数据>
Next action: <唯一下一步>
```
