# #10 应用中断核验与并发保护：2026-10-15

工程状态 IMPLEMENTED；VERIFIED 限于下列单测、集成及产品范围。范围是 Windows 文件应用的并发拒绝、
逐项中断后的只读核验，以及新基线与再次批准。Memory、真实模型策略、Trace、
十轮对话和完整发布分别验收，不作为本票完成条件。

## 行为与原要求

| 原要求 | 实现与验证接缝 |
| --- | --- |
| 原内容、存在和相关仓库状态变化时保护新改动 | 复用 Go ApplyFiles 全量预检；真实 HTTP 分别检查内容、存在和 index 冲突；浏览器保留提案及 409 提示 |
| durable intent、实际状态及 outcome 对账 | 同一 Ledger 新增 application-reconciled；绑定 owner、原 application intent、当前 source checksum 和原基线；不改写旧事实 |
| 已确定项不重放，未知项不猜成功 | 只读检查内容、mode、存在、HEAD/index 和未涉及文件；逐项分母保留；原请求重放不执行文件操作 |
| 保留冲突、partial 和隔离成果 | 沿用右侧卡片显示父状态及全部逐项状态；核验入口只接受 ID 和 CAS 游标，不接受文件内容或根路径 |
| 新基线与新的批准 | 已核验且无未知项的 partial 可结束旧任务；旧模型工作区仍拒绝续写；同仓库新会话准备当前基线、真实测试、生成余下三项提案并再次批准 |
| 共用校验与历史保留 | HTTP 使用同一 Go 模块；现有 CLI 生命周期撤销仍 fail-closed；未新增 CLI 提案应用入口，不宣称 CLI 应用产品证据 |

父应用 schema 3 复用既有 Go 进程身份检查，记录主机、PID 和启动身份。
原始 outcome 缺失且仍有未确定项时，必须确认生产者已停止；无法确认时拒绝核验。
旧 schema 1/2 继续读取，不补造历史进程身份。进程实现移动到 safety，
admission 的旧入口单向调用它，历史 JSON 不变。

最终父 outcome 与只读核验发生竞争时，生产者读取已核验事实后退出，不追加相冲突结果。
逐项是否曾派发由原始 intent 派生，重复核验不能把未派发项误认成已应用。
文件清单不包含空目录，因此删除及 rename 两端使用 rooted Lstat 确认真正不存在；
同名目录、非普通文件和不安全路径不被当作已删除。

后台 Memory 可以在核验前追加 Ledger 事实。新请求游标过期时返回序列冲突，
界面要求重新检查；测试保留 409，并刷新后沿用同请求 ID。
产品没有自动重试文件写入，也没有关闭 Memory 来换取基础票通过。

## 产品证据的边界

实际生产 Go HTTP、鉴权、Ledger、已批准分组 OpenSandbox 和 Chromium 先生成四项提案。
原始 Go 基线真实失败，隔离修改及冻结提案的真实测试通过。
测试专属的公开 Ledger 接缝在原 native 应用器各边界暂停；杀死该 Go 进程后，
启动未修改的生产二进制，通过真实 HTTP 与浏览器核验、重放并检查原文件及 index。
这是原应用器故障注入和生产恢复证据；不声称 16 次 HTTP 服务进程都在批准请求中被杀。
外部模型 HTTP／编排 gRPC 是 fixture，付费模型调用为 0。

冻结的分母：父 intent 前／后 2 项、四个文件各自 intent 后／outcome 前／后 12 项、
父 outcome 前／后 2 项，共 16 个中断边界；另有内容／存在／index 并发冲突 3 项，
以及 partial 后新基线、真实测试和再次批准 1 项。
浏览器计划 18 个场景：16 个恢复视图、1 个并发拒绝、1 个新批准。

原始输入、事实、浏览器收据和失败日志保存在
`deployments/docker/.local/receipts/application-recovery-20261015/`；
定向及完整回归日志保存在 `.scratch/application-recovery-20261015/`。
运行目录不覆盖；脱敏收据只保留范围、结果、分母和指纹。

## 本轮验证与失败保留

定向回归覆盖原始 intent provenance、目录替换、最终 outcome 竞争、任务结束与新基线，
以及并发事实产生的游标冲突，均先保留红阶段再验证绿阶段。
原始 16 项 Go 子进程矩阵通过，属于模块集成证据。

第一轮产品矩阵因测试提前结束旧任务而中止；此轮保留已完成项和全部计划分母。
第二、三轮保留 Ledger 游标被后台 memory/commit-blocked 前移后的拒绝，
第三轮另保存核验前的逐项原始事实。未到达的新基线场景不计为通过。
修正后的测试保持实际 CAS 拒绝，并模拟刷新和沿用原请求，不忽略失败分母。

完整 Go 首轮有一个沙箱 fixture readiness 失败（父、子两条 fail events）；
该组定向重跑及第二次完整 Go 均通过。完整 Python 通过。
UI fixture 首次命令把 flag 当成 URL，未到达检查；使用正确 URL 后通过。

| 最终验证 | 结果 |
| --- | --- |
| `go test ./... -count=1 -json` | 1,649 pass／78 skip／0 fail test events；45 包 pass／13 无测试；退出 0 |
| 最后游标分类与相关公开回归 | 4 pass／0 fail；退出 0；完整 Go 后的最终调整由这些定向回归及最终产品运行覆盖 |
| `go vet ./...` | 退出 0 |
| `.venv/Scripts/python.exe -m pytest -q` | 2,507 pass／17 skip／0 fail；3 warnings；退出 0 |
| `npm test`、`npm run build`（frontend） | 12 pass／0 fail；类型检查及构建退出 0 |
| UI fixture 核验 | 7／7 检查；退出 0；不作为真实产品证据 |
| 最终 `matrix-v4` | 16／16 中断、3／3 并发冲突、新基线／再次批准 1／1；退出 0 |
| 最终生产浏览器 | 18／18 场景、123／123 检查；退出 0 |

四个独立产品运行均保留原始日志：首轮中止；第二、三轮失败；第四轮通过。
浏览器核验／批准数组及 Go 核验 CAS-refusal 产物保留实际尝试；不宣称记录了
fresh-proposal 分支的全部 HTTP 重试次数。最终 526 项源码／依赖／指令及浏览器构建
输入逐项与当前文件核对相同，原始 source SHA 为 `85f305abf893736423ca02d480015e803147e74b`
加本轮实现；纯证据和 Goal 文档更新未参与上述运行。

完整结果、源码绑定、命令退出码、各轮分母和产物指纹见
[脱敏收据](application-recovery-2026-10-15.receipt.json)。
Standards／Spec 为 AI_REVIEWED，各 0 项未解决发现。
本票声明范围无未解决 blocker；新付费模型调用、新 CLI 应用入口及完整发布未验证。
