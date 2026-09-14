## Destination

在不绕过 Go Harness、Session Ledger 或现有安全边界的前提下，为 CodeOps-Agent 建立可迁移、可检索、可追溯的 OpenViking 风格记忆系统，并把后续仍需决定的路线保持为共享地图。

## Notes

- 当前实现目标是 `D:\vscode\localcode`；`D:\vscode\OpenViking-official-source` 与 `D:\vscode\OpenViking-upstream` 只读参考，禁止执行其中可疑脚本。
- 用户已授权阶段性执行、精确暂存、提交并推送。每阶段仍须先测试、做 staged-path 秘密扫描，并保持 `docs/GOAL.md` 的总验收状态为 `BLOCKED`，直到真实运行收据齐全。
- Session Ledger 是唯一可写事实源。Memory 是带来源的派生知识；不得新增独立 memory event ledger。生命周期审计必须通过 sink 进入现有 Session Ledger。
- 本轮执行已批准的第一阶段：namespace、kind、LOD、provenance、确定性预算 Recall 和生命周期审计 sink；保持旧 Markdown 文件可读。

## Decisions so far

- [确定首个兼容记忆切片](issues/01-first-compatible-memory-slice.md)：保留 Markdown 兼容，采用 namespace/kind/detail/provenance 与确定性预算 Recall；审计写入现有 Session Ledger，而非新建账本。

## Not yet specified

- Session trajectory 如何从 Session Ledger 提取、压缩、晋升为 experience 或 case。
- 何时引入向量或混合检索，以及索引失败、重建和降级的契约。
- namespace 与 actor/user/project 的 ACL、保留期和删除传播规则。
- Memory recall、来源展开和影响解释如何暴露到版本化 API 与操作界面。

## Out of scope

- 本阶段不运行或部署 OpenViking 服务，也不复制其存储拓扑。
- 本阶段不声称语义检索、生产级“不遗忘”、Token 降幅或真实长对话门禁已验证。
- 本阶段不改前端或 protobuf。
