# #7 隔离 Go 提案范围验收：2026-10-14

工程状态 IMPLEMENTED；VERIFIED 范围为 #7 原五项验收：真实 Go 隔离修复、
实际项目测试、来源与基线绑定提案、查看不应用，以及已选右侧卡片。
公开仓库十轮、受批准应用、Memory 和完整 Trace 按后续票分别验收。

## 真实演示与当前绑定

[原真实演示](model-proposal-budget-2026-10-13.md)及其
[原收据](model-proposal-budget-2026-10-13.receipt.json)保持原样，SHA-256
`8d3791387c270dc208889bb04fcd4ea22e15af9a463e117768206f1eb25df857`。
受控 Go 示例的基线退出 1，模型隔离修改后的测试及冻结提案复测退出 0；
8 项场景通过。前台完整分母仍为 2：1 failed／1 completed，provider 为
8：5 settled／3 unknown，不声明反思写回或所有调用成功。

当前源码追溯 `013fec88749269711a71fdc9b205d2fa93d5da9c`。
原完整运行输入清单中 855 项指纹相同、40 项变化；提案生成、diff、冻结输入、
卡片、API 和前端类型六项核心文件保持。变化涉及工作指令、续行／工具准入、
读取缓存、租约恢复、期限协议、Docker 分组、错误展示及对应测试。
这不是整个旧模型运行与当前源码相同的声明。
旧前端 CSS 相同，JS／HTML 随错误展示变化；本轮重新运行前端检查和浏览器 fixture。
逐文件比较保存于 `.scratch/proposal-closeout-20261014/binding.json`，SHA-256
`0aea5f2109ac0eebc4a896f2aefae03233777747a7f406de2799a9f28856e574`。

当前批准的分组 manifest SHA-256
`39f4d592d297f32337cb4938e0e96045ccefc602d458da34301ce13f751b3b7c`；
任务镜像保持 `sha256:c35ffc50ff2d4e97ea90f973b29836945912397204592a0b06a0b6fbbba59b58`。
本轮使用现有 `localcode` 控制服务，生产 Go HTTP、Ledger、工具与真实 OpenSandbox；
模型 HTTP／编排 gRPC 是外部 fixture，真实 provider 调用为 0。
此新鲜回归覆盖隔离副本、实际测试、提案刷新／历史请求重放、语义篡改拒绝和原仓库保护，
原真实模型证据只用于证明模型曾完成一个 Go 修复。

## 原验收映射

| 原要求 | 验收依据 |
| --- | --- |
| 真实模型／Go／Python／批准环境、实际计划与进展 | 原真实演示的任务事件、截图、完整尝试及模型调用分母 |
| 起点 diff、存在状态、实际命令／退出码、红绿保留 | 原 baseline 1／修改后 0／冻结复测 0，原提案收据；当前 HTTP 回归及浏览器 diff／输入绑定 |
| 仅 Task Workspace 副作用，unknown 保留 | 原模型隔离修改与原仓库保护；当前 TaskWorkspace／FrozenInput 回归和生产 Go HTTP 拒绝边界 |
| owner／Session／基线、Ledger、查看／刷新不应用 | 原提案记录与当前 HTTP 绑定、重放／篡改拒绝及浏览器刷新；实际应用属于 #8 |
| 现有组件、已选布局、前端检查与交互 | 复用已有只读 diff 和右侧卡片，本轮 tests／build／Chromium fixture 通过 |

## 当前命令和完整分母

| 范围 | 命令 | 结果 |
| --- | --- | --- |
| Go 定向 | `go test ./internal/session ./internal/worktree ./internal/sandbox -run 'Proposal\|TaskCopy\|TaskWorkspace\|FrozenInput' -count=1 -json` | 15 pass／0 skip／0 fail；退出 0 |
| 生产 HTTP／真实 OpenSandbox，模型 fixture | `.venv/Scripts/python.exe .scratch/proposal-closeout-20261014/run_runtime.py` | 1 顶层测试 pass／0 skip／0 fail；退出 0；provider 0 次 |
| 前端单测 | `npm test`（frontend） | 12 pass／0 skip／0 fail；退出 0 |
| 前端类型／构建 | `npm run build`（frontend） | 退出 0 |
| Chromium 外部 HTTP fixture | `node tests/taskProposal.browser.mjs`（frontend） | 6／6 检查通过；退出 0；provider 0 次 |

Go 定向与前端日志在 `.scratch/proposal-closeout-20261014/`，HTTP 原始事实／日志在
`deployments/docker/.local/receipts/proposal-closeout-20261014/`，浏览器 artifact 为
`output/playwright/task-proposal-1791971354593/receipt.json`。
[本轮脱敏收据](proposal-closeout-2026-10-14.receipt.json)，SHA-256
`62828664ee2418edc3f2165f483af162974dd98899e7b4dcbfa4e12618518e89`。
辅助指纹检查最初误用产物路径退出 1 两次，未改变 Ledger 或写入收据；纠正后通过。
额外启动 dev server 因已用端口退出 1，随后复用现有浏览器入口，未停止其他进程。
这些辅助失败保留，不修改原产品结果。
收据辅助脚本最初未处理已删除的旧导航文件，退出 1 且未写收据；按缺失状态核对后退出 0。

本轮不改变运行源码，完整 Go／Python 使用[原范围回归](model-deadline-2026-10-14.md)
作为历史链接，未冒充本轮运行。完整发布、受批准应用与十轮专项未运行。
两轴审阅按 #7 原要求进行，Standards 0 项、Spec 0 项；审查是 AI_REVIEWED。
