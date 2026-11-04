# 独立 200 轮尝试 v8：第 1 项被 HTTP 402 拒绝

用户报告 BeeAPI 恢复后，按原授权启动新的独立完整 200 轮验证。
本次结果为 **0/200 FAIL**：第 1 项首次及唯一允许的新请求重试均失败，
第 2–200 项共 199 项未运行。未发送第三次请求，未拼接旧尝试的成功前缀。
[#27](https://github.com/huadeng408/CodeOps-Agent/issues/27) 保持 OPEN；#20 继续暂缓。

## 实际运行与完整分母

使用官方便携包 v15，运行源码为
`f46e7d13880a56b9483da46a3dd762ba0666ed77`，
[对应源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/39650841558) 为 5/5。
621 个源码文件、3225 个运行文件、17 个过程文件及加载的指令均核对通过。
原 200 条普通仓库请求字节不变，SHA-256 为
`3b53ca87f1e5a21acc26636d4597574baccf7024adfe6fd00a825230e3143e2f`；
应用上下文窗口仍为 32768。运行身份、模型／路由、任务与原件、控制资源、
共享账本前缀及计划恢复场景均在付费派发前冻结。

Session 为 `8b48ca8ad176fdb9fdda09c72c520daf`。

| 范围 | 实际结果 |
| --- | --- |
| 原始计划 | 200 项；完成 0、第 1 项失败、未运行 199 |
| 请求／run | 两个唯一准入身份，均 failed；0 条助手回复、0 次工具调用 |
| 原生事件 | 23；两次终态均通过 Go hash-chain 核验 |
| 模型分母 | 4 次：foreground 2、memory_reflection 2；均 HTTP 402、usage unknown |
| 后端 Trace | 两棵树、14 个结束 span，全部 ERROR；两条原生根、父链、模型身份及 purpose 对应 |
| Memory | 两条轨迹已提交，两条 commit-blocked 保留；不算反思成功 |
| 压缩／恢复／代码 | 0 compaction；刷新 40/80/120/160、中点重启 100、181–200 实施与项目测试均未运行 |
| 产品结束 | 实际 UI finish 返回 200；任务槽释放，活动调用 0；结束前后真实计量字段一致 |
| 资源与数据 | 浏览器、自有 Go/Python 子进程停止；两个原控制容器恢复 exited，Compose project 仍为 localcode |

原生请求与两个提交响应的身份、输入均已核对；失败出口未导出完整浏览器 POST 数组，
不将已保存的提交响应描述为完整网络捕获。原两个 run 为
`run:58800e92b2c4eb1801343c4ac6f93951`、`run:57e87cbe07d807984f390d811b215b0a`。

四次 unknown、原预留及未知费用状态保留，没有补写零消耗或费用。
全部 42494 条运行前记录的 10 列、146 条原流头、333 个原文件及 Git 索引／HEAD
均未改变；更早的 40199 条原记录及已批准 831 封存事实也再次核对。
没有重复封存，没有删除容器、volume、业务数据或旧失败证据。

只能确认本机产品使用的 BeeAPI／grok-4.6／Anthropic Messages 路由仍返回 402；
不能据此推断余额、key 限额、分组或提价保护的具体原因。
已请求核对该 key／分组，若换过密钥则更新原本机凭据入口。
控制台读取连续两次遇到 CDP focus-emulation 超时，未读取账户详情或修改设置。

## 命令、产物与验证范围

- 过程、冻结和原始请求：`deployments/docker/.local/profiles/product-long-dialogue-20261022-v8/`。
- 原生／后台读回、提交、终态及停止记录：
  `deployments/docker/.local/receipts/product-long-dialogue-20261022/go/v8/`。
- 截图：`output/playwright/product-long-dialogue-20261022/go/v8/`。
- 完整命令、退出码与校验和：`.scratch/product-long-dialogue-20261022-v8/`。
  `python -X utf8 -B <profile>/freeze.py` 退出 0；交互式 `driver.mjs go <inputs-sha256>`
  退出 0，保留产品失败；`read-backend.py ended1 round1-attempt1-terminal.json
  round1-attempt2-terminal.json` 和 `audit-ended-attempt.py` 均退出 0。

| 产物 | SHA-256 |
| --- | --- |
| 便携 manifest | `7a34c20a2f5c679f1116fab8b16f1606e656b916d094aedc60908a86a0aa9200` |
| 冻结 inputs.json | `5c9f65b4a1c6f5e5214666cb1453266031eb761b7a1cef2ee31798bacf08e2fa` |
| 最终只读审计脚本 | `f4c4794a52c42d564285e2e81f1b77fb4f0a7006e788c75e01c6e0c9af3e5f7b` |
| 最终只读审计 stdout | `9c9c2e2bdfbeb5efba7d8e76e6b717ede2d493b2869bfd9432ae0dbbc2522bf3` |

本次只读分母、身份、保留与生命周期审计通过；完整 200 轮产品验证失败。
本地 Go/Python 单测未重跑；引用的是既有源码 CI。完整产品语义、代码任务 Trace、
Memory 成功与发布均未验收，不以本次诊断或文档交付关闭 #27。
