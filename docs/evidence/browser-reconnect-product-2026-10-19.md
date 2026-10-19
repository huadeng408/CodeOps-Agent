# #25 真实产品的双标签页重连与空闲重启

对应 [#25](https://github.com/huadeng408/CodeOps-Agent/issues/25)，接续
[#24 的源码／fixture 修复](browser-reconnect-2026-10-19.md)。本轮六项计划场景全部通过，
范围为实际便携产品、Go／Python／真实 provider 的只读对话、页面断线重放及空闲重启。
没有修改产品运行源码；原 #1/#19 首批结论、失败与后续长期验收范围保持。

## 输入与产物

- 运行源码：`99b4e3c9aacfe87149aff9ac8cc4b623dbee7eaa`，现有工作指令改动保留。
- 使用官方 `scripts/build-windows.py` 和既有固定缓存，`PIP_NO_INDEX=1`、`GOPROXY=off`；
  两份组件 archive、53 个 wheel、六份补充许可材料不引入新版本或在线下载。
- 新包：`deployments/windows/.local/Portable Git 20261019 reconnect v7/`，
  manifest SHA-256 `4108e0f58e8768bfcb192479410774fc97837f819fc64d786f878183f87512dd`；
  612 个源码输入、3,225 个运行文件，包含 #24 的当前页游标修复。
- 六项场景、两个 prompt、批次、原文件／index、image 与控制服务在调用前冻结。
  `deployments/docker/.local/profiles/browser-reconnect-product-20261019/inputs.json`
  SHA-256 `024ed6f1619e9a2ea06431b3e90ff2963b742c51924171355202c486c8d7d962`。
- 不可变验证入口：`.scratch/browser-reconnect-product-20261019/artifact-manifest.json`，
  66 个物料，SHA-256 `ea32a97be4ed24910d242af29149e625ded78f97127c51482dd66ef5f5b30118`。
  不包含数据库、cookie 或 provider 配置快照；固定依赖由包 manifest 逐文件引用。

## 六项完整分母

| 计划场景 | 实际结果 |
| --- | --- |
| 1. 冻结与启动准备 | 原批次无活跃调用、任务槽空；新包、234 个原文件、三个 index 和两个现存控制服务身份核对通过；旧两包保持 |
| 2. 首条真实请求 | 实际注册／登录，创建并准备同一 Go Session；模型经 Go `Read` 读取 `go.mod`，工具退出 0，答复与 run 均 completed |
| 3. 单页断网期间另一页执行 | 同一浏览器 context 的页面 A 阻断自己的 HTTP／WebSocket；页面 B 经正常 UI 完成第二次模型读取 `README.md`。A 仍只有序号 3、16 两条消息，B 已有 3、16、22、35 四条 |
| 4. 实际重连 | B 留下真实 head=39 的旧游标提示；A 从自己已接收的 21 重连。四条消息按序出现且唯一，两页全部筛选的 30 个可见记录一致；六条工具记录和两项原始输出与 Go API 一致。A 草稿保留，没有额外发送 |
| 5. 刷新与空闲重启 | 刷新后四条消息恢复；ActiveCalls=0 后精确停止所属 launcher Job，重新启动实际 Go／Python、登录并重开。重启前后原生 41 条事件逐条相同，仍只有两次派发 |
| 6. 结束、原生审计与回收 | 实际 UI 结束返回 200；任务槽空、ActiveCalls=0。原生流、计量、Trace、原文件和运行物料核对通过；自动化 Chromium、两轮所属 Job／子进程均停止，两个控制服务恢复原 exited 状态 |

两个 prompt 是正常的只读仓库问题：读取 `go.mod` 概括模块／Go 版本，以及读取 README
概括路由用法；没有参考答案、修复提示、host Task Workspace 编辑或虚构响应。
页面 A 的故障只作用于自己的传输，WebSocket 使用实际 Go server 转发；页面 B 直接连接 Go。
记录了断网期被阻断的实际请求，恢复使用真实一次性 ticket 与已有重放接口。

刷新和重启不发送新消息，不要求跨标签页的草稿持久化隔离。验证的是断线期间的内存草稿；
此范围在调用前已声明，没有因观察到的结果改写旧草稿或增加未声明能力。

## Ledger、计量与后端读回

同一新 Session 保留 41 条 canonical events，两个 completed run／两次 lease attempt，
均为 attempt 1；两项 `Read` 工具结果退出 0，`code/modified` 为 0。
用户提交只有 A、B 各一次；原 2,891 条 admission 前缀不变，最终 2,912 条。

两次用户请求实际产生六个唯一模型调用：四个 foreground、两个后台反思。
六项均已结算，无新增 unknown；确认增量 67,690 tokens，累计 51,331,170。
原 unknown、5,306,024,576 原预留及费用 unknown 均保留，批次未重置。
结束及进程停止后的批次投影一致；继续使用服务商的一亿 token 硬上限，没有逐笔批准。

实际 Phoenix 后端读回两棵树、23 个已结束 span，0 ERROR、0 缺父链；
六个原生调用 ID 都在读回中，两项根身份与 canonical run-completed 的 W3C 身份一致。
使用未修改的 `eval.harness.phoenix.read_product_spans`，读回器 Git 内容绑定于上述运行源码；
额外绑定与凭据检查见 `.scratch/browser-reconnect-product-20261019-review/post-run-binding.json`。
这是本轮的真实采集核对，没有生成完整代码任务／发布的正式 integrity receipt，
也没有重写旧 v1 FAIL、v2 结果或历史故障树。

三份 index、234 个原文件、新包 3,225 文件／612 源码输入及旧两包的 6,444 文件均再次匹配。
控制服务按原 ID／image／状态恢复；最终观察到 43 个容器，没有删除业务 volume、数据或索引。
默认浏览器属于操作员进程；本轮回收范围是自动化 Chromium 与 launcher 所属 Go／Python Job，
不把用户浏览器进程算作受管子进程。66 个物料检查未发现已知 provider／沙箱凭据字面值。

## 失败与观察器边界

首次驱动退出 1：只读历史计量导出为 1,166,130 字节，超过旧 1 MB stdout 缓冲。
发生在控制服务／产品／模型启动之前；原目录与停止记录保留。唯一一次启动重试使用
有界 32 MB 导出缓冲和独立 v2 数据／输出目录，原预算及输入没有改变。

v2 的 19 项观察器命令中保留三个失败：新建按钮带可见 `＋` 前缀、创建接口实际返回 200
而非观察器预期的 201、以及现有页面尚未启用新注册的 WebSocket route。
按实际界面／源码修正定位与状态判断，并在故障前只刷新 A 来启用 route。
没有重复创建 Session、准备工作区或重发用户消息；三项不是产品成功，也不是新增模型尝试。
补充根身份核对首次误读 lease payload 的字段，改用实际终态携带的 trace_context，原导出不改。

## 命令与验证状态

命令在仓库根运行；读回脚本的进程设置 `PYTHONPATH=<仓库根>`、`PYTHONIOENCODING=utf-8`。
Windows Docker 先调用公共 `Wait-DockerDaemonReady` 入口，未另写探针或修改系统代理。

- `python scripts/build-windows.py --output <新包目录>`：0；构建器内部生产前端类型检查／构建 0。
- `node --check <driver-v2.mjs>` 与 transport helper：0。
- `python <profile>/freeze.py`：0；首次错误的预算摘要字段观察另行保留，无副作用。
- `node <profile>/driver.mjs go <freeze-sha>`：1；未启动产品或模型。
- `node <profile>/driver-v2.mjs go <freeze-sha>`：0，六项产品场景 6/6；三项观察器失败仍保留。
- `python .scratch/browser-reconnect-product-20261019/read_traces.py`：0。
- `python .scratch/browser-reconnect-product-20261019/audit.py`：0；源码、计量与原数据核对如上。
- 变更仅为本轮证据与 Goal 入口，未在本地重复无关 Go／Python 全量；交付源码 CI 单独记录。

本轮 `VERIFIED` 限于上述真实重连／刷新／空闲重启产品范围；不升级为完整恢复矩阵、
200 assistant-turn／compaction、模型质量对照、官方评分或完整发布验收。
完整发布状态不变，#20 继续暂缓，父规格 #1 保持原文和 OPEN 状态。
