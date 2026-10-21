# 独立长对话 v7：40 轮范围核验

本记录交付同一次实际产品运行的前 40 轮证据。
[#27](https://github.com/huadeng408/CodeOps-Agent/issues/27) 保持 OPEN；
原完整 200 条输入、后续恢复、代码／项目测试与最终验收继续执行。
旧失败尝试见 [180/200 与基础修复](product-long-dialogue-2026-10-21.md)，
没有拼接不同 Session 或缩减分母。

## 运行绑定

- 实际路径：官方便携 v13 → Go Harness → Python → 浏览器；
  Session 为 `b5b44f0c5b326b35b1bdfad2b7927ff5`。
- 运行源码：`befc2b0069128ea6652c70e99f5a89e2e2849303`。
  后续说明文档不改变这份原始运行绑定。
- v7 冻结输入 SHA-256：
  `7a77bdd616bcd4c65bb95e1c63de47712bcf95893944b4a917126e3a107c1639`。
- 原 200 条正常仓库任务输入、grok-4.6、32,768 应用窗口及原计量批次保持。
  模型名称和应用窗口不代表已验证的服务商模型 revision 或费用。
- 本节点核验 621 个源码、3225 个运行文件、17 个过程指纹，
  以及 333 个受保护原文件、Git 索引和 HEAD；均保持原指纹。
  准备与原任务封存范围见 [构建与冻结输入](product-long-dialogue-preparation-2026-10-21.md)。

## 本节点的实际分母

| 项目 | 已观察结果 |
| --- | --- |
| 计划轮次 | 完成前 40/200；完整任务尚未验收 |
| 原生 run／HTTP 提交 | 41 个不同请求，40 completed、1 failed |
| 失败与重试 | 第 38 轮首次失败、0 条 assistant 回复；唯一一次新请求重试完成 |
| 实际刷新恢复 | 第 40 轮重开同一 Session；刷新前后 945 个事件完全相同 |
| 压缩 | 6 次真实模型压缩；当前输入、来源 checksum 和来源工具配对通过 |
| Memory | 41 份原生轨迹投影一致；4 次反思错误及 blocked 事实保留 |
| 后台 Trace | 41 个 run、468 个结束 span；457 UNSET、11 ERROR |
| 工具 | 43 次调用／43 个结果／43 个后台工具 span；42 次退出 0、1 次退出 1 |
| 模型 | 130 次原生预留与 130 个后台 attempt 身份／purpose 完全对应 |
| 已知与未知用量 | 128 次用量已确认，2 次 unknown；原预留保留，费用状态仍未知 |

130 次模型尝试包含 83 次 foreground、41 次 memory_reflection、6 次 compaction。
本前缀已确认输入 2,851,883 tokens、输出 291,918 tokens；未知调用没有补写零消耗。
分母核验用计划第 41 项输入对应的下一条原生 user/message（第 42 次已准入请求）
作为边界，确认前缀内的 Go 预留集合与实际后台 attempt 集合相同；
后续运行不计入这份前缀结果。

第 18 轮并行 ReadSpill 被工作区准入明确拒绝，随后普通 Read 完成读取。
原拒绝结果与后台 ERROR span 对应，未变成 tool/unknown。
ReadSpill 并行分类的一行候选修复已在离线 Go overlay 复现 RED、验证 GREEN：
20 项定向测试通过、0 跳过、0 失败。候选尚未接入冻结的运行版本，
源码接入、完整回归和独立审查保留为后续工作。

反思错误发生在第 2、14、23、24 轮；后台 stage 均为 `reflector`，
其中三次为 `reflection_invalid_or_unavailable`、一次为 `reflection_error`。
四条原生 blocked 事实与四个反思 ERROR span 对应。
第 38 轮失败的前台 attempt 在 HTTP 200 下记录 ERROR 和 usage unknown；
原失败、未知计量及重试请求身份均保留。

实际截图显示重开后的同一标题、事件数和历史消息；初始面板加载状态保留。
随后第 41 项提交截图显示隔离工作区已准备、99 项清单及可用测试入口。
这是 `AI_REVIEWED` 的局部视觉观察；浏览器错误的完整汇总、后续交互和最终 UI 范围仍待核验。

## 命令与原始观察

以下只读命令均退出 0；完整命令、输入指纹及 stdout／stderr 保留在
`.scratch/product-long-dialogue-20261021-v7/`：

- `verify-browser-boundary-v3.py 40`：实际请求、前后原生前缀、Surface、Memory 投影、
  文件／运行指纹、原件和两个既有控制容器身份核验。
- `read-backend.py boundary40 <全部 41 个终态观察文件>`：真实 Phoenix `default` 项目读回；
  容器的 Compose 分组仍为 `localcode`。
- `join-runtime-cutoff.py boundary40`：根／工具／模型／attempt 与原生事实逐项对应；
  保留全部 ERROR、失败、未知用量和费用状态。
- `check-boundary40-models.py`：前缀完整原生模型预留集合与后台集合相等。

原生会话、准入及后台原始文件保留在
`deployments/docker/.local/receipts/product-long-dialogue-20261021/go/v7/`；
实际截图保留在 `output/playwright/product-long-dialogue-20261021/go/v7/`。

| 观察文件 | SHA-256 |
| --- | --- |
| `boundary40-after-session.json` | `17d70cf6eb9dfa5a4183fe0b5e1a795ea21eb4b3504b06c70b5931388eb4c731` |
| `boundary40-backend-index.json` | `1fe2b6cd172b0d9276243879652b8f0bca01231ef9f5ac5e712230d377883f22` |
| `boundary40-verification.json` | `3b571168c6b365a2dd06d32427190e9966aacda8fd223789ac587d086051083e` |
| `boundary40-native-trace-join.json` | `91ce199895508ded9ac0bf5f9846df561fd92546356522d1347c9b84f97c48f3` |
| `boundary40-model-denominator.json` | `af052e07b58d44acca76652c1ec5e0640940005bd05b93f7306cb3a1bf9f0f37` |

早期 span 名称统计与一次读回命令入口错误的原记录、更正和失败退出码均保留；
本节点使用实际 span 名称及上述完整原生／后台集合，不使用原错误的零计数。

## 尚未完成的范围

完整 200 assistant-turn、后续刷新和中点 Go/Python 重启、目标召回与语义质量、
压缩后的实际代码／项目测试、产品结束、完整最终 Trace 及资源恢复仍待验收。
两个控制容器此时继续为同一次运行服务；没有删除容器、volume、业务数据或旧证据。
本说明文档不是完整产品收据，未运行完整发布门禁，也不据此关闭 #27。
