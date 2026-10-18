# 待审批取消修复的真实产品验证

源码修复 [`d34f6b4d813333dcc091b40f5a4455a614d9c5cc`](https://github.com/huadeng408/CodeOps-Agent/commit/d34f6b4d813333dcc091b40f5a4455a614d9c5cc) 的独立浏览器专项 `VERIFIED`。本次只验证批准写入、取消待审批写入及其原生结果／真实 Trace；不计入 #17 原 Go 任务的十轮验收，不替代 Memory、故障矩阵或发布。

## 固定运行与完整分母

新包绑定上述提交，逐项核对 609 个源码指纹与全部 3,219 个运行文件。bundle manifest SHA-256：`441d82c013ed821effa62c19f67a9424317f63fea31c23bdfe91dd613477af2e`；实际 server 二进制：`0393183d83d9737ef8ecfbe8a67c31fa6cddefd727b532c82de43649eb2c490b`。模型仍是配置别名 `grok-4.6`，不声称已验证不可变 provider revision；Go 任务镜像和两个固定 Compose `localcode` 控制服务沿用已批准 pin。

| 实际产品步骤 | 结果 |
| --- | --- |
| 新鲜身份与入口 | 实际 UI 注册 HTTP 200 并自动登录；准备原始 Go 工作副本 HTTP 200，包含既有 dirty/new 文件，未注入旧候选或修复答案 |
| 产品基线 | 实际右侧卡片运行 `go test ./... -count=1`，HTTP 200／exit 0／passed，输出未截断、隔离任务停止已确认 |
| 批准路径 | 第一轮实际模型提出 `Write cancel_probe.md`；UI 批准 HTTP 200。Go 实际派发，文件落盘 SHA 与 native `code/modified.after_sha256` 相同，run 完成 |
| 取消路径 | 第二轮实际模型提出 `Write cancel_probe_not_approved.md`；审批前 UI 取消 HTTP 202。原生顺序为 pending → denial → 已知最终工具结果 → canceled/stopped；同一身份只有一份 denial/result，无 dispatch、commit_pending 或 unknown，该文件不存在 |
| 明确结束 | 实际 UI 结束任务 HTTP 200，原生 ActiveCalls=0、任务槽为空 |

完整 Session 共 53 个原生事件、两个 canonical run／各一次实际尝试：一轮 completed、一轮 canceled。批准文件 SHA-256：`6b287de63a5df367499975d529bc4d409f070f068f0f0468032457a7885459af`。取消不计作完成轮次，本专项的两次运行都不计入原编码任务的十轮。

## 真实后端读回

保持已批准 `product-integrity-v2` 和默认 v1 分析器原样，CLI 从实际 Phoenix API 读回全树：15 span、两棵 Go 实际执行根、两个工具请求／一次 dispatch、四次模型调用。已派发写入有唯一执行 span；未派发的取消写入没有执行 span。原生工具状态分别为 succeeded 与 denied，取消状态 stopped。

v2 结果 PASS／complete／VERIFIED，退出 0，missing=[]、problems=[]、unsettled_call_ids=[]；同一全量数据的 legacy_report 仍 FAIL，原样保留。四次调用累计已确认 42,899 tokens；共享批次累计 27,106,732，历史 unknown 和原预留不变，费用金额 unknown。

正式收据 `probe-trace-v2-iso/receipt.json` 的 SHA-256：`e3e43cf421c2ac4048f60c3a183efa54917d2aed8d31c61c482f3bd1883a3b25`。读回绑定原构建、冻结输入、完整原生 Session／准入前后导出及实际审计二进制，不用 mock 或合成 span。

## 未删除的失败与操作纠正

- 第一份构建使用的测试虚拟环境没有 pip，退出 1，半成品保留；改用已有构建 Python 和已校验本地缓存，在新目录成功构建，未安装新依赖。
- 首次取消观察器使用按钮文字而非其实际 accessible name，超时；没有发出取消请求，原生记录仍只有 heartbeat。修正定位后才发生上述实际取消；失败观察和中途导出均保留。
- 首份正式 Trace 读回为 unknown／FAIL／退出 3，共六次完整读回尝试，每次两个 run 均查询失败（12 次按 run 查询）。PowerShell `ConvertFrom-Json` 将 ISO 时间转换为 DateTime，传给 CLI 的实际参数成为 `10/18/2026 17:41:12`；相同 reader 的对照证明该参数 HTTP 422，而原 ISO 字符串可读到第一棵实际树。显式传入原 UTC RFC3339 字符串后，同一 Session／原生导出／二进制完整读回通过，没有重跑模型或改写首份收据。Phoenix SDK 不参与这个 stdlib reader，无需为此安装依赖。

## 保护、清理与交付边界

四份原工作副本的 264 个文件与四个 index、用户已有 AGENTS.md、上一批 161 项封存产物均保持原 checksum。Docker 原 33 个容器、96 个镜像和 332 个 volume 的身份集合完全相同；本次浏览器与准确拥有的 launcher/job 子进程均停止，两个控制服务恢复原 exited，page errors=0。未删除业务数据或其他 Docker 资源。

源码提交的[远端 CI 四项全部通过](https://github.com/huadeng408/CodeOps-Agent/actions/runs/39116001818)：Go、Python、Frontend、Windows startup contracts。本次后续只新增证据与时间参数说明，不重跑无关本地源码全量；完整发布未运行。#17 原任务、同任务 Memory／Trace 和 #19 仍独立待验收。

本机完整产物在 `deployments/docker/.local/receipts/pending-cancel-20261018/go/v1/`；构建和诊断日志在同一 Docker 管理目录的 `.local/logs/pending-cancel-20261018/`。所有失败、旧收据和运行身份均保留，运行数据不进入提交。
