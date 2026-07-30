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
- **有意义的进展需要写入 `D:\Obsidian\code-autogrowth\私人\localcode`**（如架构决策、阶段性成果、踩坑记录等，便于沉淀到 Obsidian 知识库）。
- **需要模型调用来测试 agent 或其他效果时**，从 `D:\Obsidian\code-autogrowth\项目进展\api-key.md` 读取 DeepSeek 官方 API key，模型用 `deepseek-v4`；注意 key 只用于本地测试，不要硬编码或提交进代码仓库。

