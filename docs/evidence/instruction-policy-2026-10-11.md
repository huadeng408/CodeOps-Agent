# 指令与验收约束调整

范围：用户批准的六项规则，以及指令加载器、设计地图检查器和发布收据绑定的三处必要实现。
当前状态：`VERIFIED`（单测及本地 CLI／Git 契约集成），本范围无未解决 blocker。
本记录不代表完整产品或发布合格。

## 变更

- [AGENTS.md](../../AGENTS.md) 为唯一指令正文；[AGENT.md](../../AGENT.md) 和 [CLAUDE.md](../../CLAUDE.md) 为入口。
- [GOAL.md](../GOAL.md) 仅保留当前目标、缺口和证据链接；原文按字节保存在[历史快照](../archive/receipts/GOAL-log-2026-10-11-before-instruction-consolidation.md)，并更新[索引](../archive/INDEX.md)。
- 三套前端方案只用于新布局或明显视觉改版；已选方案及普通修复沿用现有选择。
- 按任务读取必要文件；[设计地图检查器](../../scripts/design_map/check.py) 取消固定数量拒绝，保留路径及状态检查。
- 单测、集成、产品和发布分别记录；只有声明范围内缺必需条件才标为 `BLOCKED`。
- [发布门禁](../../eval/release_gate.py) 核对最终运行内容和文件模式，允许普通说明文档沿用原收据，保护源码、配置、指令、依赖和任务输入；原收据结果与 pin 不改写。

[指令加载器](../../internal/config/agentmd.go) 每个目录优先读取 `AGENTS.md`，不存在时兼容 `AGENT.md`，保留全局／项目／工作目录顺序、去重及读错误。门禁拒绝未确认的指令链接目标、未知收据目录和依赖变化。
公开回归接缝为 `LoadInstructions`、检查器 CLI 和 `evaluate_release`；测试仓库与合成 receipt 仅证明判定契约。

## 验证范围

| 范围 | 结果 |
| --- | --- |
| 单测与本地 CLI／Git 契约集成 | `VERIFIED`；下列回归通过 |
| 完整产品、浏览器与真实模型 | `NOT_RUN`，不在本次声明范围 |
| 完整发布 | `NOT_RUN`，原七条 lane 门槛保留 |

| 命令 | 完整结果 | 退出码 |
| --- | --- | --- |
| `go test ./... -count=1 -json` | 1,370 个通过、60 个跳过、0 失败的测试终态（含子测试）；43 个有测试的包通过 | 0 |
| `go vet ./...` | 通过 | 0 |
| `python -m pytest -q --junitxml=…` | 2,485 项：2,468 通过、17 跳过、0 失败；31 个 warnings | 0 |
| 门禁／检查器／Phase 1 定向回归 | 62 项通过；随后依赖和链接的最终 18 项回归全部通过 | 0 |
| `python scripts/design_map/check.py` | 当前 2 个任务、4 个 workstream 通过 | 0 |
| `git diff --check`、`git diff --cached --check`、精确暂存秘密／路径扫描 | 通过；16 个暂存文件，0 个匹配 | 0 |

源码基线为 `2c01be2f70c890292f60c5671d172b57c036e381` 加当轮工作副本的文件指纹；
每轮验证前后源码指纹一致，交付时再次核对 Go 输入。审查为 `AI_REVIEWED`，
Standards／Spec 均无剩余问题；未产生 `HUMAN_REVIEWED` 或真实运行能力结论。

完整命令、源码指纹、退出码及产物 SHA-256 见[交付收据](../../.scratch/instruction-policy-review/delivery-receipt.json)。
原始记录为[首轮完整回归](../../.scratch/instruction-policy-review/regressions-843215e2-407c-41f0-b8e8-a4d9d64cc381/receipt.json)
和[最终 Python 回归](../../.scratch/instruction-policy-review/regressions-441400b4-b90d-4ae4-a6b5-bc44cdc98ce8/receipt.json)。
首轮 Python 保留 2,454 通过、17 跳过、1 失败的完整记录；失败原因是 Goal 未明确保留
完整发布受阻和真实 E2E 不可替代的声明。修正为发布范围的状态后重跑完整分母，原失败与 TDD RED 产物均保留。

最终 Python 原始收据 SHA-256：`087ecb9c310ac79f2dd00f374b09ded8b84ff6e69ba13d9deb7baf13bf887fe8`。
历史快照 SHA-256：`9a08565f567534ee65dfb078a8431aec2a7ac7a8ed5cb12ee9bffd84ebfa1cde`。
未修改前端、协议、API 预算或凭据约定；OpenSandbox 三票的已批准方案保留，接入实施待续。
