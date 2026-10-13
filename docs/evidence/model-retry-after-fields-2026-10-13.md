# 重复 Retry-After 停止自动重试

关联 [#3](https://github.com/huadeng408/CodeOps-Agent/issues/3)。工程状态
`IMPLEMENTED`；Linux 合成 HTTP／公开 Gateway.Invoke 与临时 SQLite 重开
回归范围 `VERIFIED`。完整模型产品验收仍 `BLOCKED`，Issue 保持 open。

本记录保存 `7fb49daf` 的原始测试输入和收据。后续发现 split-date 拼接及
显式空字段未覆盖，两项新增 RED 保留；最终修正见
[字段数量与空值证据](model-retry-after-arity-2026-10-13.md)。本记录不证明
这些后来发现的反例已通过，原成功结果不代替后续源码验证。

## 行为与契约

原网关通过 Header.Get 只读取第一条 Retry-After。合成 429 响应同时给出
0 与 11 秒时，旧行为忽略第二条并发出三次请求。该轮拼接全部 field lines
交给既有单值解析器；所测完整重复字段停止当前调用的自动重试，保留 429／503
分类和已经持久结算的用量。单字段秒数、HTTP-date 和无 header 的既有
有界重试继续有效。unknown usage 仍先保留完整预留并阻断，不因 header
变化变成零消耗或允许重试。

[RFC 9110 §10.2.3](https://www.rfc-editor.org/rfc/rfc9110.html#section-10.2.3)
将 Retry-After 定义为单个 HTTP-date 或 delay-seconds；
[§5.3](https://www.rfc-editor.org/rfc/rfc9110.html#section-5.3) 限制发送方
重复非列表字段。拒绝重复字段是本项目的 fail-closed 重试政策选择，
不声称 RFC 要求客户端必须采用该策略；所测相同重复值也停止自动重试。

## 验证与审查

基线：`6b18f6b6ba06697ef38a27f4f361e2b6bd7a5118`；Go 1.26.0。
首个冲突字段回归 RED 退出 1：实际三次请求，期望一次。
修复后 GREEN，随后增加日期＋秒数、相同秒数两个重复拒绝场景，以及
合法单零值、单 HTTP-date 两个成功对照；表中直接声明 header values。

所有场景读取公开 Budget.Inspect 并重开真实临时 SQLite：拒绝自动重试时
保留已结算 8 tokens、零预留；合法三次尝试累计 24 tokens。重开后的
投影相同。没有访问 canonical Ledger，也没有创建产品替代批次。

最终源码的新鲜运行：

| 命令／范围 | 结果 | 退出码 |
| --- | --- | --- |
| 定向 Gateway 回归 | 28 pass／0 skip／0 fail | 0 |
| `go test ./internal/admission -race -count=1 -json` | 92 pass／4 skip／0 fail；1 包通过 | 0 |
| `go test ./... -count=1 -json` | 1493 pass／79 skip／0 fail；45 测试包，13 包无测试 | 0 |
| `go vet ./...` | 通过 | 0 |
| `git diff --check` | 通过 | 0 |

定向命令：

```sh
go test ./internal/admission -run 'TestGateway(RetainsRetryBoundaries|RetriesOnlyAfterErrorUsageSettles|RejectsAmbiguousProviderUsage|CancelBackoffRetainsOnlySettledAttempt)$' -count=1 -json
```

三个 admission skip 是父进程 fixture helper，另一个是未开启的 opt-in
Linux namespace fixture。所有 RED、审查前及最终成功日志和完整 skip
分母保留。Python／frontend 未改，本机本轮不重复其完整测试。

- Standards：初次有一项低优先级测试名称与 header 行为耦合建议；改为
  显式 field-values 数据后复审解决，无硬性违规或未解决 heuristic。
- Spec：无缺失、范围扩张或错误实现。

按 implement／TDD／code-review 执行，固定基线与提交前 working-tree diff
分别完成两轴审查。状态 `AI_REVIEWED`，不声明 `HUMAN_REVIEWED`。
审查修改测试输入后重新执行定向、完整 Go、race；旧成功收据不冒充最终绑定。

| 最终绑定 | SHA-256 |
| --- | --- |
| `internal/admission/gateway.go` | `6390a0eded0328c131a7c339117ad592e38e1dae2590b9120944c76a1c77a517` |
| `internal/admission/gateway_test.go` | `6817003a8fa09c76ff4af8e7be71601534763b76395434529c68b76c9c94dfcd` |
| 467 项 Go 源码／模块／工作指令清单 | `2812161df77ffbd02523370a2ffe896ef10fb46ab64048b854902ab81ea745f2` |
| 不可变验证收据 | `56f2499980104591794bf7e1966d6c2e1387842138a3fc59fe229491b4aa8c1c` |

本轮零真实 provider 调用；原 known 22,526 tokens 与 reflection unknown
502,048 预留仅引用历史记录，未在云端读回或对账。原批次及任务占用不因
工程回归释放。真实模型闭环、Memory 成功写回、完整恢复及 Windows 产品
验收未运行，源码回归不代替这些范围。
