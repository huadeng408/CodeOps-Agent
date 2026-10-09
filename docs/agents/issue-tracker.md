# Issue tracker: GitHub + Local Markdown

GitHub Issues 保存正式规格、任务、依赖和讨论。
本地 Markdown 保存草稿、拆解和工作笔记，链接对应 GitHub Issue。
已发布任务的标题、分流、负责人和依赖以 GitHub 为准。

## GitHub

- 从当前仓库 remote 确认目标仓库，使用 gh CLI 操作。
- 技能要求“publish to the issue tracker”时，创建 GitHub Issue。
- 读取任务使用 gh issue view <number> --comments。
- 创建、更新和评论的多行正文写入临时文件，通过 --body-file 提交。
- 发布前检查正文，排除凭据、机器路径及未经审核的运行数据。
- 外部写操作遵守当前任务授权和仓库交付流程。

PRs as a request surface: no.

## Local Markdown

- 规格：.scratch/<feature>/spec.md。
- 任务：.scratch/<feature>/issues/<NN>-<slug>.md，一张任务一个文件。
- 探索地图：.scratch/<effort>/map.md。
- 已发布文件记录 GitHub-Issue: <Issue URL>。
- 未发布草稿记录 GitHub-Issue: pending；恢复网络后统一发布一次并回填链接。
- Triage: 保存分流标签；Status: 保存实现或验收状态。
- 工作笔记追加到 ## Comments，不覆盖原始需求和失败证据。
- .scratch/ 保持忽略；需要团队共享的内容发布到对应 Issue。
- 旧五张对齐任务卡保留在 docs/archive/plans/codeops-fast-alignment-2026-10-09/；只追溯历史时读取。当前任务从正式规格和批准后的拆解开始，避免重复发布。

## Dependencies and wayfinding

- GitHub 地图 Issue 关联子任务；本地地图保存同一组 Issue 链接。
- 优先使用可用的原生子任务和依赖；否则在正文记录
  Part of #<map> 和 Blocked by: #<number>。
- 按地图顺序选择未领取且无未关闭阻塞任务的子任务。
- 领取时设置 GitHub assignee，本地只记录链接与工作笔记。
- 完成后提交结果和证据指针，满足任务验收条件才关闭 Issue。
- Issue 关闭不代表产品 runtime 或发布门禁已经 VERIFIED。
