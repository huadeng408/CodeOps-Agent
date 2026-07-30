# Project Instructions

- Keep the Go harness and Python orchestrator boundaries separate.
- Prefer small, reviewable changes that follow the design document.
- Keep Go code `go test ./...` clean.
- Keep Python modules importable from the repository root.
- Keep `proto/codeagent/orchestrator.proto` and the implementation tree in sync.
- Avoid adding dependencies unless a module boundary already needs them.

## Skill 优先原则

- **涉及前端 UI/视觉设计时，优先调用 `frontend-design` skill**，由它指导配色、字体、布局、动效等设计决策，避免生成千篇一律的 AI 风格界面。
- **涉及代码架构、后端设计、系统设计时，优先使用 `superpowers` 工作流**（brainstorming → writing-plans → executing-plans → code-review），不要跳过规划直接写代码。
- **已有 skill 能覆盖的任务，优先用 skill**，不要从头手写 prompt——skill 自带经过验证的最佳实践和质量闸门。
- **变更代码前先读相关文件**，理解现有风格和命名习惯，保持一致性。
- **用中文回复**，代码和注释保持项目原有语言。

## 通用规则

- 涉及多文件修改时，先用 EnterPlanMode 出方案，用户确认后再写代码。
- 能用专用工具（Read/Glob/Grep/Edit/Write）就不用 Shell 命令。
- 提交代码前跑一下 `git diff --stat` 确认改动范围符合预期。
- **有意义的进展必须同步写入 `D:\Obsidian\code-autogrowth\私人\localcode`**，包括架构决策、阶段性成果、真实验收、数据状态变化、踩坑记录和重要未完成项。
- 进展记录使用按日期命名的 `PROGRESS-YYYY-MM-DD.md`，或更新当天已有的对应文档；至少写明分支/提交、事实证据、执行过的验证、当前数据快照、未完成项和回滚边界。
- 严格区分 `DESIGNED`、`IMPLEMENTED`、`VERIFIED` 和 `BLOCKED`：存在规格或测试代码不等于实现或真实验收通过，不得把计划写成完成。
- 在提交或交接前检查从上一份 Obsidian 记录到当前 HEAD 的提交，补录所有尚未沉淀的有意义进展。若当前环境暂时不能写 Obsidian，先在仓库 `docs/` 生成同名待同步文档，并在获得写入权限后完成同步。
- **需要模型调用来测试 agent 或其他效果时**，从 `D:\Obsidian\code-autogrowth\项目进展\api-key.md` 读取 DeepSeek 官方 API key，模型用 `deepseek-v4`；注意 key 只用于本地测试，不要硬编码或提交进代码仓库。

## RAG continuation memory

- Record every meaningful RAG milestone in both `docs/PROGRESS-YYYY-MM-DD.md` and `D:\Obsidian\code-autogrowth\私人\localcode\PROGRESS-YYYY-MM-DD.md` before handoff or push.
- State `DESIGNED`, `IMPLEMENTED`, `VERIFIED`, and `BLOCKED` precisely, including commands, current MySQL/ES/MinIO counts, unfinished plans, and rollback boundaries.
- All PDF entry points use MinerU in explicit OCR mode. Tika is limited to non-PDF office documents such as DOCX, PPTX, and XLSX.
- Do not claim the multimodal corpus complete until Plans 2-7, human qrels, index gates, and explicit integration tests pass.
