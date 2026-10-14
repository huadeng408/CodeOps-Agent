# #8 单文件提案决策：2026-10-14

工程状态 IMPLEMENTED；VERIFIED 范围是 Windows 本机单用户、一个既有文件的确认／拒绝，
共享 Go 校验、持久 intent／outcome 和恰好一次请求。新建、删除、重命名及多文件
属于 #9，完整崩溃恢复与对账矩阵属于 #10；不据此声明完整发布合格。

## 行为与边界

确认／拒绝入口仅接受 Session、proposal／request identity、决定和游标，
owner 取自鉴权。HTTP 不接受仓库根或文件内容；Go 从已验证 Ledger 重建内容，
核对原提案的 owner、基线、存在状态、模式、冻结测试输入和已知工具结果。
审批请求须来自本机配置批准的 owner／仓库，模型仍只修改隔离副本。

应用先独立验证完整基线 checksum 与当前 HEAD／index／文件清单；固定祖先目录、
Git metadata、index 和其他输入文件，再独占打开目标既有文件并核对内容和模式。
意图 CAS 成功后再次核对授权／空闲状态，才写入；以同一句柄同步、读回并确认结果。
保留既有空文件，原文件缺失时拒绝，不用空字符串推断存在状态。
已有 dirty 内容可作为基线，原 index 和无关改动保留。

本票采用 Windows 独占既有文件句柄，避免检查后覆盖编辑器的并发写入。
部分写入、同步／关闭失败或 outcome 无法落账时保留 unknown／pending intent，
不声明写入原子事务或崩溃后自动恢复；拒绝重复写入及后续模型工具／任务结束，
交由后续对账恢复。其他平台的应用明确不可用，源码仍可编译与测试。

同一 request identity 与相同决定回读原结果，忽略旧游标且不访问写入路径；
更换决定／提案或给已决提案另建请求都被拒绝。拒绝提案不写原文件，
隔离结果、diff 和测试仍可查看。右侧卡片沿用已选布局，按钮显示服务端终态；
网络失败／unknown 不先显示成功，也不自动重试写入。
当前 CLI 没有提案应用入口，不增加第二个执行器；适用调用方共用 TaskWorkspaces 边界。

## 原验收映射

| 原要求 | 验收接缝 |
| --- | --- |
| owner／权限／根／存在／基线；未批准拒绝 | Go Decision、HTTP 身份和未知字段拒绝、intent 后授权撤回、原基线／目标／硬链接拒绝 |
| empty 与 missing、dirty baseline、index／无关内容保留 | 真实 Windows Manager 文件回归；生产浏览器应用前后 HEAD／index／原文件核对 |
| 重放、Ledger intent／outcome、CAS 后才成功 | Go 重建 Workbench／TaskWorkspaces 后重放、相同 identity 不同决定拒绝、intent CAS 拒绝、outcome 不可用保留 unknown；实际浏览器重放 |
| 拒绝保留原副本和隔离结果；应用与 diff／测试对应 | 实际浏览器确认／拒绝；生产 Go 核对已测试内容及隔离文件，右侧刷新终态 |
| 并发／unknown 不覆盖或盲重试，共同校验 | 独占句柄拒绝并发 writer、内容／index 变化拒绝；未知结果重开不再写入并阻断工具 |

## 验证范围

单测／集成：当前定向 Go 20 pass／0 skip／0 fail，退出 0；完整 Go 为
1,601 pass／74 skip／0 fail test events，58 包中 45 pass／13 无测试 skip，退出 0。
`go vet ./...` 退出 0。前端单测 12 pass／0 skip／0 fail，构建与类型检查退出 0。

产品接缝：实际生产 Go HTTP、Ledger、文件系统、已批准分组 OpenSandbox 与 Chromium。
外部模型 HTTP／编排 gRPC 是 fixture，真实 provider 调用 0；该范围验证用户决策和应用，
原真实模型生成提案证据仍见 [#7](proposal-closeout-2026-10-14.md)，不新增付费模型门禁。
两项初轮浏览器测试及最终输入冻结后的两项测试分别保留，不合并为一次运行。
四项顶层测试均 pass／0 skip／0 fail，每项 5／5 浏览器检查通过；各命令退出 0。
最终两项冻结源码与前端产物指纹逐项核对相同，源码追溯为
`d96b1f03595853fb407cea039872b237ba22e77a` 加本轮实现；不以父提交 SHA 代替实际运行输入。

| 当前命令 | artifact／结果 |
| --- | --- |
| `go test ./internal/session ./internal/handler ./internal/worktree -run 'TestProposal\|TestApplySingleFile' -count=1 -json` | `application-final-targeted-v2.jsonl`；20 pass；退出 0 |
| `go test ./... -count=1 -json` | `application-go-full-final.jsonl`；1,601 pass／74 skip／0 fail；退出 0 |
| `go vet ./...` | `application-go-vet-final.log`；退出 0 |
| `npm test`、`npm run build`（frontend） | `application-frontend-test.log`／`application-build.log`；12 pass／构建通过；退出 0 |
| `node tests/taskProposal.browser.mjs`（frontend） | `output/playwright/task-proposal-1791973389050/receipt.json`；6／6 fixture 检查通过；退出 0 |
| `.venv/Scripts/python.exe .scratch/proposal-closeout-20261014/run_decisions.py approve final` | 生产 Go／OpenSandbox／浏览器确认；1 test、5 checks pass；退出 0 |
| `.venv/Scripts/python.exe .scratch/proposal-closeout-20261014/run_decisions.py reject final` | 生产 Go／OpenSandbox／浏览器拒绝；1 test、5 checks pass；退出 0 |

工程日志在 `.scratch/proposal-closeout-20261014/`，决策原始事实、截图和输入清单在
`deployments/docker/.local/receipts/proposal-application-20261014/` 的四个独立运行目录。
[脱敏收据](proposal-application-2026-10-14.receipt.json)，SHA-256
`20482c5208de666255499b43d37982e6b9bbff342b162940dc366749f0f70868`。
运行和源码证据分别保存，不声称 fixture 执行了新的真实模型调用。

Standards／Spec 当前源码审阅各 0 项，审查为 AI_REVIEWED。
最终审阅纠正了“跨进程重放”的表述：本轮重建 Go 模块投影和回读持久 Ledger，
浏览器重放沿用同一 Go 服务，没有验证 OS 进程重启。本票原要求的同 identity 重放通过。
Python 源码未变，完整 Python／Memory／Trace／十轮／发布未运行。
所有红阶段、辅助编译／格式化错误、首次完整回归的失败均保留；最终通过不覆盖原失败。
首次完整 Go 的分母是 1,412 pass／74 skip／0 fail test events，但一个包因测试辅助类型
引用错误未构建：44 包 pass／13 无测试 skip／1 fail，命令退出 1；首次 vet 同样退出 1。
修复测试引用后上述定向、完整 Go 和 vet 重跑通过，不将首轮构建失败藏在通过数量中。
