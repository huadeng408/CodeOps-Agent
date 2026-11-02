# Agent 执行效率：五家公司官方技术报告与项目采用建议

调研日期：2026-11-02。覆盖 OpenAI、Anthropic／Claude Code、智谱 GLM、Google／Google DeepMind、DeepSeek。材料包括官方工程文章、团队技术论文和官方协议文档；发布日期与当前文档的查阅日期分别标注。本报告聚焦工程执行效率，补充此前的[Agent 评测调研](agent-evaluation-landscape-2026-10-31.md)。

**建议优先验证程序化工具编排、工具检索与 schema 按需加载，再评估旧工具观察的选择性折叠。** 稳定前缀、输出落盘、压缩和独立评分在本项目已有基础，先补观测和实际使用。保留当前 grok-4.6、xhigh、thinking 开启以及同模型内部对照的约定。

这里区分三种证据：厂商在特定设置下报告的结果、可移植的工程机制、本项目的采用判断。厂商数字不等于本项目收益，训练或推理集群优化也不等于应用层加一个模块就能获得的效率。本轮没有运行新的模型实验或查询账单。

## 1. 官方材料及其实际说明了什么

### OpenAI：减少 agent loop 内的重复工作

| 官方材料 | 具体机制 | 证据边界 |
| --- | --- | --- |
| [Unrolling the Codex agent loop](https://openai.com/index/unrolling-the-codex-agent-loop/)，2026-01-23，Performance considerations | 保持精确相同的提示前缀；工具顺序稳定；环境变化追加为新消息；到阈值后压缩历史 | 文中具体讨论缓存被破坏的原因，包括工具枚举不稳定。描述的是当时的 Codex／Responses API，不能据此认定 BeeAPI 的缓存协议或实际命中率 |
| [Harness engineering](https://openai.com/index/harness-engineering/)，2026-02-11，application legibility／repository knowledge／architecture | 让 Agent 能查询 UI、日志、指标；短入口指向分层知识；自定义 lint 和结构测试直接反馈修复办法 | 团队经验报告，文中的工期收益是作者估计。其合并门禁取舍不能覆盖本项目已经确认的验收要求 |
| [How GPT-5.6 fuses frontier intelligence with frontier efficiency](https://openai.com/index/gpt-5-6-frontier-intelligence-efficiency/)，2026-07-29，agentic harness 部分 | 按需发现工具、Skill 与插件；限制单工具输出，文中默认 10,000 tokens；会话追加、工具排序稳定，审批策略在运行时执行 | 工具发现、上下文和前缀设计可借鉴；GPU kernel、推理集群调度及 speculative decoding 属于模型服务端，不能算作本项目 Harness 改造收益 |

采用判断：把常见排障变成工具可直接执行的检查与明确错误信息；让 Agent 获取有定位的结果，减少反复探索。静态前缀和工具目录要测实际变化，不能只在配置里写“启用缓存”。以上分别对应官方文章的机制，尚无本项目加速率。

### Anthropic／Claude Code：工具发现、程序化调用与跨窗口交接

| 官方材料 | 原始数据或机制 | 应怎样使用这个结论 |
| --- | --- | --- |
| [Introducing advanced tool use](https://www.anthropic.com/research/advanced-tool-use)，2025-11-24，Tool Search／Programmatic Tool Calling | 大工具库设置中，官方称工具上下文 token 用量减少 **85%**；复杂研究任务的程序化调用平均 **43,588→27,297 tokens，减少37%** | 85% 与37%的设置和分母不同，均不是本项目编码耗时的降幅。按需发现会增加检索步骤，小而紧凑的工具库可能不划算 |
| [Code execution with MCP](https://www.anthropic.com/engineering/code-execution-with-mcp)，2025-11-04，progressive disclosure／context-efficient results | 在执行环境中串接工具、过滤中间数据，仅让必要结果进入模型；文中 **150,000→2,000 tokens，98.7%** 属于按需工具定义加载示例 | 不能写成全任务提速98.7%。可移植的是工具接口渐进发现和结果在代码侧处理；代码执行、工具授权和错误处理都有额外成本 |
| [Effective harnesses for long-running agents](https://www.anthropic.com/research/effective-harnesses-for-long-running-agents)，2025-11-26 | initializer 准备环境与功能清单；后续会话增量推进，读取交接状态，检查环境并做端到端验证 | 没有公布受控的耗时或 token 降幅。对本项目应从 Ledger 派生交接视图，保留单一事实源 |

程序化工具调用最相关：例如多个独立源码读取、检索、JSON 汇总，交给一个有界程序协调，把有限结果返回模型。它减少的是反复“工具结果→模型→下一工具”的往返；底层工具请求和授权仍逐项发生。当前计算机操作中的批量工具调用已经具备这种能力，应先用好；产品侧采用时仍由 Go 授权和审计。

### 智谱 GLM：有同模型的上下文管理消融

**GLM-5 的这项实验最接近可直接复现的 Harness 变量。** [团队技术报告 v1](https://arxiv.org/html/2602.15763v1)发布于2026-02-17，§4.2.4、图8报告：只折叠早于最近 `k` 轮的工具观察，实验取 `k=5`；同一 GLM-5 的 BrowseComp 成绩从 **55.3%→62.0%，提高6.7个百分点**。它保留问题、动作和推理序列，处理的是旧 observation 内容，并非只保留五条消息。该节没有给这两个点的完整耗时和 token 明细。

其后再组合 Discard-all、选择32K阈值，并改变计算预算得到的75.9，不能拿来减去55.3当作同预算单变量收益。搜索任务的策略也不能直接套到有文件副作用、权限和恢复责任的代码任务。[GLM-5 §4.2.4](https://arxiv.org/html/2602.15763v1)

[GLM-4.7 官方模型卡](https://huggingface.co/zai-org/GLM-4.7/blob/main/README.md)与[Thinking Mode 文档](https://docs.z.ai/guides/capabilities/thinking-mode)提供 preserved thinking 契约：相关模式要求历史 reasoning 字段完整、原序返回。它属于具体 Provider 能力，公开材料没有单独给出该功能的效率消融，不能给 Grok 请求直接添加 GLM 参数。

采用判断：优先测试“旧只读工具观察折叠＋原始引用按需重取”。折叠模型视图时，原始 Ledger、工具配对、未决副作用、权限和当前约束保留；`k` 与阈值由项目实验决定。这个建议是在现有压缩机制上的增量，不能称为新建 Memory 或另一个 checkpoint 系统。

### Google／DeepMind：并行是否划算，以及验证如何分层

| 官方材料 | 机制与结果 | 限制 |
| --- | --- | --- |
| [Towards a Science of Scaling Agent Systems](https://arxiv.org/html/2512.08296v1)，v1 2025-12-09；[Google Research 解读](https://research.google/blog/towards-a-science-of-scaling-agent-systems-when-and-why-agent-systems-work/)，2026-01-28 | 五种架构、三模型家族、180配置；控制总 reasoning-token 预算和工具访问。PlanCraft 中多 Agent 相对单 Agent 成功率下降 **39–70%**；可并行财务任务有明显正收益 | 四个基准是 Finance-Agent、BrowseComp-Plus、PlanCraft、WorkBench，非软件修复专项。下降指相对成功率，不能改写成编码墙钟变慢，也不能推出通用 Agent 数量上限 |
| [ADK 的生产上下文架构](https://developers.googleblog.com/architecting-efficient-context-aware-multi-agent-framework-for-production/)，2025-12-04；[压缩](https://adk.dev/context/compaction/)／[缓存](https://adk.dev/context/caching/)文档，查阅2026-11-02 | 持久 Session 与每次 Working Context 分开，按处理顺序编译模型请求；稳定前缀与变化后缀分开，缓存用真实 usage 检查 | 架构说明没有端到端加速倍率。无需迁移到 ADK 才能采用投影思路；Gemini 的缓存对象与 BeeAPI 能力不能混同 |
| [AlphaEvolve](https://arxiv.org/html/2506.13131v1)，v1 2025-06-16，§2.4／§2.6；[DeepMind 公告](https://deepmind.google/blog/alphaevolve-a-gemini-powered-coding-agent-for-designing-advanced-algorithms/)，2025-05-14 | 可执行 evaluator；按成本和难度递增的级联检查，提前淘汰错误候选；异步协调采样与评估 | 主要面向可机器判分的算法发现。流水线吞吐提高不等于每项计算延迟降低；特定算法优化成果也不是通用 Agent 加速率 |

采用判断：独立查证和互不重叠的审查并行；依赖前一步环境状态的操作由一个控制者推进。验证先走便宜且能捕获当前错误的边界，再做昂贵检查；不能因“提前止损”丢掉失败、unknown 和未运行分母。共享宿主的重构建、完整回归与正式耗时测量需安排资源。

### DeepSeek：模型、思考模式和 Harness 必须一起看

[DeepSeek-V3.2 技术报告 v1](https://arxiv.org/html/2512.02556v1)发布于2025-12-02。§3.2.1讨论工具交互中的思考历史；§4.1与附录C明确区分框架：Terminal Bench 2.0 的46.4使用 Claude Code，因为其 thinking 上下文管理与 Terminus 不兼容；同一 Claude Code 设置下的 non-thinking 为37.1。**这说明模式与框架兼容性重要，不是某个编排库的纯加速消融。**

报告也指出，重复自检可造成过长轨迹和窗口溢出；工具输出使用 `tool` 角色，而非伪装为新的用户轮。DSA、训练环境合成和 RL 收益属于模型／训练层。官方[模型卡](https://huggingface.co/deepseek-ai/DeepSeek-V3.2/blob/main/README.md)另提醒，参考 parser 主要处理良构字符串，不能替代生产异常处理。[技术报告 §3.2、§4.1及附录C](https://arxiv.org/html/2512.02556v1)

[Context Caching on Disk 公告](https://api-docs.deepseek.com/news/news0802/)发布于2024-08-02；[当前缓存指南](https://api-docs.deepseek.com/guides/kv_cache/)查阅于2026-11-02。其可借鉴点是前缀复用与服务端命中观测。它主要减少重复 prefill，不能直接缩短 Go 编译、pytest 或长输出解码。本文没有采纳历史价格或把该服务的缓存字段当成 BeeAPI 已支持的事实。

采用判断：保留真实 call/result 配对，按实际 Provider 契约传递受支持的历史块；记录无新增证据的重复自检和重复读取。协议、模式和 Harness 作为完整运行输入冻结，不能只写一个模型名称。

## 2. 对本项目，哪些是现有能力，哪些才是增量

以下来自当前工作树的源码核对，表示实现路径存在，不表示已经测得效率收益。

| 机制 | 当前情况 | 本次建议 |
| --- | --- | --- |
| 工具排序与权限过滤 | [tools.py](../../orchestrator/runtime/tools.py)已有 allow-list 与稳定排序；`openai_schemas()`仍返回全部已注册完整 schema，[conversation.py](../../orchestrator/runtime/conversation.py)每轮发送 | **明确的增量候选：工具检索／schema 选择。** 先测实际 schema 占用和额外检索往返；目录小时保留直接加载 |
| Skill 渐进加载 | [SkillManager](../../orchestrator/skills/manager.py)已有正文懒加载 | 复用；工具 schema 的按需发现是另一处边界 |
| 前缀／缓存 | [system.py](../../orchestrator/prompts/system.py)有稳定与动态分段，Provider 有部分缓存字段处理 | 增加分段指纹、真实缓存命中与 TTFT 观测；未提供的字段保留 unknown |
| 大输出 | [executor.go](../../internal/tools/executor.go)、[spill.go](../../internal/tools/spill.go)已有脱敏落盘、有界回读与内容引用 | 优先在工具侧过滤和汇总，避免读回大段无关内容；保留可追溯引用 |
| 压缩与恢复 | [compactor.py](../../orchestrator/context/compactor.py)与 Go [compaction.go](../../internal/session/compaction.go)已有结构保护 | 测试选择性观察折叠和交接保真；原始事实继续由 Ledger 保存 |
| 独立验证 | [evaluation_grader.go](../../internal/session/evaluation_grader.go)已有 Go 管理的独立评分 | 使用现有 checker，改进验证顺序与失败反馈，无需先增加一个通用 critic 模型 |

## 3. 推荐采用顺序和成本边界

这是工程判断，未承诺收益百分比。改造规模不包含真实实验的等待时间或 API 费用；账单仍由用户处理。

1. **先用程序化编排压缩工具往返。** 在一次有界调用中协调独立读取、检索和结构化汇总，结果限额返回。当前执行工具可立即采用；产品侧需要复用 Go 授权入口，每个子操作保留事实和错误。改造重点是调用组织与结果格式，不新增平台。
2. **测量后引入 Tool Search／schema 选择。** 这是源码中最明确的功能增量，但有新增检索延迟和漏选工具风险。保留常用核心工具，按需发现其余工具；发现结果不授予权限，Go 仍校验真实工具身份和参数。
3. **对旧只读 observations 做受控折叠。** 参考 GLM-5，在现有 Context 投影上试验，支持按引用取回；保护当前任务、工具配对和恢复责任。跨 Python／Go 的投影与恢复验证成本高于单纯结果过滤，应单独冻结实验输入。
4. **补齐上下文与关键路径观测，复用现有缓存和验证器。** 分别记录模型等待、工具执行、环境准备、验证与收尾；区分经过时间和 CPU 时间。多 Agent 根据依赖与资源安排，效率比较使用固定总预算。

这些机制都可先在现有 Go／Python／OpenSandbox／Ledger 上验证。引入 ADK、另一套工作流状态库、分布式执行平台或模型训练系统，会增加迁移与维护范围；当前材料尚不能证明这种成本值得付出。

## 4. 怎样证明引入后确实更快

对每项改造单独做同模型、同任务、同工具权限和总预算的配对消融。保留全部失败、unknown、重试与未运行项；新增效率实验与既有三组正式实验分别登记，不能混用成功证据。

至少比较：完整任务成功率、端到端经过时间、模型往返数、工具调用／重复读取数、工具定义与结果 token、已确认缓存 token、验证耗时。小样本展示原始逐项结果和完整分母；样本足够后才报告分位数等汇总。冷／热缓存、宿主竞争与恢复路径分别标明。

token 下降只是一个指标。若额外工具检索、摘要生成或重取使墙钟上升，或破坏最终质量，就保留负结果。报告中的85%、37%、6.7个百分点和39–70%分别来自不同任务、模型和指标，不能相加或套成项目提效目标。

**可用于面试的研究表述：**“我参考 OpenAI 的缓存友好 agent loop、Anthropic 的程序化工具调用、GLM-5 的上下文消融，以及 Google 的多 Agent 对照研究，把效率拆成模型往返、上下文、工具执行和验证关键路径。项目已有 Ledger、压缩和独立评分，所以优先补工具发现和调用组织；收益用同模型、同预算实验验证，完整保留负结果。”这段说明方法，项目收益待实测后补充。
