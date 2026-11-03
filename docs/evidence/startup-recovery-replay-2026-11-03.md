# 启动恢复：回放热点、延后派发与跨构建 Artifact

对应 [#42](https://github.com/huadeng408/CodeOps-Agent/issues/42)，接续
[上一轮投影优化](startup-recovery-cost-2026-11-03.md)。本页分别记录源码／夹具验证、
真实库只读诊断与尚待完成的新包实际启动，不代替 #27 的独立 200 轮或完整发布。

## 实际失败与固定输入

源码 `7be2f794e34be6509d57df9ceda4996ae575abc1` 的完整 CI 和 v17 官方离线构建通过后，
v11 实际启动仍因恢复超时而 attached=false、generation=0。该次没有 message POST，
0 Run、0 新模型预约、0 完成，200 项全部未运行；原收尾与 24 份校验产物保留。
其 scope-result SHA256 为 `279ac8bbda52ecd7ea80e674d59a5d98e4a16287ae3976fe93b4f2eb53d1b785`。

后续诊断固定同一真实库：235 个 canonical stream、49,981 条事件，SessionIDs union 为235。
使用真正的只读 SQLite 连接调用公开 `SessionRunner.Recover`；适配器拒绝全部六个
EventLog 写方法及模型、工具、工作区、Memory、生命周期等外部回调。
每次核对 main／WAL／SHM／journal 的存在状态、长度、mtime、SHA256，及全部 stream
的 head/count。没有复制或改写原库，没有请求模型或查询提供方账单。

## 按采样结果收敛修复

原 5 秒诊断只读到36/235。一次单独的60秒诊断上限下，Recover 实际33.5780秒完成235/235；
其中 Budget 完整回放301次，每次9,451条事实，合计2,844,751条事件及613,887,092字节payload。
startup callback 合计30.4315秒是墙钟观察；5秒 CPU 采样的 Budget.read 占76.40%，
SQLite 读取包含在该调用树内，不能相加。14,229,333,760字节为累计分配，不是常驻内存或磁盘占用。
诊断上限没有改变生产的5秒界限。

1. **仅复用已验证的 startup yielded 投影。** 只限 native SQLiteEventLog；owner/task/nil
   校验先于命中。证据来自同一租用底层连接的身份代次、data_version、total_changes、
   main／TEMP schema_version。冷读取前后证据相同才发布；变化时有界重读，未知时回原完整验证。
   旧 payload SQL UPDATE、跨进程提交、连接替换、TEMP shadow 均有覆盖。普通模型准入、计量、
   完整 Surface 和祖先验证不走此缓存，没有持久化缓存或第二份 Ledger 事实。
2. **扫描完成后再派发。** 所有候选先进入局部计划，最终 context／Close 和准入检查后才发布
   现有队列。planning 标记覆盖 recoverAgentTask 内部 SubmitMessage 的 enqueue；新 Run 仍持久化，
   再由外层重读统一规划。worker 在恢复写入前再次验证完整祖先、新鲜准入及当前 Surface/head。
   保留健康 partial 候选、显式 RecoverSession 优先级及后续恢复能力。
3. **稳定 Artifact 派生 checksum。** 同库曾有两项 integrity 错误，但完整链／Surface 均合法，
   五个 Artifact 的 checksum 全部匹配旧 spaced JSON。项目错误地对 protojson.Marshal 的原始
   字节计算 checksum；锁定的 protobuf v1.36.11 会随二进制改变结构逗号后的空格。
   两个 producer 现共用 compact 编码，读取严格兼容旧 compact／spaced 格式；字符串内逗号、
   转义和文件原始 SHA 不改。同 ID 同内容重放保留原 checksum，内容改变仍冲突。旧事实不改写。
4. **只留真正分支目标的历史 Surface 副本。** 上述修复后仍有5.0012秒 deadline，采样定位
   cloneEvents 为869MB／28.17%分配，projectSurface 合计929MB／30.13%；Budget.read 已降至1.05%。
   现在预收集 rewind／continuation 的 TargetSeq，仅保留被引用的快照；原顺序的全部 marker、
   continuation、compaction 校验仍执行，恢复时仍 clone，避免 replace 改写历史分支。

SQLite 的 data_version 仅适合同一连接跨时间比较；同连接 DML 和 DDL 由其余计数补充。
这是 SQL 可观察变化的证据，不是任意物理损坏检测。
[data_version](https://www.sqlite.org/pragma.html#pragma_data_version)、
[total_changes](https://www.sqlite.org/c3ref/total_changes.html)、
[schema_version](https://www.sqlite.org/pragma.html#pragma_schema_version)。
protobuf 官方也明确不保证同一模块版本跨构建的 JSON 输出稳定性。
[protojson v1.36.11](https://pkg.go.dev/google.golang.org/protobuf@v1.36.11/encoding/protojson#Marshal)。

## 失败、红绿与最终本机验证

原失败和中间结果全部保留，包括 cache-only 的 partial、5.0012秒 deadline，以及
明确标注的夹具／基础设施问题。诊断 wrapper 的 exit0 不代表 Recover 通过。
内层子任务首个 RED 的直接失败为 final-admission sequence conflict，未到达 early/prefix
断言；提前 enqueue 路径由源码审查确认，不追述旧日志已有直接计数。
后续非 fatal 观察验证了前缀、零模型调用及显式续行。新 producer 固定 SHA 与 legacy
同 ID 重放的补充断言为 GREEN-only，不伪称曾在初次 RED 执行。

| 范围 | 实际结果与边界 |
| --- | --- |
| startup fence 资源用例 | 独立 SQLite、513 条有效事实、warm-up 不计入32次检查；初步 RED 45,435,664B，初步 GREEN 35,584B；不是整批启动耗时 |
| 延后派发／Artifact 统一定向 | 9 pass／0 fail／0 skip，4个顶层测试；退出0；240.75秒含编译 |
| Surface 资源及既有分支回归 | 516条事实、2个目标；23,241,192B → 1,792,304B；15 pass／0 skip，10个顶层；恢复后384个源EventID/checksum逐项相同；退出0 |
| 三个相关 Go 包完整回归 | 730 pass／0 fail／11 skip，349个顶层通过；退出0，596.25秒含编译 |
| 相关 Go vet | internal/admission、internal/session、cmd/server；退出0，109.329秒 |
| 独立 Standards／Spec | 最终运行源码各0 finding，10/10指纹审前后相同；原v1的P1和后续复审均保留；本页文档另行复核 |

完整 Go 分包为 admission 291 pass／5 skip、session 365 pass／4 skip、server 74 pass／2 skip。
11个跳过中，7个为受控子进程 helper，1个为 opt-in Linux namespace，1个为本机 Windows
symlink 权限不足，2个缺批准的 pytz wheel；没有为补分母下载依赖。通过数按 JSON 测试终态
事件统计，包含子测试，表中另列顶层数量；不是模型任务分母。

源码、每段命令、完整输出、失败与跳过、原库前后指纹及 checksum 均保存在忽略目录
`.runtime/eval/agent-experiments/artifacts/startup-recovery-profile-v1/`。
封存索引含153个文件，validation-summary SHA256：
`4ad073b952a8738232c5b5c9f76955498e12483748da69453b4144c79bf1432c`；
checksums-v1.json SHA256：
`a4a43af0b79ba2d4f220e427cfe1c69349e97098f79bf12a8d8a13be825e0914`。
最终十文件 review manifest SHA256：
`8d227af7230fd21ba91ce9fec75e0331460ed868ed13fa41d52e66db4e5acb47`。
三包完整回归日志 SHA256：
`050c51401a805967347907feb23119c9be60939afbb8063b5798aea1780990f9`。

## 同库只读范围与后续门禁

最终 native 只读诊断在原5秒预算内 **4.8944095秒返回nil**：235/235、49,981条事实，
416 startup checks／415 allowed，无 deadline／partial／integrity，0外部回调或写尝试。
原库所有文件指纹和 head/count、十项 Go 输入均不变。日志 SHA256：
`15a6039fd3bd01b1c4857fce19a5c15daf25c9bd663324389a29cf8e0d987ef3`。
这是固定本机条件下的一次只读描述性观察，不是产品实际启动或长期 SLA。

新提交的完整源码 CI、官方新便携包、实际启动门禁及独立200轮仍须分别核对。
本机没有重复无关 Python／前端全套，也未操作 Docker 数据或重新拉取镜像。
新 CI 不借用旧提交的通过记录，新运行不拼接旧失败分母。
