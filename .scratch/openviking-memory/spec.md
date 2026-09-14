# OpenViking 风格记忆第一阶段设计

## 目标

在现有 Go/Python Markdown Memory 上增加命名空间、类型、LOD、来源校验和、确定性预算召回和可注入生命周期审计，同时保持旧文件可读、敏感信息 fail closed，并且不新增与 Session Ledger 竞争的事实源。

## 领域契约

- `Memory` 是从会话或外部证据提炼出的可检索知识，不是原始会话记录。
- `namespace` 标识隔离范围，第一阶段默认 `user`。
- `kind` 标识知识类别，第一阶段默认 `default`，允许 OpenViking 风格的 `profile`、`preferences`、`entities`、`events`、`cases`、`trajectories`、`experiences`、`tools`、`skills`。
- `detail` 是 LOD，合法值为 `abstract`、`overview`、`full`；旧记录默认 `full`。
- `source_uri` 指向来源，`source_checksum` 是来源内容的 SHA-256；两者必须同时为空或同时有效。
- `session_id` 可选，用于关联产生该知识的 Session，但 Session Ledger 仍是会话事实源。

## 写入与审计

保存前规范化并验证所有元数据。正文、名称、标签及元数据均不得包含凭据形状。文件写入继续使用现有 Markdown frontmatter，旧文件不需主动重写即可读取。

写入、更新和删除产生不含正文的 `MemoryEvent`，事件包含 action、memory id/name、namespace、kind、detail、session id、source checksum、memory checksum 与时间。Manager 接收可选 audit sink；配置 sink 后，sink 写入失败会阻止或回滚 Memory 变更，使审计与派生知识不会静默分叉。生产接入时 sink 必须写入现有 Session Ledger。

## Recall

新增 `Recall(query, options)`，返回条目与统计。选项支持 namespace、kind、detail、limit 和 max tokens。候选仅来自通过过滤的当前 Memory；按名称、标签、元数据、正文的命中数计算确定性分数，同分按更新时间和名称稳定排序。预算以 UTF-8 字节数除以四的保守估算裁剪，永不返回半条记录。旧 `LoadRelevant` 通过默认 Recall 保持兼容。

## 错误与兼容

- 非法 namespace/kind/detail、半套来源字段、非 SHA-256 checksum、负数预算或 limit 均拒绝。
- 旧 Markdown 缺少新字段时读取为 `user/default/full`。
- 损坏或敏感文件仍使初始化 fail closed，不覆盖现有索引。
- 本阶段不引入向量检索、不修改前端或 protobuf、不执行 OpenViking 外部脚本。

## 验证

Go 与 Python 各自覆盖：旧文件默认值、元数据往返、过滤与稳定排序、token 预算、来源校验、sink 失败不提交写入，以及旧 API 回归。完成后运行定向测试、相关集成测试、`git diff --check` 和 staged-path 秘密扫描。
