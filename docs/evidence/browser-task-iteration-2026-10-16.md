# 并发入账修复后的真实浏览器尝试

范围：#17 的实际 Go 任务尝试与失败恢复。结果：`NOT_VERIFIED`；十轮验收未完成。
本记录保留全部尝试，不作为成功代码任务、#18 或发布合格的证据。

运行源码为 `29f5618c94f6c27aff27215bc174a74865fae2a9`，证据绑定提交为
`dc9ad36c94b694c5d73a43c5499f05712d347485`。沿用原公开 chi 任务和工作副本；
608 个源码／验证器指纹及新便携包已经核对，冻结输入 SHA-256 为
`65f1042abd56ab65ab870d27a579f9f7642bdc48628fc14fe1e182cf040bcfcf`。

## 实际结果与完整分母

Session：`b2f903c581f0f286279745acccb0c6f5`。
一键启动、浏览器注册和会话创建成功；右侧卡片运行
`go test ./... -count=1`，两个包通过，退出 0，输出完整，隔离任务停止已确认。

| 尝试 | 原生 run | 结果 |
| --- | --- | --- |
| 1：读取公共接口与现有行为 | `run:f893721c71f87d0c2fa66a60eca209be` | completed；31 个工具调用与结果完整入账，无工具失败 |
| 2：收敛公共入口验收矩阵 | `run:eb187514e565e26d0afbe2953d1f36b6` | completed；区分原任务要求、兼容行为和不扩大的范围 |
| 3：新增第一个回归 | `run:541317b854563dadb886f4f34f347e83` | canceled；一次实际 Edit 参数不匹配，随后未执行的请求仍有额外缩进 |

三轮尝试中两轮完成、一轮取消，不能计作十轮成功。共有 36 个 durable 工具调用，
`code/modified` 为 0，没有生成或批准应用提案。Go 精确匹配拒绝了错误参数，未修改文件。
14 次模型调用全部结算，其中 foreground 12、反思 2；确认 398,926 tokens，
新增 unknown 为 0，费用仍 unknown。历史 unknown 及其原预留不改写。

三个 run 的真实后端读回分别有 57／7／20 个 spans，共 84 个，含 2 个 ERROR；
完整失败诊断结果为 FAIL，没有用前两轮成功子集替代整个任务。
三个 run 的原生 Session／admission 前缀与身份审计均退出 0；该审计证明事实完整性，不证明任务成功。

## 浏览器驱动故障与恢复

保留三项观察失败：Windows 默认输出编码读错中文进程路径；发送前检查发现后台调用仍活跃，
因此没有发送第三轮消息；取消时用可见文字代替 aria-label 定位按钮，响应等待 Promise 提前拒绝，
原驱动退出 1，取消尚未发生。

通过原启动进程的环境取得本轮自有测试账号凭据，仅在内存中使用，经正常浏览器登录恢复同一账号／会话，
没有重置身份库、改写 Ledger 或复制凭据到文件。恢复登录返回 200；使用实际可访问名称取消返回 202，
run 状态为 `canceled / stopped`。随后实际浏览器结束代码任务返回 200，ActiveCalls 为 0、任务槽为空。
恢复驱动退出 0，所属 Windows job、浏览器及本轮启动的 Go／Phoenix 服务均已回收或恢复原停止状态。

原工作副本及隔离副本各 99 个文件、原 index、dirty/new 状态和 608 个参与源码指纹均保持原值。
没有删除业务数据或既有 Docker 资源。初始驱动失败、恢复日志、所有截图、原生导出及完整 backend capture 均保留。

产物索引：`deployments/docker/.local/receipts/browser-tasks-20261015/go/ledger-v1/failed-edit-argument-artifact-manifest.json`，
56 项，SHA-256 `b999a277179cc36d799e9eb5ac8bf9c2bb29f246047ed8d8cdd7c1c362fe1d1d`，密钥形状扫描 0 发现。

## Python 下一步的独立缺口

现存真实 Python 基线收据显示 81/81 通过，但 `proposalAvailable=false`，原因是
`completed Go workspace and confirmed execution are required`。
当前 `TaskWorkspaces` 的提案可用条件仅允许 Go；提案显示命令和实际 Runner 请求也固定为 Go。
因此 #18 需要先补齐同一 Go Harness 内的 Python 冻结提案测试、已批准离线依赖绑定及现有应用边界，
不能仅打开按钮或再做一次只读模型调用就认为闭环可用。

#17/#18 保持 OPEN。完整发布未运行；本轮没有产品源码修改，原源码 CI 不作为十轮验收证据。
