# 只读仓库 MCP

Windows local-core 随 Go 程序启动一个固定的 stdio 服务。它只读取当前任务工作区的
依赖清单；执行仍经过 Go Session Runner、工具审批、Executor 和 Session Ledger。

| 项目 | 固定范围 |
| --- | --- |
| 服务 | `codeops-repository`，版本 `1.0.0`，项目许可 Apache-2.0 |
| 工具 | `RepositoryManifest` |
| 参数 | 必填 `file`，仅 `go.mod`、`pyproject.toml`、`package.json`；拒绝额外参数 |
| 返回 | UTF-8 内容、文件名和原始字节的 SHA-256；最多 1 MiB |
| 执行 | 同一 Go 可执行文件的 `--repository-mcp` 入口；没有命令、网络或工作区选择接口 |
| 依赖 | 官方 [Go MCP SDK v1.7.0](https://github.com/modelcontextprotocol/go-sdk/tree/v1.7.0)、JSON Schema Go v0.4.3 |

SDK 的许可处于 MIT／Apache-2.0 迁移阶段，不能简写为单一许可。
原文及新引入依赖的许可保存在 [licenses/](licenses/)；发行时同时保留 NOTICE 和这些文件。

## 授权与身份

Go 固定可执行文件 checksum、服务名称／版本和完整工具 schema catalog。
Windows 文件 pin 在进程存活期间保护可执行文件；读取 manifest 时使用 rooted 文件访问、
普通文件身份校验、读取前后检查和原生只读 pin。符号链接、junction 和身份变化会被拒绝。
每个 Task Workspace／子工作区使用独立的托管 MCP 进程；owner、Session 和当前派发身份
通过已有工作区准入检查，模型不能提供另一目录或扩大文件枚举。

新版本通过 `UserInput.mcp_catalog` 的 `MCPToolCatalog` schema 1 传给 Python Registry，
不在 Task Workspace 写入 catalog，也不放宽任务文件清单。显式空 catalog 清除该请求的
外部工具；未提供字段的旧调用方继续使用原 manifest adapter。Python 只注册声明并保留
ASK_SESSION；Go 才决定是否执行。CLI、常规 HTTP 和子调用复用相同 manager／catalog。

Go canonical tool/call 记录服务及配置/schema 绑定，成功或失败都写入现有 tool/result。
审批绑定原调用及参数，恢复与重放不能替换 binding。

## 故障与停用

stdio 使用 JSON-RPC 2.0 和已支持的 MCP `2024-11-05` 初始化协议。
请求／响应有 8 MiB frame 上限，单次 MCP 调用最多 30 秒；参数 schema 在派发前校验，
远程 schema 引用不会自动联网。当前 adapter 只接受带显式字符串的 text 内容；图片和
resource 内容尚未接入，不能把它们或畸形结果当作成功文本。

拒绝、服务不可用、协议错误、超时、取消和断连分别标识。已派发但没有可信结果的调用
保留 OutcomeUnknown，Go 停止并回收该进程；同一调用的恢复不再次执行未知副作用。
新的独立调用只在重新验证原服务和 catalog pin 后启动替代进程。
工作区副本在每次调用前核对原权威 manager 的当前 binding；父服务停用或绑定变化时，
拒绝新的 bound／unbound 调用并回收副本，旧 catalog 不保留执行权限。

停用此服务或其注册后，保留 Ledger 的历史与已知／未知结果。其他工具仍经过原 Go
授权边界。本票没有启用任意远程 MCP、第三方执行脚本或新的网络权限。
原生文件 pin 的产品契约目前限于 Windows；其他平台明确拒绝这个 bundled 服务，
通用 MCP 源码回归继续按平台能力记录。
