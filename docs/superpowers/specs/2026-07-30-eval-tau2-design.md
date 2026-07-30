# Eval Runner 与真实 tau2-bench 集成设计

## 1. 背景与目标

当前 `eval/run.py` 约定每个 benchmark 模块公开 `run(driver, limit)`，但
`eval.benchmarks.evalplus` 和 `eval.benchmarks.swebench` 只提供类方法，导致统一
CLI 在创建 driver 后立即失败。与此同时，现有 `AgentAdapter.solve_instance()` 是
单任务、单回答接口，无法表达 tau2-bench 的多轮用户模拟、领域工具调用、轨迹和
reward，因此不能把 tau2 任务压缩成一次 `solve_instance()` 并称为真实集成。

本阶段目标是：

1. 修复 EvalPlus 和 SWE-bench 的统一 runner 契约。
2. 为 EvalPlus 暴露 `base-only` 回归入口。
3. 使用本机上游 tau2-bench 的 `TextRunConfig` 和 `run_domain()` 执行真实多轮仿真。
4. 使用与本仓库 driver 相同的模型、base URL 和环境变量凭据来源，并以
   `deepseek-v4` 完成最小真实验收。
5. 在结果和文档中明确：本阶段评测的是上游 tau2 `llm_agent`，不代表本地
   `ConversationRunner` 或本地 Code Agent 已接入 tau2。

本阶段不实现本地 Code Agent 到 tau2 `HalfDuplexAgent` 的领域工具桥，也不修改
上游 `C:\Users\ieeep\tau2-bench` 源码。

## 2. 方案选择

采用“统一 CLI 契约修复 + 独立 tau2 上游运行器”的分层方案。

- EvalPlus 和 SWE-bench 继续使用本仓库 `AgentAdapter`，各自添加模块级
  `run(driver, limit, ...)` 薄封装。
- tau2 使用单独的模块和命令入口构造上游 `TextRunConfig`，直接调用
  `tau2.runner.run_domain()`。它不伪装成 `AgentAdapter` benchmark。
- 公共模型配置从一个小型配置对象生成，使本仓库 driver 与 tau2 wrapper 对
  model/base URL/API key 环境变量的解释一致，但不强迫二者共享执行抽象。

拒绝以下两个方案：

- 把每个 tau2 task 转为 `EvalInstance` 后只调用一次 `solve_instance()`：会丢失用户
  模拟、工具状态、轨迹与 reward，结果不再是 tau2-bench。
- 立即实现本地 Agent 的 `HalfDuplexAgent` 桥：需要定义消息转换、工具调用、领域
  环境状态和终止协议，范围和风险明显高于本轮 runner 修复，应另立规格。

## 3. 组件与边界

### 3.1 统一 runner 契约

`eval/run.py` 仍负责解析通用参数、创建本地 driver、调用 benchmark、汇总
`EvalResult` 并写 JSON/CSV。它不感知 benchmark 内部评分细节。

`eval/benchmarks/evalplus.py` 新增模块级 `run()`，构造 `EvalPlusBenchmark` 并返回
其逐实例 `EvalResult` 列表。评分统计仍由现有类实现生成和保存；薄封装不得绕过
官方 EvalPlus 评分。`base_only` 由统一 CLI 参数传入，默认保持严格的 plus 测试。

`eval/benchmarks/swebench.py` 新增模块级 `run()`，构造 `SWEBenchRunner` 并返回
`run_all()` 的 `EvalResult` 列表。默认路径必须加载真实 SWE-bench 数据；只有显式
dry-run/smoke-test 才允许 synthetic fallback，不能把 synthetic 结果标成正式评测。

### 3.2 共享模型配置

统一 CLI 增加一个不可变的模型配置值对象，至少包含：

- `model`
- `base_url`
- `api_key_env`

配置只保存环境变量名，不保存 key 值。运行时从 `api_key_env` 读取凭据，并把它传给
需要的客户端；错误、日志、结果 JSON 和命令回显均不得包含 key。DeepSeek 真实验收
使用 `deepseek-v4`、DeepSeek 官方 OpenAI-compatible base URL，以及现有本地约定的
key 环境变量。

现有 `HeadlessDriver(api_key="ollama")` 默认参数会遮蔽 `LOCAL_LLM_API_KEY`，并且
`create_driver()` 不能显式传入 key。本阶段必须先把 driver 的 `api_key` 默认值改为
`None`，按“显式参数 -> 指定环境变量 -> 本地 Ollama 占位值”的顺序解析，并让
`create_driver()` 接收已解析的 key。该修复是 DeepSeek 真实验收的前置条件。

### 3.3 tau2 上游运行器

新增 `eval/benchmarks/tau2bench.py` 作为 tau2 集成边界。它负责：

1. 从显式 `--tau2-root` 或 `TAU2_ROOT` 定位上游 checkout，默认仅作为便利回退使用
   `C:\Users\ieeep\tau2-bench`。
2. 在导入前把 `<tau2-root>\src` 加入当前进程的 `sys.path`，不安装、不修改上游。
3. 校验 `TextRunConfig` 和 `run_domain` 可导入，并拒绝缺少数据的 checkout。
4. 构造文本模式配置：`agent="llm_agent"`、`user="user_simulator"`、一个显式 domain、
   `num_tasks`、`max_concurrency=1`、固定 seed、有限 timeout 和独立结果路径。
5. 给 agent 与 user simulator 使用同一个已批准的模型端点配置；通过 LiteLLM 支持的
   `model` 与 `api_base`/`api_key` 参数传递，不写入磁盘。
6. 调用上游 `run_domain(config)`，保留完整 simulation、trajectory、reward、终止原因
   和上游 metrics。
7. 生成一份本仓库侧摘要 JSON，记录 domain、task 数、模型名、成功/失败计数、reward
   汇总和上游结果文件位置，但不复制敏感请求头或 key。

tau2 wrapper 是独立 CLI，不经由 `eval.run` 创建本地 `HeadlessDriver`。这样可以保留
真实 tau2 语义，也避免输出格式被错误压成 `EvalResult.answer`。

## 4. 数据流

EvalPlus/SWE-bench：

```text
CLI 参数 -> create_driver -> benchmark.run(driver, limit)
         -> AgentAdapter.solve_instance -> EvalResult[]
         -> benchmark 原生工件 + eval.run JSON/CSV
```

tau2：

```text
CLI/环境变量 -> Tau2RunSettings -> TextRunConfig
             -> tau2.run_domain -> 多轮 user/agent/tool 轨迹
             -> 上游 Results + 本仓库摘要 JSON
```

两条路径只共享模型端点配置语义，不共享 agent 执行抽象。

## 5. 参数与输出语义

统一 CLI 新增：

- `--base-only`：仅对 EvalPlus 生效；其他 benchmark 收到该参数时明确报错。
- `--api-key-env`：默认使用项目既有的 `LOCAL_LLM_API_KEY`，只表示变量名。

tau2 CLI 至少支持：

- `--tau2-root`
- `--domain`，首轮真实验收使用 `retail`
- `--num-tasks`
- `--model`
- `--base-url`
- `--api-key-env`
- `--output-dir`
- `--seed`
- `--timeout`

tau2 命令成功的定义不是“进程未抛异常”，而是：请求的任务数均产生 simulation；每个
simulation 有非空轨迹和可解析终止状态；上游 reward/metrics 已计算；摘要未把上游
失败伪装成成功。单任务 reward 为 0 仍是有效评测结果，不等同基础设施失败。

## 6. 错误处理与安全

- benchmark 模块缺少契约、tau2 checkout 缺失、上游 API 不兼容或 key 环境变量为空，
  均在模型调用前失败并返回非零退出码。
- HTTP 超时、模型错误、工具错误和上游 simulation 错误保留到结果工件，并使验收命令
  返回非零；不只打印 warning 后退出 0。
- 日志可以输出 model、base URL、domain 和任务 ID，不得输出 key、Authorization
  header 或包含 key 的异常对象。
- 测试使用临时目录写工件；仓库不提交真实模型响应、完整用户数据或 API key。
- 不修改全局 Python/Git 配置，不写入 tau2 checkout。

## 7. 测试设计

严格按 TDD 实施。

1. Runner contract 单元测试：用 fake driver 验证 EvalPlus/SWE-bench 模块级 `run()`
   返回 `EvalResult` 列表并遵守 `limit`；先复现当前“无模块级 run”失败。
2. CLI 参数测试：验证 `--base-only`、`--api-key-env` 和不支持组合的错误行为。
3. EvalPlus base-only 回归：用小规模 canonical/mock completion 验证只执行 base 测试，
   同时保留默认 plus 路径的回归。
4. tau2 配置单元测试：用假的上游模块验证路径定位、`TextRunConfig` 字段、模型参数、
   key 只从环境变量读取以及摘要生成。
5. Driver 凭据回归：验证未显式传 key 时读取选定环境变量，显式值优先，环境变量缺失
   的本地 Ollama 路径仍可使用占位值；断言测试日志和序列化结果不含 key。
6. tau2 显式集成测试：以本机真实 tau2 checkout 运行一个无需外网模型的 mock/dummy
   domain，证明 `run_domain()`、工具环境、轨迹和 reward 链路完整。
7. DeepSeek 真实验收：从指定本地文件只在进程内加载 key 到环境变量，以
   `deepseek-v4` 在 `retail` 上运行 1 个任务、并发 1；检查非空多轮轨迹、至少一次
   agent 模型调用、领域工具事件、终止状态、reward/metrics 和摘要文件。测试输出只
   报告脱敏元数据。
8. 全量回归：`python -m pytest -q`、`go test ./...`、`go vet ./...` 和
   `git diff --check`。

真实 API 测试默认带显式 integration 标记或由独立脚本触发，普通单元测试不得产生
费用。最终验收必须实际运行一次，不能只证明测试代码可导入。

## 8. 验收标准

- `python -m eval.run -b evalplus -n 1 --no-runner` 不再因缺少模块级 `run()` 失败。
- SWE-bench 模块级契约有自动化测试，synthetic 与正式结果清晰区分。
- EvalPlus `base-only` 可以从统一 CLI 显式选择，默认仍是 plus 严格评分。
- tau2 wrapper 确实调用上游 `TextRunConfig` + `run_domain()`，并保存原生多轮结果。
- DeepSeek V4 单任务真实验收满足第 7 节检查项，且 key 未出现在 git diff、日志和结果。
- 报告明确写明本阶段是上游 tau2 agent 基线；本地 Code Agent 的 tau2 bridge 仍为后续
  独立任务。
- 现有 PDF 路由不受本变更影响：所有 PDF 继续使用 MinerU OCR，Tika 仅处理非 PDF。
