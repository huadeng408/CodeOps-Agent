# Go 原任务：功能与 Memory 通过，失败轮次反思 Trace 仍未接回

本次是 [#17](https://github.com/huadeng408/CodeOps-Agent/issues/17) 的新独立公开任务，
使用源码 `b8be9e62fe04ee6d3f979703ab40aaaee96bb3e5` 的便携包和已核验的
600 秒执行／630 秒控制租约。启动时当前 main 为
`b85ee51f351aec62c05e464df03d4bf62fcfa3ab`，其后续说明变动与原运行内容分别绑定。
原旧批次、持续时间探针和准备失败不计入本次完成轮次，也没有改写旧失败。

声明范围的结果：真实功能、项目测试、提案审核应用、同任务 Memory 与本批新调用结算
已验证；完整 Trace 仍为 `BLOCKED`，#17 保持打开。源码修复和更广的 #19／发布
按各自范围继续验收，不追加为已关闭基础票的门槛。

## 冻结与真实浏览器执行

两个正常公开任务、原版本、输入、预算、source/model/task/backend pin 和十二项场景
均在新调用前冻结。本次选择 chi 的 Go 修复，基点
`9b9fb55def404397748a9fc7e044efe9db1d618e`：完整编码 token 匹配、显式 q=0
拒绝，保留 API 和无新增依赖。未提供上游修复建议、参考补丁或旧候选源码。
新产品账号初始 Memory 为空；空父 PATH、随包 Git/Python 和按进程代理均保留。
启动前核对 616 项源码、3,219 项随包文件及两份原工作副本。

所有任务修改由实际产品的 Go Write/Edit 执行；读取、审批、取消、提案、批准应用及结束
均经真实 Chromium 界面。没有从宿主直接写入隔离候选或用 HTTP 脚本补齐对话。

| 尝试 | 实际工作 | 结果 |
| --- | --- | --- |
| 1 | 仓库、测试入口与公共 HTTP 回归调查 | 完成；纯调查不计入保守开发轮次 |
| 2 | 新回归复现旧行为 | 完成；预期 RED，六个新增子例失败保留 |
| 3 | 最小修复与 GREEN | 完成 |
| 4 | 自定义编码器与原优先级兼容 | 完成 |
| 5 | 多字段边界与编辑尝试 | 工具轮次上限失败，保留所有工具结果 |
| 6 | 重试多字段边界、修复并回归 | 完成 |
| 7 | 已有 Content-Encoding、Content-Type 与 Content-Length 保护 | 完成；首个错误断言及纠正保留 |
| 8 | 三种实际 HTTP Flush 场景 | 完成 |
| 9 | 必要公开说明与交接纠正 | 完成 |
| 10 | 格式、diff/vet 检查 | 检查已通过；重复 vet 请求经界面取消，不计完成轮次 |
| 11 | 独立审阅后的测试辅助代码调整 | 完成 |
| 12 | 去掉流式读取 goroutine，消除清理并发风险 | 完成 |
| 13 | 正常完整项目测试、静态检查和交接 | 完成 |

完整分母为 13 次：11 完成、1 失败、1 取消。扣除调查轮，保守计入 10 个有意义的
完整开发轮次。最终定向 race 测试 39 条父子 PASS；`make test` 为 91 个顶层、
170 条父子 PASS、0 FAIL、0 SKIP，退出 0，停止已确认。表中预期 RED、编辑失败、
拒绝、首个错误测试断言和取消均保留。

完整 make 输出为 427 行；模型表面截断状态没有改写。Go 保存的完整脱敏 spill
按内容 SHA-256 只读取回，另存原始完整字节后统计分母。最终两份测试文件
`gofmt -d` 无差异；`git diff --check`、`go vet ./...` 均退出 0。
较早 compress.go 的 CRLF 约定保持原样，其按 LF 在内存归一后的格式检查无差异。

## 提案、原文件和 Memory

最终候选经两轴独立复核，Standards 0／Spec 0，均为 `AI_REVIEWED`。
首轮重复辅助逻辑及流式清理风险已在最终候选中解决，没有标记为人工逐条复核。
右侧按钮生成的冻结提案运行 `go test ./... -count=1`，退出 0、输出未截断、
停止已确认，测试输入等于最终状态指纹，模式变换为 `readonly-v1`。
逐项展开真实 diff 并与审阅的五份文件摘要核对后，实际点击批准，五项全部 applied：

- 修改 `middleware/compress.go`、`middleware/compress_test.go`；
- 新增 `middleware/compress_header_fields_test.go`、
  `middleware/compress_response_guards_test.go`、`middleware/compress_streaming_test.go`。

应用后全部摘要与审阅候选相同，130 个无关原文件及两份 index 不变；原 dirty/new
用户文件保留，主仓库的用户 AGENTS.md 未修改或提交。

Go 只读原生导出核对 12 份完整轨迹、12 份真实反思提案、10 次 catalog 变更及
2 次 noop；34 条实际 Experience 与来源事件／checksum、proposal、catalog/CAS 和
当前投影逐项一致，projection error 与 commit-blocked 均为 0。
这证明实际同任务经验写回，没有宣称每种终态都会单独触发一次反思。

本 Session 的 143 次准入全部结算：131 次 foreground、12 次 memory_reflection；
新增已确认 13,703,095 tokens，原批次累计 45,751,566／100,000,000。
历史预留估计 4,206,024,576 不变，费用仍未知；它不是已确认消耗，也没有虚构零费用。
实际点击结束任务后 ActiveCalls=0、TaskID 为空。

## 完整 Trace 的具体缺口

未改动的 `product-integrity-v2` 分析器对全部 13 个 canonical run 执行原生审计及
真实后端读回：13 棵树、731 个 span、238 个逻辑工具请求／227 次派发、
143 个模型调用；27 个 ERROR 和所有失败／取消／拒绝保留。
两次读回字节一致；正式结果 `INCOMPLETE / partial / BLOCKED`，CLI 退出 3，
legacy_report 仍为 FAIL。缺失项是同一个 memory_reflection 调用的 provider.call
和 provider.attempt；没有其他 problems 或未结算目标调用。

另按真实 call ID 查询后端，找到了独立的三 span 树：memory.reflect 是空父根，
provider.call／provider.attempt 已结束且实际计量已结算。该根没有指向本轮 Agent 的
parent 或 link，因此诊断数据不能拼入一个声称通过的森林。
当前已把缺口定位到失败轮次反思的上下文传递，后续源码修复、定向验证和新运行
继续独立绑定；本收据、原输入、失败分母和 orphan 树均不回填。

## 产物与恢复

本机产物位于 `deployments/docker/.local/receipts/go-original-fixed-20261018/go/v2/`；
准备阶段 v1 失败、错误事件类型的空观察结果及后续原生对账纠正、手工摘要转写、
cursor 冲突自动恢复和能力响应观察器路径错误均保留。它们没有额外计入完成轮次。

777 项封存清单：`.scratch/go-original-fixed-20261018/artifact-manifest.json`，
SHA-256 `d1d7265af8ab1dfb47d25d53ff04e7d7872a161ab4db6e58c8198dddb2e951c7`；
范围摘要 SHA-256 `526eb33c43a65ce825b51f07906d7febcbce1dfa789be4bd3ba5cd57d6373372`。
主要原始证明的 checksum 如下；清单保留完整路径／长度／checksum：

| 证明 | SHA-256 |
| --- | --- |
| 全部尝试 | `9396c45d223536225254d74da50177ad78bb69ab56eff287579bbbf5be655bf8` |
| Memory 来源核对 | `5865216b51ee1c4cbead0025006e9dc759058470f4927b6e4136f4b331148c3e` |
| 目标调用结算 | `ab721a4478cd960cd58ad9161812e6b5b959c0dad265db89696f044cf45253c5` |
| 原文件与 index 保护 | `6a8997dd3ae24b47ab51b257fd34b22237293d8ac62387dd184adf9ff762879d` |
| 原正式 v2 收据 | `4c762f6d418c4fc0b00fe1a9e14d1a2723c06a125bcbe07a7ac8c6ad3b2b8784` |
| 独立 orphan 树实际读回 | `928dbf96fe2d2678d882d16290cf2c86098959225e29f91506d4bee0067404ec` |

owned launcher 的 Windows Job 及浏览器已停止，无页面错误；两个既有控制容器均按
精确 ID、image 和 Compose project localcode 核对，恢复到原 exited 状态。
没有删除业务容器、volume 或索引；前一份 514 项封存清单也全部匹配。
本轮不声明当前源码 CI、整体故障矩阵或完整发布合格。
