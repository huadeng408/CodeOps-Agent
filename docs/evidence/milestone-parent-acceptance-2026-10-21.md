# Windows milestone 1 父规格逐项审计

本次按 [#1](https://github.com/huadeng408/CodeOps-Agent/issues/1) 的原始 Windows
milestone 范围复核 AC01–AC09，确认已有首批证据满足该范围的收口条件。
正式票状态以 GitHub 为准；不改写原规格、旧收据、失败、跳过或 unknown。

这是对已交付 v5／v6b 和原 Python 任务证据的当前完整性审计，没有重新运行产品，
也没有把旧运行绑定到当前 `97219faa6b4fe7bbbdab2a29f7ab6be360f28082`。
该提交的源码修复和 CI 仍守各自范围；后续构建的真实产品验收另行进行。

## 原范围及证据

| 要求 | 可核对的实际证据与完整边界 |
| --- | --- |
| AC01：便携启动、鉴权、历史及能力 | v5 的 local-core 实际启动；v6b 从无关目录、带空格路径实际登录并重开旧 Session，控制服务关闭时历史 READY、执行仍拒绝；见[兼容／便携记录](milestone-v5-compatibility-2026-10-19.md)及 [EXE 交付](portable-windows-2026-10-15.md) |
| AC02：两个公开任务各至少十个有意义轮次 | [Go](go-browser-task-trace-v5-2026-10-19.md) 14 run 中 12 completed、2 canceled；最终项目 86 顶层／152 父子 PASS。Python 11 completed，排除纯规划后保守计十轮，项目与提案测试 99/99；[功能与应用](python-browser-task-2026-10-16.md)、[后续 Memory／Trace 对账](trace-integrity-v2-2026-10-16.md)共同构成该任务证据 |
| AC03：工作副本、文件存在性、授权、冲突、重放及中断 | [兼容矩阵](milestone-v5-compatibility-2026-10-19.md)逐项记录 dirty/new/empty/missing、四类文件操作、路径／junction、并发和 request identity；保留原 16 个 intent/outcome 中断边界及三类冲突。11 个原应用核心指纹一致，变化的准备／Runner 等另由回归和真实场景覆盖；四项 Windows symlink 权限跳过及 helper 跳过未升级为通过 |
| AC04：五类 product-entry 场景 | [同轮五类恢复](milestone-v5-matrix-2026-10-19.md)完整保留 7 run／8 attempt：5 completed、1 failed、1 canceled；原审批接续、marker、原仓库冲突拒绝及五次结束 HTTP 200 均有原生记录。故障 unknown 和缺根 Trace 仍单独保留 |
| AC05：真实反思、来源和新 Session 检索 | Go 同任务 53 条 Experience；Python 8 次真实反思形成 32 条经验，三次失败保留。重启后的空新 Session 实际取回五条经验并完成答复，ID、revision、来源与 checksum 均对照 Go 投影；原 owner／repository 拒绝与显式扩大范围的边界保持，见[五类恢复](milestone-v5-matrix-2026-10-19.md)和[反思对账](trace-integrity-v2-2026-10-16.md) |
| AC06：成功代码任务完整 Trace | Go 14 树／449 span、102 模型调用／113 工具请求；Python 11 树／540 span、102 模型调用／190 工具请求。各自正式 `product-integrity-v2` 收据为 PASS／complete／VERIFIED，missing、problems 为空；11／10 个 ERROR、原 legacy FAIL、旧失败尝试及故障缺根均保留，不以成功任务替代故障完整性 |
| AC07：同一批次、单任务、计量及并发 | 原 100,000,000-token 服务商硬上限、Go 持久批次及调用归因见[同轮计量](milestone-v5-matrix-2026-10-19.md)。当轮最终 ActiveCalls=0／任务槽为空；历史 unknown、原预留和未知费用保持。该历史空槽不代表当前 #27 任务槽为空，不重新解释为当前余额 |
| AC08：旧调用方、full-stack、单一 Ledger 与回退 | [兼容／回退](milestone-v5-compatibility-2026-10-19.md)的三包 43 PASS、旧 Windows 161 PASS／7 SKIP 与补验 4 PASS 分别保留；full-stack 三次完整九项分母分别为 3/1/5、6/1/2、9/0/0。v6b→v5 实际登录／重开保留五个 Session 前缀与预算，未复制、清空或双写 Ledger |
| AC09：选定 UI、材料 pin 与 launcher 归属 | 已选布局及实际审批／应用截图、原 frontend tests/build、v5/v6b manifest、组件和许可材料、实际 launcher 退出均在[兼容／物料记录](milestone-v5-compatibility-2026-10-19.md)与[许可材料](windows-python-notices-2026-10-19.md)中对应；不宣称完整生产法律或发布合格 |

正式 #2–#19 与 #21–#23 均已按各自范围关闭。逐项读取原任务 `Spec coverage` 后，
US01–US52 全部有映射，缺失为 0；票关闭和映射只用于追溯，验收仍依据上表实际证据。
原 #19 要求在其实施中保留父票打开；本次是持续完成全部 tickets 目标下单独进行的父票审计。

## 本轮只读复核

命令 `python -B .scratch/milestone-parent-audit-20261021/recheck_artifacts.py` 退出 0：

- 复用原封存核验器的全部断言，只把本轮观察结果写入新目录；原文件和验证规则不改。
- 原 6,804 条记录，加五类恢复及最终兼容／交付的 398 条记录，共 7,202 个唯一
  path/checksum/size 约束逐项匹配，没有缺失或不符。
- v5 manifest `72981044fe9885e0bc7bc56704562121b804be8370a9051ae6e6e288e5800957`
  及其 3,219 个文件、v6b manifest
  `c4b410aba1eda23bfbd640111d39639491bffeff2ddf4a329e6efa16a9cd3405`
  及其 3,225 个文件均匹配。二者原源码清单差异仍仅在构建器、组件 pin 与随包说明；
  没有将两份重建二进制称为相同。
- 两项成功代码任务收据的真实 run／span／工具／模型分母、v2 PASS 和原 legacy FAIL
  均重新读取核对；不启动后端或产生新的 runtime 结果。

本轮核验摘要 `artifact-recheck.json` 的 SHA-256：
`c608979e8d320d8d3801ffb767816e8599bd8d9c71a38672157ba1f2f337cdd5`。
原始规格正文 SHA-256：
`6321b88a91abbb683a2b0834fbd1b61b579cf8443329f135d58e2a632e798f5d`。
规格、票映射、命令日志、包绑定和核验结果保留在
`.scratch/milestone-parent-audit-20261021/`；这些是只读观察，Ledger 仍是事实源。

本机运行 `python -B -m pytest -q tests/eval/test_ci_scope.py
tests/eval/test_release_gate_workflow.py tests/eval/test_release_evidence.py`：
23 PASS、0 FAIL／SKIP，退出 0。四项文档的相对链接、精确 staged paths 的脱敏扫描及
`git diff --cached --check` 均通过；这些检查只支持本次文档／审计交付。

## 保留的独立缺口

[#27](https://github.com/huadeng408/CodeOps-Agent/issues/27) 完整 200 轮、长期故障、
Workflow／Skills、token 对照、官方 scorer 及完整发布仍有各自完整验收要求；
[#20](https://github.com/huadeng408/CodeOps-Agent/issues/20) 仍按用户要求暂缓。
#1 原文已将这些专项列为 Out of Scope，不追加为首批 milestone 的门禁。

最新 180/200 失败、缺失工具结果、取消 unknown、任务槽和 Trace 缺口仍见
[本轮产品记录](product-long-dialogue-2026-10-21.md)。本次未调用模型、未打开真实数据库，
未启动或删除 Docker 资源，未重跑产品源码的功能回归、真实产品或发布。
后续文档 CI 仅验证本次交付；持续 goal 保持未完成。
