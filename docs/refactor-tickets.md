# Windows 代码产品任务导航

正式规格：[milestone 1，Issue #1](https://github.com/huadeng408/CodeOps-Agent/issues/1)。
实现状态、负责人和依赖以 GitHub Issues 为准；选择已满足阻塞条件的开放任务。
验证范围和当前缺口见 [GOAL.md](GOAL.md)，工作与前端选择约定见 [AGENTS.md](../AGENTS.md)。

| 草案编号 | 正式任务 |
| --- | --- |
| 01 | [#2：无外部服务的安全本机启动与历史查看](https://github.com/huadeng408/CodeOps-Agent/issues/2) |
| 02 | [#3：真实调用前置检查与持久化 1 亿 token 批次上限](https://github.com/huadeng408/CodeOps-Agent/issues/3) |
| 03 | [#4：从当前工作副本准备隔离任务并查看基线](https://github.com/huadeng408/CodeOps-Agent/issues/4) |
| 04 | [#5：真实仓库问答贯通 provider 扩展点与 P0/P1/P2](https://github.com/huadeng408/CodeOps-Agent/issues/5) |
| 05 | [#6：在批准环境准备依赖并运行 Go/Python 基线测试](https://github.com/huadeng408/CodeOps-Agent/issues/6) |
| 06 | [#7：真实 Go 修复生成可审查提案和项目测试结果](https://github.com/huadeng408/CodeOps-Agent/issues/7) |
| 07 | [#8：确认或拒绝单文件提案且应用恰好一次](https://github.com/huadeng408/CodeOps-Agent/issues/8) |
| 08 | [#9：准确预览和应用新增、删除、重命名及多文件修改](https://github.com/huadeng408/CodeOps-Agent/issues/9) |
| 09 | [#10：并发冲突与应用中断后的保留和对账](https://github.com/huadeng408/CodeOps-Agent/issues/10) |
| 10 | [#11：一个 MCP 工具贯通模型调用、Go 授权和结果展示](https://github.com/huadeng408/CodeOps-Agent/issues/11) |
| 11 | [#12：取消真实代码任务并停止其受管资源](https://github.com/huadeng408/CodeOps-Agent/issues/12) |
| 12 | [#13：代码进程故障恢复与 Go/Python 重启后重开旧会话](https://github.com/huadeng408/CodeOps-Agent/issues/13) |
| 13 | [#14：真实反思写回并在重启后的空新会话召回](https://github.com/huadeng408/CodeOps-Agent/issues/14) |
| 14 | [#15：成功代码任务完整 Trace 在真实后端读回](https://github.com/huadeng408/CodeOps-Agent/issues/15) |
| 15 | [#16：脱离开发者 PATH 的 Windows 一键 EXE 交付](https://github.com/huadeng408/CodeOps-Agent/issues/16) |
| 16 | [#17：公开 Go 修复的至少十轮真实浏览器验收](https://github.com/huadeng408/CodeOps-Agent/issues/17) |
| 17 | [#18：公开 Python 功能的至少十轮真实浏览器验收](https://github.com/huadeng408/CodeOps-Agent/issues/18) |
| 18 | [#19：五类恢复场景与首批产品完整验收对账](https://github.com/huadeng408/CodeOps-Agent/issues/19) |

## OpenSandbox 接入

[#21：Go 基线](https://github.com/huadeng408/CodeOps-Agent/issues/21)、
[#22：Python 离线基线](https://github.com/huadeng408/CodeOps-Agent/issues/22)、
[#23：取消、故障重开与 CLI](https://github.com/huadeng408/CodeOps-Agent/issues/23) 是 #6 的子任务。
当前采用 OpenSandbox，沿用已选 A 右侧测试卡片；各票的范围证据从 Goal 进入。

## 后续产品专项

| 正式任务 | 证据入口 |
| --- | --- |
| [#24：浏览器重连漏事件修复](https://github.com/huadeng408/CodeOps-Agent/issues/24) | [源码与浏览器回归](evidence/browser-reconnect-2026-10-19.md) |
| [#25：真实双标签页重连与空闲重启](https://github.com/huadeng408/CodeOps-Agent/issues/25) | [真实产品范围](evidence/browser-reconnect-product-2026-10-19.md) |
| [#26：CLI 配置的上下文窗口](https://github.com/huadeng408/CodeOps-Agent/issues/26) | [传输接缝回归](evidence/cli-context-window-2026-10-19.md) |
| [#27：200 轮压缩与恢复](https://github.com/huadeng408/CodeOps-Agent/issues/27) | [最新完整失败分母与修复边界](evidence/product-long-dialogue-2026-10-21.md) |
| [#28：压缩后的 Memory 写回](https://github.com/huadeng408/CodeOps-Agent/issues/28) | [真实写回范围](evidence/memory-compaction-2026-10-20.md) |

## Module 深化

规格 [#31](https://github.com/huadeng408/CodeOps-Agent/issues/31) 覆盖三个独立改动：
[#32 Context 压缩](https://github.com/huadeng408/CodeOps-Agent/issues/32)、
[#33 Task Workspace 准入](https://github.com/huadeng408/CodeOps-Agent/issues/33)、
[#34 Session 终态收尾](https://github.com/huadeng408/CodeOps-Agent/issues/34)。
[#35 汇总验证与审查](https://github.com/huadeng408/CodeOps-Agent/issues/35) 依赖前三项。
各票保留既有授权、Session Ledger 与兼容契约；源码和跨进程 fixture 验证不替代产品或发布验收。

## 可选能力

[#20：Cloudflare Free](https://github.com/huadeng408/CodeOps-Agent/issues/20) 按用户要求暂缓，
不作为本机开发或首批 milestone 的前提。当前状态与依赖仍以正式 Issue 为准。

发布时任务与依赖快照保留在 [历史原文](archive/policy-cleanup-2026-10-14/docs--refactor-tickets.md)，
其 `DESIGNED` 状态描述发布时情况。[归档清单](archive/policy-cleanup-2026-10-14/manifest.json)保存原文校验值。
