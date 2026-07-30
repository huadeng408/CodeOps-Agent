# Multimodal RAG Corpus Implementation Roadmap

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement these plans task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将当前仅适合链路烟测的 RAG 数据升级为可追溯、可评测、支持 MinerU OCR 多模态证据展开的官方技术语料库。

**Architecture:** 先建立稳定的来源/文档/元素/分块契约，再把 MinerU OCR 产物通过 Python orchestrator 转换为结构化 chunk；Go pipeline 负责持久化和权限边界。文本主链使用独立的 BGE-M3 1024 维蓝绿索引，视觉页面索引和 ColQwen2 重排器只在独立 pilot 门槛通过后启用。

**Tech Stack:** Go 1.25, GORM/MySQL, Elasticsearch 8, Python 3.11+, FastAPI, Pydantic, MinerU OCR, BGE-M3, Docker Compose, pytest/Go test。

---

## Plan Set

| 顺序 | 计划 | 产出 | 通过条件 |
|---|---|---|---|
| 1 | docs/superpowers/plans/2026-07-30-rag-foundation-contract.md | 来源、文档、结构化 chunk provenance 和配置契约 | 单元测试通过；旧表/旧索引仍可读 |
| 2 | docs/superpowers/plans/2026-07-30-structured-mineru-ingestion.md | content_list.json/middle.json 到 Element Schema、父子 chunk | PDF 只走 MinerU OCR；结构元数据丢失时失败 |
| 3 | docs/superpowers/plans/2026-07-30-bge-m3-index-migration.md | 1024 维文本索引、alias、BM25+vector RRF 和证据展开 | mapping/维度/权限集成测试通过；不切生产 alias |
| 4 | docs/superpowers/plans/2026-07-30-official-corpus-loader.md | 六个官方来源锁定、许可证审计、pilot loader | 每个来源先 pilot；失败停止全量导入 |
| 5 | docs/superpowers/plans/2026-07-30-retrieval-evaluation-cutover.md Tasks 1-2 | 指标契约、180 条文本 qrels、至少 120 条多模态 qrels | schema 和人工复核数量通过，尚不运行 cutover |
| 6 | docs/superpowers/plans/2026-07-30-multimodal-visual-pilot.md | 独立页面视觉索引、候选模型 bake-off、ColQwen2 adapter | 500-2,000 页和许可证/显存门槛通过才允许视觉 alias |
| 7 | docs/superpowers/plans/2026-07-30-retrieval-evaluation-cutover.md Tasks 3-4 | 完整对照报告、一致性审计、真实 Agent E2E 和原子切换 | 所有指标、审计和回滚演练通过 |

## Dependency Gates

1. Plan 1 完成前不改变 document_vectors 的生产写入路径，不创建 v2 index。
2. Plan 2 完成前，任何 PDF 仍由现有 MinerU OCR 路由处理；Tika 只服务非 PDF。
3. Plan 3 先创建物理 index 和只读验证命令，禁止把 knowledge_base_current 指向 v2。
4. Plan 4 的 loader 只接受锁定 commit 和许可证哈希，所有来源按 20-50 文件 pilot；任一来源失败停止全量阶段。
5. 先完成评测计划 Tasks 1-2 并得到人工复核 qrels，再执行视觉 bake-off；不得用自动生成答案替代 qrels。
6. 视觉链必须能返回 disabled，不能伪造视觉结果，也不能把视觉向量写进 BGE-M3 vector。
7. 评测计划 Task 4 是唯一允许切换 alias 的任务；任一门槛失败保留旧 index 和 v2 分析数据。

## Global Verification

~~~powershell
go test ./...
go vet ./...
python -m pytest -q
git diff --check
~~~

每个计划完成后都要追加 D:\Obsidian\code-autogrowth\私人\localcode\PROGRESS-YYYY-MM-DD.md：记录提交、命令输出摘要、MySQL/ES/MinIO 快照、状态（DESIGNED/IMPLEMENTED/VERIFIED/BLOCKED）和回滚边界。模型下载、官方来源抓取、真实 DeepSeek 调用和 alias 切换都必须使用显式 integration 命令，普通单元测试不得联网。

## Explicit Non-Goals

- 不删除旧 knowledge_base、5 条孤立 smoke chunk、历史 E2E 数据、模型缓存或 Docker volume。
- 不使用 Tika 解析 PDF；Docling、Unstructured、Marker 不得重新打开源 PDF。
- 不在本组计划中实现真 token 流式、subagent 或 tau2 运行时；tau2 保持独立设计。
- 不在视觉模型许可证、显存、效果和索引体积审计完成前锁定视觉生产模型。
