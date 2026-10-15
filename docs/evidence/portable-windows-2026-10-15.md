# #16 Windows 便携包验收

范围：[GitHub #16](https://github.com/huadeng408/CodeOps-Agent/issues/16) 的五项验收。
工程 `IMPLEMENTED`；本次单测、集成及 Windows 便携产品范围 `VERIFIED`。
源码提交 `b2fb8d52c5c29939182695f2caa085c4cebbdc51`；
取消测试观察修正为 `153e430ddb348fd01ff1fdc1f0a4e854fa48bbb0`。
[源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38687634335) 的
Go、Python、Frontend、Windows startup contracts 四个 job 全部通过。
完整发布、Go/Python 十轮对话及完整故障矩阵另行验收，不作为本票追加门禁。

## 产物与运行前提

本机交付物：`deployments/windows/.local/releases/CodeOps-Agent-windows-amd64-20261015.zip`，
113,297,555 字节；SHA-256
`57ffe509065b220e26699c0f826e39ccc346c3899651bed2e62bcbf324b71b45`。
ZIP 的 3,220 个文件逐项匹配实际运行目录：3,219 个运行文件及一个 manifest。
没有创建或上传公开二进制 release。

适用环境为 Windows 10/11 x64、系统 UCRT 和默认浏览器；实际验收机为 Windows 11 x64
10.0.26200（build 26200）。
Go Harness 使用 go1.26.0；随包 CPython 3.13.16、MinGit 2.56.0.windows.2、
53 个 Python wheels、98 个实际链接 Go 模块及 3 个前端生产依赖。
固定来源、archive/wheel hash 和许可见[组件 pin](../../deployments/windows/components.json)、
[Python 锁文件](../../deployments/windows/requirements.lock)及包内 manifest、license 文件。
操作和首次身份设置见[使用说明](../../deployments/windows/README.md)。
模型凭据、允许的仓库及 Docker/WSL 和已批准 OpenSandbox 是对应执行能力的外部前提。

启动器从自身位置解析包，校验完整清单；启动时不下载、编译或回退开发机工具。
Python 使用隔离 embedded 路径；运行数据和工作区在安装目录之外。
Windows 原子 Job 归属覆盖 Go/Python 及其后代，关闭 Job 只回收本次子进程。
默认浏览器与外部 Docker 服务不属于该 Job。

## 验收映射

| 验收项 | 实际证据 |
| --- | --- |
| 固定组件和无私有输入 | 清单、版本、checksum 和许可齐全；只打包已纳管 Python 源码；未收录忽略的测试 sentinel、数据库、凭据或开发者绝对路径 |
| 迁移与隔离运行 | 将真实包迁移到含中文和空格的目录，从无关临时目录、空父进程 PATH 经 Windows ShellExecute 启动；实际注册、登录、打开历史、准备隔离工作区并完成真实模型只读问答 |
| 缺前提准确受阻 | 未配置模型/沙箱时身份和历史 ready，执行 blocked；已配置但控制服务退出时沙箱 blocked；精确启动已批准控制服务并刷新后执行 ready。没有调用开发机安装兜底 |
| 重复启动、退出和归属 | 大小写目录别名重复启动被拒绝，原实例保持可用；终止启动器后原子 Job 回收所有后代；重启登录保留旧历史；显式 exit 有 Go/Python 终态；外部控制服务未被产品关闭，测试结束恢复原先 exited，数据保留 |
| 可复现收据 | ZIP、启动器、服务端和 manifest hash；实际 Windows 环境、8 张浏览器截图、命令、退出码、Ledger 审计与完整失败记录均封存 |

ShellExecute 使用与资源管理器双击相同的 Windows Shell 启动路径；这里记录的是实际 EXE 的
Shell 启动和浏览器交互，没有声称执行了物理鼠标双击。验证采用规格允许的迁移目录和隔离 PATH 条件。

最终 v6 的完整场景分母为 12：组件包、迁移/空 PATH、无配置的登录/历史、目录别名重复拒绝、
重启保留历史、已配置控制服务退出拒绝、真实只读问答、原仓库/index 保护、含 MCP 后代的关闭、
显式退出结果、外部控制服务保留/恢复、运行后包文件不变，12/12 通过。

## 真实问答与生命周期

实际 Session `9d06db18983df75831e78bb12fe048a7`，
run `run:d7987a48e0f53f8c470208e5540a9d0a`，
模型为 grok-4.6，传输为 Anthropic messages relay。
在隔离 chi 工作区只调用一次 Go `Read README.md`，回答三个有文件依据的项目特点；
没有 Write、命令、子 Agent 或提案应用。原仓库 HEAD
`9b9fb55def404397748a9fc7e044efe9db1d618e`、98 个文件及 index 在前后和最终复核中不变。
原生 Go 审计通过 23 个产品事件、833 个准入事件；浏览器“结束代码任务”后任务槽为空。

同一批次 `02cfc8d9c93975b3ea63f27cc0a3331d` 新增 3 次调用，前台 2、反思 1，全部结算；
已确认用量由 3,149,395 增至 3,180,325，差额 30,930 tokens。
历史 unknown 和原预留 3,606,024,576 保留，费用仍 unknown；最终 active calls 0。

终止本产品启动器后，5 个已确认后代均停止，包括 Go、Python、conhost 和两个真实 MCP 后代。
另一次显式 `exit` 的启动器退出 0；Go 子进程退出 0，Python 子进程退出 3221225786
（CTRL_BREAK 终止），均有终态且无剩余子进程，不将 Python 记为正常退出 0。
本次拥有的 Playwright 浏览器已关闭；系统默认浏览器不受产品 Job 管理。

实际控制服务镜像为
`sha256:ca96deb69878ffb6f14f4f072f5df74c4eb124be631086027546de21ece5ae16`，
保持原 Compose `localcode` 身份，未创建、删除或清空 volume/业务数据。
批准的 Go 任务镜像 pin 为
`sha256:c35ffc50ff2d4e97ea90f973b29836945912397204592a0b06a0b6fbbba59b58`；
本票只读问答没有执行基线测试，不能把该 pin 记为本轮任务镜像执行证据。
本轮未启动 Trace 后端，能力保持 unknown；产品 Trace 已在 #15 单独验收。

## 验证与完整失败分母

| 命令/范围 | 本轮完整结果 |
| --- | --- |
| `go test ./cmd/launcher ./cmd/server ./internal/serverconfig -count=1 -json` | 52 passed、1 skipped，3 包通过，退出 0 |
| 首次 `go test ./... -count=1 -json` | 1,731 passed、2 failed、79 skipped；46 包通过、1 包失败、12 包无测试，退出 1。两项 fail 为同一个新 EXE 测试的父/子项，5 秒测试启动等待不足 |
| 修正测试等待后的完整 Go 重跑 | 同命令 1,733 passed、79 skipped；47 包通过、12 包无测试，退出 0 |
| `go test ./cmd/launcher -count=1 -json` | 测试等待修正后 6 passed，退出 0；仅测试等待从 5 秒改为 30 秒，产品期限不变 |
| `go vet ./...` | 退出 0；最终 `go vet ./cmd/launcher ./cmd/server` 亦退出 0 |
| `python -m pytest -q` | 2,544 passed、17 skipped、31 warnings，退出 0；这是宿主环境回归，包内 Python 3.13.16 的真实运行另由产品验证证明 |
| frontend `npm test` / `npm run build` | 12/12 passed、0 skipped，退出 0；build 退出 0，打包冻结窗口内也重新生成前端 |
| queued child 取消观察回归 | `go test ./internal/session -run '^TestRootCancellationAcknowledgesItsQueuedChildWorkspace$' -count=50 -json`，50/50 passed，退出 0 |
| 打包/产品核验 | 最终构建、embedded import、实际产品断言、完整 ZIP/checksum、原生 Ledger 审计和当前运行内容绑定均退出 0 |

早期失败保留：v1 实包启动拒绝；v2 默认工作区配置错误；v3 身份目录混入其他数据；
v4 Python 投影写进安装目录；v5 后经复核补齐源码冻结、原子 Job、已纳管源码和前端许可。
v6 安装文件在运行后仍为原 3,219 个 hash。
浏览器生命周期辅助命令有两次路径分隔符比较错误，归属校验拒绝停止；规范化路径后成功，
两次失败保留在 `browser-commands.json`。没有借此重复派发模型。

初次源码 CI [38681344135](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38681344135)
的 Python、Frontend、Windows startup contracts 通过，Go 命中旧取消测试的瞬态观察断言。
修正仅接受 attempt 0 的 canceling 或已确认 canceled/stopped，仍要求两个确切工作区回收 ACK；
未知停止状态不被接受。修正后的源码 CI 单独记录于收据。
该 CI 首次 Python job 在测试前因 GitHub runner 无法下载已固定的 setup-go archive 而失败；
只重跑该 job，原失败日志和其余三个成功 job 均保留，没有因此改写源码或忽略测试失败。

首次封存索引误将仍在写入的 stdout 文件按空文件计入；原索引保留为诊断，
`artifact-manifest-v2.json` 明确排除该 stdout，745 个产物逐项校验通过。
没有改写原始运行结果、分母或校验值。

## 证据与当前绑定

原始产品/启动证据：`deployments/windows/.local/receipts/v6/`；
实际模型/准入证据：`deployments/docker/.local/receipts/portable-windows-20261015/v6/`；
截图：`output/playwright/portable-windows-20261015-v6/`；测试日志和退出码：
`.scratch/portable-windows-20261015/`。

封存根：`deployments/windows/.local/receipts/portable-windows-20261015/final/`。
权威 745 项索引 SHA-256
`9ccdd245564d0d71e107b577dadcdc57354cbb118a3094281058ee7061491112`；
原产品核验 SHA-256
`81f56b9da7aac6d043df71d57c3892fd04c2b1300a988096fbf41096354acfba`。
603 个构建输入的原文与 hash 保留；当前仅两处 `_test.go` 内容不同，均不参加生产 Go 编译。
Go/Python、配置、运行中加载的指令和依赖以及实际 3,219 个安装文件保持一致。
追加的 delivery/current-binding 保留这两处测试差异，SHA-256 为
`82c1a5bd269e088b8dfd7d8c86e8d2f414ee4b40ac8b21f18401a86df745d3ef`；
新 CI 结果和复核另存 delivery 补充清单，11 个产物逐项校验通过，清单 SHA-256 为
`fc4664357a33861eeb2af6efd32ad13c75e6a233da3bc34eb998a162074f32bc`；原收据不改写。
更多 hash 和独立状态见[收据](portable-windows-2026-10-15.receipt.json)。

双轴最终复核为 Standards 硬违规 0／Smell 0、Spec 未解决项 0／范围扩张 0，
分类 `AI_REVIEWED`，不声明人工复核。
完整发布与后续专项 `NOT_RUN`；该限制不改变本票声明范围的通过结果。
