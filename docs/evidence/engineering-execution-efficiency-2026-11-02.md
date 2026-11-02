# 工程执行提效：实施与证据边界

范围：[规格#42](https://github.com/huadeng408/CodeOps-Agent/issues/42)、
[公共验证入口#43](https://github.com/huadeng408/CodeOps-Agent/issues/43)、
[夹具构建复用#44](https://github.com/huadeng408/CodeOps-Agent/issues/44)、
[可靠复用与实测交付#45](https://github.com/huadeng408/CodeOps-Agent/issues/45)。

用户已确认先工程执行、后产品运行提效；首阶段交付公共入口、前后实测和一次新鲜完整回归。
新增测试边界为公共验证CLI到Verification Receipt、日志checksum与真实子进程最终状态。
输入未变才可复用明确通过的定向验证；无法确认或输入变化就新跑，最终完整验收强制新跑。

## 当前状态

- 三张任务已发布并读回；#45原生依赖#43、#44，#43与#44可独立实施。
- 公共验证入口已实现；首次高层边界定向检查为15例：14通过、1例因POSIX平台条件跳过，退出码0。后续组合、负例修复及完整回归分别见下文。
- 夹具构建复用已实现；旧v1补充自检曾受Windows防护阻断，后续v2同条件比较与复制隔离自检均已通过，原失败记录保留。
- 严格定向验证复用已实现；普通Go／pytest、真实外部服务、故障、性能和最终完整验收不自动跳过。只有Windows Job约束与明确输入闭包满足时才运行相应工程检查或复用结果。
- 本文不声明真实Agent策略收益、200轮产品验收或完整发布通过。

## 公共入口的定向证据

封存产物：[engineering-verification-entry-v1](../../.runtime/eval/agent-experiments/artifacts/engineering-verification-entry-v1/summary.json)。
`python -m pytest -q tests/engineering/test_verify.py`得到14通过、1跳过，退出码0；类型检查与后续相关定向检查的命令、原始输出及checksum保存在同一产物中。
该记录绑定成本门槛接入前的源码；后续复用接线及最终源码需要各自的验证，不能仅继承此处通过结论。

组合验证：[engineering-targeted-v1](../../.runtime/verification/engineering-targeted-v1/summary.json)。
通过公共CLI执行`python -m pytest -q tests/engineering --durations=15`及`git diff --check`，2项检查均新执行且通过，入口退出码0。
pytest完整分母为48例：47通过、1例POSIX平台拒绝检查在Windows跳过，0失败；耗时162.87秒。
覆盖收据布尔数值／无效时间、原日志损坏、源码／配置／指令／命令／文件清单／实际部署依赖／工具／环境变化，
以及普通Python／pytest强制新跑、成本门槛、完整模式连跑两次均实际执行全部7项命令。
Go／pytest隐式早停的真实失败均先复现，再保留已观察到的失败计数并将未确认的完整分母记录为unknown。

3个新增运行模块的`python -m mypy --follow-imports=skip scripts/verify.py eval/harness/process_tree.py eval/harness/verification_reuse.py`
最终退出码0；初始5个类型错误及修复后的输出均保留。精确暂存31文件的非披露秘密扫描未发现匹配，暂存diff检查退出码0；提交前仍复核最终暂存内容。

## 定向复用的单次实测

[static-reuse-measurement-v1](../../.runtime/eval/agent-experiments/artifacts/static-reuse-measurement-v1/summary.json)
在相同父环境、工作目录、工具与输入下顺序运行预热、新跑、复用，3次退出码均为0。
输入是一份含100,000个常量声明的Go文件，用于观察检查成本大于输入核验成本的情形；不代表日常小文件或完整测试套件。

| 样本 | 新检查进程数 | 复用数 | 端到端秒数 | 命令执行／复用核验秒数 |
| --- | ---: | ---: | ---: | ---: |
| 预热，单列 | 1 | 0 | 1.734 | 执行0.406 |
| 新跑 | 1 | 0 | 1.703 | 执行0.375 |
| 复用 | 0 | 1 | 1.016 | 核验0.188，无新执行耗时 |

复用核验包括原收据校验、当前输入捕获及匹配，低于本例0.300秒的成本门槛（原执行耗时的80%）。
这是单次局部样本，保留既有OS／Go缓存，不能外推整套工程或模型任务的提速。

冻结后补测：[static-reuse-measurement-v2](../../.runtime/eval/agent-experiments/artifacts/static-reuse-measurement-v2/summary.json)
使用工具链修正后的实现与Go 1.26的gofmt，同一输入的预热／新跑／复用分别为1.734／1.719／1.031秒，3次均退出0；新跑执行1次，复用执行0次并引用1份原记录。
v1与v2分别保留各自工具指纹，不跨版本拼接加速率。

编译清单和强制编译修正后的最终Python实现另绑定
[static-reuse-measurement-v3](../../.runtime/eval/agent-experiments/artifacts/static-reuse-measurement-v3/summary.json)：
预热2.906秒、新跑3.203秒、复用1.813秒，3次均退出0；实际执行数1／1／0，最后一次复用1份原始通过记录。
不同样本的主机经过时间有变化，分别保留，不选择较快一轮代替完整记录。

## 夹具构建复用的实际运行

封存产物：[fixture-build-reuse-v1](../../.runtime/eval/agent-experiments/artifacts/fixture-build-reuse-v1/summary.json)。
该范围使用既有协议程序与公共pytest入口；没有调用真实模型、Docker或账单接口。

| 运行 | 案例完整分母 | 实际Go构建次数 | setup / call / 总经过时间（秒） |
| --- | --- | ---: | --- |
| baseline | 5通过／0失败／0跳过 | 5 | 30.529 / 25.732 / 60.102 |
| optimized | 5通过／0失败／0跳过 | 1 | 34.104 / 28.109 / 75.292 |
| optimized-v2，恢复原编译目录语义 | 5通过／0失败／0跳过 | 1 | 28.136 / 39.706 / 78.036 |
| 定向功能回归 | 12收集：11通过／0失败／1 POSIX跳过／0未运行 | 本次模块编译 | 总182.538 |

跨上述重复执行共27次pytest案例执行，26次通过、1次跳过，对应17个唯一选中案例。
这不是27个不同任务；另有3项失败／受阻补充检查单列在原artifact，不能从分母中删去。
每侧运行中源码未变化，三侧Go夹具源码SHA一致；实际构建启动与结束记录分别为5／1／1。
原有42个测试函数和非夹具AST节点在该优化切片中保持；之后的公共入口异常修复另保留自身红绿记录。

**结论限于实际消除了重复构建，没有证明端到端提速。**
三侧完整父环境摘要不同，原wrapper没有记录实际cwd／GOMOD；首次模块级准备还改变了隐式编译目录，随后已显式恢复。
因此不能将上述经过时间作为严格受控的加速率，也不能用局部构建耗时推导整套测试的收益。

## Windows防护影响

补充自检读取共享测试程序时，系统返回文件包含病毒或潜在垃圾软件。
只读元数据将该生成物识别为`Trojan:Win32/Bearfoos.A!ml`；本文不据此断定其为恶意程序或已证明误报。
5个案例副本已观察到不同文件身份且链接数各1；但共享模板随后不可读取，额外的字节修改／删除后重验未能完成，未发生该自检的二进制修改。

原样保留防护记录、读失败和未完成范围，没有更改保护、恢复隔离、添加排除规则或修改二进制以规避检测。
其余不依赖此生成物的实现与验证继续推进；本机完整验收仍需如实包含后续实际状态。

## 夹具的同条件补测与隔离读回

[fixture-build-reuse-v2](../../.runtime/eval/agent-experiments/artifacts/fixture-build-reuse-v2/summary.json)
在一个父进程中冻结相同环境、pytest参数与basetemp路径，顺序运行两侧；阶段后将专属临时目录归档，未删除旧物料。
两侧采用包含最新异常收尾修复的同一Go驱动源码，只改变fixture构建策略；均为5例全部通过、CLI退出0。
独立读回原始Go日志确认：实际编译目录、GOMOD／GOWORK、Go 1.26 SDK指纹及驱动源码指纹相同。

| v2运行 | 实际构建启动／结束 | setup / call / teardown秒 | 外层端到端秒 | Go构建秒 | 元数据探针秒，单列 |
| --- | --- | --- | ---: | ---: | ---: |
| baseline | 5 / 5 | 10.622 / 24.516 / 0.002 | 40.632 | 8.267 | 0.695 |
| optimized | 1 / 1 | 2.806 / 21.802 / 0.002 | 28.801 | 1.887 | 0.116 |

这是同一主机的单轮pilot，沿用既有缓存，系统及防护竞争未假定消失；报告观察值，不外推固定收益。
本轮5份副本与模板均为不同实际文件、链接数各1；覆盖／删除单份副本后的隔离校验及6次原生协议检查通过。
两侧公共入口均确认拥有的进程树已停止，源码运行前后不变；临时baseline源码最终恢复到原优化版本的相同SHA。
60项checksum独立核验一致，防护配置及隔离内容未更改；v1受阻记录不改写为通过。

补测还暴露并修复了实际工具链识别问题：PATH内Go 1.24.5与项目声明1.26不一致。
完整模式现在选择已安装或已缓存的实际SDK并记录二进制指纹，所有子进程使用同一版本，保持离线执行。
公共完整模式验证先真实失败，再通过两次7项检查；未缓存SDK拒绝和只读工具副本失效的定向负例也已通过。

## 原有bug修复的证据继续保留

- 无改动候选的独立评分与schema兼容、启动时保护yielded根任务及借用子Agent的围栏，各有原红绿和限定范围的运行产物。
- 新的非交互审批立即拒绝；旧pending经原实体CAS追加拒绝决议；普通交互审批保留。
- abandon之后的inspect从canonical finalization投影，保留实际Run状态；不以请求动作伪造终态。
- 缺失或损坏最终回执时保留可信started身份，经Go公共inspect／abandon／inspect收尾；原尝试保持infra_error／unknown，不制造任务评分。

这些修复当前的定向结果不代替本阶段要求的新鲜完整回归；原三组模型实验、原长对话Session和七条发布lane仍各按声明范围验收。

## 后续定位的无效等待与重复构建

[sandbox-readiness-fixture-v1](../../.runtime/eval/agent-experiments/artifacts/sandbox-readiness-fixture-v1/summary.json)
保留了公开CLI的3秒超时RED，修复后同一Native拒绝用例通过，测试事件耗时0.05秒；原运行约60.03秒。
共享选择器仅对既有规则定义的永久错误立即返回，Docker／OpenSandbox／Windows WSL的动态不可用仍按原机制重试。
readiness、取消及8个共享选择调用等共15个终态事件：14通过、1例因本机符号链接条件跳过；副本覆盖／删除／新拷贝自检另1例通过。

同一固定三例、父环境与实际Go 1.26工具链的比较：

| 运行 | 测试／包分母 | 真实构建启动／结束 | 构建秒 | 探针秒 | 公共入口墙钟秒 |
| --- | --- | --- | ---: | ---: | ---: |
| baseline | 3通过、1包通过 | 3 / 3 | 4.364 | 0.577 | 34.626 |
| optimized | 3通过、1包通过 | 1 / 1 | 1.786 | 0.225 | 33.618 |

仅描述本次约1秒墙钟差异，不以构建次数降幅替代端到端收益。缓存只保存固定程序字节及构建／清理错误，每例独立文件和数据；没有共享可写二进制。
首次31.058秒样本的计数wrapper被Go自动前置的GOROOT/bin绕过，实际构建次数为unknown；原样保留，不能记为0。
有效比较使用标准`go test -exec`入口恢复相同的测量PATH；源码、编译目录和GOMOD均核对一致，87项checksum独立核验通过。

## 第一轮完整回归与入口修正

[engineering-full-v1](../../.runtime/verification/engineering-full-v1/summary.json)
源码在运行前后保持一致，7项均执行并确认拥有的进程树停止：5通过、2失败，入口退出1。
Go测试退出0，2,182通过、87跳过，47包通过、13包无测试；vet、Agent构建、Python导入与diff检查均退出0。
pytest完整分母为2,857：2,837通过、1失败、19跳过，31条warning，退出1。
唯一失败是排队测试的临时仓库未提供新增SDK预检需要的`go.mod`，修正fixture后同一公开边界通过。

另一个失败来自旧编译命令递归进入Git忽略的SWE下载仓库，遇到第三方故意错误语法样例。
入口已改为保存Git记录及未忽略新增的项目Python源码清单，再逐文件编译；公开完整模式验证忽略物料不会被扫描、项目坏源码仍会失败。
审查进一步复现了同mtime旧pyc掩盖坏源码的假通过，现使用`compileall -f -i`强制编译；diff检查使用`git diff HEAD --check`同时覆盖暂存和未暂存内容。
旧入口将这两项负例误报为7/7通过的记录及修正后的RED／GREEN均保留在开发日志中，不升级为产品证据。
同一公开负例修正后通过，退出0；当前215份项目Python源码的强制编译经公共入口单独确认通过，
见[project-python-compile-v1](../../.runtime/verification/project-python-compile-v1/summary.json)。

上述第一轮结果绑定优化前源码，失败和完整分母保留；后续修复及最终源码需要新的完整回归，不能将此运行改写为通过。

## 最终源码验证与交付

[engineering-full-v2](../../.runtime/verification/engineering-full-v2/summary.json)
通过同一公开入口执行最终源码的固定7项检查，全部新执行、退出码均为0，无复用、失败或未运行项；每项拥有的进程树均确认停止。
命令为`python -m scripts.verify --plan .runtime/verification/full-plan.json --output .runtime/verification --run-id engineering-full-v2`。

| 检查 | 实际结果 | 经过秒数 |
| --- | --- | ---: |
| `go test -vet=off -p 1 -json ./... -count=1 -timeout=30m` | 2,183通过、0失败、87跳过；47包通过、13包无测试 | 1710.266 |
| `go vet -p 1 ./...` | 退出0 | 338.515 |
| `python -m pytest -q --durations=20 --durations-min=1` | 2,838通过、0失败、19跳过；31条warning，完整分母2,857 | 1872.922 |
| Go Agent构建 | 退出0，产出本轮实际`bin/agent.exe` | 56.172 |
| 项目215份Python源文件强制编译 | 退出0，源码清单及SHA随产物保存 | 1.265 |
| Python运行入口导入 | 退出0 | 3.469 |
| `git diff HEAD --check` | 退出0 | 0.219 |

源码绑定为原HEAD `427ca67375d8e5293da9252031b8a53e2e77a154`加本轮实际工作树，运行前后pin一致；
受测暂存tree为`262b95a2a2854701aba4f606c4973a8b9fcecbaf`。
运行后34份working文件SHA与预冻结记录逐项相同，所有产物checksum独立核验通过；非披露秘密扫描34文件无匹配，暂存diff检查退出0。
两路独立审查最终均无剩余finding；此前pyc、错误分母、工具链和进程边界问题的红绿记录仍保留。

收据SHA为`021159377c08c88009048b47bad79304c5e69c4d21eb4fff0ca4dd8043e55c52`，
校验清单SHA为`57663fac095e7311a6b6b6461432650c835f8334fcdd56ad7b081e9e5ad1d656`，
Agent二进制SHA为`2cf985c455fbd873b092ffeab4ba57a3aae18f351af69554c4b250737ec26ff5`。
实际SDK为Go 1.26.0，SDK指纹另保留在原始收据；构建产物供后续绑定核对，不重复构建替换。

本节是运行后补写的唯一普通工程证据文档；源码、配置、指令和测试保持受测内容。
提交后的来源绑定另存原blob及原／当前tree和hash，保留原始full pin；该记录不伪造一次新运行。
工程阶段结果不代表#39／#40／#41真实模型实验、#27长对话或七条发布lane完成；这些范围继续独立验收。
