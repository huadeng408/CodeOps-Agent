# Trace 验证入口

需要 Trace 验收的任务，读取当前 [发布契约](release-gate.md)、[Go tracer](../internal/telemetry/genai/tracer.go)、
[Python 环境配置](../orchestrator/config/env.py) 和相应验证入口，例如
[跨进程 Trace 脚本](../scripts/test-trace-e2e.ps1)。执行前核对本次后端、凭据、端点与模型配置。
新增 Docker 资源遵循 [统一目录约定](../deployments/docker/README.md)。

2026-08-04 的 Phoenix 运行核对已移入 [历史原文](archive/policy-cleanup-2026-10-14/docs--observability-phoenix-live-trace-runbook.md)。
旧导入进度、端口、模型和禁止启动服务的条件不是当前前提。普通修复的验证范围见 [AGENTS.md](../AGENTS.md)。

## 实际产品任务读回

`python -m eval.product_trace` 对真实产品 Session/run 使用 `product-v1` 契约：
从 Phoenix API 找到 Go 的 `agent.main`，再按 trace ID 分页读回整棵树，
逐项核对 Go Ledger 中的工具和模型调用。Python `provider.call` 表示一次逻辑调用，
Go `provider.attempt` 表示每次已准入的 HTTP 尝试；重试全部保留。
前台调用必须位于 Python `chat` 下，实际使用的反思等后台调用也必须连接到 Go 根。

执行前保存冻结的源码指纹、实际二进制、模型、任务镜像与后端 pin，并保留构建、
后端身份核验和原仓库保护证据。准备完整的 Session 事件及准入 Ledger 前后导出，
包含原始序号、checksum 和 payload；只筛选 call 事件的导出不能校验完整链。
命令内部复用 `go run ./cmd/trace-audit` 的原生事件链和预算投影，需要 Go 工具链。
它核对当前任务所有调用已结算且任务已停止；其他已结束任务的历史 unknown 不阻塞本次读回。
同一代码任务包含多轮对话时，重复 `--run-id` 提供各轮实际 run。命令核对每轮的 Go 根、
完成状态和工具归属，并用同一任务的完整准入前后分母检查全部模型调用；只读轮次不需要伪造写入。
整个所选任务仍须有真实代码修改及工具执行。缺一轮、遗漏调用或跨轮错误父链均不能通过。
请求列表必须覆盖 Session 的全部 canonical run；未完成的轮次仍会拒绝验收。
后续轮次读回失败时保留先前已核验的部分 Trace 和完整调用分母，并记录失败轮次，保持 `BLOCKED`。

```powershell
python -m eval.product_trace --phoenix-url http://127.0.0.1:6006 --project default `
  --started-at <冻结的起始时间> --run-id <实际 run ID> `
  --session-events <完整 Session 导出> --budget-before <完整准入前缀> `
  --budget-after <完整准入末态> --inputs <冻结输入 JSON> `
  --binary <实际产品二进制> --output <尚不存在的收据目录>
```

命令核对当前源码和二进制 checksum、实际 provider 的请求模型，再保存允许字段的读回和校验结果。
敏感内容在允许字段筛选前检查；缺失、未结束、错误、取消、后端不可用或输入校验失败不产生成功收据，
返回退出码 3，并保留 `partial`／`unknown` 与每次尝试。通过仅代表该次产品 Trace 范围，
不授予发布资格；旧 Python-only eval 及独立探针仍保持各自分类。
