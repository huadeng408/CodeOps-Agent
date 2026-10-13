# Provider usage 歧义字段拒绝

关联 [#3](https://github.com/huadeng408/CodeOps-Agent/issues/3)。工程状态：
`IMPLEMENTED`；Linux 合成 HTTP／公开 gRPC 回归范围：`VERIFIED`；
完整产品 runtime 验收继续 `BLOCKED`，Issue 保持 open。

## 修复范围

原 Go JSON 解析会让后出现的重复字段覆盖先前值。重复的 usage、输入／输出
计数或缓存计数因此可能把非零消耗解释为零，释放预留并允许再次派发。
现在按解码后的字段名拒绝当前 provider 的重复计费字段，包含转义后同名的键。
无关 metadata、其他协议的计数和非计费缓存 metadata 保持原来的兼容行为。

歧义响应沿用既有 unknown 路径：完整预留保留，不结算成零；后续请求被
阻断。429／503 保留原错误分类且不重试。有效 usage 的缓存／reasoning
计入一次以及已结算有界重试逻辑继续由成功对照覆盖。未修改共享协议、
Ledger schema、批次身份、任务槽或产品调用方式。

## 本轮验证

源码基线：`cdc1c6d2e03dc2a569622351de5a70edc6a3cb97`；Go 1.26.0。
最终源码包含两文件修改；下面结果来自该修改后的新鲜运行。

| 命令／范围 | 结果 | 退出码 |
| --- | --- | --- |
| 初始歧义回归 RED | 3 个拒绝场景失败，原解析接受了歧义计费 | 1 |
| 缓存歧义 RED | 1 个缓存拒绝场景失败 | 1 |
| 审查后的非计费重复键兼容性 RED | 2 个成功对照失败 | 1 |
| 定向 Gateway GREEN | 9 个歧义子场景＋3 个成功／重试测试，13 个 pass 事件，0 fail／skip | 0 |
| `go test ./internal/admission -race -count=1 -json` | 87 pass／4 skip／0 fail；1 测试包通过 | 0 |
| `go test ./... -count=1 -json` | 1488 pass／79 skip／0 fail；45 测试包通过，13 包无测试 | 0 |
| `go vet ./...` | 通过 | 0 |
| `git diff --check` | 通过 | 0 |

定向命令：

```sh
go test ./internal/admission -run 'TestGateway(RejectsAmbiguousProviderUsage|ReservesBeforeUpstreamAndCountsUsageOnce|CountsAnthropicCacheCreationAndReadOnce|RetriesOnlyAfterErrorUsageSettles)$' -count=1 -v
```

九个拒绝场景包含 OpenAI 重复 usage、输入、转义输出、缓存详情，429、503，
以及 Anthropic 输入、缓存读取、缓存创建。通过公开 Gateway gRPC 调用和
Budget.Inspect 读取，检查 103 token 预留完整保留、零已结算消耗、unknown
标志，以及第二次调用未到达合成 HTTP 上游。SQLite 只使用各测试临时夹具。

三个 admission skip 是父进程中的子进程 fixture helper，另一个是未启用的
opt-in Linux namespace fixture。完整 Go 的其他 skip 原样保留在运行日志中，
不计入通过分母。Python／frontend 源码未改，
本轮本机不重复其完整测试。Windows 产品与真实 provider 验收未运行。

## 审查与追溯

按 implement／TDD／code-review 执行。审查在提交前比较上述固定基线与
working-tree diff；两个并行审查轴分别记录，不冒称已有提交的 three-dot diff。

- Standards：最终源码无文档标准违规或需修改的 baseline smell。
- Spec：最初发现拒绝无关 metadata 的范围扩张；计费字段限定和两个成功
  对照已解决该发现。最终无缺失、范围扩张或错误实现。

状态为 `AI_REVIEWED`；不声明 `HUMAN_REVIEWED`。保留三轮 RED 日志、最终
GREEN／race／完整 Go 原始日志及不可变验证收据；审查修正前的成功结果
也保留，但不代替最终源码验证。

| 绑定 | SHA-256 |
| --- | --- |
| `internal/admission/gateway.go` | `76092ab8dc7554f136d2db48cdfcd5ec791c8b5fcc152bf78981bcf0a65cc28d` |
| `internal/admission/gateway_test.go` | `06ddd3ad40cc2cce04677fbad1b98611b33a2532f935b82bb6e1c8df2c25f581` |
| 467 个跟踪 Go 源码、模块与工作指令文件的指纹清单 | `8ebd64c9ba24c3c3d843b407971c407d0565c61c9a19aa2cfa3da2e1faae8954` |
| 不可变验证收据 | `677a60beda4e3199fbafaaa79d0c0486be03ba98b4249aad37f9f2f6716148e8` |

本轮零真实 provider 调用，未访问或替换 canonical Ledger。历史已结算
22,526 tokens 与后台反思 502,048 token unknown 预留仅引用原记录；
不能声称已在云端读回或完成对账。原预留与任务占用仍需 provider 记录或
逐笔用户确认，不能沿用上一笔零用量结论。源码回归不代替真实代码修改、
提案批准应用、完整恢复、Memory 成功写回或 Windows 发布验收。
