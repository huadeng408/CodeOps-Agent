# 同一 Session 长对话：当前断点

范围：[#27](https://github.com/huadeng408/CodeOps-Agent/issues/27)，同一逻辑200项、同一原 Session 分段续跑。
当前 **193/200合格；202个实际Run＝193完成、8失败、1取消**。第194项累计attempt2失败，195–200未运行；下一项为194累计attempt3。

最新执行段 `product-long-dialogue-20261103-v12-resume-07` 绑定源码 `2c29eee6a5e7d47c393ebb8a59e2ed4f57ea07cd`，
真实使用既有Windows包、Go Harness和OpenSandbox。前导换行的恢复交接修复后已实际冻结、登录原账号并续行；没有重新执行1–193。

第194项attempt2的最后一次前台调用返回HTTP502，用量unknown；随后Memory反思也返回502。
Skill1、Read2、Bash1都有已知exit0结果，Bash已确认停止，本次没有Write。两个unknown及原预留保留；
前台502是本次直接失败，最初attempt1停滞的底层原因仍unknown。

本次Run耗时869.734秒，两个审批pending到approved合计699.101秒（80.38%）。
这只说明本次审批等待占比，不能外推全部历史或当作模型推理时长。

正常结束代码任务的真实UI请求返回HTTP200，ActiveCalls0、任务槽空。Phoenix仍在时，冻结后端读取入口实际采集本段1个Run、21个已结束span，退出0；采前后原生heads不变。
唯一的done实际退出0，拥有的产品／浏览器已停止，两个控制容器恢复原exited状态，没有活动task／egress，observer／cleanup错误均为空。

| 原生边界 | 计数／head | checksum |
| --- | --- | --- |
| Session | 9130／9129 | `9b3b311f80213f9b363bdbf34c238a3b0370fc99e8331c393680f9933b91e126` |
| Admission | 11954／11953 | `de4be58c7852d9334b6706c946a6825ab3f38004ea4d87d6bf312cb78e980b1d` |

本机原件保留在 `deployments/docker/.local/receipts/product-long-dialogue-20261103-v12-resume-07/go/`：

- `driver-stop.json`：`d3985869212348f01f496b7661f3e178d9a3752699ce75bc93b65d28e5a8461a`。
- `exit-denominator.json`：`a8a833132aae8eb78f26a82647aade6310c952d8f69faa33c92a28e6712ac0be`。
- `backend-index-1.json`：`2a7b5f3bd29a36608d9d08234e9db4a653c3ac08de0dd72669ea6d546477ee15`。

剩余范围：共享有限重试判定、后续7项、compaction73语义审计、完整累计原生／Trace／Memory来源与最终验收。
此前201项原生核验、旧取消恢复、失败stop及全部unknown保留，见[194项原故障恢复](product-long-dialogue-fault194-2026-11-04.md)。
本次正常封存不等于失败轮次合格，21个span不是累计202个Run的完整Trace验收。
