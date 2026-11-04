# 同一200项任务：186项封存与源码修复

本记录区分实际模型运行、原生完整性审计和源码修复验证。
[#27](https://github.com/huadeng408/CodeOps-Agent/issues/27)保持OPEN；完整200项与七条发布lane尚未通过。
原v12的123/200结果及所有后续失败文件保留，未改写成成功。

## 当前实际边界

原Session仍为`a7b27cbd0ff3ce9af1f2ae2908861cfa`，同一200项输入跨冻结执行段续跑。
完成前缀不重跑，实际失败、未知用量与原预算预留不清零；模型保持grok-4.6、
xhigh和thinking开启。服务商账单不在本轮调查范围。

| 完整分母 | 已验证事实 |
| --- | --- |
| 200项逻辑任务 | 186完成；187首次失败且无assistant；188–200共13项未运行 |
| 193个实际Run | 186完成、7失败；下一项为187的累计attempt2 |
| 原生模型与工具记录 | 763个准入调用、763个物理attempt、506个工具调用匹配；11个已结束usage unknown保留 |
| 最新原生终态 | Session7985事件、head7984；Admission11790事件、head11789；受保护旧Session1640事件及启动围栏不变 |
| 计划边界 | 40、80、100、120、160均已有实际证据；后续段复用这些边界，不再重启100 |
| 第187项副作用 | 21个工具请求与结果配对；两次Edit失败、一项Write和一项已停止且exit0的Bash均保留；当前工作文件仍须在新段前核对 |

最新段使用源码`751ac92aad76b0613ed8508f297a5317bd039297`及缓存v20包。
已通过实际产品正常结束任务，确认任务槽为空、ActiveCalls为0；在Phoenix仍可读时
采全193个实际Run的完整新Trace树，包括旧Run迟到的Memory和失败Run，采前后原生头不变。
随后仅一次done成功退出，所属进程停止，两个原有控制容器恢复到exited。
没有删除Docker数据或重拉镜像。

私有原件位于`.scratch/issue27-resumable-20261103/`及对应Docker `.local`运行树，
不进入提交。关键收据保留如下，路径均为该scratch目录下的相对位置：

| 原件 | SHA-256 |
| --- | --- |
| `cumulative-p5-capture-v2/partial-after187-v1/index.json` | `fa2ebddb7d57d6781446acddac418b0f645d2795304f8766c7d0bb5d0d9237ed` |
| `cumulative-p5-join-v4/partial-after187-v1/join.json` | `2d04dff26c88cabb52aef8df32ab80805d08ed99e5ba80be3292aca5a345cb18` |
| `p5-stopped-negative-seal-v1.json` | `5d1baed833193dfe6d88984c36e6818f9c0aeb7c089a2842a02b96ef63295c1f` |

该join对全部193个实际Run运行原生校验器，193/193退出0。
Trace v2则为FAIL：有764个`provider.call`逻辑意图，比763个Go准入调用多一个。
多出的ERROR叶span没有对应全局Admission记录，原归一化快照没有观察到物理子attempt。
这不是HTTP成功或模型完成证明；不能过滤此span来得到PASS。

## Trace负结果与数据保留

Python在调用Go准入接口前创建逻辑span。因此，准入前取消、构造失败或RPC拒绝可以产生
已结束ERROR逻辑意图。固定源码和公共边界fixture复现了这个机制，未证明第187项的
具体失败阶段；真实原因及账务仍为unknown，不将其填成零消耗。

正式票已明确区分逻辑意图、Go准入调用和物理attempt，并要求显式选择新的分析版本。
旧v2 FAIL保持原样。新规则不能仅凭ERROR状态放行，仍需全局Admission、完整树、
原生终态、实际源码及包、封存span身份和非敏感传输元数据证据。

审查另发现旧Phoenix读取器只保留allowlist，可能丢失其他usage／HTTP字段和links，
零值也会被丢弃。因此，旧归一化快照中的缺失字段不能证明原始数据不存在。
已从原停止容器只读复制18,018,304字节Phoenix库，容器始终保持exited；副本以
SQLite只读模式核查，原span的attributes无usage／HTTP／retry／cost／token路径，
events为空。库没有独立links存储，links继续unknown，不伪造完整观察标志。
库副本SHA为`095ebf538784bb4cdb779484bdf301185f4541dcdc02cecc9abc50df6d73c298`。
这个补充核查没有改变原捕获文件、Ledger或v2结论。

正式票另增显式`product-integrity-v3-admission-v1`的限定准入对账，正文发布后逐字读回，
SHA为`02538b4f81291355c3e7f57da069c4c7dbff34a27ffdcba215d058a4a7c9d696`。
它只在完整Go准入、实际managed transport源码／包、已停止前沿、原始属性反证及完整树均可核时，
认定某个exact call ID没有经该Go路径发起物理请求。SDK links、具体失败阶段及账务仍unknown，
不生成虚假的raw complete，不将此范围声明为完整Trace或产品验收。

新的历史离线重评已实际退出0：同一193个旧Run、2890个唯一span全部保留，
764个逻辑意图／763个准入根／763个物理attempt；1个未准入失败意图得到限定对账，
未分类意图为0。原v2与strict-v3仍FAIL，新报告metadata_complete=false。
本次没有新native执行、后端请求或模型调用，只复用同一未变封存边界的原native193/193证据。
11个unknown、7个失败、186/200及87条未确认的per-span源码标签保持原样。
`domain193-reanalysis-v1/actual-v1/report.json`的SHA为
`34ee9d8a214f38a0a75fb2eb9ffdc5d65928a501074396a5c805f853a3cb104d`；
另存`source-provenance-preservation.json`，SHA
`12073f5978e739bc9e374ef97be82a37427e549b39899a44321520331bd9e535`。
最终源码审查随后发现另一项presence漏判，旧重评源码已按原字节另存，旧报告不改写。
修复后的同边界离线重评再次实际退出0，分母及限定结论相同；没有新增native、backend或模型调用。
`domain193-reanalysis-v1/actual-v2/report.json`的SHA为
`fc5af6716dccd67fa64852ef9ed14a0ea655a80f1025b85380f213a1efde8058`。
未来新段结束后仍须真正执行全部累计N的native校验和完整新backend捕获，不能继承这193行冒充。

读取器修复只在显式v3路径、原始字段投影前保存四个固定观察字段：版本、完整性、
是否存在传输／计量字段、是否存在raw links；不保存未知属性值或links身份。
零值、false和嵌套字段仍会产生拒绝信号。缺原始attributes或显式links列表则不完整，
旧快照不能默认安全。默认v1／v2读取和序列化保持原行为。
原候选的漏审和NO-GO保留；修正后两轴独立审查GO／0项P1/P2，各重放76项纯fixture。
真实工程入口另跑新例、既有contract、reader及CLI回归，退出0；完整分母见对应收据。
读取器修复本身没有补齐旧193次历史观察；上述限定准入重评另有实际执行和原件。

这里的strict-v3与后续限定准入版本分开记录。后续限定版本经过独立审查，修正了重复JSON父键
可吞掉usage反证、本地只读SQLite辅助文件误拒、derived observation参与原body比较不一致等问题。
另外，真实第187项有两次Go lease；其terminal root合法地以较早同Run root为parent。
原raw覆盖检查的无parent假设会拒绝真实193数据，已通过新恢复反例先RED再修复。
最终151项portable用例均通过；274项项目入口回归包含这151项，不将两者相加。

最终两轴审查各报告一项相同P2：真实Phoenix读取器会漏掉allowlist外的
`llm.token_count.total=0`，以及嵌套数组内的计量／响应字段，可能使strict-v3误判通过。
修复复用同一个投影前presence检测器处理原始读取和只读库副本，保留零值、false、
空父字段及数组中的拒绝信号；移除第二份较弱扫描，默认旧版本的序列化形状不变。
两次RED分别为26失败／37通过、4失败／2通过／60未选；修复后66项定向、
146项相关纯回归均退出0。这些回归有重叠，不相加，也不替代最终独立审查与源码CI。
原两份NO-GO及失败输入保留在`final-source-review-v1/`和`final-presence-fix-v1/`。

## 源码修复及定向验证

源码修复发生在最新付费段完全停止并封存之后；没有热改冻结产品。

| 改动及边界 | 实际验证 |
| --- | --- |
| 恢复时把活动用户输入保留在已提交工具尾之前，只在发送给Python的历史中清除其事件标记；输入只出现一次且NewTurn=false，Go保留原Memory查询 | 原实现overlay真实RED，修复后公共SessionRunner.Recover→压缩回调→SQLite Ledger定向9项通过；前缀与工具配对不变 |
| supervisor的Recover预算独立为30秒，保留健康轮询、关闭与退避；连接异常记录固定安全原因，拒绝的恢复回调只收尾一次 | supervisor／任务槽26项、Memory1项、server33项通过；30秒是有界工程策略，未声称旧超时底因或长期SLA已验证 |
| ReadSpill在公共工具入口复用严格locator解析，错误裸hash返回可操作的已知工具失败 | 9项定向通过，含错误locator、分页边界及脱敏；不自动补前缀、不允许无效locator |
| 验证CLI区分完整Go测试失败与基础设施未知；要求正常FAIL终结标志，识别单／双横线failfast并拒绝panic、Goexit等提前结束 | 实际RED保留；修复后40项通过，含真实Go子进程；完整测试失败为FAIL，缺完整分母继续BLOCKED |
| 进程E2E fixture接入真实Go Gateway／准入／Ledger及回环确定性provider，复现错误ReadSpill后纠正与工具结果后的进程恢复 | ReadSpill真实跨进程用例通过；continuation在同Run中停止旧Go、重启Go/Python后完成，工具外部执行恰1次，原前缀不变 |

进程E2E使用确定性provider，属于跨进程集成验证，不是新的付费模型能力证据。
恢复断言读取公共Go Ledger及新Python进程回复，不再要求Harness托管模式下本来关闭的
私有Python第二套checkpoint事实。第187项实际坏压缩帧未留存，所复现问题族的修复
不能声称已追溯证明该次真实失败的精确原因。

公共验证入口为`python -m scripts.verify --plan <plan> --output <dir> --run-id <id>`。
定向结果目录`.runtime/verification/repairs-after-p5-v1/`保留每次命令、日志校验和、
退出码和子进程终态；早先缺model scope、错误工作目录、过时断言及有意RED均保留。
两个进程收据为`.runtime/e2e/continuation-process-recovery-active-input.json`及
`.runtime/e2e/tool-spill-process-invalid-locator-recovery.json`，均有只写一次的保留副本。

13个修复源码及14次验证记录的索引为
`source-repairs-after-p5-v1/targeted-proof-index-v1.json`，SHA
`e192c9f7759ad423dae9bca1e7e1a5282ae1bcb9eb10369d5c1da092a3a0ce73`。
两轴独立审查先保留2项P2，再对修正后的源码给出GO／0项P1/P2；
其后实际执行完整工程入口`full-regression-01`，7/7项执行：

| 检查 | 实际结果 |
| --- | --- |
| Go测试 | 命令退出0，观察2267 pass／89 skip、0 fail；一条Output日志被脱敏损坏，分母complete=false |
| Go vet | 退出0 |
| Python完整回归 | 2979 pass／19 skip／31 warnings，退出0 |
| agent构建、Python编译／导入、diff检查 | 各退出0 |

全部子进程确认停止，源码前后无漂移；整体收据仍为BLOCKED，原日志与checksum不改写。
该收据SHA为`bd065f6e13ed06491340238c5146e5dfea5969c0d3b54d93ed733778bd7abbe7`。
损坏来自对整个Go NDJSON做文本正则脱敏。最小修复仅对Go stdout先解析，再复用现有
JSONL写入器逐字段脱敏／序列化；无效原JSON始终保持unknown，并用LF分帧避免误切合法NEL字符。
stderr及其他日志路径保持原行为。先保留RED与一次fixture编码失败，再通过独立31项公共fake-Go回归；
真实middleware完整7项及公共CLI42项修复回归均退出0、分母完整。
`.runtime/verification/repairs-after-p5-v1/log-json-regression-01/`保留这次实际修复验证。

显式分析版本另通过274项相关回归，退出0、0 skip，源码前后无漂移；收据SHA为
`9017880fb2e30abf37e59f81e282d3168fb957000b04587d7dbffe8728c761ae`。
这些后续检查不回改完整入口的BLOCKED。最终提交及新源码CI另行记录，不能引用旧CI冒充。

## 摘要及后续恢复

压缩1–66的语义审计、历史loss、unknown及失败Read均保留。
C63–66的修订审查纠正了摘要前tail已含规则的误判，旧失败审查仍保留；
63条历史代表性记录不是“当前缺失63项”的穷尽统计，功能字面保留也不是测试覆盖。
Memory目录／来源结构保留不等于自动Recall完整注入或模型采用，这些因果仍unknown。

新段继续前必须完成新源码验证、审阅与包冻结，实际核对187原写入和Shell结果、
原Session／身份／任务工作区／预算及所有前驱原生前缀。每段仍保留原尝试和unknown，
未知已接受请求沿原ID对账。新的Write、Edit或Shell逐次检查实际请求／patch后走产品UI批准；
不通过宿主机代写答案、重放完成前缀或附加问题补造历史证据。
完整200及最终全分母审计完成前，不关闭#27；#20及完整发布继续独立处理。
