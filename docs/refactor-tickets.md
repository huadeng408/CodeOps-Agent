# Windows 代码产品任务导航

正式规格：[milestone 1，Issue #1](https://github.com/huadeng408/CodeOps-Agent/issues/1)。
18 张纵向任务及 32 条阻塞依赖已按用户批准的拆解发布，分流标签为 `ready-for-agent`。
任务实现状态仍为 `DESIGNED`；runtime 与发布结论以 [GOAL.md](GOAL.md) 和对应收据为准。

标题、负责人、当前状态和依赖以 GitHub 为准；下表是 2026-10-09 发布时的导航快照。
每张 Issue 包含完整验收、验证、复用和回退条件。父规格 #1 的正文、状态及标签保留。

| 草案编号 | 正式任务 | 发布时 Blocked by |
| --- | --- | --- |
| 01 | [#2：无外部服务的安全本机启动与历史查看](https://github.com/huadeng408/CodeOps-Agent/issues/2) | 无 |
| 02 | [#3：真实调用前置检查与持久化 1 亿 token 批次上限](https://github.com/huadeng408/CodeOps-Agent/issues/3) | [#2](https://github.com/huadeng408/CodeOps-Agent/issues/2) |
| 03 | [#4：从当前工作副本准备隔离任务并查看基线](https://github.com/huadeng408/CodeOps-Agent/issues/4) | [#2](https://github.com/huadeng408/CodeOps-Agent/issues/2) |
| 04 | [#5：真实仓库问答贯通 provider 扩展点与 P0/P1/P2](https://github.com/huadeng408/CodeOps-Agent/issues/5) | [#3](https://github.com/huadeng408/CodeOps-Agent/issues/3)、[#4](https://github.com/huadeng408/CodeOps-Agent/issues/4) |
| 05 | [#6：在批准环境准备依赖并运行 Go/Python 基线测试](https://github.com/huadeng408/CodeOps-Agent/issues/6) | [#4](https://github.com/huadeng408/CodeOps-Agent/issues/4) |
| 06 | [#7：真实 Go 修复生成可审查提案和项目测试结果](https://github.com/huadeng408/CodeOps-Agent/issues/7) | [#5](https://github.com/huadeng408/CodeOps-Agent/issues/5)、[#6](https://github.com/huadeng408/CodeOps-Agent/issues/6) |
| 07 | [#8：确认或拒绝单文件提案且应用恰好一次](https://github.com/huadeng408/CodeOps-Agent/issues/8) | [#7](https://github.com/huadeng408/CodeOps-Agent/issues/7) |
| 08 | [#9：准确预览和应用新增、删除、重命名及多文件修改](https://github.com/huadeng408/CodeOps-Agent/issues/9) | [#8](https://github.com/huadeng408/CodeOps-Agent/issues/8) |
| 09 | [#10：并发冲突与应用中断后的保留和对账](https://github.com/huadeng408/CodeOps-Agent/issues/10) | [#9](https://github.com/huadeng408/CodeOps-Agent/issues/9) |
| 10 | [#11：一个 MCP 工具贯通模型调用、Go 授权和结果展示](https://github.com/huadeng408/CodeOps-Agent/issues/11) | [#5](https://github.com/huadeng408/CodeOps-Agent/issues/5)、[#6](https://github.com/huadeng408/CodeOps-Agent/issues/6) |
| 11 | [#12：取消真实代码任务并停止其受管资源](https://github.com/huadeng408/CodeOps-Agent/issues/12) | [#7](https://github.com/huadeng408/CodeOps-Agent/issues/7) |
| 12 | [#13：代码进程故障恢复与 Go/Python 重启后重开旧会话](https://github.com/huadeng408/CodeOps-Agent/issues/13) | [#7](https://github.com/huadeng408/CodeOps-Agent/issues/7) |
| 13 | [#14：真实反思写回并在重启后的空新会话召回](https://github.com/huadeng408/CodeOps-Agent/issues/14) | [#13](https://github.com/huadeng408/CodeOps-Agent/issues/13) |
| 14 | [#15：成功代码任务完整 Trace 在真实后端读回](https://github.com/huadeng408/CodeOps-Agent/issues/15) | [#7](https://github.com/huadeng408/CodeOps-Agent/issues/7) |
| 15 | [#16：脱离开发者 PATH 的 Windows 一键 EXE 交付](https://github.com/huadeng408/CodeOps-Agent/issues/16) | [#5](https://github.com/huadeng408/CodeOps-Agent/issues/5)、[#6](https://github.com/huadeng408/CodeOps-Agent/issues/6) |
| 16 | [#17：公开 Go 修复的至少十轮真实浏览器验收](https://github.com/huadeng408/CodeOps-Agent/issues/17) | [#10](https://github.com/huadeng408/CodeOps-Agent/issues/10)、[#11](https://github.com/huadeng408/CodeOps-Agent/issues/11)、[#12](https://github.com/huadeng408/CodeOps-Agent/issues/12)、[#14](https://github.com/huadeng408/CodeOps-Agent/issues/14)、[#15](https://github.com/huadeng408/CodeOps-Agent/issues/15)、[#16](https://github.com/huadeng408/CodeOps-Agent/issues/16) |
| 17 | [#18：公开 Python 功能的至少十轮真实浏览器验收](https://github.com/huadeng408/CodeOps-Agent/issues/18) | [#10](https://github.com/huadeng408/CodeOps-Agent/issues/10)、[#11](https://github.com/huadeng408/CodeOps-Agent/issues/11)、[#12](https://github.com/huadeng408/CodeOps-Agent/issues/12)、[#14](https://github.com/huadeng408/CodeOps-Agent/issues/14)、[#15](https://github.com/huadeng408/CodeOps-Agent/issues/15)、[#16](https://github.com/huadeng408/CodeOps-Agent/issues/16) |
| 18 | [#19：五类恢复场景与首批产品完整验收对账](https://github.com/huadeng408/CodeOps-Agent/issues/19) | [#17](https://github.com/huadeng408/CodeOps-Agent/issues/17)、[#18](https://github.com/huadeng408/CodeOps-Agent/issues/18) |

从 [#2：独立启动与鉴权历史](https://github.com/huadeng408/CodeOps-Agent/issues/2) 开始；
后续选择阻塞任务已完成、且尚未领取的 Issue。取消、重启和 trace 分支按实际依赖推进。
两项真实验收共享已批准批次，执行侧保持一次一个代码任务；依赖图不额外制造串行关系。

本地每张任务的投影位于 `.scratch/matt-refactor/issues/`，保留实际 Issue 链接和工作笔记，
不另立任务权威。完整长期门槛继续独立推进，发布任务卡不构成产品完成或 runtime 验证。
