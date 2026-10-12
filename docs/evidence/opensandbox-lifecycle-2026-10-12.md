# OpenSandbox 取消、恢复与 CLI：2026-10-12

范围对应 [#23](https://github.com/huadeng408/CodeOps-Agent/issues/23)，依赖已关闭的
[#21](https://github.com/huadeng408/CodeOps-Agent/issues/21)、
[#22](https://github.com/huadeng408/CodeOps-Agent/issues/22)。沿用 A 右侧卡片和已批准的
Workspaces／Runner／CLI／Ledger 公共接缝，执行 Matt Pocock implement → TDD →
Standards／Spec 两轴审阅。单测／集成及本机基础执行生命周期范围为 `VERIFIED`；
完整模型产品与发布另行验收。

## 执行与恢复契约

执行结果与资源停止确认分别保留。流中断、空流、部分输出或无法确认停止均保留
unknown；确认精确资源已停止不会把执行结果改成成功。取消使用原请求，清理使用
独立、有界 context，携带 Go 保存的 operation、逻辑 sandbox、物理 task／sidecar
身份；无法确认所有权不删除。已确认停止且没有未知模型用量时才释放原任务槽。

CLI 先保存 invocation 和 operation，再执行 Hook／工具。各阶段 runtime 收据先进入
Ledger，下一副作用才能开始；unknown 工具不派发后置 Hook。重复请求、重开、fork
和 rewind 保留原 operation、状态与结果，不重放既有副作用。历史投影恢复真实
call／result 配对及最新对账结果，原 raw events 保留。对账须明确核对原 runtime 身份、
停止及所有未确认的历史 runtime，默认空结果或替代身份拒绝。

Shell、Git 和可执行 Hook 共用所选 Runner；结构化参数保持字面内容，模板只展开一次。
已批准 Python 镜像没有 Git：实际 `shutil.which` 为 null，结构化 Git 在创建前明确拒绝。
Go CLI 的 Git 接入保留 Windows worktree 管理路径在隔离环境中不可访问的 exit 128，
未扩大挂载至原仓库 Git 管理目录。OpenSandbox 不支持的后台 job 明确拒绝，无 host
fallback。Docker／WSL 共用路径从已确认的精确 CID 清理传回停止状态；实际 Docker
取消、输出上限和缺镜像拒绝回归通过，未宣称本机真实 WSL 运行。

## 当前证据与完整分母

- 最终源码 v8 在运行前冻结 473 个源码／测试输入，基点
  `0d40006dd660040f14ddcf91f0d1c1de404d843f`；native 与诊断 driver 分别保存摘要。
- `go test ./... -json -count=1`：退出码 0，1,433 pass／60 skip／0 fail test actions，
  45 个测试包通过、13 个无测试包。`go vet ./...`、定向公共接口 GREEN、
  `git diff --check` 均退出码 0；此前所有 RED 和全量 v6 的 8 个失败保留。
- 前端 12/12 pass、0 skip／fail，`npm test` 与 `npm run build` 退出码 0；之后前端源码
  与实际加载的构建资产未变化。已检查真实浏览器取消／待核验／停止确认截图。
- v6 实际产品完整 Go 两包、Python 全部 81 项正常基线通过，未截断；原源码／副本
  分别 98＋98、32＋32 文件及 index、Python 唯一批准 wheel 均保持相符。实际测试
  根与 Runner 子 Trace 已结束并从 Phoenix 读回。v6 Go 取消保留 unknown＋停止确认，
  v8 Python 取消也保留同一契约；精确资源缺失且原任务槽释放。
- 冻结 v2 的真实故障矩阵共 12 场景：Go／Python 各覆盖丢 create 响应、流中断、空流、
  超时、停止未确认、进程故障。另 4 次部分输出尝试保留，包括两次 Go 驱动超时。
  进程被杀时原 Trace 未结束／缺失，明确保存为失败证据；重启读回同一请求、依赖和
  intent 不重放。该证据绑定原 v2 源码、二进制和任务输入，不改写为 v8 的新运行。
- v2 不确定资源仅在到期、原逻辑身份 404、operation 物理库存为空、无未知模型用量
  等证据齐备后，由显式 Go Budget 操作释放该槽；原 unknown 事实不改写。
- CLI 使用真实公共 `CLI.Run` 和真实固定 OpenSandbox，只有外部 orchestrator gRPC
  是传输 fixture。6 个请求／5 个唯一 invocation，包括缓存重复、完整基线、两个
  Git 调用、实际可执行 Hook 和 job 拒绝。此诊断不证明模型对话能力。
- 最终 v8 Go／Python CLI 各退出码 0，缓存请求未重跑；实际结束根 Trace 分别为
  `371bbfe7dda948285fc417baf639d30f` 与 `e20f90edd083290440dae74e16e673b0`。
  最终构建重开并重发全部 12 个原故障请求：测试、依赖、operation 与 event count
  均相同，0 次重放；原 v2 失败与未结束 Trace 不改写。
- v8 实际流中断各保留 Go 第一包的 `ok` 输出、Python 的批准 wheel processing 输出，
  两者均为 unknown＋停止已确认，不伪造完整测试结果。结束 Trace 分别为
  `a0773cd72a70614faaef94a6057d6247`、`2124ed4e79bd258f2848f4a942f7a35a`。
  Python 取消 Trace 为 `367b75a3cf739c5a8d30e9d38e351f1e`。完整运行、取消和故障
  各绑定实际阶段的 native／源码；v6 完整浏览器结果不改写为 v8 新运行。
- Standards 与 Spec 为 `AI_REVIEWED`，源码整改复审均 remaining 0；未宣称人工复核。

## 保留的失败与边界

原运行和失败均位于忽略目录 `.scratch/opensandbox-integration/lifecycle-runtime/`。
Go CLI v1 的 login shell 重置 PATH、Python CLI v1 的缺 Git／终态未知、旧 Hook 入口
未传 runtime 前提、Python 取消 race 和已完成过快的驱动尝试均保留，后续修复另行
运行，原结果不覆盖。Python cancel v7 准备时已有 Go CLI 占用唯一槽，原依赖准备
blocked 保留；该会话未执行取消，v8 在空槽下另行准备和验收。

共享批次仍为 `02cfc8d9c93975b3ea63f27cc0a3331d`、上限 100,000,000；模型调用为 0，
未读取模型凭据或重置批次。源码测试、真实基础执行和完整模型产品／发布分别记录；
完整代码任务、模型 Memory 和发布 lane 仍按 [GOAL](../GOAL.md) 验收。

## 不可变收据

原收据：
`.scratch/opensandbox-integration/lifecycle-runtime/receipt-cc843ae7-825f-41a8-9fb6-5fb091187c61/receipt.json`。
SHA-256：`a0544cd7d0a3f5ba9f6478a5f6ab1be940cfe8c16fadacee5abec285983dba38`。

收据封存 549 个运行／构建产物、八阶段共 3,780 个源码输入条目和 116 个尝试／失败
证据入口；940 个唯一内容块逐项复验，checksum 失败 0。早期两份变化源码只在原冻结
长度和摘要完全匹配时恢复，未修改 live 源码；失败的恢复候选也保留。当前 v8 的
473 个输入与工作区相符，浏览器实际加载资产与已测构建相符。

收据中的基点 SHA 只追溯构建前 checkout；交付绑定另核对当前提交与运行输入指纹。
不会改写原收据 SHA、结果、分母或失败。此为 #23 的工程范围收据，不作为完整发布 lane。
