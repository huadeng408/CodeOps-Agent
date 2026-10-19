# #28 canonical compaction 后的 Memory 提交

对应 [#28](https://github.com/huadeng408/CodeOps-Agent/issues/28)。本阶段验证 Go 源码和
既有公开接口；新便携包中的实际 compaction／反思／后端读回仍待独立验收，本票保持 OPEN。
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

## 运行绑定与尚未验证的范围

原 v8 包已在源码集成前重查 3,225 个 runtime 文件和 613 个预付冻结源码指纹，0 差异。
运行中的 launcher／Go／Python 均位于该独立包；只把本修复集成到工作区源码，不热替换原包。
原包的 SHA、已发生的 Memory 失败及 #27 输入／调用／恢复分母保持原身份。

代码、命令、完整日志、manifest、只读分析及复审位于
`.scratch/product-long-dialogue-20261019/` 和对应 `-source-review/` 目录。
新包必须另行固定版本后，观察真实模型 compaction、Go 提交反思及实际后端读回；
该范围尚未运行。完整发布亦未在本修复范围内运行，不把本阶段 Go 验证升级为发布合格。
