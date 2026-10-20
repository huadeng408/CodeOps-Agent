# 可复用组件调查

调查日期：2026-10-08。状态：`DESIGNED`。本文记录已核对的第一方源码、文档和许可证，
不代表这些组件已经接入，也不产生产品 runtime 或发布收据。

## 结论与接入边界

有可直接使用的组件。最快保留 Go Harness、现有 LangGraph 和 Go Session Ledger，
补模型服务、执行镜像、检索索引和验收 adapter。不同于安装运行时，200 轮、
8 Worker/200 任务/30 故障、40 Skills 和官方得分仍需要由本产品运行并收集证据。
下文的“直接使用”表示组件提供所需 API/二进制，不表示配置、硬件、协议或验收已完成。

| 缺口 | 优先复用 | 接入程度 |
| --- | --- | --- |
| 无云端 provider key | Ollama 本地模型 + 已有 `LocalClient` | 本地对话接口已有；真实工具调用、反思、模型 digest 与测试 provider 路线仍要验证/适配 |
| 可运行代码的沙箱 | 已有 Go Docker/WSL runner + Dev Containers 语言镜像/Features | runner 已有；构建与固定代码任务镜像，验证受限环境中的真实编译/测试 |
| Go/Python trace | 已有 OpenTelemetry；最快沿用 Phoenix；严格开放许可可选 Jaeger | 埋点已有；检查跨进程 parentage 和 readback。切换 backend 需要 readback adapter |
| 浏览器/网络故障 | Playwright、Toxiproxy | 浏览器库已有；增加真实端点与故障脚本 |
| 向量记忆 | Qdrant + 官方 Go client + embedding 服务 | 增加派生索引 adapter，保留 Ledger 权威与词法召回 |
| 反思提取 | 先完成已有 `reflect_memory`；可借鉴 LangMem 无存储 extractor | 源码已有；LangMem 不是零适配替换，不能让默认 store 产生第二个事实源 |
| Workflow 恢复 | 现有 LangGraph `SqliteSaver` 和 Worker/checkpoint 入口 | 无需再换编排框架；补真实规模与故障运行 |
| 官方评分 | pinned 旧 TB Harness/SWE-bench scorer；TB2.0 使用 Harbor | scorer 可复用；被测 Agent 必须是本产品 Go 路径，结果版本必须匹配 |

## 真实运行：本地模型可以替代云端凭据前提

[Ollama](https://github.com/ollama/ollama) 是 MIT 项目，本地 API 不要求身份验证，
本地推理与需要 API key 的 Ollama cloud 路径不同。
[认证说明](https://docs.ollama.com/api/authentication)、
[OpenAI-compatible API](https://docs.ollama.com/api/openai-compatibility)、
[工具调用](https://docs.ollama.com/capabilities/tool-calling)、
[许可证](https://github.com/ollama/ollama/blob/main/LICENSE)。

本仓库 [LocalClient](../orchestrator/llm/providers/local.py) 已允许空 key，
[provider factory](../orchestrator/llm/providers/__init__.py) 接受 `LLM_PROVIDER=local`，
可以用 `LOCAL_LLM_BASE_URL` 和 `LOCAL_LLM_MODEL` 指向已安装的本地模型；
[OpenAIClient](../orchestrator/llm/providers/openai.py) 仅在 key 非空时发送 Authorization。
因此“没有云端 key 就不能进行任何真实模型验证”范围过宽。这个代码路径可用于
本地真实推理，不能拿本地 fixture/echo 替代它。

需要选择支持工具调用、能在本机运行的模型，固定下载模型的 digest，并核验工具参数、
反思 JSON 和长上下文表现。模型权重许可独立于 Ollama 的 MIT 许可；本次未安装模型，
未对任何具体模型或硬件作运行承诺。当前 provider-backed E2E/identity 收据还需显式
支持 local provider 与可核验 model revision，不能用一个通用 fingerprint 或模型别名
冒充权重版本，也不能把本地结果直接归给指定云模型。

### 执行镜像是现成部件，隔离策略仍由 Go 控制

[Dev Containers Images](https://github.com/devcontainers/images) 提供预建语言环境，
[Features](https://github.com/devcontainers/features) 可组合 Go/Python/Node 运行时。
镜像构建源码为 MIT；镜像内软件有各自许可和 NOTICE。
[Go 镜像说明](https://github.com/devcontainers/images/blob/main/src/go/README.md)。

直接复用语言镜像或构建 Features 到本项目专用 OCI 镜像，避免重新编写运行时安装器。
不能把 `devcontainer.json` 当成安全边界：最终启动仍由
[Go sandbox runner](../internal/sandbox/docker.go) 生成参数，保持 network none、
cap-drop、no-new-privileges、受约束 mount 和资源预算；不挂 Docker socket 或使用 privileged。
当前默认 `alpine:3.20` 不代表已有 Go/Python/Node 工具链。真实代码测试还需验证工作树
写权限、依赖缓存和可执行构建产物的临时目录；默认 `/tmp` 为 noexec，不能只换镜像
就声称 `go test` 等编译执行任务能完成。镜像固定 digest，准备依赖和镜像的网络阶段
与运行任务的隔离阶段分开。

### Trace：最快复用现有服务，许可与 readback 分别处理

本仓库已使用 Go/Python OpenTelemetry；
[OpenInference](https://github.com/Arize-ai/openinference)（Apache-2.0）提供 AI span
语义和框架 instrumentor，但不会替本产品传递 Go/Python TraceContext 或补齐缺失的 span。
[OTel/OpenInference 关系](https://arize.com/docs/phoenix/tracing/concepts-tracing/otel-openinference/overview)。

现有 Phoenix 最快可继续作为外部 trace 服务使用；其当前
[LICENSE](https://github.com/Arize-ai/phoenix/blob/main/LICENSE) 为 Elastic License 2.0，
不能把 Phoenix 源码复制进本仓库后重新标为 Apache-2.0。
若要求 tracing backend 也采用开放许可，
[Jaeger](https://github.com/jaegertracing/jaeger) 为 Apache-2.0，支持 OTLP 接收，
也有稳定的 trace 查询 API；需要适配本项目原有 Phoenix readback，不能只更换 UI。
[Jaeger API](https://www.jaegertracing.io/docs/latest/architecture/apis/)。

## 记忆与编排：补检索索引，不增加第二个事实源

[Qdrant](https://github.com/qdrant/qdrant) 与
[官方 Go SDK](https://github.com/qdrant/go-client) 均为 Apache-2.0。
可复用 dense/sparse 检索、payload filter 和 RRF hybrid query；
[官方 hybrid query](https://qdrant.tech/documentation/search/hybrid-queries/)包含 Go 示例。

适配位置为 [LedgerMemory.RecallWithOptions](../internal/memory/ledger.go)：Go 负责
请求过滤与候选校验，索引只存由 Ledger 派生的数据，以 owner/workspace、记忆 ID、
revision/source checksum 和 embedding model digest 绑定。召回后核对有效 revision、
删除/过期状态与来源；索引可从 Ledger 重建，不承担事实提交。保留已有词法召回，
再做同任务/同模型预算的语义与 token 对照。

Qdrant 不是 embedding 模型；自托管场景另需 embedding 服务，可选
[Ollama embed API](https://docs.ollama.com/api/embed)。固定模型与维度，验证输入未被
静默截断，不能把“向量库可搜索”解释成检索质量或 token 降幅已达标。

`sqlite-vec` 可以作小型嵌入式索引备选，但不是当前 `modernc.org/sqlite` 的直接插件。
[官方 Go 指南](https://alexgarcia.xyz/sqlite-vec/go.html)提供 CGO 与
`ncruces/go-sqlite3`/WASM 两条接入路径。为了向量功能替换 Go Session Ledger 的
数据库驱动，会扩大刚完成的 Windows/恢复回归范围；本次不建议这样做。

[LangMem](https://github.com/langchain-ai/langmem) 为 MIT，可复用结构化提取、记忆更新
和反思策略。[无存储模式](https://langchain-ai.github.io/langmem/guides/extract_semantic_memories/)
中的 `create_memory_manager` 只提取/返回候选，适合接本项目 Go 验证与 CAS；
`create_memory_store_manager` 的默认直接 upsert/delete 路径不宜接成另一套权威存储。
当前 [reflect_memory](../orchestrator/memory/reflection.py) 已有候选提取，而
[Go evolution](../internal/memory/evolution.go) 校验 source IDs 与摘要后提交事实；
优先诊断真实写回，而不是靠替换库隐藏 provider/schema/provenance 错误。
若采用 LangMem extractor，仍需适配现有自定义 LLMClient、source IDs、取消、预算和模型身份，
先锁定依赖交集，不直接无约束升级整个 LangChain/LangGraph 依赖树。

[LangGraph](https://github.com/langchain-ai/langgraph) 为 MIT，本仓库
[main graph](../orchestrator/graph/main_graph.py) 和
[subagent graph](../orchestrator/graph/sub_agent_graph.py) 已接 `SqliteSaver`。
沿用现有 Worker/checkpoint 与 Go 任务生命周期做 8 Worker/200 任务/30 故障，
不需先换另一套调度平台；checkpoint 仍是工作流投影。
[官方 persistence 文档](https://docs.langchain.com/oss/python/langgraph/persistence)
解释 checkpointer 与长期 store 的不同职责。持久化组件不会自动保证任务成功率，
relay 的 1–10 聚合并发限制也继续由本项目控制。

## 产品可靠性：直接复用 Playwright，按需增加 Toxiproxy

| 组件 | 可复用部分 | 本仓库还需要做的部分 | 许可证与来源 |
| --- | --- | --- | --- |
| Playwright | 已在 `frontend/package.json` 安装；浏览器操作、WebSocket 帧观察、截图、Trace Viewer | 沿用现有浏览器脚本模式，另外跑真实 Go/Python/provider 入口；定义 200 轮任务、compaction 触发、刷新、ticket 过期、重复发送、重连与重启断言 | [Apache-2.0](https://github.com/microsoft/playwright/blob/4357c237cfde9135fb5b7894c22a45468321a973/LICENSE)；[网络与 WebSocket](https://playwright.dev/docs/network)、[Trace Viewer](https://playwright.dev/docs/trace-viewer) |
| Toxiproxy | 独立 Go TCP 代理、HTTP 控制 API、官方 Go client；可直接用 Windows EXE 或容器 | 把测试端点指向代理，在 Browser→Go、Go→Python gRPC、provider/trace 外部连接分别注入故障；用真实进程终止覆盖 crash | [MIT 与源码](https://github.com/Shopify/toxiproxy/tree/f83c9865e568ddd795f00076b58b0ba0e7df1146)；[v2.12.0 Windows 二进制](https://github.com/Shopify/toxiproxy/releases/tag/v2.12.0) |

Playwright 已提供实际浏览器、网络与 WebSocket 观察能力，无需换前端或增加另一套浏览器
自动化框架。`frontend/tests/sendMessage.browser.mjs` 使用 `route.fulfill` 和
`routeWebSocket`，仍应作为接口替身回归保留；复制它的流程并取消替身不等于验收完成，
还必须检查 Go Ledger 的终态、event count、compaction 与重启后的连续性。
[官方网络语义](https://playwright.dev/docs/network)明确区分继续真实请求与直接返回替身响应。

Toxiproxy 可注入 latency、bandwidth、timeout、reset_peer、limit_data 等 TCP 故障，
适合真实 reconnect 和 gRPC 故障。它不生成合法的业务 HTTP 429/409、不终止进程、
不验证 Git 回滚或 SQLite 持久性；这些继续由现有 Go 测试入口和进程控制覆盖。
不要用“断浏览器网络”替代 Go/Python crash。能力范围见
[官方 README](https://github.com/Shopify/toxiproxy/blob/f83c9865e568ddd795f00076b58b0ba0e7df1146/README.md)。

最快路径判断：先用已安装的 Playwright 完成真实小矩阵，再用 Toxiproxy 补真实网络故障；
暂不引入 Selenium、Cypress 或大型 chaos 平台。200 次发送本身不能证明 200 次完成，
需要以 Ledger assistant-turn/terminal events 计数，失败也留在分母内。

## Skills：复用标准与明确开源的样例，继续跑本仓库矩阵

[Agent Skills](https://github.com/agentskills/agentskills/tree/69ef37e9424c0a7ea9dd2293b559e43ec8176379)
提供 `SKILL.md`、scripts/references/assets 的跨产品格式，按 metadata→instructions→resources
渐进加载。代码为 Apache-2.0、文档为 CC-BY-4.0，不能把文档许可与代码许可混为一谈。
本仓库已有 Skill discovery、lazy-load 与 `eval/harness/skill_selection_eval.py`，
应复用这些入口，而不是另建一套 Skill registry。
[格式规范](https://agentskills.io/specification)、[项目许可证说明](https://github.com/agentskills/agentskills/blob/69ef37e9424c0a7ea9dd2293b559e43ec8176379/README.md)。

`skills-ref validate` 可以直接作为开发/CI 格式检查，检查 frontmatter、命名与目录匹配。
其 [README](https://github.com/agentskills/agentskills/blob/69ef37e9424c0a7ea9dd2293b559e43ec8176379/skills-ref/README.md)
明确标为 demonstration only，不建议生产使用。因此不把它放进生产执行路径，也不把
格式通过解释成 Skill 能完成任务、权限正确或 40 Skills 实际跑通。

[anthropics/skills](https://github.com/anthropics/skills/tree/683bc88e56f3e09ba94f7055977f3d3aa499f202)
是混合许可仓库，已经逐目录核对以下候选：

| 目录 | 许可核对 | 建议 |
| --- | --- | --- |
| `webapp-testing` | [Apache-2.0](https://github.com/anthropics/skills/blob/683bc88e56f3e09ba94f7055977f3d3aa499f202/skills/webapp-testing/LICENSE.txt) | 可复用 Playwright 示例和服务启动思路；本仓库已有脚本，优先借鉴断言，避免重复安装框架 |
| `mcp-builder` | [Apache-2.0](https://github.com/anthropics/skills/blob/683bc88e56f3e09ba94f7055977f3d3aa499f202/skills/mcp-builder/LICENSE.txt) | 可复用工具设计/测试规范；具体 scripts 的副作用仍由 Go 授权 |
| `frontend-design` | [Apache-2.0](https://github.com/anthropics/skills/blob/683bc88e56f3e09ba94f7055977f3d3aa499f202/skills/frontend-design/LICENSE.txt) | 可复用指令；不直接替代本项目的 UI 审核和截图验收 |
| `skill-creator` | [Apache-2.0](https://github.com/anthropics/skills/blob/683bc88e56f3e09ba94f7055977f3d3aa499f202/skills/skill-creator/LICENSE.txt) | 可作本仓库 Skill 编写参考，不必增加新运行时 |
| `docx`、`pdf`、`pptx`、`xlsx` | [docx](https://github.com/anthropics/skills/blob/683bc88e56f3e09ba94f7055977f3d3aa499f202/skills/docx/LICENSE.txt)、[pdf](https://github.com/anthropics/skills/blob/683bc88e56f3e09ba94f7055977f3d3aa499f202/skills/pdf/LICENSE.txt)、[pptx](https://github.com/anthropics/skills/blob/683bc88e56f3e09ba94f7055977f3d3aa499f202/skills/pptx/LICENSE.txt)、[xlsx](https://github.com/anthropics/skills/blob/683bc88e56f3e09ba94f7055977f3d3aa499f202/skills/xlsx/LICENSE.txt)：保留全部权利且有额外使用限制 | 排除直接复制、改作或分发到本 Apache-2.0 仓库；沿用现有 PDF/Office 实现 |

不要整包复制并重新标 Apache-2.0；[官方 README](https://github.com/anthropics/skills/blob/683bc88e56f3e09ba94f7055977f3d3aa499f202/README.md)
也明确四个文档目录为 source-available、非开源。以上仅核验表中候选，未对其他目录作
整体许可承诺。实际引入还需保留对应 LICENSE/NOTICE、固定来源 revision、检查脚本依赖。

`allowed-tools` 是标准里的实验字段，客户端支持并不一致；它不能为外部 Skill 提升
Go Harness 权限。将兼容的 Skill 放入现有 catalog 后，仍要跑每项 discovery、
lazy-load、授权执行及错误恢复；40 Skills / 1,000 cases / 948 正确选择是本项目
验收门槛，不是上游保证。[规范](https://agentskills.io/specification)、
[本仓库发布门禁](release-gate.md)。

## 官方评测：复用 scorer，接入被测 Go 产品

| 组件 | 能否用自定义 Go Agent | 能直接用什么 | 必须适配什么 |
| --- | --- | --- | --- |
| 旧 Terminal-Bench `terminal-bench==0.2.18` | 能；Python `BaseAgent.perform_task(instruction, TmuxSession, logging_dir)` 可作为薄启动器运行 Go CLI | 本仓库已固定 package、official Harness、task tree pin 与收据采集 | adapter 必须实际调用本产品 Go Harness；测试环境/权限、trace join、真实输出与完整分母仍要接通 |
| Harbor / Terminal-Bench 2.0 | 能；官方支持自定义 Python `BaseAgent` import path，或环境内运行的 `BaseInstalledAgent` | `harbor` CLI、Docker 环境、任务 verifier、并发 trials、已有 Codex/pi 等对照 Agent | 启动/安装本产品 Go+Python；实现 `setup`/`run`/context；版本化收据转换与 TB2.0 门禁；不能沿用旧 scorer 身份 |
| SWE-bench | 能；评分器消费 patches，不限定产生 patch 的 Agent 语言 | 官方 Docker scorer，`predictions.jsonl` 格式 | 沿用本仓库 AgentAdapter/预测生成；固定 scorer/data/model/image revision，读取匹配版本原始输出并加产品 trace/source 收据 |

旧 Terminal-Bench 的自定义 Agent API 与 import path 在
[BaseAgent](https://github.com/harbor-framework/terminal-bench-1/blob/d28711d0da2675d0bb1d56de45ae5df6082438a3/terminal_bench/agents/base_agent.py)、
[AgentFactory](https://github.com/harbor-framework/terminal-bench-1/blob/d28711d0da2675d0bb1d56de45ae5df6082438a3/terminal_bench/agents/agent_factory.py)
可见。仓库许可为 [Apache-2.0](https://github.com/harbor-framework/terminal-bench-1/blob/d28711d0da2675d0bb1d56de45ae5df6082438a3/LICENSE)。
本机此次只读确认安装版本为 `0.2.18`；当前 Python 环境没有 `swebench` 的 package metadata。

最快先保留现有官方旧 Harness 的版本和收据边界。当前
`eval/swebench_work/deepseek_tb_agent.py` 直接请求模型并用 `session.send_keys` 执行命令，
是 Python 终端 Agent，不能自动算作 Go 产品验证。需要在任务容器内运行本产品 Go CLI
及 Python orchestrator，让权限、工具、Ledger、进程生命周期继续经过 Go，Python
评测 wrapper 只负责准备/启动/采集。原始测试和解答留给官方 verifier，不注入 Agent。

实际 adapter 还要明确 Go/Python 在 host 或 task container 的部署位置，并把官方任务
环境的执行接到 Go 授权链。不能为了嵌套 Docker runner 而给 Agent 容器挂宿主 Docker
socket；若复用官方容器的隔离，需要 Go 可核验的 environment adapter 及独立隔离验证。

[Harbor README](https://github.com/harbor-framework/harbor/blob/4b94505a91c5ddcb70b5740ac95718ddee13e5a0/README.md)
明确 Harbor 才是 Terminal-Bench 2.0 官方 harness，支持安装包 `harbor` 与
`harbor run --dataset terminal-bench@2.0`。旧 `terminal_bench.Harness` 不是同一协议：
其结果是 `results.json`/`run_metadata.json`；Harbor 是新的 Trial/AgentContext/
VerifierResult schema，任务用 `instruction.md`、`task.toml`、`environment/`、
`tests/test.sh`，verifier 写 `/logs/verifier/reward.txt` 或 `reward.json`。
[自定义 Agent](https://docs.harborframework.com/agents/custom-agents)、
[任务格式](https://docs.harborframework.com/tasks/overview)、
[TrialResult 源码](https://github.com/harbor-framework/harbor/blob/4b94505a91c5ddcb70b5740ac95718ddee13e5a0/src/harbor/models/trial/result.py)。

若目标明确是 TB2.0，就新建一个版本化评测 adapter，并审核门禁契约；不能只把
现有 `scorer_name` 改成 Harbor、把数据集叫 v2 或改 package 版本来制造兼容。
Harbor 和 [Terminal-Bench 2](https://github.com/harbor-framework/terminal-bench-2) 均标
Apache-2.0；任务所装第三方软件仍有各自许可。
[Harbor LICENSE](https://github.com/harbor-framework/harbor/blob/4b94505a91c5ddcb70b5740ac95718ddee13e5a0/LICENSE)。

SWE-bench 官方预测只需 `instance_id`、`model_name_or_path`、`model_patch`，本仓库
`eval/benchmarks/swebench_predictions.py` 已提供该 adapter，无需搬整套 SWE-agent。
官方 [MIT 源码](https://github.com/SWE-bench/SWE-bench/tree/02e7a74ffd0b707aab73d203fe87bdc7c76afc8e)
和 [评分指南](https://www.swebench.com/SWE-bench/guides/evaluation/)可直接复用。
但 [当前 README](https://github.com/SWE-bench/SWE-bench/blob/02e7a74ffd0b707aab73d203fe87bdc7c76afc8e/README.md)
已描述 v5 CLI/task-repo 构建及 `logs/evaluation/<run_id>/results.json`；旧
`python -m swebench.harness.run_evaluation` 仍兼容，本仓库却还读取
`logs/run_evaluation/.../report.json`。应固定经过验证的 scorer 版本，升级时定向验证
输出采集，避免直接安装 latest 后默认为旧收据路径可用。

官方本地评分依赖 Docker；官方资源建议为 x86_64、120GB 空间、16GB RAM、8 CPU。
Windows 优先复用现有 WSL/Linux 路径及共享 Docker readiness helper，不新增探针或
删除容器/volume。云评分可作资源不足时的备选，不是最快默认路径。
[官方 README](https://github.com/SWE-bench/SWE-bench/blob/02e7a74ffd0b707aab73d203fe87bdc7c76afc8e/README.md)。

这些轮子提供执行器和判题器，不提供本产品已通过的证明。安装成功、oracle 成功、
第三方 Codex/pi 成功或已有旧 SHA 分数都不能解除发布 `BLOCKED`；仍按
[本仓库门禁](release-gate.md)收集当前 source pin、空 dirty hash、trace/backend readback、
run ID、所有失败、artifact SHA-256 和锁定分母。

## 最快推进顺序

1. 配置现有 local provider 或有效云端 provider，准备固定 digest 的代码任务镜像；
   跑少量真实代码修改/测试、反思写回、重启召回与 trace readback。
2. 用已有 Playwright 跑真实入口，加 Toxiproxy 与现有进程控制补 reconnect/crash/Git
   恢复；通过小矩阵后，再做完整 200 轮与规模故障分母。
3. 以 Go adapter 接 Qdrant 派生索引和固定 embedding 模型，验证隔离、语义质量及
   token 对照；沿用 LangGraph 与现有 Skills catalog，不重建整套 Agent 平台。
4. 将官方评测的被测 adapter 接到产品 Go 入口，按目标版本使用旧 TB/Harbor 与
   pinned SWE-bench；采集本产品的当前源码、trace、原始判题与完整失败收据。

本次仅完成源码/文档/许可调查，未部署组件、未改生产逻辑、未运行上述产品验收。
