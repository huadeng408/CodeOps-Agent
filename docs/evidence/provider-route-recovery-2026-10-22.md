# 独立模型连接验证：HTTP 402

在 [v7 的 61/200 失败](product-long-dialogue-v7-2026-10-21.md)及
[#29](https://github.com/huadeng408/CodeOps-Agent/issues/29) 的只读准入修复交付后，
使用新便携包做了一次独立产品连接验证。**1 次计划请求、1 次失败、0 次完成**；
前台及后台反思各返回一次 HTTP 402，用量和费用均未知。
这不是新的 200 轮尝试，不改变 v7 的失败分母。

当前只能确认 BeeAPI／grok-4.6 路由拒绝调用，不能据 HTTP 状态判断具体余额、
配额或此前 502 的原因。已请求服务商后台的拒绝原因或恢复状态。
[#27](https://github.com/huadeng408/CodeOps-Agent/issues/27) 保持 OPEN；
恢复连接后再冻结新的独立 200 轮。#20 继续暂缓。

## 冻结与实际结果

官方构建入口为 `python -X utf8 -B scripts/build-windows.py --output <新目录>`，
使用既有缓存离线构建，退出 0。源码提交为
`959ec2a5b9ba86a9fa8585c2982ac403c3e66729`，
[对应源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/39642523219) 为 5/5。
包内 621 个源码、3225 个运行文件的指纹均核对通过；构建不包含凭据或用户数据库。
该包位于 `deployments/windows/.local/Portable Git 20261022 route v14/`。

探针通过原浏览器／Go／Python／OpenSandbox 入口运行，只要求阅读原测试仓库的
`README.md` 并回答仓库用途和声明的 Go 版本，没有提供答案、补丁或修复提示。
请求、原文件／索引、共享账本前缀、模型／路由、运行包和两个既有控制资源均在
付费调用前冻结；Go 继续管理鉴权、准入、凭据、HTTP、预留及计量。

| 范围 | 实际观察 |
| --- | --- |
| 浏览器及原生请求 | 1 次 POST、1 个请求身份、1 个失败 run；没有新请求重试 |
| 助手及工具 | 0 条助手回复、0 次工具调用；失败发生在读取 README 之前 |
| 模型分母 | 2 次：foreground 1、memory_reflection 1；均为 HTTP 402、usage unknown |
| Memory | 原生轨迹已提交；反思的 `memory/commit-blocked` 保留，状态 pending、retryable true；不算 Memory 成功 |
| Trace | 7 个结束 span，7 个 ERROR；原生根、父链、两次模型身份／purpose 与 Go 预留完整对应 |
| 正常结束 | 实际 UI 结束请求返回 200；任务槽释放，活动调用 0 |
| 计量 | 两次 unknown 和原预留保留；没有补写零消耗或费用；结束前后真实计量字段一致 |
| 生命周期 | 浏览器及自有 Go/Python 子进程停止，两个控制容器恢复 exited；pageerror 和清理错误均为 0 |

Session 为 `dceaa42c2205379ef599b781a9419962`，run 为
`run:215264ba10aac648ac9887cf0b4a6b1a`。
原生导出通过 Go hash-chain 核验；探针前全部 42449 条事件的 10 列、144 条旧流头、
333 个原文件及其 Git 索引／HEAD 均保留。更早的 40199 条原始事件及已批准的
831 准入封存事实也再次核对。没有删除容器、volume、业务数据或历史证据。

首次启动驱动因非交互标准输入结束而正常退出，未创建测试 Session 或发出模型请求，
其停止记录保留。后续交互驱动使用独立路径，不覆盖首次启动产物；上表分母只计算
后续实际发出的单个请求。

## 观察入口及校验和

- 配置、冻结输入和过程：`deployments/docker/.local/profiles/provider-route-recovery-20261022-tty/`。
- 原生／后台导出、浏览器响应及停止记录：
  `deployments/docker/.local/receipts/provider-route-recovery-20261022/go/health-tty/`。
- 截图：`output/playwright/provider-route-recovery-20261022/go/health-tty/`。
- 只读审计和完整命令：`.scratch/provider-route-recovery-20261022/`；
  `freeze-tty-result.json`、`backend-result.json`、`audit-exit.json` 均为退出 0。
  `read-backend.py health1 probe-selection.json` 与 `audit-ended-probe.py`
  复用此前读取器和保留检查，分别核对实际后台分母及结束后的原文件／旧数据／生命周期。

| 产物 | SHA-256 |
| --- | --- |
| 便携 `bundle-manifest.json` | `9e645bb126211edf9ec22ca32904a8b9215384f9933270e5243856b1d5666e67` |
| 探针冻结 `inputs.json` | `d3106190ece119d5310dfd02b05d9450f9d209c6020ca97d600b0be347810664` |
| 只读审计脚本 | `245885ef2fb5115f227b02f091d7356942cdbb094c82ee0bc8fa7f2c7509aa47` |
| 只读审计标准输出 | `695196c854150203c227a30fef8deabfe3ea04ddf8b59627ba5d91640aa5738b` |

状态分别为：#29 源码单元／集成修复已交付；本次模型连接验证失败；
只读分母与保留／收尾审计通过；新 200 轮及完整产品／发布验收未运行。
