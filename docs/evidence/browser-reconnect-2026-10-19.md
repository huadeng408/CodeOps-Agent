# #24 浏览器重连游标修复

范围：已完成 Session 在两个标签页打开，单页断线后补齐自己的 Ledger 投影。
对应 [#24](https://github.com/huadeng408/CodeOps-Agent/issues/24)。
这是前端恢复行为修复；#1、完整长对话矩阵与发布分别验收。#20 按用户要求暂缓。

## 问题与修改

旧实现取当前页面游标与共享 localStorage 游标的较大值。断线页只收到序号 1，
另一个标签页收到序号 2、3 后，断线页却用 `after=3` 重连。Go 正确返回该游标
之后的事件，但页面缺失的两条事件没有被补齐；已完成 Session 没有活跃运行轮询，
且没有下一条事件触发缺口检查时，遗漏持续存在。

`useWebSocket` 现在仅使用调用页面已合并事件的游标。删除唯一的旧游标读取器、
三处无用途的持久化调用与只断言旧规则的单测；保留按序合并、REST 历史和缺口恢复。
旧 localStorage 值可以保留，它们不再影响重放。没有新增依赖、接口、数据迁移或布局。
Go 的 owner 检查、一次性 ticket、重放序列校验与 Ledger 实现均未修改。

## 源码与运行输入

- 基点：`70d423678c7a1bae2877eeb4310c0041bedc02d0`。
- 原始与修复后输入：`.scratch/browser-reconnect-20261019/{red,green}-inputs.json`。
  包含相关源码、测试、包锁、构建输入与本机工作指令的 SHA-256。
- 不可变验证物料：`.scratch/browser-reconnect-20261019/validation-artifacts.json`，
  30 个文件，SHA-256 `1f8c7ebe5a396a7c2c4b441b6faf78d00046755cfecabdc186ed12e3dc599b35`。
- 真实 Go 浏览器二进制 SHA-256：
  `cb683e13cc5fb68828491eb3360f234b2f9d76f370b5104a9d89545e8559093a`。
  对应收据还保存全部源码、实际生产静态资源及其运行前后指纹。

## 验证与完整分母

| 验证 | 命令 | 结果／退出码 |
| --- | --- | --- |
| 原源码的浏览器 RED | `node tests/webSocketReconnect.browser.mjs http://127.0.0.1:4174`，在 `frontend/` | 6 项：2 PASS、1 FAIL、3 NOT RUN；1 |
| 修复后的浏览器 GREEN | 同上 | 初次及最终脚本各 6/6 PASS，0 FAIL／SKIP；均 0 |
| 前端完整单测 | `npm test`，在 `frontend/` | 11/11 PASS，0 FAIL／SKIP；0 |
| 生产构建／类型检查 | `npm run build`，在 `frontend/` | 0 |
| 既有静态预览构建回归 | `npm run build:preview`，在 `frontend/` | 0；不代表线上部署 |
| 新浏览器脚本语法 | `node --check frontend/tests/webSocketReconnect.browser.mjs` | 0 |
| workflow YAML | Python `yaml.safe_load` 并核对新增步骤 | 0 |
| 原有真实 Go 本机浏览器 | `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 CODE_AGENT_BROWSER_HEADLESS=1 node tests/e2e/local_core_browser.mjs` | 9/9 PASS，0 NOT RUN；0 |

浏览器回归使用同一 Chromium context 的两个真实页面和 HTTP/WebSocket fixture：
初始历史、另一页产生进展、原页重连补齐、草稿与无重发、刷新历史、无页面错误。
测试明确保留一个旧前端可能留下的较新存储游标，恢复页面须显示四条消息且顺序唯一。
原 RED 的确切结果是新连接请求 `after=3`，结果消息等待超时；两次 GREEN 都以
该页实际已收的 `after=1` 补齐。后续刷新允许从 `-1` 或已加载的当前进度重放。

单测从 12 项变为 11 项是删除了断言不安全共享游标规则的旧单测；对应用户行为改由
6 项浏览器回归覆盖，原有两项事件合并单测保留。新浏览器回归已接入 Frontend CI，
使用锁定的现有 Playwright 依赖，启动独占 preview 进程并在退出时按 PID 回收。

浏览器物料分别在 `output/playwright/browser-reconnect-20261019-{red,green,green-final}/`。
每次均保存完整计划分母、实际游标、提交请求、页面错误、截图、退出码和浏览器关闭结果。
RED/GREEN 均已关闭所属浏览器；只另一页提交一次消息，恢复页没有发送新消息。
草稿断线恢复在本轮范围内；多标签页的草稿持久化隔离不属于本轮验收。

真实 Go 回归收据：
`output/playwright/local-core-99384580-e624-4163-84cc-52a1398bba8b/receipt.json`。
覆盖登录、创建／打开 Session、能力与计量展示、缺执行前提时拒绝、服务不可达后的
UNKNOWN、Go 重启后的历史及退出登录。两个所属 Go 进程均停止；未调用模型或创建 Docker
资源。它证明既有 Go 本机流程兼容；新的双标签页故障场景本轮只在 fixture 边界验证。

第一次尝试真实 Go 回归时，Git index 仍列出已删除的 `ledgerCursor.ts`，源码物料清点报
`ENOENT` 并以 1 退出；计划 9 项均未启动，未产生 Go 运行收据。将两处已审阅的删除
加入 index 后，第二次完整 9/9 通过。原失败退出码和错误边界保留在本轮验证摘要中。

## 状态

- 单测／类型检查／构建：`VERIFIED`，范围与命令如上。
- 浏览器 UI 集成：`VERIFIED`，6 项 fixture 行为；不升级为 Go／provider 运行证据。
- 产品兼容：`VERIFIED`，原有真实 Go 本机浏览器 9 项；没有宣称完成新故障的真实 Go 验收。
- 双标签页真实 Go 故障专项、200 assistant-turn／compaction、模型能力、完整发布：未运行。
- 本修复声明范围内没有未解决 blocker；当前全项目发布状态不变。
