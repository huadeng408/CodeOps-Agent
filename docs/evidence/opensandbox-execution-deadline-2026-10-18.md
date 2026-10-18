# OpenSandbox 长命令与停止核验

工程状态 `IMPLEMENTED`。固定点 `e0001c926e2f696baa061c5cffc3372aa137d978`
加本轮运行时、profile 和 Go Session／Gateway 修改。范围是命令期限和显式结束恢复；
Trace 分析器、未知副作用的验收要求及原始收据保持。

## 真实失败及边界

新的 Go 原任务实际有十二次 canonical 运行：七次完成、四次取消、一次失败，
不满足十轮有意义的完成对话。最后一次模型请求 300 秒的 `make test`，但旧
Runner、execd 与控制服务 lease 固定为 120 秒。输出中 router race 已完成，
middleware 尚未完成；结果为 exit 126、输出截断、执行 unknown、停止未确认。
不能据此宣称全项目测试通过。

实际浏览器“结束代码任务”返回 HTTP 409；原生模型调用全部结束，但任务槽仍占用。
形式化 v2 读回退出 3、`BLOCKED`，没有把采集拒绝改为通过。已有二十条同任务
经验经原生来源 checksum 与 Memory 投影逐项核对；两次反思 commit-blocked、
全部取消和失败保留。原工作副本／index 未应用这次候选修改。

运行证据保存在本机忽略目录 `deployments/docker/.local/receipts/go-original-20261018/go/v1/`。
原失败、旧运行包和十二轮分母保持独立，不与早先成功探针拼成 #17 的完成证据。

## 实现

- `internal/sandbox/opensandbox.go`：命令 context／execd 上限为十分钟，lease 为
  630 秒；较短调用方 context 仍有效。执行前校验控制镜像的不可变期限标记，
  同步 Python 创建策略与 profile 生成器，旧控制镜像在创建前拒绝。
- 同一 Runner 的 `ConfirmStopped` 只读核对固定服务和保留的三段 runtime identity。
  原物理 ID、逻辑名及三类标签资源必须全部不存在，生命周期 API 必须返回 404；
  任何资源仍在、重绑、daemon／API 不可用或无效身份都保留 unknown，不删除资源。
- `internal/session/runtime_stop.go`：只为同 actor 的终态 run 和先于原结果的唯一
  同 run／call／name 派发追加版本化停止证明。证明绑定原结果 event ID／checksum／
  runtime ID，重放时核验当时的事件前缀；CAS 有界重读，重复结束幂等。
- Gateway 仅在显式 Finish 时调用固定 Runner。`ModelTaskIdle` 可用该证明核对物理
  停止并释放任务槽；原未知命令不改写，工作区续行、提案、Memory 和 Trace 的
  unknown 门槛不变。缺观察器的旧调用方继续拒绝未确认停止。

没有新增依赖、协议或前端布局。旧 pin、历史事件与用户 AGENTS.md 保留。

## 验证范围

| 检查 | 完整结果 |
| --- | --- |
| Runner／不可变策略 RED → GREEN | 期限与生成配置原为 120 秒；修复后验证 600 秒命令、630 秒 lease、短调用方取消、旧镜像创建前拒绝和旧身份只读恢复，退出 0 |
| 物理停止拒绝边界 | 物理 task、重绑名、漏 operation 标签的 egress、生命周期仍在／不可用、daemon 不可用和无效身份七项全部拒绝，退出 0 |
| 原证明来源 RED → GREEN | 缺 canonical run、错误工具派发在修复前被接受，退出 1；写入／重放的 actor、证明前缀终态、唯一前置派发及有界 CAS 均验证 |
| `go test ./... -count=1 -p 2 -json` | Go 1.26；1,824 PASS／81 SKIP／0 FAIL，47 包通过、13 无测试，退出 0。限制 package 并发以沿用已记录的 Windows 启动验证方式；测试内容和超时未放宽 |
| 最终相关 race | `go test -race ./internal/session ./internal/admission -run '^Test(RuntimeStopConfirmationKeepsIdentityTerminalAndCASFences\|GatewayRuntimeStopProofRejectsUnboundResults\|GatewayExplicitFinishConfirmsStoppedRuntimeWithoutChangingUnknownOutcome)$' -count=1 -json`；19 PASS／0 FAIL/SKIP，退出 0 |
| `go vet ./...` | 退出 0 |
| 差异检查 | `git diff --check`、`git diff --cached --check` 均退出 0 |

全量之后仅细化一个测试文件：六个损坏引用负例改用真实 SessionRunner 生成同 actor
终态 run，新增合法证明的正例，避免全被缺 run 的前置错误遮蔽。临时删除 checksum
guard 后该负例确实失败（退出 1），随后恢复；最终十九项 race 包含全部相关测试。
生产源码与全量运行时指纹相同，完整分母分别保留，不把新增正例并入上一次全量数量。
独立 Standards／Spec 的冻结差异和复核收据单独保存，状态使用 `AI_REVIEWED`。

本次单测／集成／相关 race 范围 `VERIFIED`；81 项跳过保留。本记录不宣称真实
长命令／浏览器恢复已经通过。新构建还需验证实际停止恢复和超过 120 秒的执行；
#17、#19 与完整发布分别验收，不由该修复自动关闭。Python 编排与 frontend 未修改，
其无关全量和完整发布未运行；Python 控制策略在重建部署后另验真实接口。

TDD 原失败均保留：120／120000 期限、profile 最大值 120、Finish 的停止未确认冲突、
漏查无 operation 标签的 egress、无 canonical run 的伪证明、错误工具派发被接受，
以及旧控制镜像仍可新执行。编译／fixture 准备错误的较早失败也保留，不计为行为 RED。

源码验证、审阅和产物清单位于本机忽略目录
`.scratch/opensandbox-execution-deadline-20261018/`。源码验证摘要
`source-validation-summary.json` 的 SHA-256 为
`4e6df745269da3a73acdc004970f859b6106678396d9420f1e4e0d8b8a622a1a`。
实际 runtime 和发布当前未验收，不能用 HTTP fixture、单测或 CI 代替其证据。
