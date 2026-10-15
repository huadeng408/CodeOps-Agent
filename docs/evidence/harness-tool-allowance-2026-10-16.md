# #17 受管代码任务的有限工具额度

范围：修复真实浏览器分析在六轮工具交互后提前终止的问题。
工程 `IMPLEMENTED`；源码、公开协议和便携包构建范围 `VERIFIED`。
#17 的十轮真实 Go 任务仍在进行；本收据不关闭 #17，也不授予发布资格。

源码 `084c96b983cdff7cf67fb2b9cc0009a15f08cbe7`，
[源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38769966569) 四个 job 全部通过。

## 实际失败与诊断

冻结的 chi 任务先后创建两个真实浏览器 Session：
`c19e029738a477fde15074b0f584c77a` 和 `13982dcf8ef611d8629775b5e4850716`。
两个首轮分析均返回 `tool_round_limit`，没有完整助手回答或代码修改，不计为成功轮次。
两次隔离完整 Go 基线均通过；原工作副本的 99 个文件及 index 保持一致。
第一次的 Git／可选检索错误和外网读取拒绝全部保留；第二次只使用本地 Read/Grep，仍在同一额度边界失败。
真实后端分别读回 63、41 个 spans，失败根保留为错误；不把失败树称为成功或完整反思证据。

在现有 Converse + ModelGateway 外部协议接缝，七个连续成功的源码读取同样被六轮额度截断。
最小回归先得到 `tool_round_limit`，退出 1；它不依赖搜索错误、真实网络模型或浏览器等待。
只为已要求 Go 模型网关的 Harness 受管请求设置十六轮有限额度后，同一回归通过。
独立 Python runner 仍保留原六轮默认值；耗尽额度的请求仍执行无工具收尾并返回失败。
Go 的鉴权、工具授权、持久批次、并发和调用计量没有旁路或重置。

## 当轮验证

| 范围／命令 | 完整结果及退出码 |
| --- | --- |
| 原最小公开协议回归 | 1 failed，1 warning，退出 1；修复后 1 passed，1 warning，退出 0 |
| 最终额度／续行定向回归 | 8 passed、60 deselected、1 warning，退出 0；包含七次读取完成、持续请求在十六轮停止及原有续行边界 |
| `python -m pytest -q tests/test_server.py` | 68 passed、1 warning，退出 0 |
| 当前源码 CI 的 `python -m pytest -q` | Linux：2,563 passed、12 skipped、3 warnings，退出 0 |
| 当前源码 CI 的 Go／vet | 47 包通过、13 包无测试、0 失败包，退出 0；命令未输出逐个用例数量，未据此填造数量；vet 退出 0 |
| 当前源码 CI 的其他 job | Frontend tests/build 与 Windows startup contracts 均通过；完整 CI 分母 4/4 |
| Python 编译、diff／staged 检查、精确两路径凭据扫描 | 全部退出 0，扫描发现 0；私有 AGENTS.md 改动保留且未提交 |
| Windows 便携包重建 | 退出 0，3,219 个运行文件；实际运行源码与新 manifest 全部相符 |

双轴复核：Standards 硬违规 0／Smell 0；Spec 未解决项 0／范围扩张 0。
分类 `AI_REVIEWED`。提交后的三点 diff 与审阅过的 staged diff checksum 一致。

旧批次 `02cfc8d9c93975b3ea63f27cc0a3331d` 的两次失败共 16 次模型调用，
其中前台 14、反思 2，全部结算，确认 331,036 tokens；费用 unknown，历史 unknown 和原预留保留。
两个失败任务均通过产品 UI 结束，活动调用归零、任务槽释放，所属进程停止，控制服务及 Phoenix 恢复原先 exited 状态。
这些末态只描述已封存的两次失败；后续实际验收使用新收据，不改写旧记录。

新便携包 manifest SHA-256：`cb286652d117d67f9c5797434d2484c626c8885a8d845e79d7ede4248beb5333`。
Go 实际二进制 SHA-256：`c1ce7273a11c2c7e5eb705df9d5427651e990664c35d40b34bd74bb3c081ed9c`。
两个公开任务的原始输入、版本及工作副本保持相同，在后续付费运行前重新冻结；
607 个源码指纹及新运行物料核验通过，新 inputs SHA-256：
`e6f893c34d974b85fe1ab49a2f4d2faba304b11acfc86ac017d3b2152f41a22a`。

失败证据位于 `deployments/docker/.local/receipts/browser-tasks-20261015/go/v1/`，
60 项产物 manifest SHA-256：`ade8ae35e058aea95ba9ee18a484c7f3d1acfaba184fcef2b1ce04c7e70a3c63`。
源码／协议／构建证据位于同批次 `round16-source/`，22 项产物 manifest SHA-256：
`1276398edcd4833fdb48570527622f81209fe1372b02b7b1c92bde3f90984cdc`。
更多绑定见[结构化收据](harness-tool-allowance-2026-10-16.receipt.json)。

尚未以本次源码证明真实十轮、代码修复／审核应用和完整成功任务 Trace；这些继续由 #17 独立验收。
