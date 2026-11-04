# 长对话第194项故障与有界恢复

GitHub-Issue: https://github.com/huadeng408/CodeOps-Agent/issues/27

本记录区分故障段原结果、源码修复、副本验证和实际 Go 恢复。完整200项、
累计原生／Trace／Memory及资源验收仍未完成，#27保持OPEN；完整发布另按七条lane验收。

## 实际分母和原故障

- 原Session跨冻结段完成193/200；201个实际Run为193完成、7失败、1取消。
- 最新产品段是`product-long-dialogue-20261103-v12-resume-06`，
  运行源码`0913b53020d0dace08ac512d7a8d53850d47cd3f`，
  输入SHA-256 `016fb2b3f27f25578af5935a30dc4861ea6dabfc6a29073ff822806e2bd8150a`。
- 第194项实际Write已成功，后续Run未正常终结。一次真实产品UI取消返回202，
  原Run随后为`canceling/unknown`；已知写入和工具结果均保留，未重放工具。
- 原驱动只执行一次`done`，exit1。浏览器和owned产品进程实际停止，
  `observer_errors=[]`；因当时ActiveCalls=1且任务槽未释放，控制服务恢复被拒绝。
  原`resources_restored=false`不回改，底层卡住原因仍为UNKNOWN。
- 故障段完整导出为Session8987条、Admission11936条。冻结backend读取实际采集
  7个终态Run／206 spans；第194项另存32个已结束spans，根为ERROR，属于partial观察。
- 72次压缩已审至C70–72；它们的原生序号为8521、8878、8979。
  C72为1个direct source／35个展开引用。历史loss、unknown和当前恢复限制保留。

## 源码修复范围

工程状态为IMPLEMENTED，以下定向验证不代替新源码CI、产品运行或完整发布。

| 修改 | 机制与验证范围 |
| --- | --- |
| 当前Run取消收尾 | 区分当前Run和其他历史Run的租约；历史外部资源仍保留unknown围栏。原代码RED复现；Session包375通过、4跳过，exit0 |
| 模型计量收尾 | 每attempt惰性共享一个5秒窗口；成功reservation不再被事后可取消重读隐藏；bound观测只经一个finalizer持久化；失败保留活动围栏并输出固定脱敏诊断。原代码RED复现；Admission包311通过、5跳过，exit0 |
| Bash工具指引 | 只读挂载报错后使用完整formatter stdout和已有授权Write／Edit路径；权限、参数和执行逻辑未改。相关Python调用方184通过、1跳过；真实registry／system／OpenAI和Anthropic payload只读断言exit0。未声称模型耗时或token实测收益 |
| Go取消恢复入口 | 严格绑定原owner／actor／cancel ID、原生前缀和head、完整工具结果、物理停止证明；只闭合已有且唯一匹配的commit，再追加原Run取消终态。15项域测试通过；fixed helper后缀42项、进程实例27项纯测试通过 |
| CLI最终收据 | 固定原Run身份，并从同一已验证snapshot投影Run状态和checkpoint，避免超时／轮询的旧值或零值与最终Ledger边界混用。旧任务清理fixture同步到当前Run的canceled／unknown语义；foreign资源围栏及后续拒绝断言保留 |

源码及恢复程序均经过独立Standards／Spec审查。早期helper的abandoned后缀识别缺口、
错误静态分母以及真实PID复用预检失败均有独立旧版本／NO-GO／失败工件，不以成功覆盖。
应用补丁时的一次错误落点被编译拒绝，纠正后五个Go文件与已审查候选逐字节一致。

第一次本机完整验证入口的Go日志保留2271个pass、89个skip、24个fail终结事件
（含父测试和子测试）；旧任务清理fixture和CLI收据问题由这次组合回归发现。
Go阶段结束后，入口在Python检查期间经原执行通道中断，exit1，未生成最终summary；
因此这次本机完整验证不记为PASS，Python完整分母及剩余检查记为未完成。
后续修复使用相关高层用例定向回归，最终完整源码回归由新提交对应CI单独证明。
组合修复后的定向入口`cross-module-cancellation-targeted-v1`实际exit0：
36个测试终结事件全部通过、无跳过，覆盖3个Go包；受影响包`go vet`及`git diff --check`
也均exit0，三个检查均确认子进程结束。该结果只证明定向工程范围。

## 实际副本和原库恢复

所有原生事实写入经Go入口；没有host SQL追加、假lease ownership、新Session、
模型／工具重放或预算重置。同一本机账号的登录凭据经已有Go入口恢复，UID、用户名、
角色及原Actor绑定不变；凭据仅保存在进程内，不进入本记录。

1. v4只读预检exit0，原库前后head完全一致。PID数字被新进程复用时，使用可信创建时间
   验证原实例已停止；未知状态仍拒绝，不停止无关进程。
2. 固定Ledger副本实际恢复exit0；重复执行exit0且零追加；错误鉴权实际exit1，
   副本和原库文件均不改变。独立Go读取器完整导出、前缀和checksum链核验通过。
3. 原库`real-one-apply`实际exit0，仅追加10条事实：7个原结果的`commit_done`、
   原Run的`session/run-canceled`、原调用的`admission/call_unknown`及正常`task_finished`。
4. 独立Go只读导出exit0：Session8995／Admission11938，ActiveCalls=0、任务槽空。
   原批次、已确认用量、完整预留、unknown成本状态及受保护1640条原事实不变。
   原Session累计821次reservation中809已settled、12已结束unknown，无活动调用。
   工具`commit_pending`与`commit_done`均为557项。

原第194项为取消且已确认停止，仍未合格；无新增assistant成功或Memory反思。
该项Memory明确为`NOT_RUN_ON_CANCELED_RECOVERY`，原执行TraceContext未知，
不以恢复helper的源码或Trace冒充原模型执行来源。新194尝试仍需普通Memory及工具验收。

## 证据指针

原故障工件位于`deployments/docker/.local/receipts/product-long-dialogue-20261103-v12-resume-06/go/`，
原始`driver-stop.json`、`exit-denominator.json`和两个final导出保持不变。

本次恢复位于`.scratch/issue27-resumable-20261103/round194-go-recovery-execution-v1/`：

- `08-original-ledger-v4-stdout.json` SHA-256：
  `869c76f71e20d67d65278a39de31933403a13a623c12d4305c80007ec7ce786e`。
- `native-after-original-v4/session.json` SHA-256：
  `62129ebb0eccc6132920760cb1648499b72a9f8cb1ee3785f39ed5eb58ab2508`。
- `native-after-original-v4/admission.json` SHA-256：
  `452137156511846f4e0ce0ea4c4325669050a9ae5fbe9cb63bfeb04fe5612d9f`。
- `native-after-original-v4/receipt.json`保留完整计量、原前缀和受保护围栏观察。

副本结果的独立核验见
`.scratch/issue27-resumable-20261103/round194-go-recovery-copy-spec-review-v1/NATIVE-READBACK-ADDENDUM.md`。
下一段需绑定真实恢复、控制服务状态、新源码CI与便携包后再冻结；原失败段和完整分母继续保留。
