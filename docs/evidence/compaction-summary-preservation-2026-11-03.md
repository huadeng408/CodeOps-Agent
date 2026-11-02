# 确定性再压缩保留已有摘要

关联 [#27](https://github.com/huadeng408/CodeOps-Agent/issues/27) 的约束保留要求和
[#40](https://github.com/huadeng408/CodeOps-Agent/issues/40) 的受控需求更新实验。

## 已确认的故障

原付费五轮任务的源码为 `c6eab0b1899fe4fa7e2fd47b85dc8c3ebe3a8c58`，
最终经新 reader 独立评分为 7/8，业务不通过，正常结束至 310 条事件。
原结果和源码绑定见[完整实验报告](agent-evaluation-experiments-2026-11-03.md)，不改写原负结果。

只读检查发现：seq102 摘要仍完整包含历史修订，seq129 的确定性摘要却把它当普通消息，
规范空白后仅保留前497字符加省略号；后部修订规则全部消失。
seq120 当前用户输入与配置、实际请求的 SHA 相同，当前输入没有丢失。
这些观察定位了一条确定性丢信息路径；为何采用该路径、它是否为最终错误的唯一原因仍未证实。

## 修改

- 内部压缩消息保留已有 `source_events`，据此识别带 canonical 来源的 system 历史摘要。
- 确定性路径完整平铺保留旧摘要，不论它在 older 或 recent 区间；不重复 JSON 转义。
  其余历史继续使用既有有界 digest，不扩大总字符预算。
- 无法在预算内保留时明确抛错，不发布新的压缩 update；原 Surface 与来源保留。
- 先尝试有效模型摘要，只有确需确定性路径时才构造它，避免预算拒绝抢先阻断有效模型结果。

没有新约束数据库、协议字段或 Ledger 事件类型。有效模型摘要仍可能遗漏语义，
本次修复不提供模型总结质量保证，也不把源码回归当作原任务付费复测。

## 实际验证

公共 `compact_surface` 边界使用合成修订标记，未复制业务 gold 或修改隐藏检查。

| 检查 | 结果 |
| --- | --- |
| older／recent × 异常、空、无效、过长模型摘要 | RED 8 fail，退出 1；GREEN 8 pass，退出 0 |
| 扩展预算、有效模型摘要、重复压缩、当前输入与工具配对 | 13 pass，退出 0 |
| 相关 7 个 Python 文件 | 169 pass、1 skip，共 170；退出 0 |
| 两个跨进程案例统一 UTF-8，线程警告升错误 | 2 pass，退出 0 |
| AST／diff check | 均退出 0 |
| Standards／Spec 独立审查 | 各 0 finding |

相关回归原有 3 条警告保留；其中 2 条 GBK 读 UTF-8 子进程管道警告已按上述严格方式复核，
依赖弃用警告保留。跳过项是本机 Windows 无法创建测试 symlink。
419 个相关 Python、模板、指令与依赖输入在最终回归前后指纹一致。

忽略目录 `.runtime/eval/agent-experiments/artifacts/compaction-summary-preservation-v1/`
保存原 RED、GREEN、全部分母、警告、输入指纹和只读诊断索引；20 项 checksum 全通过。
summary SHA-256 为 `56cdda5adda21909ef0345b91777f20dec76ef7c8464b2ed40c5fe7f7871b169`，
diagnosis digest SHA-256 为 `b1001b512e056d10e4fdc93c4a468afb22460dfc61bece162d7887284ccecc9f`。
该范围没有模型、Docker 或真实 Session 写入；新的 200 轮与完整发布仍单独验收。
