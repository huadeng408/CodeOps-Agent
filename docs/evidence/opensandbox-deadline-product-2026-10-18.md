# OpenSandbox 长命令与显式结束实测

本记录验证 [b8be9e62 源码修复](opensandbox-execution-deadline-2026-10-18.md)
的两个范围：同账号旧任务的实际浏览器结束恢复，以及固定真实 OpenSandbox 上超过
120 秒的命令。两个范围 `VERIFIED`；原 Go 十二次尝试继续作为部分结果保留，
不据此关闭 #17、#19 或完整发布。

## 新运行包与控制策略

官方 `scripts/build-windows.py` 在提交
`b8be9e62fe04ee6d3f979703ab40aaaee96bb3e5` 后使用已核验缓存构建，退出 0；
构建期间 frontend build 退出 0。新便携包的 612 项源码和 3,219 项运行文件逐项核验。
原便携包全部 3,219 项文件及其 manifest 保持原摘要。

| 绑定 | SHA-256／身份 |
| --- | --- |
| 新 bundle manifest | `f6ad745c87570560b5f99e93580fea9698c4965eaf114023f5aa7e4c58292093` |
| 新 server 二进制 | `4b67bb5f16c3b43ecb5f1c15703780da9900321ecebfd79a16aebca7ffec859d` |
| Go 控制镜像 | `sha256:819b3fa7d7f06346a61f3d094c40c57151897bcc6d911471db28bd30d20b1a9b` |
| 新控制 policy | `41098fc9ee03ae07eb3b3d951ac424f5cdef62aa2f6e71de85ea37d22d13903b` |
| 新控制 manifest | `00b58fc907e4f235ebd7382de9dc4c75e57cd0572b28332647d8b18db3588329` |

控制镜像从原已验证副本复制，核对 139 项 server 文件和 51 项冻结依赖／许可证物料；
仅替换本次版本化配方和重新生成配置／策略，使用 `docker build --pull=false --network=none`，
退出 0。原控制服务、输入和 pin 保留。新控制容器在创建时归入 `localcode`／
`opensandbox-go`，有独立 owner、purpose 和 run／operation identity。

## 实际浏览器恢复

保留原浏览器、账号和 Session，以新便携包登录，继续使用原固定控制服务做只读核验。
实际点击“结束代码任务”返回 HTTP 200；先前同按钮的 HTTP 409 收据保留。
随后刷新能力，该任务的结束入口消失；无新模型调用。

Go Ledger 从 1,875 条变为 1,876 条：全部旧事件 checksum 相同，只追加一条
`tool/runtime-stopped`，引用原工具结果的 event ID／checksum／runtime ID。
原命令的 exit 126、截断输出、`outcome_unknown=true` 和 `stopped=false` 均保持。
新证明只核对物理停止，任务槽已空、ActiveCalls=0；原提案未生成或应用。

## 真实长命令

公开 Runner 在新固定控制服务中执行冻结的单测试 Go 夹具，正常命令为
`go test ./... -count=1`。夹具自身等待 125 秒并检查没有提前返回。
真实执行总耗时 139.499706 秒，测试输出 `ok deadlineprobe 125.058s`，退出 0，
输出未截断；冻结输入 SHA-256 为
`77d763fd94273bd37fc22c279ffcdf885fa7e91172d694ae7e7e4c69c7e8392a`。
原夹具文件未变，task／egress 均由原 Runner 精确回收；随后独立 `ConfirmStopped`
再次确认物理、逻辑、三类标签资源和 lifecycle API 均不存在。

完整准备／尝试分母保留：本机探针首次编译引用了 raw Result 不存在的字段，退出 1，
修正后编译退出 0。第一次执行因给封闭冻结命令多加 `-v`，在创建前被 Go 拒绝，
退出 1、无 runtime identity；改用原有固定命令后第二次执行通过。没有扩大冻结输入
允许的命令，也没有把该人工时长夹具算为原项目测试或一个模型完成轮次。

## 原任务、Trace 与保护

原任务十二次 canonical 运行仍为七完成、四取消、一失败。Memory 重新通过 Go 导出，
二十条实际经验与二十条投影逐项匹配，八次 trajectory commit、六次 reflection proposal、
两次 commit-blocked 全部保留。

实际后端读回十二棵树、454 span、25 ERROR，包含 129 个工具请求／118 个派发和
91 个已结算模型调用。原 Trace v1 收据和新的 v2 收据均保留；v2 仍
FAIL／partial／BLOCKED，CLI 退出 3。原因包含原未知工具结果及一条没有对应 Go
reservation 的 `provider.call` span；只读诊断已定位该 span，未删除或放宽分析器。
调用计量完整不等于执行与 Trace 完整。外层 PowerShell 未显式转发退出码而观察到 1，
与 subprocess 记录的原 CLI 退出 3 分别保存。

实际浏览器驱动最终退出 0、page error 为 0，所属新 launcher／job 子进程均停止。
原 Go 控制服务和 Phoenix 恢复原 exited，新 Go 控制服务也停止并保留 pin。
264 个原文件、四份 index、366 项既有封存产物及用户 AGENTS.md 均保持。
Docker 原 33 容器／96 镜像／332 volume 身份保留；仅增加一项有归属的控制容器和
一项控制镜像，无新增 volume，无遗留 task／egress，未删除业务资源。

修复与恢复未增加模型调用，批次已确认累计 32,048,471 tokens，历史 unknown 和原
预留保留，费用未知。[源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/39130511772)
四项全部成功。源码／单测／集成证据与本记录的两个真实运行范围分别验收；
Python 控制服务重建实测、完整 Go 成功任务和完整发布未在本记录通过。

本机不可变产物位于 `.scratch/opensandbox-execution-deadline-20261018/`、
`deployments/docker/.local/receipts/execution-deadline-20261018/` 及
`deployments/docker/.local/receipts/go-original-20261018/go/v1/`，含所有失败、
原运行包／新恢复运行包独立绑定、完整原生导出、后端读回和精确停止收据。
`runtime-validation-summary.json` SHA-256 为
`df10cf47facf6fed21175286309731a02b6d6745b0f6b99fbc1cb78222ea17c2`。
