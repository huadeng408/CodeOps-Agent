# 取消任务丢失文件读取结果后的准入封存

范围为 [#27](https://github.com/huadeng408/CodeOps-Agent/issues/27) 的 Go 恢复边界，
基点 `93e291a2067f601e02edb4aa9a59baecbf09e276`。
工程状态为 IMPLEMENTED；源码回归、不可变原生导出和一次性账本副本分别通过。
真实账本尚未封存；独立 200 轮产品验证和完整发布未运行，本记录不授予其资格。

## 行为与安全边界

本轮原始尝试仍为 [180/200、未通过](product-long-dialogue-2026-10-21.md)。
两个已派发的内置普通文件 Read 丢失原生结果，保留 `tool/unknown` 和取消 unknown；
不补造结果、停止证明或计量，不修改原 Session，不重放工具或模型。

只调整既有显式 `AbandonCanceledTask`：同一 run／调用的 preparation、pending、
dispatch、unknown、commit_done 必须各唯一且顺序正确，工具、参数、operation 身份一致，
closure 精确绑定原 pending 的序号、checksum 和 commit ID，且 dispatch 早于该 run 的取消请求。
PDF Read 会启动 MinerU，明确排除；MCP、未确认 runtime、已有结果／代码修改、重复／错身份、
缺审计事实或未取消的历史 run 也拒绝。仅未知 Read 候选需要这项检查。

普通结束、续行、Trace、提案／应用／测试、子任务、owner、活动调用和 scope 继续使用原边界。
显式封存只追加 `admission/task_finished`、`purpose: "abandoned"`，永久关闭旧模型身份，
释放任务槽；批次、已确认用量、未知预留和费用状态不变。重复封存不能释放后续任务。

## 源码回归

- 原有公开 Gateway／SessionRunner／真实 SQLite 接缝保留；正例证明普通结束拒绝、
  显式封存可用、原事件不变、旧身份拒绝、独立任务可准入及重放幂等。
- 13 个新增拒绝场景覆盖 PDF／参数别名、MCP、runtime、错参数、重复派发、缺准备、
  缺 closure、错 checksum、未取消历史、取消后派发、活动调用及活动 scope。
- 正例及真实形状的 closure 在修改前均 RED，退出 1。新增取消后派发用例也曾 RED、退出 1；
  修复顺序约束后通过。原失败日志保留，不以之后的通过覆盖。
- 最终定向范围为 admission 的 `TestAbandonCanceledAdmission*` 与 handler 的
  `TestModelTaskAbandonRoutePreservesSessionAndOwnerBoundary`：
  包含子测试的 28 PASS／0 SKIP／0 FAIL，退出 0；日志 SHA-256
  `ada5efc256a06ae9d094db3c94ebcf0db2973eac98be4a7efb4cb98cc64be067`。
- 新鲜完整 `go test ./... -count=1 -json`：包含子测试的 1910 PASS／82 SKIP／0 FAIL，
  47 个测试包通过、13 个包无测试，退出 0。外部产品／模型开关已移除；跳过保留，
  不作为产品证据。日志 SHA-256
  `4f4c93dedd8ee2cc6aaea22eee3b05a63643a79322e8b0a09f8a0e5e4101ee82`。
- `go vet ./...` 退出 0。源码双轴 AI_REVIEWED：Standards 0／Spec 0；不代表人工审阅。
- 文档契约命令 `python -B -m pytest -q tests/eval/test_ci_scope.py tests/eval/test_release_gate_workflow.py tests/eval/test_release_evidence.py`：
  23 PASS／0 SKIP／0 FAIL，退出 0。精确 7 个暂存路径的秘密／机器路径扫描及相对链接检查通过，
  `git diff --cached --check` 退出 0，扫描没有输出匹配值。

## 原生导出与一次性副本

不可变的原 Session 导出有 5465 个事件，SHA-256 为
`94315e16f3d6ae62114c36cb04210a4dfa3946a59b754cf254114d8bac1af070`。
最终严格实现验证其 Go checksum 链，普通结束仍拒绝，显式封存安全预检通过，退出 0；
该检查不打开数据库、不写事实。

从真实账本以只读连接生成一致性 SQLite backup，Go 仅在副本调用公开 Gateway。
副本封存退出 0，142 条事件流的全部原事件保持不变；仅增加一条封存，零起始序号 7167，
checksum 为 `b48cf94e9fc17fdf21ee2221701b6545d5f6804272d9c0e80a7fef6aff21aca1`。
原 Session 5465 个事件、`canceling/unknown`、原批次、87,306,321 已确认 tokens、
6,706,024,576 未知预留及未知费用状态均保留。数字只是本次快照，不是服务商余额。
旧身份重新预留被拒，封存重放不增加事实；没有派发模型或工具。

另以只读 SQL 比较真实账本与副本的全部 40,199 个原始事件、每行 10 列，完全一致，退出 0。
真实账本文件的检查前后 SHA-256 相同，仍保留原任务槽与 7167 个准入事件。
上述一次性副本不参与真实模型准入或生产运行。

原始日志、程序及收据位于忽略目录 `.scratch/readonly-admission-retirement-20261021/`。
副本收据 `native-copy-receipt.json` SHA-256 为
`2a9446ce66f2ce600b8abe545a39b2eb30c2aa55ff3a9e1a3332525a0e2627b4`；
逐列核对见 `copy-audit.json`，实际操作前的只读指纹见 `activation-pins.json`。
此前对另一旧测试任务的批准及实际封存见 [独立记录](canceled-model-admission-2026-10-21.md)，
不重复执行，也不作为本轮任务不可逆封存的授权。
