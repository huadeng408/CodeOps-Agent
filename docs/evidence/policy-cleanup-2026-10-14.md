# 指令、导航与门禁清理：2026-10-14

用户批准审计建议 1–8。工程状态：`IMPLEMENTED`；验证范围为指令加载、导航／归档、
公开发布门禁接口和开放票正文。完整产品与发布分别按 Goal 验收。
基线：`eb66b786e34c6308db781b8a76befc3104cb445a`。

| 项 | 完成内容 |
| --- | --- |
| 1 | Python 每个目录优先读 AGENTS.md，缺失时兼容 AGENT.md；Go Context 候选与模型引导同步，Harness 原文仍经 Go 授权读取 |
| 2 | 移除永久断言 Goal 必须写 BLOCKED 的测试；公开 evaluate_release 在七条收据缺失时仍拒绝发布 |
| 3 | Goal、Token 文档和 #3 追加状态按本票计量／准入分母验收；后续完整任务、反思写回及 Trace 按各自票验收 |
| 4 | 简短 Design Map 纳入版本控制；退休依赖忽略 YAML／scratch 路径的检查器及其测试 |
| 5 | 十二份旧正文、规划和检查器原文按字节保留，清单校验一致；活跃页面只保留当前入口 |
| 6 | #3、#5、#7–#19 共十五张开放票更新并读回核对；三套视觉方案仅针对新布局或明显视觉改版 |
| 7 | CONTRIBUTING.md／SECURITY.md 与普通说明文档可保持运行等价祖先收据；运行指令、源码、配置、依赖和链接检查仍拒绝相关变化 |
| 8 | 四个无用本地 LFS hook 移入可恢复目录；历史 LFS 文件为零，无 LFS 属性，其余 hook 和 Git 配置保留 |

正式 Issue 的验收条款、依赖和未完成状态保留。发布阈值及七条 lane 未降低。
归档见 [索引](../archive/INDEX.md#policy-and-navigation-cleanup--2026-10-14)及
[十二项清单](../archive/policy-cleanup-2026-10-14/manifest.json)，清单 SHA-256：
`821626cd578cfea052c2f14d46249af257d2546420be58d47fd9a1b9b3ab3364`。
Git 属性保留清单绑定的原字节，历史源文件以 `.txt` 后缀保留。
四份本机 hook 的恢复清单位于 `.scratch/repository-qa-20261014/local-lfs-hooks/manifest.json`。

## 验证

| 命令／检查 | 当轮结果 | 退出码 |
| --- | --- | --- |
| 既有 Conversation 指令加载回归 | RED：canonical 缺失 1 失败／legacy 1 通过；GREEN：两项通过 | 1 → 0 |
| 既有 Manager Context 回归 | canonical 候选错误复现，修复后 metadata／wire 与候选通过 | 1 → 0 |
| 说明文档收据回归 | RED：仅新增两份说明文档使收据误拒绝；GREEN 已包含在下列 67 项 | 1 → 0 |
| `python -m pytest -q tests/eval/test_release_gate.py tests/eval/test_release_gate_workflow.py tests/eval/test_phase1_gate_compliance.py` | 67 通过，运行输入／symlink／依赖拒绝边界保留 | 0 |
| `python -m pytest -q tests/eval/test_release_gate.py::test_release_gate_rejects_missing_lane_receipts` | 新增缺七条收据的公开行为检查，1 通过 | 0 |
| `go test ./... -count=1 -json` | 1,523 通过／72 跳过／0 失败；45 个包通过，13 个包无测试 | 0 |
| `go vet ./...` | 通过 | 0 |
| `python -m pytest -q` | 2,481 通过／17 跳过／31 warnings；此轮收集后新增的缺收据测试另按上行验证 | 0 |
| 归档及当前导航 | 十二项 SHA-256／字节数匹配；八份当前导航的本地链接存在 | 0 |
| Standards／Spec 审阅 | Standards 原范围冲突已修复，最终两轴各零项剩余发现 | — |
| `python -m eval.release_gate --json --skip-tests` | 完整发布 BLOCKED：工作树改动及七条 lane 缺失／陈旧／smoke；此命令不运行测试 | 3 |

原始 RED、失败入口、跳过和日志保留在 `.scratch/repository-qa-20261014/`。
最终 Python 日志为 `python-policy-final.log`；与先前 2,484 项日志分别保留。
单测、集成和文档清理的完成不代表完整模型产品或发布通过。
同期实际仓库 QA 和两项运行修复的范围见 [问答／恢复证据](repository-qa-2026-10-14.md)。
