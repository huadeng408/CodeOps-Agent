# Retry-After 字段数量与空值纠正

关联 [#3](https://github.com/huadeng408/CodeOps-Agent/issues/3)。工程状态
`IMPLEMENTED`；Linux 合成 HTTP／公开 Gateway.Invoke／临时 SQLite 重开
范围 `VERIFIED`。整票真实模型 runtime、Windows 与发布验收继续 `BLOCKED`。

## 已发现的覆盖缺口

基线 `7fb49dafe2d5ac538b1dd1174172ccb648c8ef51` 的拼接方案通过其原有
测试和四项源码 CI，但未覆盖两条字段拼成合法 HTTP-date 的反例。
根 AI 代理追加检查后新增 split-date 与单空字段两个 RED：各实际派发三次，
期望一次；失败日志与确切源码／输入指纹保留，没有以旧 CI 抹去失败。

最终改为保留字段列表，解析前拒绝多条字段。无字段允许原默认退避；
单字段才进入既有秒数／日期解析，显式空字符串因此被拒绝。合法单日期
中的逗号不按列表切分。此内部类型变化不修改 gRPC schema 或 Python caller。
未知、取消和准入失败先按错误分类终止，nil 字段列表不会赋予重试资格。
只有已经持久结算的 429／5xx 才继续检查 header 和有界退避，本轮瞬态 fixture 为 503。

这是本项目对重复／malformed 字段的停止政策。
[RFC 9110 §10.2.3](https://www.rfc-editor.org/rfc/rfc9110.html#section-10.2.3)
定义单日期或秒数；[§5.3](https://www.rfc-editor.org/rfc/rfc9110.html#section-5.3)
约束发送方重复非列表字段，不声称其强制客户端采用本项目策略。

## 新鲜验证

Go 1.26.0；所有下面结果绑定本轮最终源码与两个新增输入。两个新反例
GREEN 后，完整定向范围包括 17 个重试边界子场景、9 个歧义 usage 子场景、
两个表测试父项及两个其他测试，共 30 pass 事件。

| 命令／范围 | 结果 | 退出码 |
| --- | --- | --- |
| split-date／空字段 RED | 2 个子场景失败，各实际 3 次／期望 1 次 | 1 |
| 定向 Gateway GREEN | 30 pass／0 skip／0 fail | 0 |
| `go test ./internal/admission -race -count=1 -json` | 94 pass／4 skip／0 fail；1 包通过 | 0 |
| `go test ./... -count=1 -json` | 1495 pass／79 skip／0 fail；45 测试包，13 包无测试 | 0 |
| `go vet ./...` 与 `git diff --check` | 各通过 | 0 |

定向命令：

```sh
go test ./internal/admission -run 'TestGateway(RetainsRetryBoundaries|RetriesOnlyAfterErrorUsageSettles|RejectsAmbiguousProviderUsage|CancelBackoffRetainsOnlySettledAttempt)$' -count=1 -json
```

本轮两个新增拒绝场景保持一次派发、8 tokens 已结算、零预留和原错误分类；
单零值／单日期成功对照三次尝试累计 24 tokens。全部表案例重开临时 SQLite 后投影一致。
三个 admission skip 是父进程 fixture helper，另一个是未启用的 namespace
fixture；完整 Go skip 分母原样保留。Python／frontend 本机本轮未重跑。

## 审查与追溯

- Standards：纠正后的源码无文档标准违规或 actionable heuristic。
- Spec：无缺失、范围扩张或错误实现，明确核验 nil／单空字段和未知分支。

两轴重新比较固定基线与提交前 working-tree diff，状态 `AI_REVIEWED`，
没有用户人工逐条复核，不声明 `HUMAN_REVIEWED`。上轮零 finding 和成功
收据只属于其声明输入；新增失败与上轮原始日志／源码／收据全部保留。

| 最终绑定 | SHA-256 |
| --- | --- |
| `internal/admission/gateway.go` | `f878eaf01504774037f58e6098e9372564cbc6c2bf074db644b97595e3cbf40a` |
| `internal/admission/gateway_test.go` | `6ac4bf7c6abc0f7003ed7db50966b1d1d8503e1f732356d23c861fe0ab2747bc` |
| 467 项 Go 源码／模块／工作指令清单 | `09bec854b7fec5b79db62f2f455f4fba5790183bec58bf292168a01539049935` |
| 不可变验证收据 | `d63bf8c1398de894b3a7fee960b0c56fc14e88673b9206ee563697f7fcdd0201` |

本轮零真实 provider 调用，未访问、复制或重置 canonical Ledger。原 known
22,526 tokens、反思 unknown 502,048 预留与任务占用仅引用历史记录，仍须
provider 记录或逐笔确认后在原批次对账。源码测试不证明真实模型任务、
Memory、完整恢复、Windows 产品或发布已通过，#3 保持 open。
