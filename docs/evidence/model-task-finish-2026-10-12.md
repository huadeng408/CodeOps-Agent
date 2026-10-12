# 代码任务显式结束 — 2026-10-12

关联 [#3](https://github.com/huadeng408/CodeOps-Agent/issues/3)，基点
`92a173b5f78edec5769bbbf3977e48a731d30d6f`。工程状态 `IMPLEMENTED`。
本记录只验收共享 Go 结束边界、鉴权 HTTP 接线和已选右侧卡片中的结束按钮。
单测、集成和外部 fixture 浏览器验证按下列分母记录；完整模型产品与发布
未运行，#3 保持开放。

## 行为与边界

`POST /api/v1/sessions/:id/task/finish` 从已认证身份获取 owner 和 actor，
先核对 Session 归属，再调用既有 Gateway／Budget。其他用户或已删除的
Session 不可访问；子 Session 不能结束根任务。结束无需有效 provider 凭据。
local-core 与 full-stack 共用该路由，CLI `/clear` 沿用同一 Go 结束边界。

活动普通 run、未结束子任务、缺工具结果和未确认停止均拒绝结束。旧 CLI
`session/state` 中的 Invocation 状态及 pre/tool/post 阶段 runtime 收据也
必须确认。已确认停止但结果 unknown 的任务可以显式结束，unknown 原事实
仍保留，不升级为执行成功。

模型 scope 的绑定和释放在同一 admission Ledger 中记录；capability 明文
仍瞬态，落盘的 scope ID 是其摘要。绑定事实不创建 token 批次或预留。
结束通过同一序列 CAS 检查活动 scopes，因此另一个 Go 进程仍持有绑定时
HTTP 返回 409。正常 Gateway 关闭先等待 handlers 停止，再释放其 scopes。
进程崩溃或释放写入失败会保留占用；此占用的显式恢复仍待 #3 对账实现。

重复结束与 HTTP 重启后的重复结束不重置额度，也不释放后续新任务。
显式结束未使用的 root 会留下零 token 的完成事实；已结束 root 不能重新
绑定。全部结算后，即使额度耗尽也能结束，但不能因此获得新额度。

右侧入口只显示当前用户拥有且选中的任务。未结算、活动 run 和未知预算
禁用结束；服务拒绝会保留任务并显示恢复说明。切换会话后不显示旧任务的
结束错误。成功提示明确历史和累计额度保留，新任务仍按剩余额度准入。

## 新鲜验证

| 范围／命令 | 完整分母／退出码 |
| --- | --- |
| `go test ./... -count=1 -json` | 1,454 pass、65 skip、0 fail；45 测试包通过、13 无测试包；0 |
| `go vet ./...` | 0 |
| `python -m pytest -q` | 串行复跑 2,473 pass、17 skip、31 warnings、0 fail；0 |
| `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 go test ./tests/e2e -run '^TestProductionLocalCore' -count=1 -json -timeout 300s` | 11 pass、2 需批准现场环境的 opt-in skip、0 fail；0 |
| `frontend/` 中 `npm test`、`npm run build` | 12 pass、0 skip、0 fail；两项退出 0 |
| `node tests/taskFinish.browser.mjs http://127.0.0.1:3001/` | 实际浏览器对最终生产构建运行，7/7 checks；0 |

Go 数量包含父测试／子测试 actions，不是独立产品场景数。HTTP 使用真实
生产进程、鉴权和 SQLite；计量部分通过独立 Go Gateway 的外部模型 HTTP
fixture 注入。它覆盖其他用户的 Session 返回 404、无认证 401、跨进程活动 binding 409、
完成／重复／重启、新 root 累计额度和未知 usage 拒绝；外部 fixture 总量
保留为 synthetic 数据，不算真实 API 计量。浏览器 HTTP／WebSocket 是
外部 fixture；没有替换前端模块。

原始 1 亿 token 批次通过 Go 只读接口读回：身份仍为
`02cfc8d9c93975b3ea63f27cc0a3331d`，上限 100,000,000，已用和预留均 0，
任务槽空、usage 已知、费用 unknown。本轮真实模型调用 0，未读取 API
密钥文件，未创建该批次的替代身份，也未重置它。

## 审查、失败与收据

两轴 `AI_REVIEWED` 均 0 项未解决发现。原 legacy 未确认执行、跨进程
活动 binding 和 unused-root noop 均由公开 Manager／Ledger／Gateway／HTTP
RED 复现后修复。没有人工逐条复核声明。

原失败全部保留：缺 HTTP 路由、缺 owned task ID、旧 root 再绑定、缺工具
结果／未确认停止、unused root、legacy 五个拒绝场景、跨进程活动 binding。
初始 legacy 测试使用不存在的字段产生编译诊断，修正后保存有效 RED；
初始浏览器 fixture 缺必需 run 字段，补齐后旧构建有效复现入口缺失。
另保留 unknown-usage 错误码预期诊断、scope 回归中“完全没有事件流”的
旧断言及前端空值类型诊断；最终检查确认无批次／无资金预留，允许已核对
的 scope 生命周期事实。所有失败未被成功子集覆盖。

Python 首轮完整分母为 1 fail、2,472 pass、17 skip、31 warnings，退出 1。
旧 non-cooperative-adapter 期限断言实测 0.254 秒超过 0.25 秒；该文件和
实现均未修改，独立单文件重跑 27 pass、退出 0，随后串行重跑完整分母。
串行完整复跑耗时 522.05 秒，通过数量、跳过和 warning 分母见表；首轮
602.93 秒与单文件日志均保留。

不可变收据：`.scratch/model-task-finish/receipt.json`；完整分母、源码／
运行输入摘要、命令、退出码、原日志和截图在该文件中绑定，共 961 个源码／
依赖摘要。SHA-256：
`9f684c777fa8556c27285a49ce8c1c658418f3d32fd23539c430cc218ce3359e`。
19 个精确 staged paths 的秘密／机器路径扫描和 `git diff --cached --check`
均退出 0；验证源码指纹一致。提交后的精确 staged／源码 CI 绑定另存补充。原先工作区
接线收据不改写，用户 AGENTS 私有 footer 保持未暂存。

仍需 scope 故障占用恢复、未知 usage 对账、可计量的有界重试，以及真实
Python／模型、反思／child／重启、浏览器完整代码任务和后端 Trace 证据。
工程结束入口不能代替完整产品或发布验收，不据此关闭 #3。
