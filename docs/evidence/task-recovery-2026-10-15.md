# #13 任务故障与旧 Session 重开：2026-10-15

工程状态 IMPLEMENTED；VERIFIED 限于 Windows 产品的任务进程故障、Go/Python 重启及同一旧 Session 恢复。
#14 空新会话 Memory recall、#15 完整 Trace、#19 完整故障矩阵和发布分别验收。

## 原要求与结果

| 原要求 | 实现与证据 |
| --- | --- |
| 实际任务故障和 Go/Python 重启 | 浏览器批准真实 Write/Bash 后对确切 owned 沙箱注入 SIGKILL；分别终止持有模型请求的 Go、处于空闲状态的 Python，使用原运行配置、Ledger、身份及工作区重启 |
| Ledger、owner、surface、plan、lease、checkpoint | 原事件前缀和 native Go hash chain 保留；同一 run/checkpoint 在 Go 重启后 attempt 2 完成；同一工作区与原文件保持。空 plan 投影保持为空；13 个 LangGraph checkpoint 父链、step 0–12 和 Go seq/checksum/surface 绑定核验 |
| 普通新 turn 与严格 Continuation | 跨 Session checkpoint 404、过期 cursor 409；合法浏览器 Continuation 202 并完成，随后普通新消息 202 并生成新 checkpoint，均在原 Session |
| 已确认作用不重放，未知结果保留 | 实际 Write 的 canonical result 保持一次，marker checksum 不变；被杀任务保留 partial 文件，unknown workspace 拒绝续行。原 commit_pending/unknown 准入未放宽 |
| 历史恢复与 Memory recall 分开 | 全部重开和续行使用原 Session，未当作空新会话 Memory recall，也未升级为完整生产故障矩阵 |

发现并修复的缺口：旧模型 holder 已死亡、SQLite 已重开时，原任务仍因 ActiveCalls 被拒绝。
现复用既有进程身份核验和 `call_unknown`/`scopes_recovered` 事实，在 TaskWorkspaces 验证 owner 后恢复同一任务。
Go 核对 host、平台、PID 和启动身份；活进程或无法确认的身份继续拒绝。
原 TaskID/owner、用量及预留保持，恢复不会关闭任务或清零批次。
既有 FinishTask 同步使用该方法，其结束契约保持。

## 实际产品分母

运行前冻结十二项场景、921 项源码/配置/指令/依赖、assets、二进制、prompt、预算、model/image pin。
十二项均完成，所有 observer 失败另行保留，没有删掉原分母。
使用生产 Go/Python、BeeAPI `grok-4.6`、pinned OpenSandbox 和真实 Chromium；外部 fixture 回归分别记录。
现有 identity helper 注入测试身份，实际 middleware 对未鉴权请求返回 401；不宣称注册/登录验收。

任务故障 Session 为 `93ef9ee713046a2171dc7ec4ad515251`。
真实模型 Write 已批准完成，随后 Bash 执行 `sleep 120`，实际 `docker top` 确认进程正在运行。
核对容器的新身份、任务 image、Compose project `localcode` 和工作区挂载后，只杀该 owned 容器，退出 137。
Go 保留 failed、partial 文件和 unknown 作用；工作区 blocked，继续请求 HTTP 409，没有重放副作用。
确认停止后结束任务 HTTP 200，用量记录保留。

重启 Session 为 `a468a43404e9d9afcb1e39d1873f86e1`。
先完成真实 Write，`restart-note.txt` checksum 为
`dca98bcc34e0ff850b51374712e5ee86170e224baf3f89986847a1bd1d4b18611`。
第二个正常 turn 的模型调用正在执行时，终止 Go PID 35516，重启为 29028。
原 run `run:46bc97d6d970b1666af97f8e5477a8ad` 与 checkpoint
`fa955d2f10175eb6ee59fb7f28687998ad2648a56672f2fc9b2779afc0ad3ce4` 保持，attempt 2 实际 completed。
Ledger 中新增一次 scopes_recovered；旧请求用量 unknown 和原预留保留，原 TaskID 不变。

随后终止 Python 根 PID 12264 及其受管子进程，Go health 明确先 detached；重启为 36516 后重新 attached。
原 Session 的 owner、run/checkpoint、工作区、事件前缀和 marker 不变，浏览器草稿保留。
在原 Session 暂停后，外来 checkpoint 返回 404，旧 cursor 返回 409，未产生新模型预留。
实际点击“继续历史任务”返回 202 并完成；随后普通新 turn 也返回 202 并 completed，使用新的自动 checkpoint。
完整重启 Session 有 81 条 canonical events，真实 Write result 仍只有一条。

原 chi 仓库固定 `9b9fb55def404397748a9fc7e044efe9db1d618e`。
原 HEAD/index/status 与 98 项文件不变；两份任务工作区各 98 项原文件保持，partial marker 均留存。
57 个旧 Session event count 和 55 个既有容器的 ID/image/name/state 不变。
本轮四个 Go/Python 根 PID 最终均不再存在，driver 退出 0、page errors 为 0；控制容器恢复原 exited 状态。
进程被故障注入或测试清理显式终止，退出 1，不称为自然成功退出。没有删除其他容器、volume 或数据。

LangGraph 审计只读已有 projection，Go Ledger 仍是事实源。
13 个 checkpoint 的 Saver step 从 0 到 12 连续，parent 链完整；每条 metadata 的 Ledger seq/checksum 和 surface hash 与 Go 原事实一致。
单次请求的 `checkpoint_revision` 局部计数与 Saver step 分别记录，不混称为跨请求的同一计数器。
没有 plan/todo 指令，故空 canonical plan 投影仍为空；不伪造计划以满足演示。

## 测试、失败与计量

| 命令 / 范围 | 当轮结果 |
| --- | --- |
| Session/server recovery 定向测试 | 34 pass / 1 skip / 0 fail，退出 0 |
| 新 native Go holder + SQLite 重开回归 | RED 实际失败：进程已死仍拒绝原任务；修复后与活跃 reflection 拒绝共 2 pass，退出 0 |
| server/admission scope/owner/进程边界 | 16 pass / 3 skip / 0 fail，退出 0 |
| 上述 server 两项 `go test -race` | 2 pass / 0 fail，退出 0 |
| `go test ./... -count=1 -json` | 1,700 pass / 79 skip / 0 fail test events；退出 0 |
| `go vet ./...` | 退出 0 |
| 生产 Go/Python、真实 API、OpenSandbox 与 Chromium | 十二项实际产品分母完成，driver 退出 0 |

Python、protobuf、依赖和前端实现未改，未重复其完整单测/构建；真实 Python 终止/重启与 Go/Python 协议恢复在本轮产品中验证。
完整发布和后续完整故障矩阵未运行。

一轮产品 driver 共 15 条观察/操作命令，首条创建 Session 后的 title 正则同时匹配 sidebar 与主标题，发生 strict-mode 错误。
原失败保留，沿用已创建的 Session 继续，未重复创建或重发模型请求。没有其他观察失败。
原定向 RED、GREEN 与全部八次命令退出码和日志 checksum 保留。

同一批次共 12 次实际模型预留、6 次结算、6 次 ended usage unknown，已确认增加 81,320 tokens。
批次 `02cfc8d9c93975b3ea63f27cc0a3331d` 从已确认 2,434,312 到 2,515,632，
原未知预留从 3,006,024,576 到 3,606,024,576；旧 accounting 前缀不变，费用未知。
预留不代表已消费量或新增预算；服务商 100,000,000-token 硬上限持续生效。
最终 ActiveCalls 为 0、TaskID 为空；取消、故障及重启未重置额度。

## 绑定与审查

base SHA `cebb68940232aec626e62563d66b88ee7b72ac81` 加本轮实现。
冻结的 921 项源码/配置/指令/依赖和全部前端 assets 与当前指纹相同；实际二进制 SHA-256 为
`beed05dd43f8b21866dcb39bc1dc36585a400da2025cb44bd3dd9aead7832127`。
后续纯证据与 Goal 更新不参与上述运行。

Standards：AI_REVIEWED，硬违规 0、独立 Smell 0；Spec：AI_REVIEWED，源码缺陷 0、缺失/部分实现 0、范围扩张 0。
两轴不等于 HUMAN_REVIEWED；实际进程与恢复证据由主流程逐项核对。本票声明范围无未解决 blocker。

原始只读产物在 `deployments/docker/.local/receipts/task-recovery-20261015/`，测试日志在
`.scratch/task-recovery-20261015/`。场景、全部尝试、当前源码绑定与 checksum 见[脱敏收据](task-recovery-2026-10-15.receipt.json)。
