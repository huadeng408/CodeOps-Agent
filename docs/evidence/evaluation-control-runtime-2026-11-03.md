# 评测控制命令的策略运行时依赖修复

关联 [#42](https://github.com/huadeng408/CodeOps-Agent/issues/42) 与
[#36](https://github.com/huadeng408/CodeOps-Agent/issues/36)。本次先修复真实实验暴露的阻塞，
再继续原同模型实验；不扩大为新的 Context、工具检索或缓存工程。

## 真实触发与保留边界

`production-freeze-v31` 冻结 `06ed8075a4a8863d71a11073d13baab8e9b10054`。
`production-budget-v6` 首例完成了 6 次模型调用、10 次工具调用和 1 次 OpenSandbox 执行，
公开结构验证退出 0。确认用量为 109,790 token；费用未知，不查询账单或推算为零。
实际 profile 为 grok-4.6／xhigh／thinking=true，JSON 传输，输入／输出／上下文上限保持批准配置。

随后的第一次 `inspect` 触发外层固定 30 秒超时。stdout 仅有 started，尚未取得独立评分。
日志显示完整 App／Python 编排启动已接近该上限；之后一次终态读回的启动后处理约需数秒。
这支持优先消除控制命令对策略进程的启动依赖，不据此推导整个 Agent 的提速比例。

原试次保持 `infra_error`、`success=null`；另 5 个预算格、7 个恢复格和 1 个多轮格保持
`not_run`。Go finalize 与最终 Ledger 读回通过，后台实际读回 20 个匹配 Span，
包含对应 Trace／Session／Run 的已结束 agent.main。既有 controller／Phoenix 已恢复原停止状态，
无镜像拉取、构建或数据删除。原收据与 checksum 不改写。

原始证据位于忽略目录：

- `.runtime/eval/agent-experiments/artifacts/production-budget-v6/`
- `.runtime/eval/agent-experiments/artifacts/production-control-v2/`
- `.runtime/eval/agent-experiments/artifacts/production-freeze-v31/`

## 实现范围

- `internal/cli/evaluation.go` 继续复用统一的 Binding、权限、Ledger、用量和收据投影。
- `internal/cli/evaluation_control.go` 为 inspect、已终态 finalize 组装必要的 Go 组件，
  不启动 Python、MCP、SessionRunner worker 或新的模型工作。
- 仍活动的 finalize／abandon 沿用既有取消、工具对账与收尾路径；终态分类后若状态改变，
  拒绝继续，不能凭首次分类制造完成状态。
- `internal/cli/evaluation_controls_test.go` 通过公共 RunEvaluation 边界完成候选，
  停止原 gRPC 服务，再用进程启动陷阱验证 inspect → finalize → inspect，
  同时核对只读 head、计量及无新增模型／工具调用。

外围驱动另修正了 `trial.status` 枚举：安全结束的真实负结果使用 `agent_failure`，
不再误用 Go 的 `failed/canceled`。7 个离线契约案例先 RED 退出 1，再 GREEN 退出 0；
原 v31 冻结驱动保留。该检查没有调用模型或 Docker。

## 开发验证与声明范围

`.runtime/eval/agent-experiments/artifacts/evaluation-control-runtime-v1/` 保存实际命令、日志和 8 项校验和。

| 检查 | 结果 |
| --- | --- |
| 新公共案例 RED | 退出 1，实际尝试启动已不可用的策略运行时 |
| 同一案例 GREEN | 退出 0 |
| `go test -vet=off -p 1 -json ./internal/cli -run '^TestEvaluation' -count=1 -timeout=15m` | 退出 0；96 pass、4 skip，共 100 个终态测试事件（含子测试） |
| `go vet -p 1 ./internal/cli` | 退出 0 |
| 三份源码 diff check | 退出 0 |
| Standards／Spec 独立审查 | 各 0 项 finding |

Go 为已缓存的 1.26.0。夹具中三次控制操作分别为 0.984／1.035／0.916 秒；
这不是实际 Docker 的性能数据。开发回归只捕获运行后的源码身份与三文件 SHA，
运行前 pin 未捕获；原件明确记录该限制，不回造输入绑定。
最终源码绑定、构建和后续真实实验另以公共验证入口及新冻结产物记录。

旧 engineering-full-v2 只作历史工程证据，不能声明本次新 Go 源码已经取得该全量通过。
本页当前范围为实现及上述定向回归；真实评分、故障处理、多轮序列、原 #27 和七条发布 lane
继续按各自收据验收。

## 最终定向源码绑定与构建

公共入口实际运行：

```text
python -m scripts.verify --plan .runtime/eval/agent-experiments/inspect-fix-targeted-plan.pending.json --output .runtime/eval/agent-experiments/artifacts/evaluation-control-inspect-fix-v1/verification --run-id targeted
```

4 项均为本次新执行、退出 0，owned process tree 停止均确认：
5 个目标公共测试展开为 7 个 pass／0 skip、CLI vet、agent 构建及 `git diff HEAD --check`。
完整收据的 before／after source pin 相同，checksum 校验通过。
此入口补齐最终范围的运行前后绑定，不把开发回归缺少的 before pin 回造为存在。

- 原运行 Git：`06ed8075a4a8863d71a11073d13baab8e9b10054`，
  dirty hash `c42afb0583a89f4838c99bddffab635c146645e105d265d074d63b9399e18763`，2 个新增文件。
- 收据 SHA-256：`f70aa6089304d5f54049b83681a052956546aae3797d73fc7ba6153624fa6799`。
- 新二进制 SHA-256：`2d3801031aaa106dc12f7a5c17e16b69973fa368fa653283403b4ba7ae3d4108`。
- `tested-source-freeze.json` 保存实际运行的 5 份 working／index SHA 及测试树
  `9aa04ceb3ccad009c2b7fc29a218bfbadaaadd53`。暂存秘密扫描 5 路径、0 命中。

运行结束后只向本普通证据页追加实际结果；其余 4 份源码、测试和已加载 GOAL 的运行字节保持不变。
后续提交绑定必须核对这个精确差异，保留原收据与 dirty pin。真实模型／评分／后台验收
仍由新试次提供；本次构建不代表原七条发布 lane 已通过。
