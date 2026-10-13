# 发送入口的拒绝原因与草稿恢复

范围：默认工作台发送消息时的错误提示、有限游标重试和显式恢复。
源码基点 `91f995e5dd18ba8f5fe4fc5d596122aa72701a4e`，沿用当前 main
及已选右侧卡片。工程 `IMPLEMENTED`；前端单测、构建和外部传输 fixture
浏览器回归范围 `VERIFIED`，不据此声明完整模型任务或发布合格。

## 改动及边界

此前 App 将任何 HTTP 409 自动重发一次，发送提示也将所有 409 称为
会话游标冲突。真实运行中的 unknown 用量因此得到错误的重试指导。
Go 已返回具体原因，本次只修复共享前端错误处理及其发送调用方：

- 只有 `session event sequence conflict`，包括其带详细游标的形式，
  才刷新规范游标并自动重试一次；请求 ID 与消息内容保持一致。
- 未确认用量提示先对账；其他代码任务占用提示等待任务完成并结束它。
  两者均保留草稿，不自动重发。
- 其他 409 保留后端原因，提示检查任务与工作区状态后恢复。
- 用户在前提恢复后仍可显式重试原请求。Go 每次重新执行准入检查，
  前端没有结算、放行、替换批次或结束任务的额外权限。

既有 helper 测试的游标错误输入改为实际公开错误形状；新行为只通过
已批准的真实 Chromium／React／HTTP／WebSocket 接缝验证，未替换自身模块。
没有增加依赖、配置、协议或布局。

## 本轮验证与完整分母

| 检查 | 结果与证据边界 |
| --- | --- |
| unknown 用量 RED | 拒绝消息实际发送两次，`16 != 15`；exit 1，失败截图／收据保留 |
| unknown 用量 GREEN | 一次拒绝，无游标变化，具体对账提示，恢复后同 ID／内容接受；exit 0 |
| 任务占用 RED | 未自动重发，但仍误称会话更新；exit 1，失败产物保留 |
| 任务占用 GREEN | 等待／结束任务指导，草稿保留，恢复后同请求接受；exit 0 |
| 其他状态冲突 RED | 仍误称会话更新，未保留实际原因；exit 1，失败产物保留 |
| 最终消息浏览器回归 | `node tests/sendMessage.browser.mjs`：8/8 检查，15 个接受消息／20 次 HTTP 尝试，0 页面错误，exit 0 |
| 结束任务浏览器回归 | `node tests/taskFinish.browser.mjs`：7/7 检查，3 次 finish 尝试，0 页面错误，exit 0 |
| 类型检查 | `npm exec -- tsc --noEmit`，exit 0 |
| 目标测试 | `node --experimental-strip-types --test tests/sendRetry.test.ts`：4 pass／0 fail／0 skip，exit 0 |
| 完整前端测试 | `npm test`：12 pass／0 fail／0 skip／0 cancelled，exit 0 |
| 前端构建 | `npm run build`，exit 0 |
| Ledger 核对 | 通过既有 Go 公共接口读回的修复前后批次和全部 call facts 完全相同，exit 0 |

最终浏览器分母包括十次普通发送、刷新重开、游标竞争、503 草稿恢复及
三类 409 拒绝后恢复。20 次 HTTP 尝试包含全部拒绝和重试；不是模型调用数。
测试期间仅使用外部 HTTP／WebSocket fixture，没有真实模型请求或新 Trace。
截图已检查原因和草稿可见，沿用原布局。

宿主权限配置切换后，两次沙箱辅助程序在创建进程前报
`helper_sandbox_lock_failed`；旧 Vite 句柄已不存在且端口未监听，随后通过
受审查的命令权限启动本轮本地测试服务。没有修改宿主沙箱或系统代理。
这些启动失败不计为测试通过，也未重新运行已确认仍在运行的进程。

Go／Python 源码、授权和预算契约未修改，本轮不重复其完整本地回归。
完整产品代码修改、提案应用、Memory 成功写回和发布验证未运行。

## 当前阻塞与证据

原批次保留 known 22526 tokens、unknown 502048 预留及原任务占用；
后台反思仍需针对该笔提供商记录或用户逐笔确认，不能套用上一笔零用量。
本次前端修复没有新增模型 call facts，也没有读取 provider 密钥。
#3 继续开放，未知用量仍阻断新增模型调用。

原始失败与成功记录在 `.scratch/model-entry-recovery/`；最终消息截图及
收据在 `output/playwright/send-message-1791867874177/`，结束任务回归收据
在 `.scratch/model-task-finish/browser-1791867938434/`。本轮命令、源码和
构建指纹、全部 RED／GREEN 产物汇入 `.scratch/model-entry-recovery/receipt.json`；
提交／CI 绑定追加独立证据，不改写原收据。

Standards／Spec 两轴复核及推送 CI 在交付时分别核验，不把 AI 复核标为人工复核。
