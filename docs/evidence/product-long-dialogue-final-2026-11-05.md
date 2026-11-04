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

Python相关回归为183通过、1跳过，exit0；Go sandbox/tools/hooks三个包均exit0。
流内错误修复扩展既有公共适配器测试，修复前OpenAI两例失败、Anthropic两例通过；修复后provider／compactor／context／router相关92例通过，exit0，JUnit保留在`.scratch/p9-final-code-delivery-v1/`。
真实OpenSandbox Runner回归exit0：UID65534、root所有挂载、Bash status与子目录Git diff检查通过；fixture文件/索引不变，两个owned沙箱已停止，控制服务恢复exited。复用已有镜像，没有拉取或重建。
当前源码离线重编译的小型回归探针与实际测试二进制逐字节相同，绑定记录为`.scratch/p9-final-code-delivery-v1/source-binding.json`。
完整源码CI按后续提交独立记录；上述修复不回改旧200项的质量结果。

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
