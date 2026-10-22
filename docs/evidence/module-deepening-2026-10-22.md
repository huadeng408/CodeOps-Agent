# Context、准入与 Session Module 深化

实施日期：2026-10-22，验证收口延续至 2026-10-23。规格 [#31](https://github.com/huadeng408/CodeOps-Agent/issues/31)，
实施票 [#32](https://github.com/huadeng408/CodeOps-Agent/issues/32)、
[#33](https://github.com/huadeng408/CodeOps-Agent/issues/33)、
[#34](https://github.com/huadeng408/CodeOps-Agent/issues/34)，
集成与审查 [#35](https://github.com/huadeng408/CodeOps-Agent/issues/35)。

## 声明范围

工程状态：IMPLEMENTED；VERIFIED 范围为源码、单元／集成及确定性跨进程 fixture。
真实模型、产品长对话、真实 sandbox 与发布不在本次验收范围。
当前产品缺口仍以 [GOAL.md](../GOAL.md) 为准，已有运行和历史收据不改写。

- Context：`compact_surface` 接管 pressure/prune、选区、摘要 fallback、活跃输入、
  runtime prefix、tool 配对及 provenance；Conversation 保留路由、audit 和传输。
  legacy compactor 单向 Adapter 保留，Go 继续独立验证压缩提案。
- Task Workspace：生产准入只组合一次，Gateway 与 SessionRunner 共用完整入口；
  授权、恢复、活跃调用、workspace 校验及 executor 准备维持先后约束。
  准备失败或必要依赖缺失时不派发模型、不新增 run。
- Session：终态持久化共享有界、按持久化进展重试的 commit closure；
  四类收尾路径共用后处理，保留 Trace、Subagent、Memory 的各自约束。
  Memory 超时留下独立 blocked 记录，不改写已经持久化的任务结果；取消路径独立。

## 源码及输入绑定

批准的审查基准是 `047fd8d64b8a61ebcbd00571839912f5a3a2554b`。
本轮验证针对该基准加本次未提交改动；固定输出中记录的 `git_sha` 是基准 SHA，
不能单独代表本次源码。最终源码与测试文件通过本轮 manifest 的 SHA256 绑定。
无依赖、protobuf、配置或前端变更。测试输入为版本控制内的测试及现有 fixture。

本机原始日志保存在 `.runtime/validation/module-deepening-20261022/`（忽略、不提交）。
下列结果保留完整失败与跳过分母，不把 red 失败或 fixture 结果改称产品证据。
初始输入记录为 `source-manifest.json`，最终冻结输入为 `source-manifest-final.json`
（20 个源码／测试／依赖及指令入口；Go 1.26.0、Python 3.12.4）。最终 manifest
SHA256：`02773076e1d2b71d47f950aff2d32c1f488ca34cf9546f35646992046202d4bd`。
最终测试后再次逐文件核对，源码与该 manifest 一致。

## 切片验证

| 范围与命令 | 完整结果 | 退出码 |
| --- | --- | --- |
| Context 原有两个文件基线 | 31 passed，0 failed，0 skipped | 0 |
| Context 新公开入口的首个测试 red → green | red：1 failed（入口尚不存在）；green：1 passed | 1 → 0 |
| `python -m pytest -q tests/test_compactor.py tests/test_compaction_provenance.py tests/test_conversation_tool_adjacency.py` | 46 passed，0 failed，0 skipped | 0 |
| `python -m pytest -q tests/test_server.py tests/test_context_runner_events.py tests/test_context_runtime_provider.py` | 120 passed，0 failed，0 skipped；最后一次 schema 延迟获取调整前启动，最终源码由全套覆盖 | 0 |
| 完整准入准备失败 red → green | red：1 failed，观察到未接入准备时派发模型／新增 run；green：4 passed（含 3 项原回归） | 1 → 0 |
| `go test ./cmd/server -count=1 -json` | 41 个顶层测试：40 passed、1 skipped；9 个子测试全通过。终态记录合计 49 pass、0 fail、1 skip | 0 |
| Session 成功 commit closure red → green | red：1 failed；green：4 个顶层测试全通过 | 1 → 0 |
| Session 公开 Memory 超时入口 | 1 passed，0 failed，0 skipped | 0 |
| Session 最终整包 `go test ./internal/session -count=1 -json` | 顶层 197 passed、0 failed、3 skipped；叶场景 280 passed、0 failed、3 skipped；含父记录 302 pass、0 fail、3 skip | 0 |
| Docker readiness 后 `go test ./internal/cli -count=1 -json` | 顶层 120 passed、0 failed、3 skipped；叶场景 152 passed、0 failed、3 skipped；含父记录 162 pass、0 fail、3 skip | 0 |
| 3 项 Context 跨进程 fixture 最终完整复跑 | 3 passed、0 failed、0 skipped | 0 |
| 2 项生产 local-core 入口进程 fixture | 2 passed、0 failed、0 skipped | 0 |
| `python -m pytest -q tests/test_context_runtime_provider.py` | 4 passed、0 failed、0 skipped | 0 |

准入唯一 skip 是仅由父测试作为子进程调用的 `TestRecoveryModelHolderHelper`；
对应进程恢复父测试通过。Context 的既有 LangChain 弃用 warning 保留，未改依赖。
Session 的 3 项 skip 是子进程 helper 和 2 项未提供已批准缓存 wheel 的测试；
CLI 的 3 项 skip 是真实 RAG 门控、Windows symlink 权限和子进程 helper。
Docker readiness 沿用 `scripts/rag-agent-e2e-runtime.ps1` 的 `Wait-DockerDaemonReady`，
不以关闭 sandbox、绕过准入或下载依赖使测试通过。

跨进程命令（Go 测试不启用并行子测试）：

```powershell
$env:CODE_AGENT_RUN_CONTEXT_E2E = '1'
$env:CODE_AGENT_E2E_PROVIDER = 'deterministic'
go test ./tests/e2e -count=1 -json -run '^(TestContextCompactionRuntimeAcrossProcesses|TestContextCompactionPersistsIntoGoSessionAcrossRestart|TestManualCompactionRuntimeAcrossProcesses)$'

$env:CODE_AGENT_RUN_LOCAL_CORE_E2E = '1'
go test ./tests/e2e -count=1 -json -run '^(TestProductionLocalCoreStartsWithoutOptionalServices|TestProductionLocalCoreConfiguredProviderRequiresApprovedRuntime)$'
```

Context fixture 的唯一模型响应源是 loopback HTTP 确定性替身，仍经过真实
Go Gateway、Budget、Session Ledger 和压缩验证／提交回调。三项覆盖自动压缩、
Go Session 持久化后重启恢复，以及手动压缩后恢复；两项 local-core 进程检查覆盖
可选服务缺失时启动和已配置 provider 仍须获得 runtime 准入。

## 集成、审查与交付

首次及最终独立 Standards／Spec 审查均为 0 项待处理发现。最终审查覆盖精确 staged
diff 的 18 个路径，包含新增 fixture 接线和 Trace 等待修正；基准仍为批准的 SHA。

集成中保留的失败：

- Session 首次整包暴露 `TestIndependentAgentAndArtifactFailuresSetContentFreeStatus/agent`
  的异步观察竞态。任务终态从来不保证 span 已结束；测试改为通过公开 `Close()`
  等待收尾，再检查原有 span、错误状态及脱敏断言，未改变生产 Trace 逻辑。
- Context 确定性跨进程 fixture 首次 0 passed／3 failed／0 skipped，退出 1。
  自动压缩启动遇到 readiness 超时；Go Session 恢复与手动压缩分别遇到
  `model_prerequisites_missing`／`ModelGatewayError`，旧测试缺少现行 Go 模型准入绑定。
  原始日志 `context-process-fixtures.jsonl` 保留；本次未产生可验收的新 receipt。
  三份历史固定路径 receipt 已恢复，并与运行前 SHA256 一致。
  接入 Go Gateway 后首次复跑 2 passed／1 failed／0 skipped，退出 1；自动压缩项
  还缺少当前协议要求的 Go 持久化回调。补齐历史与回调后最终三项全部通过。
  两次失败分别保留在原始日志及 `context-process-fixtures-retry.jsonl`。
- 首次全 Go 命令退出 1：45 个包通过、2 个包失败、13 个包无测试；
  顶层记录 1187 pass／1 fail／78 skip，含父记录 1810 pass／2 fail／81 skip。
  Session 失败与上面的 Trace 竞态相同；CLI 在沙箱 availability 探测期间达到
  Go 默认 10 分钟包超时，其中 58 passed、2 skipped、1 started-but-unfinished，
  后续未执行的测试不计为通过。后续依仓库 helper 恢复 Docker readiness 后补跑。
- 首次全 Python 命令退出 1：2657 passed／1 failed／18 skipped，31 warnings。
  唯一失败为未改动的 `test_non_cooperative_adapter_is_cut_off_at_instance_deadline`：
  整个 harness 调用耗时约 0.323 秒，超过 0.25 秒断言。精确用例复跑 1/1 通过，
  所属文件复跑 27/27 通过；不改动或放宽该断言，原失败保留并重跑最终全套。

最终全仓验证通过，首次失败和中间复跑结果继续保留。

| 最终验证 | 完整结果 | 退出码 |
| --- | --- | --- |
| Docker readiness 后 `go test ./... -count=1 -json` | 47 个包通过、13 个包无测试；顶层 1260 pass／0 fail／79 skip；叶场景 1788 pass／0 fail／82 skip；含父记录 1916 pass／0 fail／82 skip，无未完成测试 | 0 |
| `python -m pytest -q` | 2676 项：2659 passed、0 failed、17 skipped，31 warnings | 0 |
| `go vet ./...` | 全部通过 | 0 |
| `python -m compileall -q orchestrator/context orchestrator/runtime/conversation.py tests/e2e/context_runtime_server.py` | 全部通过 | 0 |
| 导入 `compact_surface`、`CompactionResult` 和 `ConversationRunner` | 全部通过 | 0 |
| `git diff --cached --check` | 精确 18 个 staged 路径通过 | 0 |
| staged 脱敏扫描 | 精确 18 个路径，无意外／缺失路径，无凭据或机器路径发现，不输出匹配值 | 0 |

`go-full-final-summary.json` 保存完整 skip 列表及原因。叶场景指没有任何后代测试的
测试名称；含父记录会计入父测试，不能与独立场景分母混用。
`python-full-denominators.json` 保存两次完整 Python 分母及 skip 名单；最终 17 项为
16 项 Windows symlink 权限限制和 1 项未启用的独立进程 fixture 门控。
Docker 就绪后，原跳过的 stale Docker host 测试在最终全套中通过。

## 产物校验与交付边界

下列路径均相对于本机证据目录，不提交原始日志和运行产物。

| 产物 | SHA256 |
| --- | --- |
| `go-full.jsonl`（首次） | `7d79d29e7d703ad5b545d9f6c0f351ef9848231c4543f6c512c043aa91d6c6a4` |
| `python-full.log`（首次） | `ab56b3230f0b0c7f05ee1dc7791357d747f9fa4088b2162a71e7737c7aa97df9` |
| `go-full-final.jsonl` | `32b3ee58560a68ad260143104b5cdeecc783d9ea4525dab321083bd511ae5368` |
| `python-full-final.log` | `8d6391eb328a5575880fd4932c01488cf739aa2cd825d4e538a827587729e6f0` |
| `context-process-fixtures-final.jsonl` | `059de861e8215a56bb9590590b283c1e08d4e46023d29fbb7a771acf8aecfa76` |
| `local-core-process-fixtures.jsonl` | `e05225052c7b6dd8638df6b678ef98e9b998da33defe9e3835923052434069e1` |
| `final-fixture-receipts/context-compaction-process-recovery.json` | `c8dcdfa9dba3e81fe6e6d1eababfb9778f7656b3d07f1fbbc4a09aa0c31fdf10` |
| `final-fixture-receipts/context-compaction-go-session-recovery.json` | `074e765d92ab072af26ca43027b897e5ba810c1164f8fbd10415d5f1bde5095d` |
| `final-fixture-receipts/context-manual-compaction-process-recovery.json` | `4f89221b0a5c26e3185757796003a858ccb6c64212499af9a8c5cbccfa2ebf89` |

`artifact-checksums.json` 保存 70 份日志、分母摘要、输入 manifest 及 receipt 的逐文件
SHA256，索引自身 SHA256 为 `5c44f0a32eb8d97641f13a76b0225eab1eec3b5b2f86982d0b53cd93a31ee9be`。
它包含 red、失败复跑和 skip 记录，未用成功子集替换完整分母。

源码冻结后的精确 staged diff、脱敏扫描和 whitespace 检查均通过，退出码均为 0；
交付绑定以本文件所在 Git 提交及子票记录的推送 SHA 为准。三份历史 receipt 已逐字节恢复。
当前声明范围无未解决 blocker。真实模型、产品长对话、真实 sandbox 与发布门禁均未运行，
不据本轮 fixture 关闭父规格或产品／发布任务。
