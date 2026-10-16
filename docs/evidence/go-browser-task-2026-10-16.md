# Go 真实多轮任务：修复应用与剩余范围

工程状态：`IMPLEMENTED`。公开 Go 修复已完成十轮有意义的真实浏览器对话、项目测试、提案复核和实际批准应用；#17 的 Memory／成功 Trace 范围未通过，票保持打开。完整发布未运行。

## 冻结与运行边界

任务为 [chi](https://github.com/go-chi/chi) 的 HTTP 压缩协商修复：完整编码 token 匹配、明确 `q=0` 拒绝、保留公共 API 和 `SetEncoder` 优先级，补充回归并审查提案。任务 revision 为 `9b9fb55def404397748a9fc7e044efe9db1d618e`；输入没有参考答案或 patch，未读取上游修复。

共用两任务 freeze SHA-256 为 `237f8aa7882d0a13c3d3e9ed131a1a835ec06c86c30410d06b3815be41af63c8`。沿用编译源码 `ff50d0ea703d3dbe3f9b64bfb1247cdd3a0d3b67`、同一便携产品、`grok-4.6`、固定镜像、Go Ledger／OpenSandbox 和服务商 100,000,000-token 硬上限。执行前及应用后核对 610 个运行／验证文件指纹；后续 CodeOps 修复不改写本次收据。

所有任务代码执行均经产品 Go Runner；宿主仅读取证据、复核 diff 和检查保护指纹。原工作副本含已冻结的 `.gitignore` 脏改动及新源文件；已批准的 Python 结果作为保护基线保留。

## 各范围结果

| 范围 | 实际结果 |
| --- | --- |
| 完整对话分母 | 尝试 14 轮：10 轮有意义且完成，3 轮失败，1 轮取消；全部原生 run 和失败输出保留 |
| 回归与实现 | 先观察到新增三例 RED；模型经 Go 形成 11 次非空修改，最终只涉及 `middleware/compress.go` 和 `middleware/compress_test.go` |
| 功能覆盖 | 30 个协商子例、4 个自定义编码 HTTP 子例，另保留原有压缩与通配路径；自定义编码、替换和优先级均有实际测试 |
| 正常完整测试 | 最终 `make test` 退出 0：88 个顶层测试、174 个父／子测试 PASS 记录，0 FAIL、0 SKIP；两个 package 均通过。`go vet ./...` 退出 0 |
| 提案测试 | Go 冻结候选后实际运行 `go test ./... -count=1`，退出 0、停止确认、输出未截断；两个 package 通过，与模型测试分别记录 |
| 独立复核 | 固定脏基线／最终候选绑定，Standards 0 项、Spec 0 项；真实第十四轮另完成技术复核，分类为 `AI_REVIEWED` |
| 浏览器应用 | 两项完整 UI diff 与 DTO、前后 checksum、模式 438 和候选相同；实际点击确认后 Go 应用两项，原生应用事实可核对 |
| 原副本保护 | 两项获准修改应用成功；其余 130 个原文件、两份 index 和 610 个 CodeOps 运行指纹保持相同 |
| Memory | 13 个 commit-blocked 事件；取消 run 未被轨迹投影识别，后续反思在 provider 派发前受阻，未伪造反思事实 |
| Trace | 原生审计拒绝，正式 CLI 退出 3。诊断性后端读取覆盖 14/14 棵树、417/417 个已结束 span、26 个 ERROR，`missing=[]`，严格报告 `FAIL`；该诊断不绕过正式审计 |
| 任务与资源 | 实际浏览器结束任务后，Go 核对零活跃调用、任务槽为空；中断后再次核对所属进程为零、控制器回到初始停止状态，33 个容器／96 个镜像／332 个 volume 身份和元数据保持相同 |
| 发布 | 未运行；本票不替代 release lanes、200 轮或官方 scorer |

最终应用的生产文件 SHA-256 为 `45ecc8bfb5de76b54c37334d873dad76f016660dc237848178ac5a9cea474309`，测试文件为 `60452e89faf8be1339c28377ae282013199de6e39f4e0e4d7f6dc5092dcc1fae`。提案身份为 `f2739723adf83075e9bb218a2c55c6428204e0e27abe55910e994fdd3d034426`。

## 保留的失败与限制

- 第一轮没有形成有效修复，读取上游搜索／最新源码及控制信息的请求被拒绝，随后实际取消；不计入有效轮次。隔离环境里的 Git metadata 指向 Windows 原路径，`git log` 退出 128，未改用宿主执行任务代码。
- 第二轮两次 Edit 精确匹配失败，Write 一度引入测试拼写错误，随后由模型经 Go 修正；三例新增回归随后真实失败。第三轮只读沙箱的 `gofmt -w`／换行写入失败，外层 shell 的 0 不覆盖内部退出 2；该轮超时失败。第五、第七轮 provider HTTP 524，未知用量保留，不计有效轮次。
- 最终源码保留原 CRLF；直接 `gofmt -l` 对未改基线也列出文件。仅对换行规范化的内容确认格式，未声称原始 `gofmt -l` 无输出。本次修复没有扩大到完整 RFC 质量排序、wildcard／identity 或冲突重复参数策略。
- 第四轮观察器误匹配 WebSocket ticket 响应；实际消息受理另由原生事件核对，旧观察和更正均保留，没有重复提交。一次性连接票据已在封存前脱敏。
- 第十四轮原页面意外关闭，后台完成；在同一已鉴权浏览器 context 中重开后读取历史，没有重发模型请求。十张历史助手卡片截图是在恢复后补采，不冒充原完成时刻截图。
- 正式 Trace 的原生审计因 `input_error:ValueError` 拒绝，独立 Go 审计同样拒绝；两个已结束 unknown 调用尚未结算是现有审计的待核查边界。完整诊断读回仍含失败／取消 span，不能作为成功 Trace 通过依据。
- 收尾时旧驱动 exec 会话已不存在，未观察到其 `finally` 或正常退出码。恢复使用现有 Docker readiness 入口；所属进程和全部 Docker 保留身份／状态重新核对通过，没有停止或删除业务资源。未重算活动后的 volume 文件内容，也不声称连续关闭过程已验证。

冻结的 12 项场景逐项保留：10 项在各自声明范围通过，Memory 和成功 Trace 两项失败。普通代码功能和提案应用结果按范围记录完成；#17 整体尚未通过，后续修复和新运行证据另行绑定。

## 计量与收据

同一持久批次，本任务 79 项模型准入：77 项已结算，2 项 HTTP 524 后 usage unknown；没有真实反思模型派发。已确认本任务消耗 4,986,596 tokens，批次累计 24,710,997／100,000,000，费用仍未知。两个 unknown call ID 为 `651745941c2641afb4ebfcb144fdce96`、`021b2055884f417c976fa5c061d1afa3`。历史及新增 unknown 与原预留保持；预留不是实际消耗，未人工清零或逐笔索要授权。

本机收据根：`deployments/docker/.local/receipts/browser-tasks-20261015/go/command-repeat-v1/`。截图在 `output/playwright/browser-tasks-20261015/go-command-repeat-v1/`。

- `attempt-summary.json`：全部 14 轮和 12 项场景。
- `native-after-task-finish.json`、两份 admission 导出和 `task-accounting-proof.json`：Go 校验事实、完整调用与计量。
- `round-02-red-proof.json`、`round-04-test-proof.json`、`round-13-runtime-test-proof.json`、`spills/`：实际测试及完整输出。
- `candidate-review-v1/`、`proposal-diff-review.json`、`proposal-application-ui.json`、`post-application-protection.json`：固定复核、UI 与原副本保护。
- `task-finish-ui.json`、`restored-resources.json`：实际结束与中断后的状态核对。
- `trace-all-fourteen-runs-exit.json`／`trace-all-fourteen-runs-v1/`：正式 CLI 完整命令及退出 3；`trace-all-fourteen-diagnostic-readback.json` 独立保存诊断后端全量读回。

封存命令 `python .scratch/go-browser-command-repeat-20261016/seal-go-attempt.py` 最终退出 0；早期观察器因 Go JSON 省略零值字段失败一次，原因、更正和未知调用边界保留。清单 `deployments/docker/.local/receipts/browser-tasks-20261015/go/command-repeat-v1-artifact-manifest.json` 含 204 项，SHA-256 为 `7cfc9e860c6a90361f6cacfb66489cb00c509cd50168f934e9e2bb530761fe27`；逐项重新核对通过，私有产物不进入提交。
