# Trace 验证入口

需要 Trace 验收的任务，读取当前 [发布契约](release-gate.md)、[Go tracer](../internal/telemetry/genai/tracer.go)、
[Python 环境配置](../orchestrator/config/env.py) 和相应验证入口，例如
[跨进程 Trace 脚本](../scripts/test-trace-e2e.ps1)。执行前核对本次后端、凭据、端点与模型配置。
新增 Docker 资源遵循 [统一目录约定](../deployments/docker/README.md)。

2026-08-04 的 Phoenix 运行核对已移入 [历史原文](archive/policy-cleanup-2026-10-14/docs--observability-phoenix-live-trace-runbook.md)。
旧导入进度、端口、模型和禁止启动服务的条件不是当前前提。普通修复的验证范围见 [AGENTS.md](../AGENTS.md)。
