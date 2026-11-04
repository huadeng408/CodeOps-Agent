# 独立长对话失败与基础修复

本记录区分一次真实产品尝试、其资源清理以及随后源码修复的验证范围。
Session Ledger 仍是事实源；下列文件是保留的不可变观察与运行产物。
任务为 [#27](https://github.com/huadeng408/CodeOps-Agent/issues/27)，不据本轮修复关闭。

## 真实产品尝试

运行采用原批准的 Go 任务和完整 200 条输入、grok-4.6、32,768 应用窗口、
OpenSandbox、实际浏览器以及独立 v12/v6 身份。运行源码为
`43b29c8c81713744d142dd4344b37d5440c27517`。
冻结输入 SHA-256 为
`e99f17658120d2b227b156bcbaa5c360a7554dfcee1b3e4447a53c5b4f4f87be`。

结果为 **180/200，未通过**。共有 183 个 canonical run：180 个完成、
2 个失败终态，以及第 181 轮未收尾的取消请求；没有拼接其他尝试。
两个失败请求的 HTTP 524／520、未知计量、原预留和重试身份均保留。
40／80／120／160 轮的实际刷新和第 100 轮的空闲 Go/Python 重启已各自核对原生前缀，
不能代替完整产品验收。

本次出现以下独立问题：

- 第 122 轮一个成功 Read 的原生结果存在，但后台仍缺对应 tool span；
  重复读取及全局查询仍未找到。160 轮 Trace join 退出 1，原错误保留。
- 第 181 轮实际 RecallMemory 耗时 547.447513 秒，返回 0 条。
  原始功能请求仍在 Ledger 中；未给产品补发目标或修复提示。
- 第 181 轮 8 次工具调用中两个 Read 缺原生结果，并保留 tool/unknown。
  已有 run_failed 观察记录，但终态写入未完成，API 仍报告 running，lease 已过期。
  本轮共 304 次工具调用、302 个原生结果、69 次压缩，未发生代码修改。

普通 task/finish 在停止本轮自建进程后仍返回 409。
Session `831ad3442d2570d9f2ab3242162bd405` 保留 canceling/unknown 和任务槽；
没有调用 abandoned 封存、改写原结果、手工修改 SQL 或释放未知预留。
模型活动调用数为 0；这个恢复缺口不作为普通源码修复的验收门禁。

## 基础修复

1. OTLP HTTP 在一次非取消的传输错误后，复用同一批 span 身份重试一次。
   沿用现有 SDK 的状态码处理；401、取消及超时不增加该重试。
   不新增依赖、队列或事实存储。原缺失 span 不会被补造。
2. 工具结果和修改收据在取得写锁后计算各自的审计时限，避免排队耗尽时限后
   把已返回结果记成 unknown。权限、lease、取消和持久化校验保持原边界。
   失败收尾在 commit 标记持续持久化时继续；连续无进展则有界停止，保留未确认 fence。
   不重放模型或工具，不把失败改为成功。
3. Memory 复用原有压缩来源覆盖算法。当前分支中被压缩覆盖的旧完成轨迹和反思经验
   仍可校验和检索，不仅查同 Session 的最后一份完成轨迹。
   旧 commit 字节与确定性投影 checksum 不改；owner、repository、rewind、删除和来源校验保留。
   仓库归属查询只在本次检索内复用已核对的同 Session 结果，不引入可写缓存。

ContextEnvelope 仍只承载元数据，没有把原始用户内容塞进该通道。
本轮没有修改前端、协议、依赖、凭据、预算规则或发布门槛。

## 验证范围

| 范围 | 当前结果 |
| --- | --- |
| 定向回归与 SQLite／HTTP 接缝 | 已通过；原红色复现和修复过程中的失败保留 |
| 新鲜完整 Go 测试与 vet | 均退出 0；1895 项通过、82 项跳过、0 项失败，计入子测试 |
| 真实产品 | FAIL，180/200；原始目标实现未完成，工具／取消及 Trace 缺口保留 |
| 完整发布 | 未运行；本次为普通源码修复 |

真实 NewTelemetry → HTTP receiver 先复现断线丢 span，再验证原身份交付；
另检查永久拒绝及导出 deadline。SessionRunner／EventLog 接缝分别复现七个 commit
跨多个终态上下文的收尾失败，以及结果等待写锁超过五秒后丢失。
修复后检查单一终态、完整原生结果、不重放及无进展有界。
Memory 检查单次／嵌套压缩、新完成任务遮挡旧目标、反思经验和回滚拒绝。

初次三个包检查包含 1 个失败：新测试把回滚后的拒绝方式额外限定为零条结果；
随后保留既有 ErrTrajectoryIntegrity 拒绝，仍禁止返回被回滚的数据。
该失败不从本轮日志中删除。

最终源码回归使用已缓存的 Go 1.26.0，按命令移除真实 provider 凭据和 live runtime
测试开关，设置 `GOTOOLCHAIN=local`、`GOPROXY=off`、`GOSUMDB=off`：

- `go test ./... -count=1 -json`：退出 0；包含子测试的 1895 项通过、82 项跳过，
  47 个包通过、13 个包无测试；没有失败项或非 JSON 输出。
  完整日志 `go-full.jsonl` 的 SHA-256 为
  `0f6b75963133062146049fd29dc74967cc72bea42a36cbcdde3916b4f3946ca0`；
  命令、分母和退出码见 `go-full-result.json`。
- `go vet ./...`：退出 0，见 `go-vet-result.json` 及 `go-vet.log`。
- 两路独立源码审查（Standards／Spec）均为 `AI_REVIEWED`，0 项发现；
  审查未替代真实产品或发布验证。

Python、前端及完整发布未在本次普通 Go 修复范围重跑。

## 保留产物与清理

实际运行目录：`deployments/docker/.local/receipts/product-long-dialogue-20261021/go/v6/`。
源码修复的命令、日志和退出码保留在 `.scratch/project-recovery-20261021/`。

- `aborted181-final-session.json`：5465 个原生事件，SHA-256
  `94315e16f3d6ae62114c36cb04210a4dfa3946a59b754cf254114d8bac1af070`。
- `aborted181-native-admission.json`：SHA-256
  `a6ad337399ea09c6d595185e7a35f9fdf287e7e07a41b37f7197dd5606b4b3aa`。
- `aborted181-unfinished-backend.json`：第 181 轮 21 个实际后台 span；
  它没有原生终态绑定，不能作为通过证明。
- `aborted180-readback-v2-backend-index.json`：161–180 轮 20 个已结束 run、241 个 span。
  首次采集器对至少一个 reflection span 的假设失败，原日志和部分采集保留；
  后续采集允许如实记录零个已捕获 reflection，不据此宣称完整 Trace。
- `driver-stop.json`：浏览器关闭、最后一个自建 job 停止，两个既有控制容器恢复 exited，
  cleanup_errors 为空；保留 63 个实际浏览器 TypeError，未将其升级为 UI 通过。

618 个源码、3225 个运行文件、14 个冻结过程文件及 AGENTS 指纹在失败尝试结束时仍一致。
333 个受保护原文件、Git 索引和 HEAD 不变，140 个旧 stream 锚点及 5133-event
原准入前缀保留。当前修复只在失败尝试及物理清理结束后进入源码。
Docker Desktop、volume、业务数据和旧证据均保留。
