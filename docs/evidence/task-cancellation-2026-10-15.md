# #12 代码任务取消范围：2026-10-15

工程状态 IMPLEMENTED；VERIFIED 限于 owner 取消、Go 停止边界、结果留存和相应 Windows 产品验证。
进程故障恢复 #13、Memory、完整 Trace、多轮代码任务与完整发布分别验收。

## 原要求与结果

| 原要求 | 实现与验证 |
| --- | --- |
| Go 授权、停止所属资源、隔离其他任务 | owner HTTP CancelRun 经现有 Runner/slot/CLI adapter 写 canonical intent；按实际 lease 归属停止模型 scope、工具与所有子任务。真实容器停止和跨 Runner/子树拒绝分别验证 |
| provider、工具与待审批阶段有界取消 | 真实 provider pending 请求、OpenSandbox 长工具与真实 Write 待审批均经浏览器取消；上游分块响应由真实本地 HTTP 集成验证。无法确认的作用或停止结果保持 unknown |
| 保留批次消耗与计量缺口 | 原 accounting 前缀不变；本轮 5 次预留、3 次结算、2 次已结束 unknown。已停止任务可结束；服务商 1 亿 token 硬上限持续有效 |
| 原仓库、工作区、提案、草稿与事件留存 | 原仓库 HEAD/index/status 和 98 项文件不变；两份工作区各 98 项原文件与真实 Write 候选保留。正式提案通过公开入口生成、取消并重开真实 SQLite 的独立集成验证 |
| 验证后端真实停止 | canonical canceled/stopped 与 native scope accounting、实际 `docker top` 中的 sleep 进程、取消后 owned 容器消失逐项对应；不以 UI 文案替代资源结果 |

取消先持久化 fence，再停止实际 owner 的资源。迟到工具结果仍写 Ledger；远端接受或副作用不确定时不补造成功。
其他 Runner 不能凭相同 WorkerID 或空资源表声明停止，未取得清理归属时保留 canceling/unknown。
资源停止 unknown 会阻止 Submit、Continue、child 新运行和 TaskFinish；已结束调用的 usage unknown 不阻塞独立新调用。
旧请求重放保留原身份，不能停止新的 run。全部子任务先进入 fence，再等待和汇总结果。
待审批阶段从取消 intent 起拒绝批准；queued/active 子任务在 terminal 前保留工作区生命周期 ACK。

## 实际产品分母与边界

每次运行先冻结原十二项场景、源码/配置/指令/依赖、assets、二进制、prompt、model/image pin 与预算。
最终 v4/v5 合并记录为 11 项完整验证、1 项部分代码候选留存，无失败或未运行场景被删掉。
第 4 项原名 `real-write-proposal-before-tool-cancel`，实际保留的是 Write 文件及 canonical `code/modified` 的 diff checksum；
未生成正式 CodeChangeProposal，不能称为真实模型正式提案留存。此部分由下述独立集成补足。

| 阶段 | 原 Session / run | 取消至 durable stopped |
| --- | --- | --- |
| provider pending | `6d8bfbabd06d3b9dec43acd2339dbb40` / `run:3e1aeef9624ff3db0255271a52aa2e6b` | 239 ms |
| OpenSandbox Bash | `b78537b29328e0f4ff79d841d443d0d0` / `run:f6999a52fbeb684b55e68c7f20fc0000` | 2,662 ms |
| Write 等待审批 | `f041905cceb593ddd45896380cc1cd74` / `run:944797eb927b3affa84987243a0bb286` | 309 ms |

三个取消请求 HTTP 202，canonical 分别为 10、47、19 条事件；fence 后没有新工具派发或 run-completed。
provider 本地 transport 停止，远端用量 unknown 保留。生产 provider 当前使用非流式上游响应，
没有真实上游 SSE 证据；Go/Python 对话通道与本地 chunked HTTP 取消集成分别说明其范围。

长工具执行前真实 Write 已获准并完成，`cancellation-note.txt` checksum 为
`eca25e4bc6c2f5f27bee42410d65f5153d849898442916e82308823824dce42d`。
实际 `docker top` 确认 sleep 120 正在运行，Go 取消后该 owned 容器消失，partial 文件 checksum 不变。
Bash 结果保留 `started`、退出码 126、truncated 与 outcome_unknown；明确 stopped，不将它改为正常命令成功。
工作区仍要求核验这个未知作用，属于保留边界；#13 的恢复工作另行处理。

等待审批的 Write 未执行，`cancellation-pending.txt` 不存在，取消后旧批准 HTTP 409。
三个已停止任务结束均 HTTP 200，ActiveCalls 为 0，任务槽释放；不会清空 unknown 用量。
同一浏览器 storage 的 provider/tool/approval 草稿重载验证通过。v5 使用新的浏览器 context，local storage 独立；
只验证其新草稿重载，不宣称浏览器新 profile 共享旧草稿。新版 UI 读回旧工具 history、written 和 partial output。

原 chi 仓库固定 `9b9fb55def404397748a9fc7e044efe9db1d618e`；98 项文件、HEAD、index 和 status 不变。
两份工作区各 98 项原文件不变，54 个旧会话 event count 与 55 个既有容器 ID/image/name/state 不变。
测试结束后本轮 Go/Python owned PID 均不再存在，控制容器恢复原 exited 状态；没有删除其他容器、volume 或数据。
v5 driver 退出 0、page errors 为 0；被显式终止的 Windows Go/Python 子进程退出值为 1，不能称为自然成功退出。

## 验证与失败保留

| 命令 / 范围 | 当轮结果 |
| --- | --- |
| `go test ./... -count=1 -json` | 1,697 pass / 78 skip / 0 fail test events；45 包 pass / 13 无测试，退出 0；在最后 unknown fence 之前 |
| `go test ./internal/session ./internal/admission ./internal/handler ./cmd/server ./internal/cli -count=1 -json` | 679 pass / 7 skip / 0 fail，退出 0；覆盖资源 unknown 的新消息/结束门禁 |
| `go test ./internal/session -count=1 -json` | 246 pass / 1 skip / 0 fail，退出 0；覆盖最后 continuation fence |
| 三包取消定向 `go test -race` | 35 pass / 0 fail，退出 0；旧游标 fixture 修复后 |
| `TestCancellationRetainsFormalProposalAfterLedgerReopen` | 退出 0；公开 GenerateProposal/CancelRun/Inspect、重开真实 SQLite、原事件前缀及完整提案留存；外部测试 runtime 为既有 fixture |
| `go vet ./...` | 最终退出 0 |
| 项目 `.venv` 的 `python -m pytest -q` | 2,513 pass / 17 skip / 3 warnings，退出 0；Python 实现未改 |
| frontend `npm test` / `npm run build` | 12 pass / 0 fail；类型检查和构建退出 0 |
| Chromium、生产 Go/Python、BeeAPI 与 pinned OpenSandbox | provider/tool/approval 三阶段实际取消及上述保护核验通过；完整发布未运行 |

保留所有 RED/GREEN、构建失败、旧契约 fixture 失败及中间完整运行，完整退出码和 checksum 见脱敏收据。
首轮完整 Go 为 1,689 pass / 78 skip / 5 fail test events，3 个顶层旧契约相关用例导致 Session 包失败；修复后完整运行通过。
第一次最终三包 race 有 34 pass / 1 fail：retained-resource fixture 在 completed progress 后持旧 cursor，
现以同 nonce 有界读回并仅重试 ErrSequenceConflict；资源归属断言不变。
资源 unknown 的 Submit/Finish 和 continuation 回归均先实际失败，再分别通过。

产品 v1 无付费调用，stdin 提前关闭；v2 provider 取消与草稿验证完成；v3 仅准备环境。
v4 的准备 URL、正式提案按钮、部分观察与结束等待失败保留；最终 unhandled observer timeout 使 driver 退出 1，
未留下 v4 stop receipt，随后通过 owned PID 和 native Ledger 实际读回核验。没有重写原失败产物。
v5 首次等待错误的 title locator 和响应解包/隐藏输出断言失败均保留；沿用已创建会话与同一次模型请求继续核验。
原 12 项场景分母保留，正式提案生成尝试的 timeout 不被算作正式提案成功。

全部实际调用共 5 次预留、3 次 settlement、2 次 ended unknown，已确认增加 34,615 tokens。
批次仍为 `02cfc8d9c93975b3ea63f27cc0a3331d`，已确认从 2,399,697 到 2,434,312，
原 unknown 预留从 2,806,024,576 到 3,006,024,576，费用未知。预留不是已消费量或新授权预算；
服务商 100,000,000-token 硬上限继续生效。所有旧 accounting 记录前缀保持相同。

## 源码绑定与审查

base SHA `f227cffd7fdab8e0d92e45db7eb251fcaa938373` 加本轮实现，每次冻结 920 项源码/配置/指令/依赖。
v4 后仅增加资源 unknown 的准入判定；其影响经新鲜公开拒绝回归、v5 实际待审批取消、旧取消重放、旧历史和任务结束核验。
v5 的所有运行实现、assets 与当前指纹相同；之后仅调整取消测试的 cursor fixture 并补正式提案留存回归。
原冻结指纹、实际 binary checksum、后续测试指纹都保留，不改写旧收据。
纯证据文档与 Goal 的更新不参与上述运行。

Standards：AI_REVIEWED，未解决硬违规 0、独立 Smell 0。
Spec：AI_REVIEWED，源码缺陷 0、缺失/部分实现 0、范围扩张 0；正式提案证据以独立集成补足。
两轴审查不等于 HUMAN_REVIEWED。本票范围无未解决 blocker；完整上游 SSE、故障恢复及发布各按其范围继续。

原始产物位于 `deployments/docker/.local/receipts/task-cancel-20261015/`，测试日志位于
`.scratch/task-cancel-20261015/`。它们是不可变审计产物；运行事实仅由 Session Ledger 写入。
完整分母、全部测试尝试、失败与 checksum 见[脱敏收据](task-cancellation-2026-10-15.receipt.json)。
