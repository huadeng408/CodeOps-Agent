# Agent 真实对照实验：预算、恢复与多轮负结果

范围：[规格 #36](https://github.com/huadeng408/CodeOps-Agent/issues/36)、
[#39 预算策略](https://github.com/huadeng408/CodeOps-Agent/issues/39)、
[#40 故障与多轮](https://github.com/huadeng408/CodeOps-Agent/issues/40)、
[#41 报告](https://github.com/huadeng408/CodeOps-Agent/issues/41)。本次完成小规模真实实验的执行、独立评分和正常收尾；业务负结果保留，不代表完整发布合格。

## 完整分母与来源

| 来源 | 完整分母 | 结果 |
| --- | --- | --- |
| 旧 Context／Memory | 同一独立任务、2×2 配置、每臂 2 次，共 8 次 | 7 次通过、1 次提供方未知；只修正分类，不新增执行 |
| 新预算 | 同一独立任务、3 策略×2 预算，每格 N=1，共 6 次 | 5 次通过、1 次预算准入失败；6 次均 finalize |
| 新恢复 | 1 无故障＋3 同 Session 续行＋3 从头重跑，每格 N=1，共 7 次 | 7 次通过，各自独立检查 7/7；6 次实际进程中断；全部 finalize |
| 新五轮序列 | 1 个任务、5 条顺序消息，N=1 | Go 五轮完成、4 次根压缩；补评分 7/8、success=false；已正常 finish |
| 历史 v31 | 另一次 14 项计划 | 1 infra_error＋13 not_run，单独保留 |

新 14 项的付费执行冻结于 clean `c6eab0b1899fe4fa7e2fd47b85dc8c3ebe3a8c58`。
Sequence 补评分 reader 为 clean `d8188e3854de5630252bd0e26753d5d8f3add5db`，
binary SHA=`f4e783a4d604362e209a3d7dfdba59e68f0b06d6edeac08f252260bf678d76a7`。
新 reader 没有重跑模型，不声明它执行了原五轮或与原源码整体等价。
旧 Context 的源码为 `7de606d496d220d54de6510044a25cd2c65cc8e7`，
原 dirty_hash=`23c0cf1ebadeb71ec08383414cec679476bb9b04d349d540c7c777487415f7a8`、51 untracked；
历史 v31 为 clean `06ed8075a4a8863d71a11073d13baab8e9b10054`。
各来源不合并为同一预注册矩阵或总体成功率，五条消息不算五个独立任务。

## 生产边界与计量

公共评测 CLI 复用 Go TaskWorkspace、模型 Gateway、工具授权和 Session Ledger。Go 文件工具操作受约束副本；命令、提案测试与隐藏评分使用 Docker-backed OpenSandbox。Python 负责策略与编排，不能绕过 Go 执行副作用。独立评分核对候选、检查和输出哈希、评分 runtime 停止状态及 canonical 事件，再读取正常终态；隐藏答案不进入 Agent 输入或派生报告。

新运行固定 grok-4.6 请求配置：xhigh、thinking=true、ModelFast 空、上下文能力 500,000、输入上限 368,928、输出上限 131,072；主 Agent、child、重试和反思共用 Go 预算。上游权重 revision 仍 unknown。金额和币种 unknown，不查账单，不把 token 当货币。

## Context／Memory：旧来源的有限观察

常规检索基线按任务预读候选源码，产品组使用元数据与按需读源；每次总预算 1,000,000 token。Memory 隔离冻结经验的读取贡献，各次写回隔离。

| Context／Memory 读取 | 计划／通过／未知 | 完整用量试次的输入 token 合计 |
| --- | --- | ---: |
| 常规检索／关 | 2／2／0 | 156,116（2 次） |
| 按需读取／关 | 2／2／0 | 114,756（2 次） |
| 常规检索／开 | 2／2／0 | 125,007（2 次） |
| 按需读取／开 | 2／1／1 | 60,746（仅 1 次；另一总用量未知） |

关闭 Memory 的两个完整配对重复中，按需读取输入 token 合计下降 26.49%，双方均 2/2 通过；只支持单任务描述性观察，不外推总体提升，也未达到发布目标中的 60%。未知试次原有 10,578 confirmed、500,000 reserved 和 1 unknown call 保留。当前数据未证明 Memory 提升成功率，不用成功子集替代完整 8 次。

![旧 Context 四臂；灰色表示提供方未知](../../.runtime/eval/agent-experiments/artifacts/production-delivery-v3/figures/module-comparison.png)

## 预算策略：保留真实准入负例

| 策略 | 预算 | 结果 | confirmed token | 模型调用／child | 整次耗时（秒） |
| --- | ---: | --- | ---: | ---: | ---: |
| single | 600,000 | 通过，检查 11/11 | 122,084 | 6／0 | 172.97 |
| single | 1,000,000 | 通过，检查 11/11 | 116,150 | 6／0 | 224.33 |
| verify_retry | 600,000 | 通过，检查 11/11 | 111,915 | 6／0 | 201.12 |
| verify_retry | 1,000,000 | 通过，检查 11/11 | 117,945 | 6／0 | 246.34 |
| subagent | 600,000 | budget_exceeded，未评分 | 105,876 | 7／4 | 192.36 |
| subagent | 1,000,000 | 通过，检查 11/11 | 281,411 | 14／7 | 353.81 |

低预算 subagent 的剩余 494,124 小于下一次调用的 500,000 预留要求，因准入拒绝终止，实际只确认消耗 105,876；不能写“耗尽 60 万”。两个 subagent 组均有真实 child 调用。两次 verify_retry 均首次公开验证通过，attempt_count=1、retry_observed=false，未观察到重试修复收益。各格 N=1，不作策略总体排名。

六格共 45 次已计量模型调用、855,381 confirmed token，scoped reserved=0、unknown_calls=0。后台读回覆盖 6 个最终 root、45 个 provider spans，token 与 native 计量一致。

![六格预算、实际用量与耗时；货币费用未知](../../.runtime/eval/agent-experiments/artifacts/production-delivery-v3/figures/budget-success-cost.png)

## 故障恢复：真实窗口与有界最终状态语义

| 路径／中断窗口 | 计划／通过 | 含原尝试的 confirmed token | 整次耗时（秒） | 续行恢复段（秒） |
| --- | --- | ---: | ---: | ---: |
| 无故障 | 1／1 | 75,684 | 134.96 | 不适用 |
| 续行／副作用前 | 1／1 | 149,335 | 224.79 | 139.21 |
| 续行／副作用后、结果持久化前 | 1／1 | 85,787 | 230.79 | 132.90 |
| 续行／结果持久化后 | 1／1 | 84,293 | 188.23 | 101.07 |
| 从头重跑／副作用前 | 1／1 | 114,660 | 242.97 | 未单独记录 |
| 从头重跑／副作用后、结果持久化前 | 1／1 | 109,815 | 207.15 | 未单独记录 |
| 从头重跑／结果持久化后 | 1／1 | 111,304 | 212.52 | 未单独记录 |

6 次中断同时具备物理进程停止与 Go canonical held 窗口证明。3 次续行保持同 Session／Run，分别验证可重放、已应用后禁止重放及复用已持久化结果；3 次 restart 使用新 Session／工作区，保留原尝试。restart 原消耗 32,689／32,764／32,771 逐项从共同预算扣减，不把重跑当同 Session 恢复。各窗口 N=1，不外推恢复率；表中整次耗时含中断前执行及收尾，重跑段未另计时。

Go Ledger 合计 44 次调用、730,878 confirmed token；7 个最终 root 的后台捕获含 32 个 provider spans、534,690 token。其余 12 次故障前调用与 196,188 token 不在该后台捕获范围，由 canonical Ledger 保留为已知消耗；不记作零、不补造 span，也不要求 hard-kill 的旧 root 已结束。

7 格原 `physical_execution_count`、`duplicate_effects` 均 unknown，`clean_recovery_fraction` 仍 null。固定 checks 与独立 Go 评分逐项通过，可受限派生 `final_business_duplicate_entries=0`，范围严格为 `final_scored_workspace_only`：最终被评分工作区中的指定业务项恰一次、原输入未变。该指标不覆盖被放弃的旧工作区，不证明物理 exactly-once。每格的 checks、grade、final-score、grade-inspect、finalization、final-inspect 定位与 SHA 保存在交付 `source-index.json` 的 `recovery_business_semantics`；不将原字段改成 0。

![三个窗口的续行与从头重跑；N=1，耗时为整次试次](../../.runtime/eval/agent-experiments/artifacts/production-delivery-v3/figures/recovery-paired.png)

原恢复宽表另保留为 `figures/recovery-windows.png`，clean 与 duplicate 继续显示 native unknown。三张原图均按字节复制，图中 caller record 仍指各自原来源，由来源索引定位；恢复双面板只从已验证 6 个故障行派生 token 和整次耗时，不代表新实验。

## 五轮序列：评测闭环完成，业务结果不通过

原 Python 将 Go 拒绝、未 dispatch 的 Git 调用误判为“已执行但缺 runtime”，留下 sealed infra_error、success=null、completed_turns=4。原 summary、NDJSON、trace 和 checksum 均保留。原生 Go 实际五轮 completed，4 次 root compaction，23 次调用、483,328 confirmed token。

新 reader 仅执行 inspect／grade／finish：逻辑 5 步、实际 7 次 Go 操作。首次完整工作区复核追加租约事件，head 306→307；旧 CAS 被明确拒绝，0 grade intent、0 scorer。独立 inspect 后仅重试一次，真实评分至 head 309，正常 finish 后为 head 310，最终 checksum=`9363b48faab00746809710edd2c35e709dc793235daa410de57a49de6ccd6b13`。所有步骤保持原 Session／最终 Run、23 次调用和 483,328 token，reserved=0、unknown_calls=0，无新增付费模型调用。

独立 scorer 已停止、exit_code=0，8 项检查中 7 项通过、success=false：原输入完整性检查通过，最终输出未遵守已更新的格式约束。该结果属于已完成评分的业务负例，不能写成“尚未评测”或“需求更新保持成功”。工具拒绝证据和受控租约复核修复使旧候选能被真实评分，不改变旧付费执行结果。

只读 canonical 诊断定位到一条确定性丢信息路径：seq102 的已有摘要仍完整保留修订约束，seq129 再次压缩时却将它按普通消息裁剪为前497字符加省略号；位于后部的具体规则由此消失。seq120 的当前用户输入与原配置、实际请求哈希一致，当前输入没有丢失。不能把此次结果仅归为模型拿到完整契约后的执行失败；采用确定性路径的具体原因以及它是否为最终错误的唯一原因，仍未证实。

补评分控制流程 exit 0 表示控制与收尾完成，不表示业务通过。控制器和 Phoenix 已恢复原 stopped 状态，原生 owned 进程停止、active egress 为空，镜像 pull／build／recreate 均 0。没有以重跑付费模型覆盖失败。

针对这条路径的[摘要保留修复及独立源码回归](compaction-summary-preservation-2026-11-03.md)另行记录。
已有摘要完整平铺保留，预算容不下则明确拒绝，未增加第二份约束状态源。该源码修复的回归和审查通过，仍不能将本次旧候选的 7/8 改为业务通过，或宣称已经完成付费复测。

## 产物与验证范围

最终派生包为 `.runtime/eval/agent-experiments/artifacts/production-delivery-v3/`：`source-index.json`、`summary.json`、`report.md`、`interview.md`、3 张原图、恢复双面板与 `checksums.sha256`。它索引既有事实，不创建第二套 Session／评分事实源。口述源为 `.runtime/eval/agent-experiments/interview-final-v7.md`，正文少于 600 字。

生成入口为 `.runtime/eval/agent-experiments/build-production-delivery-v3.py`，只读取明确列出的原产物、调用既有 `RunArtifacts.verify_checksums`／`write_checksums`、复制既有图并用标准 matplotlib 派生恢复双面板。源 checksum、引用 SHA 与最终包校验均须为空问题列表；图像已核对包含失败、未知和原分母。生成报告不调用模型、Docker、产品 operator 或测试。reader 源码对应 [CI 41510847099](https://github.com/huadeng408/CodeOps-Agent/actions/runs/41510847099) 已全部成功；源码 CI 与本次业务评分负结果分别记录。

本轮保留 N=1／单任务、模型 revision、货币费用、物理执行次数和原未知计量的限制。旧 Context 8 次、当前 14 项、历史 v31 14 项以及更早历史分别保留来源和分母。本实验交付不替代 #27 独立 200 轮或完整发布七条 lane。
