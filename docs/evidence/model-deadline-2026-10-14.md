# Go 控制的模型请求期限：2026-10-14

基线：`a660f4b16d91951eea5f1a5ca4c2514a15fd0161`。
工程状态：`IMPLEMENTED`；本轮声明范围为 Go provider 配置、公开模型 gRPC、
Python 兼容 client 与实际 Go／Python 受控进程接缝。
[前轮真实产品](continuation-recovery-product-2026-10-14.md)的部分回答、补问超时、
失败及收据保持原结果，不将本轮 fixture 验证升级为真实 provider 或浏览器通过。

## 行为与边界

新增可选 Go 环境 profile `CODE_AGENT_MODEL_REQUEST_TIMEOUT_SECONDS`，只接受 1–600
整数秒，缺省 HTTP 请求期限保持 60 秒；环境中显式零、负数、畸形、溢出和超界值拒绝派发。
模型 payload 不能改写该设置，Python 不读取独立环境期限来延长 Go 授权。

Go 为传输／重试设置有限工作窗口，覆盖既有最多三次尝试及两次各最多十秒的等待；
终态结算和 unknown 审计共享工作窗口后最多五秒的绝对截止时间。
RPC 另外保留五秒响应余量，正式 protobuf `rpc_timeout_seconds` 将完整等待上界签发给 Python。
缺省工作窗口／RPC 为 200／210 秒；请求 profile 180 秒时为 560／570 秒；
最大 profile 的 RPC 为 1,830 秒。旧 binding 的零字段兼容原 65 秒等待，超界 binding 拒绝。
更短的父期限和取消仍优先生效；timeout 的原始预留与 unknown 保留，不触发未计量重试。
只更改正式 schema 并执行 `scripts/generate-proto.ps1`，Go／Python 生成物同步。

## 复现与验证

一秒 Go 请求 profile 的公开 gRPC 回归最初失败：旧实现直到外层四秒截止仍未返回
具体超时结果，退出码 1；修复后约一秒返回 provider_timeout_unknown，原 103-token
fixture 预留、unknown 和零活跃调用保持，退出码 0。
Python 的实际 gRPC 服务观察到旧 client 对 Go 签发的 205／565 秒仍发送固定 65 秒；
两项失败、旧零字段一项通过，退出码 1；修复后正确采用 binding。
Go binding 的三个 profile 回归最初全部未签发期限，修复后通过。

首次修复后的 Python 断言过于严格，565 秒在服务端观察为约 566 秒，保留该失败。
按本机 gRPC 1.83.0 的[官方时间编码](https://github.com/grpc/grpc/blob/v1.83.0/src/core/lib/transport/timeout_encoding.cc)，
较长 wire timeout 向上取整到秒或十秒；观察断言采用对应有限精度窗口。
Go 自身的工作及终态截止时间仍独立强制，服务端观察精度不授予额外执行时间。

Spec 审阅发现终态结算失败后可以再开启一个五秒 unknown 审计窗口，以及旧文档仍写
固定 65 秒的矛盾。首个真实 SQLite 锁探针约五秒结束，未复现额外等待，原输出保留。
随后只在数据库边界注入终态 Append 停滞，Gateway／Budget 与 SQLite 预留仍使用真实实现，
公开 Go 调用复现了约十秒等待，退出码 1；共享绝对截止时间后约 5.25 秒返回，退出码 0。
终态写入失败时保留原预留及未完成调用，不伪造已结算量或 terminal unknown；原等待事实不改写。
旧 65 秒说明已限定为零字段兼容 binding。
终态写入与 RPC 响应采用各五秒的独立余量，避免审计耗满截止时间时客户端先结束等待。

定向 Go 的完整 deadline 选择为 22 项通过，退出码 0；Python 网关文件 40 项通过，退出码 0。
实际 Go／Python 进程 fixture 为两个场景通过：已结算 429 后的有界重试成功并累计完整 usage；
一秒超时传回具体分类、只产生一次上游尝试并保留 unknown 预留。
三个 Go 测试事件含父测试及两个场景均通过，退出码 0。
畸形 profile 在公开 gRPC 派发前拒绝，零上游调用、零计量；较长 binding 仍可显式取消。

## 最终回归与交付

审阅修复后冻结最终运行输入并重新完成完整回归，前一轮输出另行保留。

| 命令／检查 | 最终结果 | 退出码 |
| --- | --- | --- |
| `go test ./... -count=1 -json` | 1,581 通过／72 跳过／0 失败；45 包通过，13 包无测试 | 0 |
| `python -m pytest -q` | 2,506 通过／17 跳过／31 warnings | 0 |
| `go vet ./...` | 通过 | 0 |
| `git diff --cached --check`、脱敏／本地链接核验 | 精确 13 路径，运行输入保持，私有 AGENTS 改动排除 | 0 |
| Standards／Spec 审阅 | 初审 Spec 两项问题已修复，最终两轴各零项剩余发现 | — |

最终 Go 全量包含修复后的终态停滞回归及两个 Go／Python 实际进程 fixture 场景。
原持久批次、275 项调用事件和零活跃调用保持；本轮新增真实模型调用为零。
源收据：`.scratch/model-deadline-20261014/receipt.json`，SHA-256：
`aa6e69cb174a791b5726a8b32b49c41c4c5cd6465a9e104ba9fe5ba42700f2c3`。
最终 15 项运行输入指纹、原始 RED／失败探针／GREEN／全量输出和审阅记录在同目录；
提交、四项源码 CI 及 Issue 逐字读回另绑定在该目录的不可变交付记录中。

本轮没有前端改动；新的真实 BeeAPI 产品 profile、第一问质量、Memory／CAS、Trace、
普通登录和完整发布仍按对应范围验收。#3／#5 保持开放，长期 goal 继续 ACTIVE。
