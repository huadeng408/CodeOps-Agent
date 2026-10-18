# 待补反思的终态 Trace 引用

工程状态：`IMPLEMENTED`；源码与公开恢复接缝范围 `VERIFIED`，结果见下表。当前改动不声称新的真实模型恢复、
正常 #17 任务或完整发布通过，不改 Trace 分析器或原收据。

## 复现与实现边界

此前修复使失败出口把仍在内存的 Agent context 交给 Memory；重开进程后补做已终态
运行的反思时，Runner 走的是创建新 Agent span 之前的恢复分支，原内存 context 不在。
通过已批准的 `SubmitMessage`／`RunStatus`／`Recover` 与 `SessionMemory.Commit`
接缝，真实 SDK 测试先复现：完成／失败两个现代运行的补反思丢父链，legacy 控制通过，
测试退出 1，回归本体约 0.11 秒。该 RED 与原始输出保留。

实现复用既有 Go 终态 Ledger 事实，附加可选、`schema_version=1` 的 Trace 引用：
当前 Session、W3C traceparent 与已准入 eval run ID。它沿用原 run／lease／attempt
核验；投影从相同事实读取，恢复只接受匹配 Session 和已知版本、有效 W3C 父链。
没有增加状态库或 checkpoint 写入者，也不复制任意 baggage、tracestate、权限或凭据。
这是 Go 私有终态事实的可选元数据；既有 protobuf/gRPC、模型协议和前端流保持不变。
引用只取本次 `StartSpan` 确实新建的 Agent 身份；每次执行先清空进程内标记，
nil／Noop tracer 不会把继承的外部 caller 冒充为 Go Agent。

失败写入继续有独立五秒期限，以 `WithoutCancel` 保留现有 Trace；反思仍使用自己的
期限，并由 Runner shutdown 取消。旧事实没有引用、版本未知或引用不可用时保留原来
的未知关系，不制造父 span、回填旧事件或把旧数据标为 Trace 通过。Memory、权限、
Ledger/CAS、终态和已结束 unknown 的计量规则保持原要求。

测试关闭首个 Runner／SDK／SQLite 连接，重新打开同一 Ledger，创建全新 Workbench、
Runner、SDK 和请求 context 后恢复。覆盖完成、失败、nil／生产 Noop tracer 回退（含
真实外部 caller span）及恢复中的 shutdown；核对原父链／eval 身份、旧终态不变、
conversation 不重放、取消在 Memory 自身五秒期限之前生效。

## 验证记录

| 命令／范围 | 当前结果 |
| --- | --- |
| 新恢复回归，修复前 | 退出 1，completed／failed 父链断开，legacy 通过 |
| 同一回归，最小修复后 | 退出 0，三场景通过 |
| 独立审阅发现的 Noop 回退回归，修复前 | 退出 1，错误继承外部 caller 父链 |
| SQLite 重开、恢复 shutdown、两类 tracer 回退、实时父链和默认／显式期限 | 退出 0，3 个顶层／13 条父子 PASS |
| 修订后定向 race | 退出 0，13 PASS，无 race 报告；前一候选结果保留 |
| `go test ./... -count=1 -json` | 退出 0，1,835 PASS／81 SKIP／0 FAIL，共 1,916 条父子结果；47 包 PASS，13 包无测试 |
| 同次全量中的完整 Session／Memory | 336 PASS／4 SKIP，两包 PASS；不与全量相加 |
| `go vet ./...` | 退出 0，无输出 |
| gofmt diff／`git diff --check` | 退出 0，源码格式差异为空 |
| 独立 Standards／Spec | 首轮各 1 项 Noop 边界问题；修订后各 0 项未解决发现，`AI_REVIEWED` |
| 新真实模型恢复／正常任务／发布 | 未运行；上游 502 及原验收缺口分别保留 |

补充 caller-span 控制时，一次测试编译因误用不存在的 operation 常量退出 1；已改用
既有常量，失败输出与随后通过结果均保留，没有把该编译失败计作语义 RED。
前一候选两包日志保留 335 PASS／4 SKIP，最终结论绑定修订后的全量运行；81 条跳过
逐项保留在完整日志和摘要，不计为通过。早期读取尚在写入的 JSONL 时观察脚本退出 1；
最终完整日志已严格解析，该观察错误没有改写 Go 测试结果。

源码基点 `19680c7edb9ab65afb37fb84ce7df0a27baf141a`，修订后审阅 patch SHA-256
`5fd5fddf85bae00e696b6cdef36977d38324ef70dcb858a94c66ec159aac17f4`。
`validation.json` 绑定两份源码指纹、命令／退出码、完整分母、日志 checksum 与两轮审阅，
SHA-256 `12d779213f1ec03292b181d848821a0884c3fc9d423899f1dafa89040b0afbe4`。
产物位于 `.scratch/memory-recovery-trace-20261019/`；既有 73／82／777／514 项
运行封存共 1,446 项逐项核对一致，保持各自源码绑定，不随本次源码变化改写。
没有新的模型调用；Python、前端和完整发布验证未运行。用户 AGENTS.md 改动排除交付。
