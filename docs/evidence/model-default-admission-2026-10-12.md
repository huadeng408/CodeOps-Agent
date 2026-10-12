# 默认模型计量边界 — 2026-10-12

关联 [#3](https://github.com/huadeng408/CodeOps-Agent/issues/3)。工程状态
`IMPLEMENTED`；本记录只验 CLI／进程／协议和本机 HTTP 的默认边界。
#3 的完整产品验收仍未完成，不能据本切片关闭票或通过发布门禁。

基点 `fb08e8e5a0dd16c9bb290a4e5edfb56873d2fd3f`；最终 WIP 的 855 个
Go／Python／proto 和依赖输入摘要绑定到下述收据，提交绑定另存补充，原收据不改写。
用户私有 `AGENTS.md` footer 保持未暂存。

## 行为与接缝

Go CLI 和 full-stack continuation 默认使用既有 Gateway／Budget／Session
Ledger，取消旧 `required` 开关的绕过路径。CLI 在初始 Session 和 actor 持久绑定后、
命令循环前触发现有懒加载 Ledger 和网关初始化，NewApp 本身仍保持懒创建；
退出先停止 Agent 和网关，再关闭 Ledger。Go 启动的 Python 强制 required 模式，
剥离 provider 密钥、token 和 provider 配置文件入口；不会向 Python 传递付费凭据。

外部 Python 的 Harness-managed Converse 请求也必须携带 Go 网关绑定，
不受外部服务器旧开关影响。独立 Python 的旧入口仍保留兼容性，不计入本批次。
本机能力卡直接报告缺少凭据、模型／地址、上界或无效配置，读取不创建批次。

测试使用公开 CLI.Run、独立 OS 子进程、gRPC／HTTP 和实际 SQLite Ledger。
只有外部 transport 是 fixture；不把该结果描述为真实模型用量或代码任务。
旧 Context Envelope 测试迁移到外部 Gateway fixture；旧 `/clear` 测试改用
真实 SQLite INSERT 故障，验证失败后历史保留。

## 红绿与失败保留

- CLI 无旧开关：红测为 `binding_missing`；修复后缺密钥返回
  `model_prerequisites_missing`，用户输入保留，批次／已用／预留均未创建。
- Go 子进程：红测为 `credential_exposed`；修复后即使父进程声明 disabled，
  子进程也只报告 admitted，并确认没有 provider 凭据和配置文件入口。
- 外部 Python：红测在缺 binding 时仍返回 success；修复后显式拒绝，
  `tests/test_model_gateway_client.py` 的九项检查全部通过，退出码 0。
- 本机 HTTP：红测仍报告 `model admission is not enabled`；修复后指明
  `model credentials are missing`，额度读取和重启保留检查通过，退出码 0。
- 首次相关 Go 回归发现两项测试失败：NewApp 提前打开 SQLite 的清理问题，
  及旧内存 Session fixture 与统一 Ledger 不一致。改为懒加载网关和真实数据库
  故障后，三项受影响定向测试通过，退出码 0。该失败不由后续通过抹除。
- 第一轮完整回归及本机 HTTP 批次被中断：日志没有结束记录或退出码，不能计为
  通过。原产物保存在 `.scratch/model-product-admission/default-admission-checks/`；
  后续检查使用新的 `default-admission-checks-v2/`，原文件不覆盖。
- 两轴静态审查发现同一初始化顺序导致首次 `/compact` 缺 binding、`/budget` 和
  `/config` 显示旧限额、`/clear` 跳过任务完成边界。公开 CLI.Run 红测复现前两项；
  将初始化移至 Session／actor 绑定后，新增恢复后压缩、启动额度、unknown 下拒绝
  清空的公开检查。六项定向检查通过，退出码 0。原两轴发现及失败日志保留，
  复核为 Standards 0／Spec 0 未解决发现，均为 `AI_REVIEWED`。
- 额度检查还复现长 batch ID 将 `100000000` 从数字中间换行；现在 batch 与额度
  分行显示，公开输出检查覆盖。初次测试编译时使用了错误 protobuf 类型名，
  修正为既有 CompactionUpdate 后才计为行为红测；编译失败同样保留。

## 最终验证与收据

| 声明范围／命令 | 完整分母与退出码 |
| --- | --- |
| 单测／相关集成：`go test ./... -json -count=1` | 1,438 pass、61 skip、0 fail；45 测试包通过、13 无测试包；0 |
| `go vet ./...` | 0 |
| `python -m pytest -q` | 2,470 pass、18 skip、31 warnings；0 |
| 本机公开 HTTP：`CODE_AGENT_RUN_LOCAL_CORE_E2E=1 go test ./tests/e2e -json -run '^TestProductionLocalCore' -count=1` | 九个实际进程／HTTP 入口全部通过，0 skip、0 fail；0 |
| 产品模型／浏览器闭环、发布 | 本切片未运行，不能继承为通过 |

Go 计数包含父测试／子测试 actions，不能写成 1,438 个独立产品场景。
Python 完整运行之后没有修改 Python 源码；后续 Go 启动修复后重跑完整 Go 和 vet。
本机 HTTP 重新输出 JSON，以保留精确入口分母；相关源码未发生后续变更。

收据：`.scratch/model-product-admission/default-admission-checks-v3/receipt.json`。
SHA-256：`fc67fceb452ffd065e48df5362e6ebaf9eca4282a831064c31d711f2425d1b3f`。
原中断、编译、行为、清理和数字换行失败均保留；两轴复核只表示 `AI_REVIEWED`。
原 #23 收据摘要 `a054…dba38` 再次核对不变，后续源码变化不改写其原运行结果。

Go 的公开 Budget.Inspect 新鲜读回原批次 `02cfc8d9c93975b3ea63f27cc0a3331d`：
100,000,000 上限、已用／预留 0、unknown false、任务槽空、费用 unknown。
该核对未调用 Open／Reserve／reset，也没有调用模型。

## 当前界限

该切片没有发起付费调用，也没有改写既有批次或任何已封存 OpenSandbox 收据。
真实模型、Trace 后端读回、批准沙箱与 Task Workspace 的模型准入、HTTP 任务完成、
未知用量对账和可结算重试仍按 #3 验收；源码回归不代替这些证据。
前端源码及布局未改，本切片不重做布局选择或前端构建。
