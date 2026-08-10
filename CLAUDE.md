# Project Instructions

- Keep the Go harness and Python orchestrator boundaries separate.
- Prefer small, reviewable changes that follow the design document.
- Keep Go code `go test ./...` clean.
- Keep Python modules importable from the repository root.
- Keep `proto/codeagent/orchestrator.proto` and the implementation tree in sync.
- Avoid adding dependencies unless a module boundary already needs them.

## Skill 优先原则

- **前端 UI/视觉设计优先调用 `frontend-design` skill**，避免千篇一律的 AI 风格界面。
- **代码架构、后端、系统设计优先走 `superpowers` 工作流**（brainstorming → writing-plans → executing-plans → code-review），不跳过规划直接写代码。
- **已有 skill 能覆盖的任务用 skill**，不从头手写 prompt。
- **变更代码前先读相关文件**，保持风格和命名一致。
- **用中文回复**，代码和注释保持项目原有语言。

## 通用规则

- **诚实评测，严禁作弊**：Agent 只能获得与真实场景一致的输入（问题描述和代码仓库），不得在 Prompt 中夹带答案、定位提示或修复方向。评测目的是真实验收能力，不是跑通。
- 多文件修改先用 EnterPlanMode 出方案，确认后再写代码。
- 能用专用工具（Read/Glob/Grep/Edit/Write）就不用 Shell 命令。
- 提交前跑 `git diff --stat` 确认改动范围。
- **有意义的进展必须同步写入 `D:\Obsidian\code-autogrowth\私人\localcode`**，用中文，含架构决策、阶段性成果、真实验收、数据状态变化、踩坑和未完成项。
- 进展记录用 `PROGRESS-YYYY-MM-DD.md`（或更新当天已有文档），写明分支/提交、事实证据、执行过的验证、数据快照、未完成项、回滚边界。
- 提交或交接前检查上一份 Obsidian 记录到当前 HEAD 的提交，补录未沉淀的进展。不能写 Obsidian 时先在仓库 `docs/` 生成同名待同步文档，获得权限后补同步。
- 严格区分 `DESIGNED`、`IMPLEMENTED`、`VERIFIED`、`BLOCKED`：有规格或测试代码不等于实现或验收通过。
- **需要模型调用测试时**，从 `D:\Obsidian\code-autogrowth\项目进展\api-key.md` 读取 DeepSeek 官方 key，模型用 `deepseek-v4-pro`；key 只用于本地测试，不硬编码、不提交。
- **阶段性任务完成后可自动 `git commit` 并 `git push`**，提交信息概括本轮要点，结尾附 `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`。
- **需要 Docker 时可直接隐藏启动 Docker Desktop**，无需确认；不含删除容器、volume、索引或业务数据。
- **本地代理不通时用 Clash for Windows**（默认 `127.0.0.1:7890` HTTP 代理）：
  - Windows 侧：`$env:HTTP_PROXY='http://127.0.0.1:7890'; $env:HTTPS_PROXY='http://127.0.0.1:7890'`
  - WSL 侧：`export http_proxy=http://<Windows主机IP>:7890 https_proxy=http://<Windows主机IP>:7890`（主机 IP 用 `ip route show default | awk '{print $3}'` 取）
- **超过 15 分钟的工作（下载大文件、Docker 构建、pip install）先确认走代理是否更快**。优先在代理可达的环境下载（如 WSL 直连而非容器内），再把文件传入 Docker build context。

## RAG continuation memory

- **四目标工作的唯一权威执行地图是 `docs/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md`。** 处理自研 Harness、多模态 RAG、评测集或可观测性前必须完整读取，按其中的 Phase 依赖、artifact/trace 契约、状态口径、任务卡和发布门禁执行，不得另建冲突路线或越过前置门禁。
- Record every meaningful RAG milestone in both `docs/PROGRESS-YYYY-MM-DD.md` and `D:\Obsidian\code-autogrowth\私人\localcode\PROGRESS-YYYY-MM-DD.md` before handoff or push.
- State `DESIGNED`, `IMPLEMENTED`, `VERIFIED`, and `BLOCKED` precisely, including commands, current MySQL/ES/MinIO counts, unfinished plans, and rollback boundaries.
- All PDF entry points use MinerU in explicit OCR mode. Tika is limited to non-PDF office documents such as DOCX, PPTX, and XLSX.
- GPT-5.6 Sol 复核必须标记为 `AI_REVIEWED` 或 `DISPUTED`，不得生成真人 `reviewer_hash` 或冒充真人复核；只有真实人工参与后才可标记 `HUMAN_REVIEWED`。
- Do not claim the multimodal corpus complete until the design map's data, qrels, index, visual bake-off, observability, and explicit integration gates pass.
