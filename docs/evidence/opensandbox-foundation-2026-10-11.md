# OpenSandbox foundation evidence — 2026-10-11

工程范围：`IMPLEMENTED`；Go 单测／外部接口 fixture 集成：`VERIFIED`。
完整 [#21](https://github.com/huadeng408/CodeOps-Agent/issues/21) 与
[#3](https://github.com/huadeng408/CodeOps-Agent/issues/3) 继续开放。
本项没有运行真实 OpenSandbox、公开 chi 基线、产品浏览器或发布门禁。

## Delivered scope

显式 OpenSandbox 选择复用现有 Runner 和配置。Go 核对批准 manifest 的
checksum、固定官方源码、私有本机 endpoint、镜像 pin 和容器隔离后才派发。
采用固定 JSON／事件流契约和有界解码，不增加 SDK。创建请求使用调用方事先
持久化的 operation ID；缺 ID 的旧调用明确拒绝。主 CLI 与独立 Agent 在
`enabled=false`、大小写或空白变体下仍进入所选 Runner，不落到 host shell。

执行结果需要明确的命令状态与退出码；不完整状态、超长事件和无法确认的
终止保留 unknown。输出保留截断标记。清理前核对官方归属标签、完整 CID、
逻辑名称绑定及 operation；替换容器不删除。该范围仅支持已核对的容器资源。
上游 managed runtime volume 明确不支持，阻止命令派发并保留 unknown，
不能把仅容器的缺失读回当作全部资源已清理。

非模型任务复用现有 admission Ledger/CAS 的持久任务槽，重开保留消费量和
占用；重获槽位不授权重放副作用。产品 Task Workspace 尚未调用该执行入口。
本轮模型调用 **0**，没有读取模型密钥、下载 pytz 或重置 token 批次。

## Verification and review

| 范围／命令 | 结果 |
| --- | --- |
| 公开 Runner、配置、主 CLI／独立 Agent 与持久任务槽定向回归 | 退出 0；外部 HTTP／Docker executable fixture，非实际部署 |
| `go test ./... -count=1 -json` | 退出 0；1,390 pass、60 skip、0 fail test actions；43 passing packages，14 no-test packages |
| `go vet ./...` | 退出 0 |
| 实际 OpenSandbox、A 卡片／完整 chi 基线、真实 Trace | 未运行；不继承为产品验收 |
| Python／frontend 完整回归、发布 gate | 未运行；对应源码未改，本项不声明这些范围通过 |

Test actions 含子测试与 parent，不是 1,390 个独立场景。完整分母、跳过、
源码 hash、日志 checksum 和全部 RED／中间失败都在不可变 receipt 中保留。
CLI 规范化测试曾暴露已有 readiness 等待不一致；共享探针已与选择器统一规范化。
Standards／Spec 双轴审阅剩余均 **0**，分类为 `AI_REVIEWED`。

基础比较 SHA：`eeb536b1b502ce1f9db167f6b2185a12043fffde`；测试的是本轮
修改后的工作树。receipt 记录 **441** 个 Go 源码／模块文件的原始与 Git
规范化 hash；提交后另写绑定核验，不改写原结果或原 SHA。

Receipt：`.scratch/opensandbox-integration/foundation-3d48aa0c-609c-4d9d-b559-77da7c3641b6/receipt.json`。
SHA-256：`c1f5c99311d9f73959a6c20256d4a039db2baca9d651daccb8836f51ebbb0303`。
审阅报告：`.scratch/opensandbox-integration/foundation-review.md`。
既有私有 `AGENTS.md` footer 保留并排除本轮暂存。

## Remaining vertical acceptance

固定实际部署与 artifact／许可证／凭据、禁止隐式 pull、保护 runtime 文件、
所有新资源的归属与有限存储、原子条件删除、实际 packet／文件系统／用户隔离
仍需验证。请求中的 `dns+nft` 和双栈 deny 不代替真实网络探针。
当前基础实现不能接受官方默认部署，必须先完成受限部署适配。

完整 #21 仍需产品执行 intent／outcome、去重和 unknown 对账、A 测试卡片、
正常完整 chi 基线、原仓库／index／准备基线保护，以及真实浏览器／Trace 收据。
随后按依赖完成 [#22](https://github.com/huadeng408/CodeOps-Agent/issues/22)
与 [#23](https://github.com/huadeng408/CodeOps-Agent/issues/23)。
原父 #6 的验收不变；本项不关闭任何未完成的票。
