# Memory 反思失败诊断

工程状态：`IMPLEMENTED`；公开 `LedgerMemory.Commit` 接缝的诊断与隐私回归范围
`VERIFIED`；完整 Go 回归通过，独立 Standards／Spec 各 0 项发现，`AI_REVIEWED`。
新产品运行与发布未运行。

#27 v10/v4 的两次已结算反思仍失败，原 Trace 没有足以区分原因的信息；本次没有
为旧失败补写原因或修改收据。源码确认 Go 将 Python 返回的所有错误类别合并为通用
错误，`memory.reflect` 只留下不含正文的 ERROR。这是本次修复的诊断缺口。

失败 span 新增固定阶段 `memory.reflection.failure_stage`：catalog_read、sources、
proposal_read、reflector、validation、proposal_write、catalog_write。
`memory.reflection.error_code` 默认为 reflection_failed；回调错误／空响应为
reflection_unavailable，非空响应错误默认为 reflection_error。只保留四个既有固定值：
invalid_reflection_sources、reflection_provider_unavailable、
reflection_invalid_or_unavailable、model_gateway_response_invalid。
其他值一律归为通用类别，不复制错误正文、任意返回码或模型内容。

成功 span 不带失败字段；原 ERROR、空 status description 和 eval／Session 关联不变。
调用方错误、来源完整性验证、Ledger/CAS、重试、计量、协议和依赖均保持原行为。
本轮未新建产品运行或发起付费模型调用。

## 验证

| 范围／命令 | 结果 |
| --- | --- |
| 新诊断回归，修复前 | 退出 1；9 场景均缺诊断字段，含父结果共 10 FAIL |
| 同一回归及既有成功／隐私控制 | 退出 0；含父子结果 14 PASS、0 FAIL／SKIP |
| `go test ./internal/memory ./internal/session ./internal/telemetry/genai -count=1 -json` | 退出 0；含父子结果 375 PASS／4 SKIP／0 FAIL，3 包通过 |
| `go vet ./...` | 退出 0，无输出 |
| `go test ./... -count=1 -p 1 -json` | 退出 0；含父子结果 1,865 PASS／82 SKIP／0 FAIL，47 包通过、13 包无测试 |
| gofmt／`git diff --check` | 退出 0，格式差异为空 |
| 独立 Standards／Spec | 各 0 项发现，`AI_REVIEWED` |
| 新真实模型／产品／发布 | 未运行；#27 的完整 200 轮仍待通过，#20 继续暂缓 |

9 场景覆盖回调错误、空响应、四个允许的错误类别、两类未知返回码以及 Go 来源校验
拒绝；同时核对 span 已结束、ERROR 不含正文、未知值不透传、来源错误仍拒绝。
通过数量包含顶层及子测试，不能相加为产品场景数量。全部跳过保留在原日志中。

修改路径为 `internal/memory/evolution.go`、`internal/memory/telemetry_test.go`、
本文件及 `docs/GOAL.md` 的证据链接。源码基点
`a8915a98ca59eeca27646299d4849eaf1655e9c9`，源码 patch SHA-256
`5fa49dcfbab0d951227cac2d5d8c11dc8d1c949604092a1573e33a341fab55b7`。
原 RED／GREEN、相关包、完整 Go、vet 日志和独立审阅保存在
`.scratch/memory-reflection-diagnostics-20261020/`，不进入提交；
`validation.json` 绑定源码指纹、命令／退出码、完整分母、跳过身份与日志 checksum，
SHA-256 `f2487c583a3b6da7876f09fcb1f43734790165759e2ebef0872bd908b326723f`。
Python／前端未做单独本机验证；新提交的源码 CI 另记于 #27 交付评论，不升级为产品证据。
本次只能改善新发出的失败 Trace；两次历史失败的具体原因仍未知，不能据此宣称
Memory 内容生成、200 轮产品验收或完整发布通过。用户 AGENTS.md 改动排除交付。
