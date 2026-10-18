# Go 正常任务与完整 Trace：v5 产品验收

对应 [#17](https://github.com/huadeng408/CodeOps-Agent/issues/17)。本轮使用 Windows
一键产品、实际浏览器、Go Harness、Python 编排、真实 grok-4.6 和固定 OpenSandbox
环境完成公开 Go 修复。功能、项目测试、提案应用、同任务 Memory 及用户已批准的
`product-integrity-v2` 完整 Trace 均为本票范围 `VERIFIED`。
[#19](https://github.com/huadeng408/CodeOps-Agent/issues/19) 的首批完整矩阵及整个发布仍单独验收。

## 输入与运行绑定

- 原产品源码：`01977c2872da7cea4bb3564cdc2531389596e7ea`；使用 v5 便携包。
  本轮没有修改 CodeOps-Agent 运行源码、分析器或门禁。
- 包 manifest SHA-256：`72981044fe9885e0bc7bc56704562121b804be8370a9051ae6e6e288e5800957`；
  server：`2063c0fe972b6bb3dd78f703ada8ea88fcff6afd8da09e792cc208dee5713319`。
  本轮结束时重新核对 612 份源码及 3,219 份运行文件，全部一致。
- 用户任务：go-chi/chi `9b9fb55def404397748a9fc7e044efe9db1d618e` 的 HTTP 压缩协商修复。
  完整编码名匹配、显式零质量值拒绝；保留 API、Go 1.20 约定和无新增依赖。
  两项公开任务及场景在调用前冻结，未引入上游修复补丁或参考答案。
- Go 任务镜像：`sha256:c35ffc50ff2d4e97ea90f973b29836945912397204592a0b06a0b6fbbba59b58`。
  模型为 grok-4.6；服务商没有提供不可变模型 revision，未虚构该 pin。
- 本轮公共输入 SHA-256：`cb0eca2373a931e01731288c4ca9853ad8256ccb5de36680efac37e310a85b81`。
  原 99 文件副本包含既有 dirty/new 源文件；原 Python 副本及更早 Go 副本保持原样。

## 实际执行与完整分母

| 范围 | 本轮结果 |
| --- | --- |
| 原基线 | 实际右侧卡片运行 `go test ./... -count=1`，两个包通过，退出 0、未截断、停止已确认 |
| 浏览器对话 | 14 个 canonical run：12 完成、2 取消且均为 `stopped`；取消未计入完整轮次 |
| TDD | 完整 token、字面零及小数零三个切片先取得真实 RED，再取得 GREEN；保留全部 RED 输出 |
| 最终定向测试 | `go test -race -count=1 -v -run "TestCompressor" ./middleware`：28 条父子 PASS，退出 0，完整输出、停止已确认 |
| 最终完整项目测试 | `make test`：两个包、86 个顶层／152 条父子 PASS，0 FAIL、0 SKIP，退出 0；入口保留 race |
| 冻结提案测试 | 实际产品运行 `go test ./... -count=1`：两个包通过，退出 0、未截断、停止已确认 |
| 工具 | 113 次请求、113 个结果，108 次派发；5 次拒绝全部保留，unknown 副作用为 0 |
| 模型调用 | 102 项准入／102 项结算：90 foreground、12 memory_reflection；本任务 unknown 为 0 |
| Memory | 新账号初始为空；Go 校验的同用户、同仓库投影最终为 53 条 Experience，来源与 Ledger/CAS 均核对 |
| CodeOps 源码单测／集成 | 本轮未重跑，运行源码未变；原构建源码 CI 与修复证据见 [恢复父链记录](memory-recovery-trace-2026-10-19.md)，不代替本轮产品验证 |
| 发布 | 本轮未运行完整发布验收，未声明发布合格 |

最终测试前曾完成一次 90 顶层／155 父子 PASS 的完整项目测试，随后按独立审查建议
将 14 个新增回归输入归入既有测试表，共 20 个表项，去除四份重复 fixture。
原有输入、响应正文和响应头断言均保留；最终 86／152 分母对应整理后的候选。
两次完整测试均保留各自源码绑定和完整日志，不合并或覆盖分母。

测试输出在模型界面的截断标记保留；完整项目输出经 Go 保存的 redacted spill 取回并核对摘要。
最终 `make test` 原始完整输出 SHA-256：
`ee6ebfc1325bdc26243f57ed8337db379af894d3b222464ab5c7d9e4397b7814`。
只读 `gofmt -d` 的完整旧候选输出也已核对，差异为 CRLF→LF；未用内联截断子集宣称完整。

已知失败包括三次精确编辑匹配拒绝、三次预期 RED、两次并发工作区准入拒绝和五次工具拒绝。
反复请求已通过的 vet，以及测试整理时重复不匹配的编辑，分别经实际 UI 取消并继续；原结果保留。
准备租约过期时，正常浏览器续行触发 Go 核验和同一工作区续租；前端恢复一次 409 游标冲突，
Ledger 仅新增一个完整只读核对轮次。未重建副本或从主机修改任务文件。
观察器的缓冲区、断言、准备和控件刷新错误另存原始记录，未计作完成开发轮次。

## 提案与原副本保护

实际 Go Write/Edit 生成两项修改：`middleware/compress.go` 与 `middleware/compress_test.go`。
独立 Standards／Spec 初审为硬性违规 0、Spec 0，以及一项 P3 测试重复建议；处理后终审两轴均 0，
状态为 `AI_REVIEWED`。没有标作人工逐条审阅。

终审快照 manifest SHA-256：`9c80d8d11a271f42a7448fdaabdb663f3dd12f622147b9e496753b3f7ac86bfb`。
实际产品 diff 与该快照一致；测试输入、最终状态及只读归档绑定后，按已授权的测试副本验收范围
点击“确认应用这 2 项修改”，HTTP 200、两项均为 `applied`。

- 提案最终状态／测试输入：`b3373c0c6f60a602772cd20cbf257ca8d965205bddeede437695dcbdafc35018`。
- 只读测试归档：`c8085012867fed547af7c8f6220595bbcf61744c5d313f969331f2658c6cd2b2`。
- 应用后源码：`979ab0ecd391545eb746ddfb91b436621d10e5a1cb7a123b2789338791dcf136`；
  应用后测试：`6e4ad85db38952be37295035e544cb4c1db7852123fafef5da0b14f2fee0b8cd`。

应用前 234 个原文件及三份 index 全部保持原样；应用后仅上述两文件为批准的新摘要，
其余 232 文件和三份 index 不变。用户既有 `.gitignore`、新源文件及主仓库 `AGENTS.md` 原样保留。

## 完整 Trace、用量与停止

实际“结束代码任务”返回 HTTP 200，活跃调用为 0、TaskID 为空。
未改动的 `python -m eval.product_trace` 使用原 runtime manifest、三份原生导出、冻结输入、
Go 1.26.0 原生审计器及全部 14 个 canonical run，从真实 Phoenix `default` 项目读回：

- 14 棵树、449 个已结束 span；102 项模型调用、113 个工具请求／108 次派发全部对账。
- `product-integrity-v2` 为 PASS，`capture_state=complete`、`status=VERIFIED`，退出 0；
  missing／problems 均为空，本任务没有未结算调用。
- 11 个 ERROR span、所有拒绝和两次取消完整保留；同数据的 `legacy_report` 仍为 FAIL。
  已知执行失败没有被改写为成功，也没有当作采集缺失。

正式 receipt SHA-256：`b84fe10a4bb4a6119e2931a0989a1af626e2498c720fff9c8f411a983a1bcb41`；
后端完整读回：`06ad44dc8af180f4c40ee6c5c300ab9bfd12312a505763f997d406d7e660228f`。

本任务确认新增 4,924,748 tokens；同一 100,000,000-token 服务商硬上限批次累计确认 51,004,063。
历史 4,806,024,576 tokens 预留保持原样，历史 unknown 及未知费用保留；未虚构零消耗或金额。

任务结束后已停止所属 Windows Job 中的产品 Go/Python 进程并关闭验收浏览器，
两个既有 Compose `localcode` 控制服务恢复原 `exited` 状态。没有删除既有容器、volume、索引或业务数据。
停止前后 Session 1493 个事件及预算一致；五批旧 seal 共 1,529 项产物全部保持原摘要。
另保留交互观察进程供同一 goal 的 #19 使用，未运行产品或模型任务。

## 产物入口与边界

本地只读派生产物入口：

- `.scratch/go-original-v5-20261019/`：计划、全部观察错误、两轴审查、完整测试、正式 Trace 及摘要核对。
- `deployments/docker/.local/profiles/go-original-v5-20261019-v2/`：冻结输入和浏览器驱动。
- `deployments/docker/.local/receipts/go-original-v5-20261019-v2/go/v1/`：实际 UI 响应、原生 Session／admission 导出、Memory、提案应用及进程资源恢复。
- `output/playwright/go-original-v5-20261019-v2/go/v1/`：逐轮、提案、批准、取消与结束截图。

这些文件是 Ledger 的只读投影和验证产物；Ledger 继续是唯一可写事实源。
旧 502、unknown、失败轮次父链和严格 Trace 失败记录均保留，本轮不改写它们。
#19 五类恢复与兼容矩阵、父 milestone 和完整发布均未因本票通过而自动合格。
