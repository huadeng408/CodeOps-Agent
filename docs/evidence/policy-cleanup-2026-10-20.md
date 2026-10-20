# 指令、历史入口与门禁整理

范围：用户批准的十项清点建议。基点为 `7c58f356909065bab24bc16632748c1dd204ffd4`。
本轮不运行模型、不重新执行 #27、不修改 Ledger、任务槽、Docker 或业务数据。

## 归档与文档

9 份 tracked 原文保存在 [归档目录](../archive/policy-cleanup-2026-10-20/)，
[manifest](../archive/policy-cleanup-2026-10-20/manifest.json)记录原路径、字节数和 SHA-256。
12 份本机旧材料移入该目录的忽略子目录 `local-only/`；原文保留，本机 manifest 记录校验与恢复位置。
旧设计材料不再从 `docs/design/` 进入，旧检查器缓存不再留在 `scripts/design_map/`。

指令明确已授权的提交由 agent 完成验证和差异审阅后推进；Goal 只保留当前目标、缺口及证据入口。
历史研究的报告／CSV 原文归档，活动入口保留按任务选型的导航。
任务导航补齐后续专项及暂缓 #20，状态仍由正式 Issue 维护。
本机执行能力、Git 新旧工作区、RAG pin 和人工审核范围按当前实现／数据身份修正。

## 测试与门禁

删除 RAG 文档标题数量、注释原句和固定 CI job 全集等版式断言，保留参数、只读与原子切换检查。
CI 保持四个原检查名称，新增轻量范围检查；只有确认的普通 Markdown 差异才跳过全量。
指令链接目标、可执行 Markdown、数据／收据 JSON、缺失历史和未知路径仍运行全量。
范围检查失败时原四项也运行，不因 classifier 故障跳过验证。

发布绑定新增 lane 参数。已确认独立的三阶段 workflow fault runner 可以保留不受普通前端源码／
浏览器 fixture 变化影响的收据；其他 lane 和未确认路径继续使用整树判断。
来源 SHA、原始失败、checksum、clean-tree、链接／submodule 和七条指标不改写。
参见 [发布契约](../release-gate.md)；本轮未产生新的合格发布收据。

定向回归 78 PASS；追加历史缺失、链接目标与可执行 Markdown 等边界回归 14 PASS／60 deselected，
均退出 0。两项新行为各有 RED（退出 1）→GREEN（退出 0）的原始记录。
完整 Python 回归为 2652 PASS／18 SKIP／0 FAIL、31 个依赖弃用警告，退出 0，
耗时 816.59 秒。该候选完整运行期间保持源码固定；随后仅修正 CI 取消保护及相应契约断言。
原四个 job 使用未取消条件，在 scope 失败时继续运行完整验证，同时响应 superseding cancellation。
修正后运行 `python -B -m pytest -q tests/eval/test_ci_scope.py tests/eval/test_release_gate_workflow.py`：
17 PASS，退出 0。增量审阅记录随本机交付收据保留。

本机原始验证、Junit 分母、初审及增量审阅位于 `.scratch/policy-cleanup-20261020/`，
该忽略目录不随提交发布。源码／文档验证、产品验证和发布资格分别记录；
本轮产品和发布验证未运行，也不把其他范围的未运行项作为本次修复的 blocker。
历史原始收据、完整失败分母、权限、沙箱、Ledger、人工复核和七条发布指标继续保留。
