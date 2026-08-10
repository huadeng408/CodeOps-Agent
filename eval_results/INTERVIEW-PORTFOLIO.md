# 面试 Portfolio：代码智能评测体系

> 状态：**非定稿，无任何可发布分数**。本文档 2026-08-09 按设计地图 §20.4「声明与证据对照」全面下修：此前的 `10/10`、`avg=0.7`、`reward=1.0`、`RAG valid`、`Phoenix traced`、`✅ Done` 均为**探索结果或仅连通性验证**，不是发布证据。
> 唯一权威口径：`docs/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md` §20.4 / §20.6 / §20.8。本文与设计地图冲突时，一律以设计地图为准。
> 分支：`feature/complete-design-implementation`

## 1. 执行总结（面试可直接引用）

**一句话**：搭了一套覆盖 3 个国际基准 + 自建 RAG 的评测体系，**目前真正交付的是「诚实的证据纪律」，不是分数**。核心发现不是"模型行不行"而是"评测管线自己有没有 bug、有没有在骗自己"。

**可引用的事实（全部已下修到证据能支撑的强度）**：
- tau2-bench: 10 任务 7/10（`avg_reward=0.7`）—— **仅探索结果**。同一批题经过 prompt tuning（0.4 → 1.0 → 0.7），属 development-set contamination，缺 pins/provenance，**不能作为发布分数**。
- SWE-bench: v2 产出 10/10 **非空 patch**，`resolved` 数未知 —— 非空 patch 不等于 resolved。此前的 official `1/1 resolved` 是嵌入 ground-truth patch 的连通性 smoke（`SCORER_CONNECTIVITY_ONLY`），不证明 Agent 能力。
- Terminal-Bench: heredoc/base64 修复 = `IMPLEMENTED_NOT_VERIFIED`。官方总结果 `0/4`，最新单任务 `0/1 test_timeout`，**没有一次成功的官方 resolved**。
- RAG: BM25 0.52 / BGE-M3 0.65 / Hybrid RRF 0.56 nDCG@10 —— **可重算的探索实验**，不是 release gate。
- qrels: 180 query。原 `AI_REVIEWED=59` 因仲裁逻辑错误已作废；按修正后的严格仲裁重放为 **23 AI_REVIEWED / 157 DISPUTED**，真人复核 0 条。
- 复核模型身份：`MODEL_IDENTITY_UNVERIFIED`（请求名 `gpt-5.6-sol`，`revision=unknown`，未存响应身份）。
- Phoenix: 仅**基础连通 + 五 span 合成 trace（无 RAG span）**，生产 E2E 未验证。
- **最重要的教训**：评测本身也会出错；一个"通过了"的数字如果不能绑定 HEAD hash、原始输入、官方 scorer 原始输出、模型身份、trace 和 checksums，它就不是证据。

## 2. 架构概览

```
┌─────────────────────────────────────────────────────┐
│                   eval/run.py (统一入口)              │
│    budget / resume / artifacts / failure taxonomy    │
├─────────────────────────────────────────────────────┤
│  SWE-bench     │ Terminal-Bench  │ tau2-bench       │
│  (Docker/WSL2) │ (Docker/tmux)  │ (API tool-call)  │
├─────────────────────────────────────────────────────┤
│                RAG Retrieval Eval                    │
│    BM25 / BGE-M3 / Hybrid RRF → 180 qrels          │
├─────────────────────────────────────────────────────┤
│            Phoenix OTel Observability                │
│    agent → tool → retrieve → scorer spans          │
└─────────────────────────────────────────────────────┘
```

## 3. 诚实评测 SOP（核心方法论）

### 原则
1. Agent 只获得与真实场景一致的输入：问题描述 + 代码仓库
2. Prompt 不包含文件路径、修复方向、答案提示
3. 所有评分走官方 scorer（不接受自评）
4. Synthetic 数据标记 `synthetic=true`，不混入 official
5. API key 不进仓库（记录为技术债务，使用外部文件读取）

### 验证方法
- 每个 agent prompt 在上线前审查：确认不含 gt patch、hints_text、文件路径
- 每个 scorer 输出保存完整原始输出（不解包后重新打分）
- 失败案例分析：区分 INFRA_ERROR / MODEL_ERROR / TIMEOUT

## 4. 能力矩阵

| 能力维度 | Benchmark | 规模 | 评分方式 |
|---|---|---|---|
| 代码修复 | SWE-bench Verified | 10 实例 | WSL2 Docker official scorer |
| 终端操作 | Terminal-Bench | 4 实例 | terminal_bench.Harness |
| 工具调用 | tau2-bench (airline) | 5 实例 | tau_bench.run.run() |
| 文本检索 | 自建 180 qrels | 3 路对比 | nDCG/Recall/MRR |

## 5. 结果快照（探索结果，非发布分数）

> 本节所有数字均为**探索性运行**，未经统一 Harness + 官方 scorer + pins/trace/checksums 绑定，按设计地图 §20.4 一律不得作为发布分数引用。

### SWE-bench 10 实例

**v2 Agent（DeepSeek Chat direct-LLM，绕过 ConversationRunner 的 tool-calling bug），只有 problem_statement，预加载源码文件，无任何提示。**

> **§20.4 权威结论**：v2 一次性提供「按字母排序的前 20 个 Python 文件」，不是 Harness tool loop，且 patch 存在统一截断特征 → **存在选择偏差与截断风险，必须经统一 Harness 与官方 scorer 重跑后才有意义**。下表的 `Patch` 列只表示「产出了非空 diff」，**不表示 resolved**。

| # | Instance | 非空 Patch | Tokens in/out | Time | Notes |
|---|---|---|---|---|---|
| 1 | psf__requests-6028 | ✅ 2000B | 2795/4096 | 17.7s | Truncated response |
| 2 | psf__requests-5414 | ✅ 534B | 2836/139 | 1.6s | URL IDNA error handling |
| 3 | pallets__flask-5014 | ✅ 2000B | 2317/4096 | 17.1s | Truncated response |
| 4 | mwaskom__seaborn-3069 | ✅ 1152B | 2665/315 | 2.8s | Nominal scale grid |
| 5 | mwaskom__seaborn-3187 | ✅ 480B | 2829/131 | 1.7s | ScalarFormatter offset |
| 6 | pylint-dev__pylint-8898 | ✅ 440B | 3428/130 | 1.6s | Pylint fix |
| 7 | pylint-dev__pylint-7277 | ✅ 282B | 2621/118 | 1.3s | sys.path pop fix |
| 8 | pytest-dev__pytest-10081 | ✅ 450B | 3410/138 | 2.2s | Unittest skip check |
| 9 | sphinx-doc__sphinx-10614 | ✅ 2000B | 3119/4096 | 17.4s | Truncated response |
| 10 | sphinx-doc__sphinx-11510 | ✅ 944B | 4031/204 | 2.5s | Sphinx fix |

**v2 结果：10/10 产出非空 patch，`resolved` 未知，总计 73s。** 官方 scoring 从未在这 10 条上真实完成 —— 唯一跑通过的 official 调用是嵌入 ground-truth patch 的 1 实例连通性 smoke（`SCORER_CONNECTIVITY_ONLY`）。**因此本节没有任何 SWE-bench 能力分数。**

**关键改进**：
- v1 (ConversationRunner) 3/10 非空 patch → v2 (direct LLM) 10/10 非空 patch（只衡量「有没有产出 diff」，不衡量正确性）
- 绕过 DeepSeek tool-calling protocol bug（`Messages with role 'tool' must be a response to a preceding message with 'tool_calls'` HTTP 400）
- 预加载 repo 中 top-20 Python 文件的源码作为 prompt context
- 低 token 输出的实例（seaborn-3187: 131 tok, pylint-7277: 118 tok）仍产生了有效 patch——模型只需确认 bug location 后输出 fix

> **诚实声明**：v2 预加载了源码文件作为 context。这在 SWE-bench 的"action space"内是合法的（agent 有 Read/Bash/Glob tools，读文件是正常操作）。区别是 v2 一次性喂入所有相关文件的上下文，而不是 tool-call 逐个读取。这不是作弊——agent 仍然需要自己从 issue 和代码中定位 bug、理解根因、写正确的 diff。

**注意**：v2 的 3 个 max-token 截断的 patch（requests-6028, flask-5014, sphinx-10614）都是 2000B——恰好是 `max_tokens=4096` 时 diff 被截断。这些 patch 可能不完整。需要增加 max_tokens 到 8192。

### Terminal-Bench 4 实例

**【0/4 resolved】DeepSeek Chat via `agent_import_path` + official Harness。** 修复了 Windows Docker 跨平台路径（5 层 monkey-patch）、tmux send_keys 阻塞超时（改为非阻塞 + 预热）、build-cython-ext schema 不兼容（降为 4 实例）。tb-honest-v2 run 完成。

| Task | Result | Failure Mode | Tokens in/out |
|---|---|---|---|
| bn-fit-modify | ❌ | UNKNOWN_AGENT_ERROR (Docker build failed) | N/A |
| break-filter-js-from-html | ❌ | TEST_TIMEOUT (1200s) | 263/59 |
| build-pmars | ❌ | TEST_TIMEOUT (900s) | 278/191 |
| adaptive-rejection-sampler | ❌ | TEST_TIMEOUT (900s) | 703/4096 |

**诚实分析**: 
- `break-filter-js-from-html`: Agent 发了一个 `cat > /app/out.html << 'ENDOFFILE'` heredoc，但 tmux 把它拆成了逐行 HTML 标签——heredoc 被 `send_keys` 当成了逐行命令。这是 agent 和 tmux 之间的协议 mismatch
- `adaptive-rejection-sampler`: Agent 输出 4096 tokens 的 R 代码，但 `cat > /app/ars.R << 'ENDOFFILE'` 又遇到了同样的逐行拆分问题
- `build-pmars`: Agent 发出了 apt-get + wget + dpkg-buildpackage 命令，但非阻塞模式导致命令在容器内执行时无反馈
- `bn-fit-modify`: docker-compose build 失败（Dockerfile 依赖问题，不是 agent 的问题）
- **根因**: `send_keys` 把多行 heredoc 拆成逐行发送，破坏了 shell heredoc 语法。需要改用 `session.send_keys` 的单行模式（`\n` 转义）或者把代码写入文件改用 base64 编码绕过

### Phoenix 可观测性（仅基础连通）

- Phoenix 运行在 `localhost:6006`、OTLP collector `4317/4318`，容器 `arizephoenix/phoenix:latest`
- 已发送**合成** trace：`eval.run → eval.instance → agent.solve → tool.execute → scorer.official`，五层 span 到达 Phoenix
- **状态 = 基础连通性验证，不是 E2E 可观测性**：该 trace 由测试脚本手工构造，**不含 RAG span**，也不来自真实 agent/scorer 执行。生产级 agent→tool→retrieve→scorer trace 仍未接入（设计地图 §20.4「仅基础连通，生产 E2E 未验证」）

### tau2-bench 10 实例 (airline)

> **口径警告（设计地图 §20.4）**：下表 **不是发布分数**。task 0-4 与 prompt tuning 使用了同一批题（0.4 → 1.0 → 0.7），属 **development-set contamination**；整批缺 pins/provenance，未经统一 Harness 与 canonical artifact 产出。仅可作为**探索结果**引用。

| Task | Reward | Actions |
|---|---|---|
| 0 | 1.0 | book_reservation |
| 1 | 1.0 | cancel_reservation |
| 2 | 1.0 | 5× update_reservation_flights (downgrade all) |
| 3 | 0.0 | update_reservation_flights + baggages (r_actions=0, wrong flight selection) |
| 4 | 1.0 | update flights + passengers + baggages |
| 5 | 1.0 | same as task 4 (passenger change) |
| 6 | 1.0 | update_reservation_flights (cheapest economy next day) |
| 7 | 1.0 | same as task 6 (EWR/PHL both OK) |
| 8 | 0.0 | cancel + book (r_outputs: credit card amount wrong: 1786 vs expected) |
| 9 | 0.0 | cancel + 3× book (r_outputs: 1286 vs expected) |

`avg_reward = 0.7`（7/10），wall time 593s (10min) —— **探索结果，不可计分**。task 0-4 为受污染的 development set；task 5-9 是首次暴露（3/5），但同样缺 pins/provenance。

> **诚实分析**: Task 3 和 task 8-9 是同一类型的"多步数学计算 + 支付优化"任务。Task 8-9 要求 agent 算 gift card 总额、certificate 总额、master card 部分。Agent 算对了 gift card total (327) 和 certificate total (1000)，但 master card 最终金额错了。这是模型计算能力而非 agent 框架问题。

### tau2-bench 失败原因分析

**Task 2 (downgrade business→economy, 5 reservations)**: 模型执行了 5 次 `update_reservation_flights`，但 reward=0。原因：
- 没先查用户所有预订就直接改 → 可能用错了 reservation_id
- 应 refund to original payment，但模型可能用了错误的 payment_id
- 未输出最终节省金额（task 要求输出数字）

**Task 3 (Houston→Denver fastest return + bag)**: `update_reservation_flights` + `update_reservation_baggages`，reward=0。原因：
- "reservation id not remembered" → agent 应先用 `get_user_reservations` 搜索
- `payment_id: gift_card_6276644` → task 要求用 smallest balance，agent 可能选错了
- "you are not good at math" → agent 应负责计算和决策，但没给出正确结果

**Task 4 (NYC→Chicago change passenger + 3 bags)**: 3 个操作全部执行，reward=0。原因：
- 用户名叫 omar_rossi_1241，agent 改乘客时可能用错了名字
- "your birthday is in your user profile so do not provide it" → agent 可能要求了 DOB
- `payment_id: gift_card_8190333` → gift card 选择可能不对

**修改建议**:
1. Agent prompt 添加强制"先读后写"规则：任何写操作前必须先调 `get_user_reservations` / `get_user_info` / `search_flights`
2. 支付方式选择需显式比较余额 → prompt 中加入 "Compare available payment methods and select the one with smallest/largest balance as instructed"
3. tau2-bench 是双 LLM 架构（agent + user simulator），失败可能是 user simulator 的行为问题，需分析 dialogue log

### RAG 检索（探索实验，非 release gate）

| Method | Recall@5 | MRR@10 | nDCG@10 | 口径 |
|---|---|---|---|---|
| BM25 | 0.5500 | 0.4169 | 0.5201 | 可重算探索实验 |
| BGE-M3 | 0.6667 | 0.5453 | 0.6547 | 可重算探索实验 |
| Hybrid RRF | 0.6167 | 0.4093 | 0.5636 | 可重算探索实验 |

数值健全性已修复：所有 nDCG 落在 [0,1]，duplicate-gain / nDCG>1 的旧报告已标记 `INVALIDATED`。

但按设计地图 §20.4，这三行**不是 release 指标**，原因是：qrels 底座还没有可信金标（见下），且未过设计地图要求的 dev/holdout 隔离与污染扫描门禁。发布级 RAG 报告须建立在锁定金标集之上（§20.6.3 E5）。

### qrels 复核状态（已按修正仲裁重放）

- 总 queries: **180**
- **AI_REVIEWED: 23**；**DISPUTED: 157**；`HUMAN_REVIEWED: 0`
- 旧口径 `59 AI_REVIEWED / 121 DISPUTED` 已作废：原仲裁逻辑会把「两轮都判 false」「维度不一致」「低置信」误升为一致通过（§20.4）。修正后的仲裁要求六个 bool 维度两轮全 True、contamination=`none`、`min(confidence) >= 0.7`。
- 本次重放是**离线重仲裁**：只读已有 Pass A/B sidecar，**未发起任何新的 LLM 调用**，因此不引入新的模型身份不确定性。
- 复核模型身份：`MODEL_IDENTITY_UNVERIFIED` —— 请求名为 `gpt-5.6-sol`，但 `revision=unknown` 且未留存响应侧身份，`OPENAI_BASE_URL` 指向 DeepSeek。**不能声称由 GPT-5.6 Sol 完成复核。**
- 复核器不产生真人 `reviewer_hash`，也永不签发 `HUMAN_REVIEWED`。

### Phoenix 可观测性（同上，仅基础连通）

- Phoenix 运行在 `localhost:6006`，OTLP collector `4317/4318`
- 已部署；**生产 agent/scorer/RAG trace 未接入、未验证**

## 6. 技术挑战与解决方案

| 挑战 | 解决方案 |
|---|---|
| Windows 上 Docker 路径反斜杠问题 | 5 层 monkey-patch: put_archive, exec_run, send_keys, copy_to_container, PurePosixPath 常量 |
| WSL2 内 swebench 无法直连 Docker Hub | curl-through-Clash-proxy FakeResponse monkey-patch |
| Terminal-Bench tmux send_keys 超时 | 改为非阻塞模式，预热 echo + agent-done 信号 |
| GBK console 无法打印 Unicode emoji | sys.stdout 强制 re-encode UTF-8 |
| Python 3 不可用（Git Bash） | mnm_start.sh 手动初始化替代方案 |

## 7. 已知限制与诚实声明

1. **没有任何一条可发布的 benchmark 分数。** 三个基准都停在探索阶段：SWE 只有非空 patch 无 resolved，Terminal 官方 0/4，tau2 是被 tuning 过的同批题。
2. SWE-bench 10 实例不是随机抽样（受 Windows 环境约束，排除了大 repo）；v2 还一次性预加载按字母序的前 20 个 Python 文件，存在**选择偏差**，且 3 条 patch 有统一截断特征。
3. Terminal-Bench 只有 4 个任务（build-cython-ext 的 task.yaml 使用旧版 schema，Harness 无法解析）；heredoc/base64 修复是 `IMPLEMENTED_NOT_VERIFIED`。
4. tau2-bench 当前只覆盖 airline 域，且前 5 题经过 prompt tuning → **development-set contamination**，不是 holdout。
5. **157/180 qrels 是 DISPUTED，真人复核 0 条**；23 条 AI_REVIEWED 也只是修正仲裁后的机器一致，模型身份 `MODEL_IDENTITY_UNVERIFIED`。
6. Phoenix 已部署但尚未接入生产级 agent/scorer/RAG trace；已有五 span trace 是合成的，不含 RAG span。
7. 未做多模态 RAG 评测：多模态目录只有 README，视觉 encoder/index 是接口脚手架，无真实数据、视觉模型、索引或 bake-off。
8. **污染扫描不完整**：24,877 × 180 的完整扫描不可行，目前只有 exact-hash 子门（`0/180`）。normalized、近似、语义污染**未知**。
9. DeepSeek key 已进入 Git 历史（至少 `fde95f8a`、`3753d3a6`、`2bef2877`）。工作树已清零并改为 env-only fail-closed，但**历史泄露未闭环**。

## 8. 当前状态（2026-08-09，按设计地图 §20.4 口径）

状态词只用 `DESIGNED` / `IMPLEMENTED` / `VERIFIED` / `BLOCKED`（含证据限定词）。**本表不再出现 `✅ Done`。**

| 维度 | 事实证据 | 状态 |
|---|---|---|
| SWE-bench 10 | 10/10 非空 patch；resolved 未知；official smoke 曾嵌入 ground-truth patch | `SCORER_CONNECTIVITY_ONLY` |
| Terminal-Bench 4 | 官方 Harness 总结果 0/4；最新单任务 0/1 test_timeout | `IMPLEMENTED_NOT_VERIFIED` |
| tau2-bench 10 | 7/10 `avg_reward=0.7`；同批题 0.4→1.0→0.7 经 tuning；缺 pins/provenance | 探索结果，非发布分数 |
| RAG 3-way | nDCG 0.52 / 0.65 / 0.56，数值健全可重算；金标未锁定 | 探索实验，非 release gate |
| qrels 仲裁 | 离线重放 23 `AI_REVIEWED` / 157 `DISPUTED` / 0 `HUMAN_REVIEWED` | `IMPLEMENTED`（仲裁逻辑已修正） |
| 复核模型身份 | 请求名 `gpt-5.6-sol`，`revision=unknown`，base_url 指向 DeepSeek | `MODEL_IDENTITY_UNVERIFIED` |
| Phoenix | 6006 + OTLP 4317/4318 可达；trace 为五 span 合成、无 RAG span | 基础连通，生产 E2E 未验证 |
| 多模态 RAG | 仅 README + 接口脚手架 | `DESIGNED` |
| 污染扫描 | 仅 exact-hash 子门 0/180 | 不完整 |
| 统一 Harness 旁路 | legacy `benchmark_mod.run()` 旁路已删除并经 AST 断言（0 call node） | `IMPLEMENTED` |
| H5 官方 1 实例 smoke | 未执行；需 Go server :8081、embedding :8009、Phoenix :6006 | `BLOCKED` |
| Repo / key 历史 | 工作树 0 残留 + env-only fail-closed；历史仍含 key | `BLOCKED`（待授权重写） |

## 9. API Key 安全问题（诚实声明）

旧脚本 `run_swebench_honest_10.py`、`run_terminalbench_honest_5.py`、`run_tau2bench_honest_5.py` 等曾硬编码 DeepSeek API key。Push 被拦截是**正确的行为**。

当前处置进度：

- **已完成**：11 个 `eval/swebench_work/*.py` 全部改为 `os.environ["DEEPSEEK_API_KEY"]` fail-closed，缺失即报错，**无真实 key 默认值**；工作树 secret scan 0 残留。
- **未完成（BLOCKED）**：key 仍存在于 Git 历史。该 key 为公司提供、无法轮换，因此处置目标是**永不在 GitHub 暴露**：历史重写前禁止公开 push / PR / release / 分享 bundle。
- 历史重写需**用户对受影响 refs 的单独明确批准**，并先创建私有备份 ref/bundle；删除当前文件或加 `.gitignore` **不能**修复历史泄露。

## 10. 执行证据索引

### 文件产物
| 文件 | 内容 |
|---|---|
| `eval_results/swebench_agent_honest/predictions.jsonl` | 10 条 prediction |
| `eval_results/swebench_agent_honest/agent_log.json` | 完整 agent 日志 |
| `eval_results/swebench_agent_honest/honest_summary.json` | 结构化摘要 |
| `eval_results/tau2bench_honest/tau2bench_honest_5_summary.json` | 5 task rewards |
| `eval_results/tau2bench_honest/tool-calling-*.json` | 官方 run 产物 |
| `eval_results/terminalbench_honest/tb-honest-v2/run_metadata.json` | Harness 元数据 |
| `results/eval/techdocs/bm25-report.json` | BM25 报告 (nDCG 0.52) |
| `results/eval/techdocs/bge_m3-report.json` | BGE-M3 报告 (nDCG 0.65) |
| `results/eval/techdocs/hybrid_rrf-report.json` | Hybrid RRF 报告 (nDCG 0.56) |

> **证据强度声明**：上表列出的是**原始产物路径**，不是通过的门禁。这些文件均为 Phase 0 之前的探索期产物，**不满足设计地图 §20.7 的 canonical artifact 合同**（缺 `run-manifest.json` 的 `git_sha`/`dirty_hash`/`model` pins、缺 `scorer/official-output.*` 原始输出、缺 `traces/`、缺 `checksums.sha256`）。任何发布结论必须由重跑产生的完整 artifact 树支撑，不能引用本表。

### Git

- `2bef2877`: Honest multi-benchmark eval commit（15 files, 1613 insertions）
- 该提交及 `fde95f8a`、`3753d3a6` 的历史 diff 中含真实 DeepSeek key
- Push 被 secret 保护拦截；**在历史重写获得用户明确批准并完成前，禁止公开 push / PR / release / 分享 bundle**

### 已停止的历史进程（不再是"运行中"）

- WSL2 `score_honest_10.py` → Docker scoring：**未产出可用的官方 scorer 原始输出**，不计为证据
- Docker `determined_cray` → Terminal-Bench conda env build：已结束，官方结果仍为 0/4

## 11. 下一步（顺序由设计地图 §20.8 决定，不得跳 Gate）

按 Phase 依赖执行，**扩大规模排在门禁之后**——在 H5 官方 smoke 通过前把实例数从 10 提到 50 只会放大不可信的数字。

1. **H5（当前阻塞项）**：起 Go server :8081、embedding :8009、Phoenix :6006，跑通 1 实例官方 smoke，产出完整 canonical artifact 树（含 `scorer/official-output.*`、`traces/span-assertion.json`、`checksums.sha256`）。这是 Phase 1 Gate 的最后一环。
2. **E2 模型身份**：把响应侧 provider/model/revision 落盘，解除 `MODEL_IDENTITY_UNVERIFIED`。
3. **E3 dev/holdout 防火墙**：把被 tuning 过的 tau2 前 5 题永久划入 dev set，holdout 另取，杜绝 development-set contamination。
4. **E4 污染扫描工业化**：补 normalized / 近似 / 语义三层，替换当前只有 exact-hash 的子门。
5. **E5 RAG 发布报告**：基于锁定后的 23 条金标出报告；金标不锁定不出 release 数字。
6. **Phase 5 可观测性**：Phoenix 接入真实 agent/tool/retrieve/scorer span（含 RAG），替换合成 trace。
7. **规模扩展（Gate 之后）**：SWE 扩到 50 实例、Terminal 补 build-cython-ext schema 适配、tau2 加 retail 域。
8. **qrels 真人复核**：157 条 DISPUTED 需真实人工参与才能标 `HUMAN_REVIEWED`。
9. **多模态 RAG（Phase 7）**：真实数据 + 视觉 encoder/index + bake-off，PDF 一律 MinerU 显式 OCR。
10. **key 历史闭环**：key 为公司提供不可轮换，需在用户明确批准受影响 refs 后重写历史；重写前先做私有备份 ref/bundle，重写后复扫全部 refs 并加 CI secret scan 门禁。
