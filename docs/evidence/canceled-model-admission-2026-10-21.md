# 取消后的模型准入封存

范围为 [#27](https://github.com/huadeng408/CodeOps-Agent/issues/27) 的旧任务恢复入口，
源码基点为 `cd89460a69c565064cafdb551c00794344911a2a`。
本记录只验收 Go 实现、HTTP 边界和一次性账本副本；真实账本尚未执行封存。

## 行为与边界

旧进程拥有的历史 lease 无法由新 runner 确认资源停止，普通结束操作继续拒绝。
新增独立的所有者操作，只在最新根 run 已持久化取消请求时封存模型准入身份。
未结束的关联 run 需要各自的取消请求；原有工具结果、停止证明、测试、应用、
子任务、活跃模型调用及进程作用域检查保留。永久拒绝旧身份再次调用模型，
允许后续独立任务使用原批次；不增加预算、不重放工具或模型。

封存复用 `admission/task_finished`，以 `purpose: "abandoned"` 区分普通结束。
Session 的状态和原始事件、unknown、消耗、预留及费用状态均保留。
HTTP 接口为 `POST /api/v1/sessions/:id/task/abandon`，沿用真实 JWT 和所有者检查。
该操作不声称外部资源已停止，也不把失败尝试升级为验收通过。

## 验证

- 核心公开 SessionRunner／SQLite 接缝：7 个叶子用例 PASS，退出 0；覆盖未知历史 lease、缺取消、活跃调用、活跃 scope、缺工具结果、未确认停止及其他 actor。
- Handler 生命周期及所有者边界：4 个叶子用例 PASS，退出 0。
- 实际服务路由与 JWT 中间件：4 个叶子用例 PASS，退出 0；缺凭据、无效 token 和其他所有者被拒，正确所有者到达 Gateway 边界。
- 核心 TDD 的缺方法编译 RED 退出 1。最初 HTTP RED 与编辑重叠而通过，不计作 RED；冻结基线处理器并强制重编译后，缺方法 RED 退出 1。修复后的定向测试退出 0，所有记录保留。
- 定向命令：`go test -json ./internal/admission ./internal/handler -run '^(TestAbandonCanceledAdmissionPreservesForeignCleanupUnknown|TestModelTaskAbandonRoutePreservesSessionAndOwnerBoundary)$' -count=1 -timeout=90s`；路由命令为 `go test -json ./cmd/server -run '^TestModelTaskAbandonRegistrationRequiresVerifiedOwner$' -count=1 -timeout=60s`，均退出 0。
- 完整 Go：`go test -json -p 1 ./... -count=1 -timeout=300s`，1759 PASS／82 SKIP／0 FAIL，叶子完整分母 1841，退出 0；另有 13 个无测试文件的包。运行约 778 秒，期间实现源码保持固定。
- `go vet -p 1 ./...`、`git diff --cached --check` 和 12 个精确暂存路径的凭据／机器路径扫描均退出 0，扫描未输出匹配值。
- 独立 Standards／Spec 均为 AI_REVIEWED、各 0 项发现；Spec 另行运行三个包的 15 个定向用例，全部通过，退出 0。对应提交的源码 CI 由正式票交付评论绑定。

原账本以只读 SQLite backup 生成一次性副本。Go 检查全部 140 条事件流，
原 Session 的 3510 个事件及所有原始前缀保持不变，只追加一条准入封存事实。
普通结束仍拒绝，封存重放幂等，旧身份重新准入被拒；旧版 Go 读取器回放成功。
已确认消耗仍为 69,621,825 tokens，未知预留仍为 6,506,024,576，
它是保留的审计估计，不是服务商余额。副本取消状态仍为 `canceling/unknown`。

本机证据位于忽略目录 `.scratch/cancellation-recovery-20261020/`。
`native-copy-receipt.json` SHA-256 为
`0d1a34291d54422535bac38dcda623951abd56dc66413b0336e54c1ea4432bfc`。
副本检查后重读真实账本，原 Session 导出 SHA-256 仍为
`4d4ede5b5a63f7f5997c44ea5f7451f8471210ec900e4900bc789ace95fac83c`，
准入导出也与检查前逐字节一致。

预算导出只包含调用事实，因此另以只读连接比较真实账本的全部 32,536 个事件、
140 条流与副本中的原始前缀，所有事件列均一致。准入原前缀为 5132 个事件，
末尾序号 5131；副本仅增加上述一条封存。首次核对脚本在已通过前缀比较后，
把零起始序号误作一开始计数而退出 1；修正计数后退出 0，首次失败保留。
`canonical-all-prefixes-unchanged.json` SHA-256 为
`c9afc0c689dde383a33456a718a18eae54552124da72b8e48038f23a9322d1e8`。

产品／真实模型／发布验证未运行；未新增 Docker 资源，也未替换原产品包。
原尝试仍为 137/200，取消 unknown 与任务槽均保留；实际封存及后续独立
200 轮产品验收仍待完成，不能拼接不同尝试。
