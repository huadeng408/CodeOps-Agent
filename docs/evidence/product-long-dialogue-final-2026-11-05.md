# 200项累计结果与后续修复

同一 Session 的200项工作已结束，全部实际尝试和原失败保留。**完成数通过，不等于语义质量或整票通过；#27仍开放。**
本轮运行已正常停止，没有为最终审计追加模型问题，也没有由宿主代写 Task Workspace。

## 实际结果

| 范围 | 结果与边界 |
| --- | --- |
| 工作分母 | 200/200完成；211个实际 Run＝200完成、10失败、1取消；没有重跑完成前缀 |
| 执行段 | v12为123完成/4失败；resume-02为12/1；03为16/1；04为35/1；06为7完成/1取消；07为1失败；08为7完成/2失败。零Run启动失败另保留 |
| 最终运行来源 | `26d3ff918c96333b1391c7f167589f6a8844c983`，缓存便携包v23；此前各段保留自己的源码、包和输入，不声称单一二进制跑完200项 |
| 原生事实 | 10321条 Session事件、12218条 Admission事件；211/211逐Run原生核验 exit0，原9451条 Admission前缀和受保护1640条旧Session事实保留 |
| Trace | 211/211完整物理树采集，累计3525个唯一span；938条Go预留与物理provider attempt／用量联结无错误。原v2仍FAIL（logical call身份不匹配），原取消194终态的Trace关联另为UNKNOWN，严格完整性不标PASS |
| 压缩 | 81次持久化压缩的来源、当前输入和工具配对已核对；语义审查不通过无条件PASS，具体缺口见下文 |
| Memory | 全库旧前缀及结构来源校验通过；当前catalog186事件、830条最新记录、1538个历次来源。自动Recall注入和因果贡献仍UNKNOWN，不把结构校验当召回效果 |
| 收尾 | 正常finish为HTTP200、任务槽空、ActiveCalls=0；唯一done exit0，owned产品/浏览器退出，两控制容器恢复exited，无遗留owned任务成员；未知用量和原预留未清零 |

第196项第一次Run在成功修改并通过普通/竞态测试后，仍因工具轮次上限失败；第二次完成不能抹掉第一次失败。
第197和199项的真实全仓库测试通过；199同时分别输出`GO_TEST_EXIT=0`与`GIT_DIFF_CHECK_EXIT=0`。
第200项只读检查并交接，未再次测试或写回原仓库。40/80/100/120/160边界按已验证前缀复用，没有再次重启100。
旧59观测失败的原因未知、100首次超时及人工对账、provider失败、取消、Read负结果和各段恢复干预继续保留。

## 质量缺口与耗时

- C75起，完整存储投影没有显式保留“不增依赖／禁止网络”两组约束；禁网在第200项当前输入中重新明确，不倒改之前的缺口。
- C79关于README被截断的说法与其来源可见内容不符，C81仍有不确定表述；tail及最终回答保留了正确文档事实，二者分别记录。
- 最终交接中的“整树大量无关M”缺对应status证据；最近保存的status只有6个tracked M和1个untracked。未据此认定最终物理状态必假，也不修改模型答案。
- 旧65组代表性损失及后续恢复转换原样保留；当前输入、结构引用正确不代表所有摘要事实正确。

按原生用户消息至对应Run终态计算，总历时**39.39小时**，逻辑Run窗口累计**12.90小时**，窗口之外**26.49小时**，其中执行段之间**20.64小时**。
Run窗口包含模型、工具、审批及取消194迟到恢复终态前的等待，不等于进程活跃时长；窗口外包含Memory、启动、恢复和操作者等待，不能全部叫作停机。
各段缺少统一精确的进程停止时间，物理停机时长保持UNKNOWN；未用文件mtime补造。

## 本次直接修复

1. OpenSandbox共享Runner给当前子进程设置精确`/workspace` Git信任及`GIT_OPTIONAL_LOCKS=0`，解决root所有的只读挂载被Git拒绝的问题；Bash与子目录的结构化Git调用均覆盖。容器环境策略、镜像、分析器及全局Git配置未改。
2. `compact_surface`遇到原样结果时不发布更新；自动压力压缩没有缩短时停止重试并记录`no_progress`，保留原Surface和来源。手动强制操作仍允许实际结构合并。回归复现的3次无增益更新变为0次，摘要尝试3次变为1次；这是确定性回归结果，不是重跑200项后的改善数据。
3. Python直接OpenAI适配器不再忽略HTTP200流内错误，也不在错误后发出成功usage/done；复用已有Anthropic的固定脱敏异常策略。默认／快速模型路由及压缩调用方沿用原失败或回退处理。此缺陷未被认定为旧Go托管运行失败的原因。
4. 内置摘要模型只接收选定的历史来源及专用摘要指令；移除来源之外的Agent系统指令、`Current request`和完整工具schema，保留历史工具调用、失败标记、名称和原生图片。当前请求仍留在原Surface，Go准入、计量及模型配置沿用原路径。C75旧输出接受回放仍能复现约束丢失；输入隔离不等于已证明摘要语义无损。
5. 三个过期的跨进程压缩测试仍使用不带来源引用的旧Manager历史，其中一个查询已废弃的Python表；实际均失败。合并为自动／手动两个场景，改用Workbench、SessionRunner和公开CompactConversation，验证Go原生持久化、Python重启及原事件前缀，删除重复转换和固定收据写入代码。旧失败日志及旧收据保留。
6. 根Trace此前只在完成／失败终态中写入，普通取消与停止后取消恢复都可能丢失它。现在Go创建真实root后，先经原lease围栏持久化到既有heartbeat，再执行模型／工具；各终态复制原生root，同lease不能换根，新lease不继承。写入失败时保留永久Memory阻断，重开Session也不触发反思；阻断记录也写不成则保留非终态lease等待恢复。旧无root记录及极早取消窗口仍可能UNKNOWN，不补造旧194证据。

Python相关回归为183通过、1跳过，exit0；Go sandbox/tools/hooks三个包均exit0。
流内错误修复扩展既有公共适配器测试，修复前OpenAI两例失败、Anthropic两例通过；修复后provider／compactor／context／router相关92例通过，exit0，JUnit保留在`.scratch/p9-final-code-delivery-v1/`。
真实OpenSandbox Runner回归exit0：UID65534、root所有挂载、Bash status与子目录Git diff检查通过；fixture文件/索引不变，两个owned沙箱已停止，控制服务恢复exited。复用已有镜像，没有拉取或重建。
当前源码离线重编译的小型回归探针与实际测试二进制逐字节相同，绑定记录为`.scratch/p9-final-code-delivery-v1/source-binding.json`。
完整源码CI按后续提交独立记录；上述修复不回改旧200项的质量结果。

摘要输入隔离相关Python回归159通过，exit0；上述两个Go/Python跨进程场景通过，exit0，每例保留25条原事件、最终43条事件。上游使用确定性fixture，仅证明集成行为；真实provider摘要质量仍待独立验证。来源、命令、失败与成功日志及checksum入口为`.scratch/compaction-quality-20261105/validation.json`。

C75来源的独立真实回归在原BeeAPI通道返回HTTP502，记为`provider_transient_unknown`；原Session及Admission前缀核验通过，调用已结束、任务槽已释放，未知用量和预留保留。该回归不加入原200／211分母；失败原件在`.scratch/compaction-quality-20261105/provider-primary-v1/`。
按已授权规则改用cc-switch的DeepSeek配置前，修复Go不接受`max`及输出token字段不兼容的问题，并使摘要遵守全局thinking开关；Anthropic的thinking预算受既有输出上限约束，保留输出空间，Go拒绝越界的规则不变。DeepSeek的最高档是`max`，`xhigh`会映射为`high`，Chat API使用`max_tokens`，依据[官方思考参数](https://api-docs.deepseek.com/guides/thinking_mode/)和[Chat API](https://api-docs.deepseek.com/api/create-chat-completion/)。替代通道的实际结果另行记录，不把配置可用当连通或语义通过。

兼容性相关Python回归158通过、Go准入完整包通过，均exit0。覆盖全局thinking开关、Anthropic输出上限4096／8192／131072下的实际托管请求序列化，以及Go对DeepSeek最高思考档和输出上限的覆盖；Go预算拒绝不放宽。Anthropic开启thinking时，两种出站路径均省略不兼容的temperature，依据[官方采样限制](https://platform.claude.com/docs/en/build-with-claude/thinking#sampling-parameters)。入口为`.scratch/compaction-quality-20261105/fallback-validation-v2.json`，此前预算／采样失败回归均保留。
摘要来源修复提交`4593abcf`的首次CI因一个CLI fixture仍匹配旧prompt而失败；修复fixture后，原CLI跨进程测试通过，`04abc1a8`的[完整源码CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/41892194420)五项全部成功。保留原失败，后续兼容性改动另验，不把这次CI绑定到未运行的源码。

兼容性提交`64a16cb9`的[完整源码CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/41895017251)五项全部成功。其冻结真实回归已读取cc-switch的DeepSeek配置，通过Go发送`deepseek-v4-pro`、thinking开启、`reasoning_effort=max`的一次逻辑请求；上游返回HTTP402／`provider_http_unknown`，没有摘要。两次回归均已停止、ActiveCalls=0、任务槽空，原Session／Admission前缀不变，未知用量和预留保留；不是模型质量通过。技术结果及hash入口为`.scratch/compaction-quality-20261105/provider-fallback-outcome.json`，不检查账单，也不重复探测失败通道。

后续只读Trace核验复用了完整211次原生收据、3525个span和938次准入／物理attempt，确认939个logical intent中的唯一额外项仍是旧187；原194仍缺原生root，绑定为210／211，不能按现有v3契约升级PASS。新诊断在`.scratch/compaction-quality-20261105/trace-gap-review-v1/`，不修改旧结果。
取消修复的相关Go回归43个顶层用例通过（含子例85项），另补双写入失败边界通过，均exit0；覆盖真实SDK span与原生根一致、取消／恢复幂等、同lease冲突拒绝及重开后的零模型调用。早期完整Session测试因审查发现Memory恢复绕过而主动停止，exit-1，未记为通过；修复后的完整源码CI单独验收。入口为`.scratch/compaction-quality-20261105/cancellation-validation.json`。

后续发现Go托管客户端没有读取`ANTHROPIC_THINKING_BUDGET_TOKENS`，摘要把配置的130048回退成10000；现在复用原解析器读取该配置，仍由摘要输出上限截断和Go准入校验约束。原公共gRPC测试复现1失败／5通过，修复后相关Python回归181通过，exit0；首次回归命令因误写不存在的测试路径退出4，未算通过。记录在`.scratch/compaction-quality-20261105/aliyun-fallback-v1/budget-validation.json`。百炼替代通道的真实结果独立记录，不把本地回归当模型质量通过。

随后在`afe78d02`独立冻结cc-switch的百炼Anthropic通道，沿用原凭据及端点，将已下线的preview别名明确映射到`qwen3.8-max`，不改全局配置；依据[官方别名说明](https://help.aliyun.com/zh/model-studio/token-plan-personal-overview)。实际出站草稿开启thinking、总输出上限131072、思考预算130048；这是现有预留1024输出策略下的最高legacy预算，`xhigh`仅为[官方接口默认值](https://help.aliyun.com/zh/model-studio/anthropic-api-messages)，不冒称服务端实际采用证明。一次Go准入请求返回HTTP403／`provider_authentication_unknown`，没有摘要，具体鉴权原因UNKNOWN。已释放自身任务槽、ActiveCalls=0、owned进程退出；原工作负载及Admission前缀不变，原unknown和新增预留保留。

三条通道的失败完整保留为3／3，未重试无变化的失败通道、查询账单或重跑200项。联合技术核验exit0只证明失败、计量连续性及收尾记录一致，模型语义验证仍BLOCKED；入口`.scratch/compaction-quality-20261105/aliyun-fallback-v1/outcome.json`，SHA256 `53463455b727e6a85944622c7fa47b866e3ca539e5e83fcd687a1f5bd7b0bfdf`。开放票只剩#27及用户暂缓的#20，后者没有被恢复。

取消持久化修复`774e2ed0`的[完整CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/41897734205)及配置预算修复`afe78d02`的[完整CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/41900806198)均为五项成功；后者包含完整Go测试／vet、Python、前端／浏览器／build、Windows启动和文档检查。技术失败联合核验及票面整理分别经Standards／Spec审查通过；源码CI不改变上述真实回归3／3失败或历史验收缺口。

## 可追溯产物

本机原件均保留于忽略目录；下列为相对仓库的入口，不含凭据或模型答案。

| 产物 | 路径／SHA256 |
| --- | --- |
| 最终运行／退出 | `deployments/docker/.local/receipts/product-long-dialogue-20261103-v12-resume-08/go/`下的workload-summary、final-session、final-admission、driver-stop和exit-denominator |
| 全部物理Trace | `.scratch/issue27-resumable-20261103/p9-final-capture-v1/actual-final-v1/index.json`；全部211树为新采集，保留旧取消关联缺口 |
| 211原生核验 | `p9-final-offline-v1/actual-final-v1/native-receipt.json`；`b45811ecd2ce79ee589e9f9733d04b4da0a738e65c9f026334c39bc37be1c672` |
| 累计联结 | 同目录`integrity.json`；`35d05d25bbc41004336f35bfb76012864f774210abb9eb123cb82e9d8cbb90ca` |
| Memory来源 | `p9-final-offline-v1/memory-final-v1/memory.json`；`c21b04bcdc32138d0584dd82144bac42056c719f1baf66062c207a6b103e2cce` |
| 最终语义 | `p9-final-semantic-assessment-v1/index.json`；`b63ef5200469055d1c240b17447580e5b2eff2f817250f9073902e896ceae92f` |
| 耗时定义和逐Run区间 | `p9-final-offline-v1/elapsed-windows-v1.json` |
| 真实Git回归 | `deployments/docker/.local/receipts/git-trust-regression-20261105/receipt.json`；`9bd357ca04424c17dca3b5dd028480caa42c2b16cab4a8e16da0baed5ee4dbd7` |

上表缩写的审计目录均在`.scratch/issue27-resumable-20261103/`下。
负完整性报告声明disposition3，实际PowerShell外层返回1，未记为通过。Memory首次便携Python启动因隔离导入失败且未运行observer，改用既有venv后真实捕获exit0。
语义提取器v2仅增强output/error并存时的展示；此前误称9480/9544实际漏错已纠正，v1原本能显示这两条错误，旧记录未覆盖。

完整七lane发布及暂缓的#20继续单独验收；普通修复不等待它们。#27剩余的是摘要约束/事实质量和已知Trace证据缺口，不再是“200项尚未执行”。
