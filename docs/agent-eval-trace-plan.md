# Agent Loop + Eval + Trace 改造计划

> 对标 Claude Code 的本地 Code Agent（Go Harness + Python LangGraph 双层架构）的可观测性与评测基础设施规划。
> 编写日期：2026-07-27。基准信息基于 2026-07 的 Web 调研；部分 SOTA 分数为厂商自报，已在正文中标注"待核实"，引用前请到官方榜单二次核对。

---

## TL;DR

- **Agent loop**：✅ 已有（[orchestrator/runtime/conversation.py:run()](../orchestrator/runtime/conversation.py) 的 `for turn in range(1, max_tool_rounds+1)` ReAct 式工具循环 + Go harness 外层循环）。
- **Eval（评测）**：❌ 完全缺失——无基准接入、无 harness、无打分。
- **Trace（可观测）**：❌ 缺失 OTel/span 导出；✅ 但已有结构化事件流（`OrchestratorMessage` oneof + `ToolProgress` + `SessionMeta`）和 metrics（token/成本/缓存），是 trace 的天然数据源。

**最小可行路径（性价比排序）**：

1. **Trace 先行**：用 OpenTelemetry GenAI 语义约定（`gen_ai.*`）把**已有的** gRPC 事件流 + LLM 调用埋成 span，Go↔Python 用 W3C TraceContext 串联，落到本地 **Arize Phoenix**。低投入、高回报（每次实验都有可视化 trace）。
2. **秒级回归**：EvalPlus (HumanEval+/MBPP+) —— 纯 Python、零 Docker，CI 防退化。
3. **agent loop 日常迭代**：Aider polyglot（225 题/6 语言/两轮评测）+ τ²-bench（tool-use 主力）。
4. **硬通货门面**：SWE-bench Verified —— 先跑 100 题分层抽样，写一个薄 adapter 产 `predictions.jsonl` 交官方 harness。
5. **差异化叙事**：Terminal-Bench 2.x（讲"harness 工程价值"——开源 agent 靠 harness 在同模型上赢 Claude Code 的故事）。

---

## 一、现状评估

### 1.1 Agent Loop —— 已具备

主循环在 [orchestrator/runtime/conversation.py](../orchestrator/runtime/conversation.py) 的 `ConversationRunner.run()`：

```text
Go Harness 外层循环（internal/cli/app.go Run）
  └─ 每个用户输入 → gRPC Converse 双向流
      └─ Python ConversationRunner.run()（内层循环）
          for turn in 1..max_tool_rounds:
              1. 检查 cancel_event / 预算
              2. 复杂度路由（fast↔main）
              3. 流式调用 LLM（_stream_chat，逐块 yield TextChunk）
              4. 若有 tool_calls → 批量/串行执行（ToolRequestBatch 并行扇出）
              5. 结果回灌 → 下一轮，直到 LLM 不再请求工具
```

这是标准的 **ReAct + plan-execute** 混合循环，已支持：流式、并行工具、中断、崩溃恢复、预算控制、思考模式、多模型路由。**Loop 本身无需重写**，本计划聚焦 eval 与 trace。

### 1.2 Eval —— 缺失

仓库无任何评测代码：无基准数据集、无 harness adapter、无打分脚本、无 `predictions.jsonl` 产出。`tests/` 是单元/集成测试，不是能力评测。

### 1.3 Trace —— 缺失导出层，但事件流已就绪

| 已有 | 位置 | 可映射为 |
|---|---|---|
| `OrchestratorMessage` oneof（TextChunk / ToolRequest / ToolRequestBatch / ToolResult / SessionMeta / PlanUpdate / TodoUpdate / AgentSpawn / AskUserRequest / Done） | [proto/codeagent/orchestrator.proto](../proto/codeagent/orchestrator.proto) | span 事件 / 子 span |
| `ToolProgress`（start/finish, index/total, exit_code） | [internal/orchestrator/client.go](../internal/orchestrator/client.go) | `gen_ai.execute_tool` span |
| `SessionMeta`（turn, tokens_in/out, cached_tokens, cost, model） | proto + [internal/cli/app.go](../internal/cli/app.go) handleOrchestratorEvent | `gen_ai.usage.*` span 属性 |
| LLM 调用点（含 thinking/cache/streaming） | [orchestrator/runtime/conversation.py](../orchestrator/runtime/conversation.py) `_stream_chat` / `_chat` | `gen_ai.inference.client` span |
| 子 Agent spawn | `AgentSpawn` 事件 + [orchestrator/agents/deep_agent.py](../orchestrator/agents/deep_agent.py) | `gen_ai.invoke_agent` 子 span |
| metrics（token/成本/缓存/工具调用/轮次/错误） | [internal/metrics/collector.go](../internal/metrics/collector.go) | span metrics / 聚合 |

**结论**：事件流已基本覆盖 agent loop 的所有关键节点，缺的只是"把这些事件转成标准 OTel span 并导出到可观测后端"的薄层。

---

## 二、Trace 改造方案

### 2.1 目标 span 模型（OpenTelemetry GenAI semantic conventions）

采用 [open-telemetry/semantic-conventions-genai](https://github.com/open-telemetry/semantic-conventions-genai) 的 `gen_ai.*` 约定（2025-2026 已从主 semconv 仓库迁出到独立仓库，规划以新仓库 `model/` YAML 为准）。一次用户输入的 trace 树：

```text
gen_ai.invoke_agent           (Go: 一个用户输入/turn 的根 span)
  ├─ gen_ai.plan.internal     (可选：plan_mode 时)
  ├─ gen_ai.inference.client  (Python: 一次 LLM 调用，含 streaming/thinking)
  │    attr: gen_ai.request.model, gen_ai.usage.input_tokens,
  │          gen_ai.usage.output_tokens, gen_ai.usage.cache_read.input_tokens,
  │          gen_ai.usage.reasoning.output_tokens (thinking), gen_ai.provider.name
  │    event: gen_ai.input.messages / gen_ai.output.messages (opt-in, PII 注意)
  ├─ gen_ai.execute_tool      (Go: executor.Execute，每个工具一个)
  │    attr: gen_ai.tool.name, gen_ai.tool.call.id, gen_ai.tool.type,
  │          gen_ai.tool.call.arguments (截断), gen_ai.tool.call.result (截断)
  ├─ gen_ai.execute_tool ...
  └─ (循环：下一轮 inference + tools)
gen_ai.invoke_agent           (子 Agent：AgentSpawn → 独立 invoke_agent 子树)
```

**关键属性命名更新（注意）**：`gen_ai.system` → `gen_ai.provider.name`；`prompt_tokens`/`completion_tokens` → `gen_ai.usage.input_tokens`/`output_tokens`。约定整体仍为 **Development（实验性）**，推荐 **dual-emit**（同时发 legacy + 新键），用 `OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental` 切换。

### 2.2 双语言埋点

**Go Harness 侧**（自建轻量 OTel 包装）：
- 官方**没有** `go.opentelemetry.io/otel/semconv/genai` 包，需手写 `gen_ai.*` 常量。参考 [docker/docker-agent 的 `pkg/telemetry/genai`](https://pkg.go.dev/github.com/docker/docker-agent/pkg/telemetry/genai)（实现了 ChatSpan/EmbeddingSpan/RetrievalSpan/SandboxSpan + dual-emit + 内容捕获 + metrics）。
- 埋点位置：
  - [internal/cli/app.go](../internal/cli/app.go) `handleUserInput` 起始处开 `gen_ai.invoke_agent` 根 span；
  - [internal/orchestrator/client.go](../internal/orchestrator/client.go) 收到 `ToolProgress(start)` 开 `gen_ai.execute_tool` 子 span，`(finish)` 时结束并填 `gen_ai.tool.*`；
  - `SessionMeta` 事件到达时，把 token/cost/cache 写入当前 inference span 属性。
- 依赖：`go.opentelemetry.io/otel` + OTLP/HTTP exporter。

**Python Orchestrator 侧**（自动埋点为主）：
- 用 [OpenLLMetry (Traceloop)](https://github.com/traceloop/openllmetry) 或 [OpenInference](https://github.com/Arize-ai/openinference) 自动 instrument LLM 调用（二者都覆盖 LangGraph/LiteLLM/OpenAI/Anthropic）。
- 在 [orchestrator/runtime/conversation.py](../orchestrator/runtime/conversation.py) `_stream_chat` 手动补 `gen_ai.inference.client` span（若自动埋点不足），把 `response.usage`（含 `cached_input_tokens`、thinking tokens）写为 `gen_ai.usage.*`。
- 依赖：`opentelemetry-sdk` + `opentelemetry-exporter-otlp` + openllmetry/openinference。

**跨进程串联（关键）**：Go harness 创建根 trace，启动 Python orchestrator 子进程时注入 **W3C TraceContext**（`traceparent` 环境变量，参考 docker-agent 的 `InjectTraceContextEnv`），Python 侧从环境恢复 context，使 Go 的 `invoke_agent` span 与 LangGraph 的 inference 子 span 落到**同一棵 trace 树**。

### 2.3 后端：本地 Phoenix 起步

- **首选 [Arize Phoenix](https://github.com/Arize-ai/phoenix)**：`pip install arize-phoenix && phoenix serve`，零外部数据库依赖（内嵌存储），端口 6006（UI + OTLP/HTTP）。2026-05 起自动把标准 `gen_ai.*` span 转 OpenInference 视图渲染。许可为 Elastic License 2.0（source available，个人/内部使用无碍）。
- **增强项 [Langfuse](https://github.com/langfuse/langfuse)**（MIT，自托管）：trace + eval + prompt 管理一体，但需维护 Postgres + ClickHouse + Redis。当 Phoenix 不够用（要多用户/内置 eval/prompt 管理）时切换。
- **排除 LangSmith**：闭源 SaaS 为主，自托管为企业版，与"本地优先、数据不出机器"理念不契合。

### 2.4 工作量与里程碑

| 任务 | 文件 | 估算 |
|---|---|---|
| Go OTel 包装层（`internal/telemetry/genai/`）+ 根 span + 工具 span | 新包 + app.go/client.go 接线 | 2-3 天 |
| Python OpenLLMetry 接入 + inference span + usage 属性 | server.py/conversation.py | 1-2 天 |
| W3C TraceContext 跨进程传播 | process.go（注入 env）+ server.py（恢复） | 1 天 |
| Phoenix docker-compose + 文档 | docker-compose.yml + docs | 0.5 天 |
| **小计** | | **~1 周** |

---

## 三、Eval 改造方案

### 3.1 总体架构：薄 adapter + 官方 harness

```text
eval/
├── adapter.py          # 统一接口：solve_instance(instance) -> {patch|answer, trace_id}
├── driver_headless.py  # 无 TUI 驱动：构造 ConversationRunner，喂 issue，收 git diff
├── benchmarks/
│   ├── swebench.py     # SWE-bench: checkout base_commit → run agent → 捕获 diff → predictions.jsonl
│   ├── evalplus.py     # HumanEval+/MBPP+: 函数级，调 LLM 生成 + EvalPlus 判题
│   ├── aider_polyglot.py
│   ├── tau2bench.py
│   └── terminalbench.py
├── run.py              # 并发/断点续跑/采样/汇总
└── scoring/            # 调用各基准官方评分脚本
```

**核心抽象**（借鉴 [OpenHands](https://github.com/All-Hands-Agent/OpenHands) 的 `evaluation/` 范式：注册一个 `agent_cls` 即可同跑多基准）：

```python
class AgentAdapter:
    def solve_instance(self, instance: dict) -> dict:
        """输入 task instance，输出 {instance_id, model_patch|answer, trace_id, cost}."""
```

### 3.2 SWE-bench adapter（最高优先级）

**为何是门面**：行业绝对标杆，[swebench.com](https://www.swebench.com) Verified 子集是几乎所有头部模型/agent 发布时主推的数字。

**接入方式**（参考 [SWE-bench 官方 harness](https://github.com/princeton-nlp/SWE-bench)）：
1. `solve_instance`：在容器外的工作目录 `git clone` 目标仓库 → `git checkout <base_commit>` → 把 issue 文本作为 user_text 喂给 `ConversationRunner`（headless，不走 TUI）→ agent 跑完工具循环 → `git diff` 即 `model_patch`。
2. 写 `predictions.jsonl`（每行 `{instance_id, model_name_or_path, model_patch}`）。
3. `python -m swebench.harness.run_evaluation --dataset_name princeton-nlp/SWE-bench_Verified --predictions_path ... --max_workers N --run_id ...` → 官方 harness 在 Docker 内 apply patch + 跑测试 + 判 FAIL_TO_PASS/PASS_TO_PASS → 出 `evaluation_results/`。
4. **打分**：Resolution Rate（确定性，基于真实测试执行，无需 LLM judge）。

**官方 harness 无自动重试**（失败即记日志跳过）；想要自修复，在 `solve_instance` 内把错误输出喂回 LLM（[Aider](https://github.com/Aider-AI/aider) `benchmark.py` 的做法）。

**切入点**：先跑 **100 题分层抽样**（覆盖 12 个仓库，[vexp-swe-bench](https://github.com/Vexp-ai/vexp-swe-bench) 是现成的统一对照基线），资源充裕再上全 500 题。硬件：x86_64、≥120GB 磁盘、16GB RAM、8 核；单机 100 题数小时级。

**当前分数段（2026，多为 leaderboard 自报，待核实）**：Simon Willison 统一 prompt 榜头部约 Claude 4.5 Opus 76.8% / Gemini 3 Flash 75.8% / GPT-5.2 72.8%；开源 agent（统一用 Opus 4.5 的 100 题抽样）OpenHands ~70%、vexp+Claude Code 73%。**你的 agent 的第一个数字就放在这个语境里讲。**

### 3.3 分阶段接入顺序

| 阶段 | 基准 | 测什么 | 成本 | 简历价值 |
|---|---|---|---|---|
| **P0 秒级回归** | [EvalPlus](https://evalplus.github.io/leaderboard.html) HumanEval+/MBPP+ | 函数级算法（测试增强 80x/35x） | 极低（零 Docker，秒级） | CI 防退化，非主卖点（已饱和 90%+） |
| **P1 日常迭代主力** | [Aider polyglot](https://aider.chat/docs/leaderboards/) | 225 题/6 语言/两轮评测，天然匹配 agent loop | 低（零 Docker，pip 装） | 中——"Pass rate 2 + 成本"消融数据 |
| **P2 tool-use 主力** | [τ²-bench](https://taubench.com) retail/airline | 多轮结构化 API + 政策遵从；Passᵏ 测一致性 | 低（纯 Python+JSON，零沙箱） | 高——Anthropic 模型卡反复引用 |
| **P3 硬通货门面** | [SWE-bench Verified](https://www.swebench.com/verified.html)（100 题抽样起步） | 仓库级真实 issue 补丁 | 高（Docker，120GB 磁盘） | **最高**——行业对标口径 |
| **P4 差异化叙事** | [Terminal-Bench 2.x](https://www.tbench.ai/) | 终端长任务 | 中（Docker，89 题） | 高——CLI agent 直接能力证明 |
| **P5 防污染对照** | [LiveCodeBench](https://livecodebench.github.io/) | 竞赛算法，按时间切分防污染 | 中（无 Docker，需沙箱） | 中——算法能力 + 防污染叙事 |

**可选远期**（含金量高但赛道错位或门槛重）：
- [WebArena](https://github.com/web-arena-x/webarena)：Claude Code+GBOX MCP 在榜 68.0%，可对标；但 6 容器 ~220GB 磁盘，门槛最重。
- [AppWorld](https://github.com/StonyBrookNLP/appworld)：长 horizon 多 API + 代码生成，区分度高。
- [GAIA](https://huggingface.co/spaces/gaia-benchmark/leaderboard) validation（166 题有答案）：综合能力天花板，但大量搜索/多模态题，与 code agent 相关性偏低。
- [OSWorld](https://github.com/xlang-ai/OSWorld)：computer-use 事实标准，但 GUI 像素操作而非 CLI，赛道错位；建议只引用榜单不全套复跑。

### 3.4 工作量与里程碑

| 任务 | 估算 |
|---|---|
| `eval/` 骨架 + `AgentAdapter` + headless driver（复用 ConversationRunner） | 2-3 天 |
| EvalPlus 接入（最简，验证 adapter） | 1 天 |
| SWE-bench adapter + 官方 harness 联调（100 题抽样） | 3-5 天 |
| Aider polyglot / τ²-bench 接入 | 各 2-3 天 |
| Terminal-Bench 接入 | 3-4 天 |
| **小计（P0-P3 最小集）** | **~2 周** |

---

## 四、秋招简历价值（"含金量"拆解）

招聘方在 agent 岗位上看重的三个信号，本计划恰好全覆盖：

1. **一个可对标的分数**：SWE-bench Verified 100 题抽样的 Resolution Rate，放在"统一用某模型"的对照语境里（如 vexp-swe-bench）。即使数字不顶尖，**"自建 harness 跑通 SWE-bench Verified"本身就是工程能力证明**。
2. **harness 工程深度**：Terminal-Bench 2.1 上开源 agent Backboard R-CLI 用同款 Opus 4.8 拿 84.3% 击败 Claude Code (Opus 4.8) 78.9%——**"同一模型靠 harness 工程赢 5 个百分点"** 是可直接对标的故事。你的双语言架构（Go 执行面 + Python 编排面 + 流式/中断/崩溃恢复/并行工具）本身就是 harness 工程的体现。
3. **可观测性成熟度**：一张 Phoenix trace 截图（完整 agent loop：inference → tool → inference，带 token/cost/cache/thinking 属性）比任何文字描述都有说服力，证明你理解生产级 agent 的调试与优化。

**一句话简历条目**（示例）：
> 自建 Go+Python 双层本地 Code Agent（对标 Claude Code，对流式/中断/崩溃恢复/并行工具/思考/缓存），用 OpenTelemetry GenAI 约定实现跨语言 trace（Phoenix 可视化），并接入 SWE-bench Verified / Terminal-Bench / τ²-bench 评测，Verified 100 题抽样 Resolution Rate 达 X%。

---

## 五、推荐执行顺序（依赖感知）

```text
第 0 步（先于一切）：Trace 基础设施
  └─ OTel GenAI 埋点 + W3C TraceContext + 本地 Phoenix
     （此后每次 eval 都自动产出可可视化 trace——本身即作品集亮点）

第 1 步：EvalPlus（秒级回归，验证 eval/ 骨架与 adapter）
第 2 步：SWE-bench Verified 100 题抽样（硬通货门面，trace 已就绪可记录每题 step/tool/cost）
第 3 步：Aider polyglot + τ²-bench（日常迭代主力，产消融数据）
第 4 步：Terminal-Bench（差异化叙事）
第 5 步（可选）：WebArena / AppWorld / GAIA / LiveCodeBench

trace 后端升级：Phoenix → Langfuse（当需要 prompt 管理/多用户/内置 eval 时）
始终排除：LangSmith（闭源 SaaS，不符本地优先）
```

---

## 六、风险与取舍

| 风险 | 取舍 |
|---|---|
| OTel GenAI semconv 仍为实验性、属性命名在演进 | dual-emit（legacy + 新键），用环境变量切换；trace 后端选自动转换的 Phoenix |
| Go 无官方 genai semconv 包 | 自建薄包装（参考 docker-agent），可控且轻 |
| SWE-bench 全量成本高（磁盘/时间） | 先 100 题分层抽样；云上可用 Modal/AWS（sb-cli） |
| 官方 harness 无重试 | 在 adapter 层实现自修复（错误输出喂回 LLM） |
| Trace 内容含 PII（prompt/completion） | 内容属性默认 opt-in 关闭，仅本地开启；用 span events 承载 |
| 部分 2026 SOTA 数字为厂商自报 | 引用前到 swebench.com / tbench.ai 官方榜单二次核对 |

---

## 七、参考基准速查表

| 基准 | 类型 | 题量 | 打分 | Docker | 防污染 | 接入成本 | 2026 SOTA 段（待核实） |
|---|---|---|---|---|---|---|---|
| **SWE-bench Verified** | 仓库级 issue 补丁 | 500 | Resolution Rate | 必须 | 弱（冻结） | 高 | 头部 76-88%（已近饱和） |
| SWE-bench Lite | 同上（简单子集） | 300 | 同上 | 必须 | 弱 | 中 | 偏高 |
| SWE-bench Pro | 长程多文件 PR | 1865 | Resolution Rate | 必须 | 中 | 极高 | 头部 60-80%（区分度最大） |
| SWE-bench Live | 防污染+多语言+多 OS | 743+61 | Resolution Rate | 必须（双 OS） | **强** | 高 | 上升中 |
| **Terminal-Bench 2.x** | 终端长任务 | 89 | resolution rate | 必须 | canary+容器 | 中 | harness 组合 ~85% |
| **Aider polyglot** | 6 语言 Exercism | 225 | Pass rate 2 | 否 | — | 低 | gpt-5 88% |
| **τ²-bench** | tool-use+政策 | retail114/airline50 | Passᵏ | 否 | — | 低 | Anthropic 反复引用 |
| **EvalPlus** HumanEval+/MBPP+ | 函数级算法 | 164/378 | pass@1 | 否 | 测试增强 | 极低 | 饱和 90%+ |
| LiveCodeBench | 竞赛算法 holistic | ~1055(v6) | pass@1 | 否（需沙箱） | **时间切分** | 中 | Gemini 3 Pro ~91% |
| BigCodeBench | 工程化函数级 | 1140 | pass@1 | 否（多库） | 未明确 | 中 | 待核实 |
| WebArena | 真实网站 agent | 812 | 确定性(verified) | 必须（6 容器） | 容器隔离 | 极高 | DeepSeek v3.2 74%，Claude Code 68% |
| AppWorld | 长 horizon API | — | TGC/SGC | 可同进程 | — | 中 | 1.8→86.9 |
| GAIA | 综合能力 | 466 | 准精确匹配 | 否 | — | 中 | HAL+Sonnet4.5 74.6% |
| OSWorld | 桌面 GUI | 369 | 程序化判定 | 必须（VM） | — | 极高 | computer-use 标杆 |

> 名称核实：原口述的"CoreBench"对应的真实基准是 **CORE-Bench**（科研复现性，非污染检测）；防污染+HumanEval/MBPP 方向应选 **LiveCodeBench + LBPP**。"OSWorld-Gaia"未找到，真实存在的是 **OSWorld-G**（G=Grounding，与 GAIA 是两个独立基准）。

---

## 八、关键链接

**代码 Agent 基准**
- SWE-bench：官网 https://www.swebench.com ｜ 论文 https://arxiv.org/abs/2310.06770 ｜ 仓库 https://github.com/SWE-bench/SWE-bench ｜ Verified 数据 https://huggingface.co/datasets/SWE-bench/SWE-bench_Verified ｜ 100 题对照 https://github.com/Vexp-ai/vexp-swe-bench ｜ Live https://swe-bench-live.github.io/ ｜ Pro 论文 https://arxiv.org/abs/2509.16941
- Terminal-Bench：https://www.tbench.ai/
- Aider polyglot：https://aider.chat/docs/leaderboards/
- EvalPlus：https://evalplus.github.io/leaderboard.html ｜ LiveCodeBench https://livecodebench.github.io/ ｜ BigCodeBench https://bigcode-bench.github.io/

**通用 Agent 基准**
- τ²-bench：https://taubench.com ｜ https://github.com/sierra-research/tau2-bench
- WebArena：https://github.com/web-arena-x/webarena
- AppWorld：https://github.com/StonyBrookNLP/appworld
- GAIA：https://huggingface.co/spaces/gaia-benchmark/leaderboard
- AgentBench：https://github.com/THUDM/AgentBench

**Trace / 可观测性**
- OTel GenAI semconv（权威新仓库）：https://github.com/open-telemetry/semantic-conventions-genai ｜ span 模型 https://github.com/open-telemetry/semantic-conventions-genai/tree/main/model/gen-ai
- Go 参考实现：https://pkg.go.dev/github.com/docker/docker-agent/pkg/telemetry/genai
- Arize Phoenix：https://github.com/Arize-ai/phoenix
- Langfuse：https://github.com/langfuse/langfuse
- OpenLLMetry：https://github.com/traceloop/openllmetry
- OpenInference：https://github.com/Arize-ai/openinference

**Eval Harness**
- SWE-bench harness：https://github.com/SWE-bench/SWE-bench
- OpenHands eval：https://github.com/All-Hands-Agent/OpenHands （`evaluation/`）
- Aider benchmark：https://github.com/Aider-AI/aider （`benchmark.py`）
