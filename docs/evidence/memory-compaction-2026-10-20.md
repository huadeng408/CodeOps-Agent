# #28 canonical compaction 后的 Memory 提交

对应 [#28](https://github.com/huadeng408/CodeOps-Agent/issues/28)。`VERIFIED` 范围为 Go
源码／公开接口回归，以及独立 v9 便携包的真实压缩后 Memory 提交与后端读回。
十二项只读产品请求全部完成；两次反思失败和首次退出检查失败保留，最终停止状态已另行核验。
此修复不改变 #27 原包的结果，也不替代其完整 200 assistant-turn 分母或完整发布验收。

## 实际问题与最小修复

#27 的真实模型压缩后，前台轮次可以完成，Memory 却在反思前拒绝轨迹：隐藏的 Surface
`tool/call`／`tool/result` 被排除，但 log-only 的 dispatch／approval 仍保留，形成孤立调度。
原失败事件保留；native Surface 本身有效。

在既有 `internal/memory/trajectory.go` 中，沿当前 Surface 的实际 `compact` 事件反向展开
已由 native snapshot 校验的 sources，覆盖嵌套摘要。仅这些来源中的隐藏工具配对重新参与
身份、完成与未知副作用校验，不成为 Memory Sources 或正文。log-only 事实和实际代码
收据继续保留；非 compact 的 append／replace 摘要不能授予隐藏来源权限。
共享的结果解析同时保留 `outcome_unknown`，即使没有单独的 `tool/unknown` 也拒绝晋升。
既有 branch／rewind、身份检查与 Ledger 写入边界保持原实现；无新依赖或协议。

起点 `82d698d94fa429a831706b5188a8886944ff7d1c`；原 trajectory SHA-256
`a93567834b3b296fba94f4bec03275e5df4f6105d7e4d68396f85718973af8e5`。
修复源码 SHA-256 `754f66a32f7f015f0a57a8b6aeba0ff27f017a00fa8a8b3be7d8cd05f21aa0a8`。
新增测试 SHA-256 `5585fa190a2ac51d60cb0522c5a6bd9306560c22f6fd182d8af1e857d06b8088`。

## 验证范围与完整分母

测试在既有 `BuildSessionTrajectory`／`LedgerMemory.Commit` 接缝使用 native 临时 SQLite、
实际 Surface 操作及原有 append／checksum helper；没有 provider 或真实产品调用。

- `go test -json ./tests/go -run '^TestMemoryCompactionPreservesToolEvidence$' -count=1 -timeout=120s`：
  正式源码修改前退出 1，12 个叶子场景为 9 FAIL／3 PASS；修改后退出 0，12 PASS，
  另有 1 个父测试 PASS，0 SKIP。原失败日志保留。
- `go test -json ./... -count=1 -timeout=240s` 首轮退出 1：1,853 PASS／82 SKIP／
  2 FAIL 测试事件。两条 FAIL 为未修改的 launcher 路径拒绝 fixture 及其父测试，
  子进程在 30 秒内未启动完成；不能将该轮称作全套通过。
  同一 launcher 测试单独新跑退出 0，2 个叶子和 1 个父测试均 PASS。
  `go vet ./...` 退出 0。原失败和后续命令分别保存，不改源码或放宽期限来绕过。
- 随后 `go test -p 1 -json ./... -count=1 -timeout=240s` 退出 0：47 个有测试的包通过，
  1,855 PASS／82 SKIP／0 FAIL 测试事件；另 13 个包无测试文件。
  父子测试均计入事件数，全部跳过名称及原原因在完整 JSONL／报告中保留。
  只限制包并发，源码、断言与测试期限没有改变。
- 十二项为普通／已审批工具、嵌套／嵌套已审批、独立 unknown／嵌套 unknown、名称错配、
  active／compacted unknown-result、append／replace 伪摘要和代码收据保留。
  每项先验证 native hash／Surface，再验证 Memory；隐藏正文和来源不晋升。
- 独立 overlay 候选的相关完整三个 Go 包：676 PASS／6 SKIP／0 FAIL 测试事件，退出 0。
  这是候选阶段记录，父子测试分别计数；不是正式源码 CI 或产品结果。
  六项跳过是 opt-in Memory 跨进程 fixture、两个 Windows symlink 特权检查、一个子进程
  helper 和两个未设置 approved-wheel 路径的 Python proposal fixture；原原因完整保留。
- 另行编译同一候选，用现有 fixture 的 seed／recover 模式运行两个独立测试进程，均退出 0；
  10／10 检查通过，覆盖恢复提交、owner 隔离、预算、hash、来源和幂等。仅写新自有测试目录，
  不覆盖原有 latest 收据，不调用模型，也不作为产品进程恢复证据。
- 对实际不可变 native 导出的只读重放：原 14 个终态前缀中，12 个压缩前前缀正常、
  2 个压缩后失败；修复候选使 14 个均可投影，原 12 个 SourceChecksum 完全相同。
  native 导出 SHA-256 `c72fad36044bb883d3b6171e97d214ca232c7eb2f7388db846be10ff03f9253a`。
  此为已导出事实的分析，不是新的产品／模型收据。

候选实现与合并后的单文件 fixture 均经独立 Standards／Spec 复审，遗留发现各 0，
标记为 `AI_REVIEWED`。过程中发现的 unknown-result 和非 compact 来源权限缺口均先保存
各自 RED，再修复；原草稿、失败及复审报告不覆盖。

## 源码交付时的运行绑定

原 v8 包已在源码集成前重查 3,225 个 runtime 文件和 613 个预付冻结源码指纹，0 差异。
运行中的 launcher／Go／Python 均位于该独立包；只把本修复集成到工作区源码，不热替换原包。
原包的 SHA、已发生的 Memory 失败及 #27 输入／调用／恢复分母保持原身份。

代码、命令、完整日志、manifest、只读分析及复审位于
`.scratch/product-long-dialogue-20261019/` 和对应 `-source-review/` 目录。
源码交付时，新包中的真实 compaction／反思尚未运行；后续独立产品结果如下。
完整发布未在本修复范围内运行，不把 Go 验证或本次只读产品范围升级为发布合格。

## v9 独立产品验证

运行源码为 `e83822f3a13cf667283aebeceaf7bdd9dd5467e8`，
[新鲜源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/39294067131)
四项 job 均通过；其中 Python 为 2,637 PASS／12 SKIP，Windows 启动为 10 PASS。
官方离线构建退出 0，未下载新依赖；bundle manifest SHA-256
`764d135f6abe6ea5c0f34148f5059da768c0d5f61395da10c4a628331f5c835c`。
运行前后均核对 613 个参与构建的源码文件和 3,225 个 runtime 文件。

单独冻结十二条普通 Go 只读问题、新账户／Session、固定镜像和现有分组控制服务。
输入 SHA-256 `545b2fb220b54c555677ff7784db03b8a298f0ad28b157c0810f53bbf1520892`；
prompt SHA-256 `27e0bfeddc565d8f5a6c319dd28e7d398996a1e2a23f8242900fa5b2c1536c05`。
应用窗口为 16,384；这是应用配置，不是已验证的 provider revision／窗口。
仍用原 `grok-4.6`／Anthropic relay 和原服务商 100M 批次，不复制或重置 Ledger。
Session 为 `13b1c7920db98cbcdda54f11cee63c55`。

- 实际发送十二次，十二个唯一 request／run，十二项前台终态均 completed，均有非空回答；
  没有额外重试。十二次真实 Read，经 Go 执行；0 个代码修改，没有写入或 Shell 审批。
- 十二次 native canonical compaction，其中十一次引用较早摘要。十二条已存轨迹逐条通过
  当前 Go `BuildSessionTrajectory` 只读重算，内容／Sources／checksum 完全相同；隐藏
  Surface 来源没有进入 Memory Sources。十次实际 proposal／catalog 写回，其中八次在
  compaction 后，涵盖嵌套摘要；按 owner、repository、terminal 和 source checksum 对齐。
- 第 2、8 项各保留一次 `memory_commit_unavailable`／pending 记录及相应
  `memory.reflect` ERROR。两项均已成功提交轨迹，未产生本轮 proposal／catalog 更新。
  该公开分类不能证明底层原因，本次不将其记为成功反思或删除待恢复事实。
- 十二个 run 的 native `trace-audit` 均退出 0；实际后端十二条树全部读回，共 156 个
  已结束 span：154 UNSET／2 ERROR。十二个 root 的父链、48 个 provider.call、48 个
  provider.attempt、十二个 Read tool 与 native 身份／分母完全一致，无断链或缺 span。
  这是本次只读 Memory 范围的后端证据，未声明完整代码修改任务的 Trace lane 合格。
- 原批次新增 48 次调用：前台 24、反思 12、压缩 12；全部已结算，无新增 unknown。
  新增已确认 897,157 tokens，累计 57,657,989；旧 unknown 与原预留 5,606,024,576
  保留，费用仍未知。无其他 Session 的新增 provider 调用。

第一条压缩后成功写回为 native terminal seq 100、trajectory LastSeq 101、commit 102、
proposal 103、catalog mutation 3；来源 checksum
`3435b6e6609a78e95f7601e82f021f3873a4bb9a34f85992028e95b8dd2f09fb`。
terminal 后的合法 Go progress 也在轨迹前缀中；不能要求 LastSeq 等于 terminal seq。

## 观察错误、结束与数据保护

首轮已完成且反思成功后，v2 观察器错误要求 LastSeq 等于 terminal seq，最终返回待对账。
保存原日志／失败；修正只读关联谓词，RED 18／20、GREEN 20／20，均保留。
独立复审发现 pending 缓存影响重入，v4 修复后四项 VM 检查通过、双轴复审遗留各 0。
v4 从已捕获 HTTP／terminal 与完整 native 前缀接续第一轮，没有重发或重置 Ledger；
原观察闭包和旧产物保留。上述检查只验证观察代码，不替代十二项实际产品事实。

首次寻找结束按钮超时，未发出结束 POST；刷新产品能力后，实际点击结束，HTTP 200。
新鲜预算核验 ActiveCalls=0、TaskID 为空，累计用量与结束前相同。
提前运行最终审计器时曾因任务槽未释放而正确拒绝；其原退出 1 和边界说明保留。
最终完整只读审计命令退出 0。

驱动 finally 的首次退出检查退出 1，短轮询仍报告两个进程；浏览器关闭及两项控制服务
退出成功。随后 `memory-final-stop-check.ps1` 只读核验退出 0：原 launcher 与包内进程
均不存在、两个精确身份控制服务均 exited、Ledger 无活动调用和任务占用。没有额外强杀
或删除。原失败与最终停止收据分别保存，不将原驱动退出码改写为 0。

133 个原有 stream 的原 head／前缀保持不变。Go 启动恢复只向旧 #27 Session 追加两条
Memory 恢复事实，无新 provider 调用；原 v8 不可变导出和 74／200 结果保持原身份。
原 admission 前缀保留，本次新增事实完整导出。四份受保护工作副本共 333 个文件和
四个 Git index 不变，运行包 3,225 个文件不变；未修改原任务或已有业务数据。

## 收据与验收范围

本机原始收据位于
`deployments/docker/.local/receipts/memory-compaction-20261020/go/v1/`，
冻结输入及观察／审计代码位于对应 `.local/profiles/memory-compaction-20261020/`；
最终截图为 `output/playwright/memory-compaction-20261020/go/v1/finished-stable.png`。
这些含运行身份的材料留在忽略目录，不作为新的可写事实源或提交到仓库。

| 产物 | SHA-256 |
| --- | --- |
| `runtime-audit.json` | `deec598939177a678124ba159a707e5adcd0280582dbc5e53f8e4179d9dcccf4` |
| `native-projection.json` | `c69e2cf6a11822fe536e43c98962e19632d69ebc6e329c75996f0554881b0272` |
| `final-session.json` | `646bb884141af167685876c1fbd014ed757fc94c91da50e1e28a576d6aa88931` |
| `final-admission.json` | `d3ffd55c0cbdeb6beda09c07600e8fe865a5510f813f8d6a515e58552bbbcf2e` |
| `final-catalog.json` | `12ad68b2fb246e1e7ca065d9a5dc8e2ec60f7d004dc7c5f73eda3fb617494b64` |
| `driver-stop.json`（原失败） | `7174ce069f8682ef5c646935e85317651a5dbbc921d98e69f8b089b1e000238f` |
| `final-stop-readback.json` | `fecb4f7f30602a5eb182fa90ebee71b19f47756ad2ad9edef9ab6e03cacb8a0e` |

单测／集成与新鲜源码 CI 已按上文范围通过；十二项真实产品验证证明 #28 的压缩后
提交、来源隔离和后端读回，保留两次反思失败。独立最终 Standards／Spec 复审及 tracker
发布读回在交付时完成。#27 的完整 200 轮与完整发布仍分别待验收，不能用本次分母替换。
