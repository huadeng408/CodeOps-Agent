# OpenSandbox 本地镜像身份校验

范围：[#21](https://github.com/huadeng408/CodeOps-Agent/issues/21) 的 Runner 镜像校验。
实现状态：`IMPLEMENTED`；Go 单测／集成回归：`VERIFIED`。
实际 OpenSandbox 部署、产品浏览器、模型／Trace 和发布验收：本范围未运行，#21 保持打开。

本地构建镜像通常没有 `RepoDigests`。Runner 接受规范的完整小写
`sha256:<64 hex>`，并核对 Docker `inspect.Id`。仓库 `@sha256:` 引用
继续核对 `RepoDigests`，包含以 `sha256` 为注册表名称的原有格式。
缺失、错配和缩短的 ID 在创建沙箱前拒绝；部署组件和任务镜像共用此校验。

公开 Runner 回归验证本地 ID、组件／任务 ID 错配、缩短 ID，以及仓库摘要兼容。
错配 fixture 同时保留看似匹配的 `RepoDigests`，仍必须拒绝执行。
成功接入的 fixture 保留完整失败测试输出、退出码和精确清理结果。
两轮 RED 均以行为失败退出 1；原记录保留。

| 命令 | 退出码 | 完整结果 |
| --- | --- | --- |
| `go test ./internal/sandbox -run OpenSandbox -count=1 -json` | 0 | 20 项 test actions 通过，0 失败／跳过 |
| `go test ./... -count=1 -json` | 0 | 1396 项通过、60 项跳过；43 包通过、14 包无测试 |
| `go vet ./...` | 0 | 通过 |
| `git diff --check` | 0 | 通过 |

双轴审查为 `AI_REVIEWED`：Standards 初始 P2 一项已按 RED／GREEN 修复，
最终剩余 0；Spec 补审后剩余 0。未进行人工逐条审查。
本轮模型调用为 0。

不可变[收据](../../.scratch/opensandbox-integration/local-image-pin-6b156b64-e8ac-4c4d-9fdb-9e6261249c91/receipt.json)
SHA-256：`3119d7d17d6a25d5a561dd01fe265c84760aeaa05f44544a6f1ad6b46963f7c3`。
收据冻结 441 个 Go／模块输入、完整分母和 25 个产物，包含初始回归、失败及最终结果。
运行基线为 `72e01da01cb535ed428dd734ab87f70f829d0273` 加本轮未提交源码；
交付提交的源码与产物按[绑定记录](../../.scratch/opensandbox-integration/local-image-pin-6b156b64-e8ac-4c4d-9fdb-9e6261249c91/commit-binding.json)核验。
用户既有 `AGENTS.md` 私有尾行排除在本轮提交之外。
