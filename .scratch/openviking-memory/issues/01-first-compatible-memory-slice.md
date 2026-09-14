Type: grilling
Status: resolved
Blocked by:

## Question

CodeOps-Agent 的首个 OpenViking 风格记忆切片应采用哪些稳定概念和兼容边界，才能立即实现而不产生第二个会话事实源？

## Answer

采用现有 Markdown Memory 作为派生知识存储，并为每条记录增加 `namespace`、`kind`、`detail`、`source_uri`、`source_checksum` 与 `session_id`。旧文件缺省为 `user/default/full`。新增确定性、可预算、可过滤的 Recall 读模型；生命周期事件仅通过可注入 audit sink 交给现有 Session Ledger，sink 失败时写操作 fail closed。Go 与 Python 保持同构。本阶段不增加向量库、独立事件文件、前端或协议字段。

## Comments

- 2026-09-14：用户确认了该阶段设计，并要求使用 wayfinder 持续维护后续决策地图。
