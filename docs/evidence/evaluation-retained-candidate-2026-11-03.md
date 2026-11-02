# 已完成评测候选的证据读取与租约复核

关联 [#40](https://github.com/huadeng408/CodeOps-Agent/issues/40)、
[#41](https://github.com/huadeng408/CodeOps-Agent/issues/41) 和
[#42](https://github.com/huadeng408/CodeOps-Agent/issues/42)。
本页记录源码修复及开发验证；原候选的补评分、正常结束和真实收据另行验收。

## 原始故障与保留范围

`production-sequence-v11` 的付费执行源码为 clean
`c6eab0b1899fe4fa7e2fd47b85dc8c3ebe3a8c58`。
五条原生 Go 回合均完成，最终为 306 条事件、23 次已计量调用、483,328 confirmed token，
包含 4 次 root compaction。最后一轮有 Go 在派发前明确拒绝的 Git 调用，没有运行时。
Python 把它当作执行证明缺失，原 summary 因而保留 `infra_error`、4/5、未评分及未结束。
原 summary、完整分母、stdout、checksum 和候选工作区不改写，不重跑这五轮模型请求。

延后评分还暴露租约过期问题：工作区仍可验证，但过期租约不能直接取得评分准入。
不能通过修改时间、TTL、SQLite 或手填 head 恢复准入。

## 实现边界

- Go 从已有 canonical `tool/result.Error` 投影 `is_error`，不增加 Ledger 事件或修改协议。
  Python 仅接受明确错误、已知结果、0 dispatch／1 result／0 unknown／0 commit、
  合法结果引用且 runtime 为空的派发前拒绝；已派发工具仍须真实运行时及停止证明。
- 过期评分先验证 owner／actor／root／hold、原 CAS、固定隐藏检查、完整工作区及空闲状态。
  公共 CLI 另只读核对当前 batch 无活动调用且仍由原任务持有；历史 unknown 不单独阻塞。
- 复用既有工作区验证和续租事件。严格 CAS 冲突立即拒绝，不追随新 head；
  自身续租后返回 `grade_session_changed` 及 canonical 租约证明，0 grade intent、0 scorer。
  调用方读回新 head 后才能重新提出评分；已记录评分保持幂等。
- 测试夹具删除通用 Git 代理可执行文件，改用真实 Git 的固定 post-checkout 失败路径，
  继续验证失败矩阵和 checksum 收尾。没有关闭 Defender 或添加排除规则。

## 已执行的开发验证

Go 使用已有 1.26.0 SDK 和缓存，顺序执行；本节没有真实模型或 Docker 运行。

| 检查 | 实际结果 |
| --- | --- |
| 派发前拒绝的 Go 公共案例 | RED 退出 1，GREEN 退出 0 |
| Python 证据形状 RED | 3 个合法拒绝失败、10 个不合法形状通过，退出 1 |
| Python 最终公共 CLI 文件回归 | 112 pass、1 skip，共 113；退出 0，999.11 秒 |
| 过期租约公共行为 | 原实现 RED 退出 1；GREEN 10/10，退出 0 |
| 既有评分／工作区／续租调用者 | 44 pass、1 skip，共 45；退出 0 |
| 公共 CLI ProductionGrade | 8/8，包括活动准入拒绝；退出 0 |
| 两个相关 Go 包 vet | 退出 0 |
| Standards／Spec 独立审查 | 各 0 finding |

Python 首次完整尝试的 41 pass／71 fail／1 skip 及 Defender 阻断记录保留。
精简夹具后的最终运行前后 Python 源码闭包一致；不声称同期整个工作树未变化。
最终日志 SHA-256 为 `5412986f68db6a354cd0cb7701c1cf0017c15b086fa5d0f034d495a6b7af7b74`。

原始开发记录在忽略目录：

- `.runtime/eval/agent-experiments/artifacts/evaluation-predispatch-error-go-v1/`
- `.runtime/eval/agent-experiments/artifacts/pre-dispatch-error-fix-v1/`
- `.runtime/eval/agent-experiments/artifacts/evaluation-grade-lease-revalidation-v1/`

提交后的新 reader 构建、独立评分与终态读回必须分别记录实际源码和二进制身份，
不能宣称新 reader 源码执行了原五轮。单测、协议夹具和源码 CI 不替代真实评分，
也不代替 [#27](https://github.com/huadeng408/CodeOps-Agent/issues/27) 的独立 200 轮或七条发布 lane。
