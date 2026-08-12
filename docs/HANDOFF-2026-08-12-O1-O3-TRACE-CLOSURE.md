# O1–O3 Production Trace Closure 交接文档（2026-08-12）

> 用途：供下一个 Claude Code 窗口无损继续 O1→O2→O3 实施。本文只汇总事实、约束与恢复步骤，不代表实现或验收已完成。

## 1. 唯一权威来源

开始工作前必须完整读取：

1. `D:\vscode\CLAUDE.md`
2. `D:\vscode\localcode\CLAUDE.md`
3. `docs/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md`
   - 四目标工作的唯一权威执行地图。
   - O1/O2/O3 重点参考 §9.2、§9.3、§20.6.4、§20.7、§20.8。
4. 历史实施计划来源（仅供本机追溯，不是执行依赖）：
   - `C:\Users\ieeep\.claude\plans\jolly-cuddling-moore.md`
   - 标题：`O1–O3 Production Trace Closure Implementation Plan`
   - 可移植执行内容已归纳到本文 §9–§17；下一个 Agent 不得因本机路径不存在而阻塞。
5. 本交接文档。

若计划与设计地图冲突，以设计地图和项目 `CLAUDE.md` 为准，不另建平行路线。

## 2. 用户目标与当前工作范围

四条主线按依赖顺序推进：

1. 自研 Agent Harness。
2. 多模态 RAG。
3. 权威评测集与发布门禁。
4. 全链路可观测性。

当前主任务已经切换到 **O1→O2→O3 production trace closure**：

- **O1**：把现有 trace contract 升级为唯一、共享、版本化的 v2 acceptance contract。
- **O2**：在真实 production call site 完成 instrumentation：Harness official scorer、Go `SearchKnowledge` retrieval、真实 embedding、真实 reranker，并保真记录 error/timeout/cancel/skipped/degraded。
- **O3**：由 unified Harness 发起一次 current-HEAD、真实模型、真实 SearchKnowledge、真实 RAG、真实 official scorer 的固定实例运行，经 OTLP/HTTP 导出到 Phoenix，再按 `eval.run_id`/`eval.instance_id` 查询回来，生成 checksum-valid canonical artifact。

SWE-bench 优化已冻结：

- 不再修改 prompt、localization、tool budget、模型行为。
- 不扩大样本、不启动新一轮 review/fix。
- 只使用 frozen canonical artifacts。
- 4/20、9/20 只能称为 astropy-20 development subset 的 declared-source headline，不能称为 SWE-bench Verified score。
- C3 validation/retry 只能标记 `IMPLEMENTED`，不能因 Arm B 结果标记 `VERIFIED`。

M1 Windows MinerU 真实 OCR 已 `VERIFIED`；M2–M5 尚未完成，必须在 O3 后按设计地图继续。

## 3. 状态口径

必须严格区分：

- `DESIGNED`：有规格或实施计划。
- `IMPLEMENTED`：代码存在且通过相应实现测试，但未完成真实验收。
- `VERIFIED`：真实 acceptance gate 和证据链通过。
- `BLOCKED`：前置条件、环境或证据链不满足。

当前准确状态：

| 项目 | 状态 | 事实 |
|---|---|---|
| O1 | 部分 `IMPLEMENTED`，整体未 `VERIFIED` | 现有 v1 contract 有 kind/join/pin/credential-shape 基础检查；缺 topology、privacy key/content、rerank required、ended、strict status/degradation。 |
| O2 | 部分 `IMPLEMENTED`，关键链路 `BLOCKED` | Harness scorer、Go retrieve、Go embedding 有局部 instrumentation；缺 W3C 跨进程 parentage、canonical rerank、显式 status/flush、production index alignment。 |
| O3 | `BLOCKED` | 尚无 current-HEAD、Phoenix-backed、shared-v2-contract PASS 且 checksum-valid 的 canonical artifact。 |
| 实施计划 | `DESIGNED` | `jolly-cuddling-moore.md` 已写完；用户说“继续”后开始准备执行，但尚未修改生产代码或测试。 |

Mock、synthetic、contract test、代码存在或 in-process capture 都不能单独证明 `VERIFIED`。

## 4. 冻结的 O3 benchmark 与诚实评测约束

固定实例：

- benchmark module 计划为：`eval/benchmarks/trace_o3.py`
- instance ID：`trace-o3/go-q001`
- query source：`data/eval/techdocs/queries.text.jsonl`
- qrels source：`data/eval/techdocs/qrels.text.jsonl`
- query ID：`go-q001`
- query：

  `What is an interface type in Go, and what does it mean for a type to satisfy an interface?`

Agent task 只可自然要求：先使用内部 `SearchKnowledge` 再回答。不得：

- 强制第一轮必须选择 tool。
- 预调用 SearchKnowledge。
- 手工创建 tool/RAG evidence 或 span。
- 向模型暴露 qrels、gold document IDs、section paths、expected answer、metric/threshold、scorer internals、test patch 或定位提示。

qrels 只能由 official scorer 读取，不进入 task description、workspace sidecar、tool output 或模型可见上下文。

固定 SearchKnowledge policy：

- `mode=hybrid`
- `top_k=5`
- `disable_rerank=false`

固定 capabilities：

```python
TRACE_CAPABILITIES = ("rag", "rerank")
TRACE_PROFILE = "o3"
```

固定 runtime pins：

- corpus generation：`techdocs-2026-07-30-v1`
- physical index / authoritative `rag.index_name`：`knowledge_base_v2_bge_m3`
- embedding：`BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181`
- embedding dimensions：`1024`
- reranker：`jinaai/jina-reranker-v2-base-multilingual`

Actual ES query index、manifest `physical_index`、span `rag.index_name` 必须完全一致。若不一致，状态必须是 `BLOCKED`；不得切 alias、删/重建 index、修改数据或伪造 pin 来让 gate 通过。

## 5. 唯一共享 trace contract v2

不得新增 `evaluate_o3_trace_contract()` 等平行 verifier。唯一 evaluator 计划接口：

```python
evaluate_trace_contract(
    spans,
    run_id,
    capabilities,
    profile="default",
    expected_instance_ids=(),
)
```

`profile="o3"` 才启用严格 topology/status/privacy/causality；generic benchmark 保持 graceful degradation。

O3 必需 span：

- `eval.run`
- `eval.instance`
- `invoke_agent`
- `chat`
- `execute_tool SearchKnowledge`
- `rag.retrieve`
- `embedding`
- `rerank`
- `scorer.official`

真实 topology：

```text
eval.run
└── eval.instance
    ├── invoke_agent
    │   ├── chat ...
    │   └── execute_tool SearchKnowledge
    │       └── HTTP W3C remote parent
    │           └── rag.retrieve
    │               ├── embedding
    │               └── rerank
    └── scorer.official
```

注意：

- `chat` 与 `execute_tool SearchKnowledge` 可以是 `invoke_agent` 下不同后代。
- 不得错误要求 `chat` 必须是 tool 的直接祖先。
- 真正严格的跨进程边只有 `execute_tool SearchKnowledge -> W3C remote parent -> rag.retrieve`。
- `scorer.official` 可作为 `eval.instance` 下与 Agent/RAG 分支并列的后续分支。

v2 还必须验证：

- 唯一 `eval.run` root。
- duplicate `(trace_id, span_id)`。
- missing parent、cycle、detached subtree。
- cross-trace link 的合法目标。
- non-root required span 的 expected non-empty `eval.instance_id`。
- 每个 evaluated span `ended=True`。
- O3 成功链不得有 ERROR、timeout、cancelled、skipped、`rag.degraded=true`、`rag.reranker_applied=false`、`rag.reranker_timeout=true`。
- 敏感 attribute key/content 与 credential-shaped value 检查。

禁止持久化的 trace/artifact 内容包括：

- Authorization、API key、internal token、DSN。
- raw query、prompt。
- `gen_ai.tool.call.arguments`、`gen_ai.tool.call.result`。
- tool args/result。
- document/body/content/textContent。
- hidden qrels 或 expected IDs。

`rag.query_hash` 在 Python/Go 统一为：UTF-8 query 的 lowercase SHA-256 前 16 hex；不得持久化 raw query。

## 6. Instrumentation ownership 与进程边界

必须保持 Go Harness 与 Python orchestrator 边界分离。

Span ownership：

- Python headless runtime：
  - `invoke_agent`
  - `chat`
  - `execute_tool SearchKnowledge`
- Go `OrchestratorHandler.SearchKnowledge`：
  - 唯一 canonical `rag.retrieve`
- Go `searchService.vectorSearch()`：
  - 唯一 real-call `embedding`
- Go `searchService.rerankHits()`：
  - 唯一 real-call `rerank`
- Harness：
  - `eval.run`
  - `eval.instance`
  - `scorer.official`

Transport wrapper 不重复创建 canonical RAG span。

Python→Go 必须使用 OpenTelemetry global composite propagator：

- W3C TraceContext。
- W3C Baggage。

Python `GoBackendClient._post_json()` 使用 `propagate.inject(headers)`；`X-Trace-ID` 只能保留为非权威诊断字段，不能覆盖 W3C parentage。

Go Gin middleware 从 request headers 提取 context；Go `StartSpan()` 从 baggage 注入：

- `eval.run_id`
- `eval.instance_id`

不允许把完整 baggage、auth header 或 token 写入 span。

## 7. Safe SearchKnowledge evidence

模型可看到真实但 bounded 的 RAG `textContent`，这是正常模型输入；persisted prediction/scorer evidence 必须是独立安全形态。

允许持久化：

- query ID/hash。
- request mode/top-k/rerank policy。
- stable returned IDs（如 `fileMd5` + `chunkId`）。
- rank、score、hit count。
- rerank applied/degraded/timeout booleans。

禁止持久化：

- raw query。
- `textContent` 或文档正文。
- prompt、tool args/result。
- qrels、hidden expected IDs、scorer threshold。
- Authorization/token/DSN。

`EvalResult` 计划新增默认空的 `evidence: dict[str, Any]`；Harness 通过一个递归 allowlist validator 后才写入 `predictions.jsonl`。

O3 strict runner：

- 禁止 `--no-runner` / direct-only。
- ConversationRunner 失败时不得 direct fallback。
- 不得把 fallback context 追加到 `instance.task_description`。
- 必须有真实成功 SearchKnowledge call、固定 policy 和至少一个 stable ranked hit，否则 fail closed。
- Generic benchmark 的现有 fallback 保持兼容。

## 8. Phoenix readback 与 finalization

Legacy helper `tests/integration/trace_e2e.py` 使用 `TRACE_E2E_FIXTURE` 和 `gen_ai.tool.call.result` marker，只能保留为历史 connectivity test。新的 O3 readback 不得导入它的 acceptance policy。

新模块计划：`eval/harness/phoenix.py`，职责仅为 source adapter：

- project-scoped pagination。
- `start_time` lower bound。
- cursor-repeat detection。
- 解析 Phoenix `context.trace_id`/`context.span_id`、parent、status、times、links、attributes。
- 本地精确过滤 `eval.run_id`/`eval.instance_id`。
- 输出 allowlisted normalized `CapturedSpan`。
- 不产生独立 verdict；由唯一 shared evaluator 判定。

O3 finalization 顺序必须是：

1. 关闭所有 run/instance/agent/tool/RAG/scorer spans。
2. Python force flush。
3. Go force flush。
4. Phoenix poll/query-back。
5. 运行 shared v2 evaluator（`profile="o3"`）。
6. 写 `traces/trace-summary.json`。
7. 写 `traces/span-assertion.json`。
8. 写最终 summary/environment/manifest。
9. 最后写 `checksums.sha256`。
10. 立即运行 `verify_checksums()`。
11. checksum 写出后不得再修改 run tree。

O3 canonical artifact 至少包括：

- `run-manifest.json`
- `instances.jsonl`
- `predictions.jsonl`
- `events.jsonl`
- `failures.jsonl`（有失败时）
- `summary.json`
- 安全的 environment artifact
- `scorer/official-output.*`
- `traces/trace-summary.json`
- `traces/span-assertion.json`
- `checksums.sha256`

任何 Python flush、Go flush、Phoenix、contract、privacy 或 checksum 失败：

- 仍须保存安全 failure/trace/assertion artifact。
- 最后写 checksum 并验证。
- CLI 输出单条 `BLOCKED: <safe reason>`。
- O3 返回码为 `2`。
- 不得输出暗示验证成功的 PASS/totals。

Generic benchmark 仍允许 telemetry/exporter/Phoenix 不可用时业务 graceful degradation。

## 9. 计划中的 10 个实施任务

以下是可移植的执行任务地图；本机历史计划只用于追溯设计来源：

1. Upgrade the Single Shared Trace Contract to v2。
2. Add the Fixed, Non-Leaking O3 Benchmark and Safe Evidence Contract。
3. Implement the Real Headless SearchKnowledge Bridge and Strict Runner Semantics。
4. Wire O3 Profile, Exporter-Before-Capture, and Honest CLI Failure。
5. Propagate W3C TraceContext and Baggage from Python into Go。
6. Instrument the Real Go RAG Call Sites and Align Pins/Index。
7. Add Phoenix Readback as a Pure Source Adapter to the Shared Contract。
8. Make O3 Finalization Flush, Query, Assert, Persist Failure, and Checksum Last。
9. Run Hermetic Cross-Language Regression and Privacy Review。
10. Execute One Authorized Current-HEAD Real O3 Acceptance Run。

Task 10 只有在 Tasks 1–9 真实 PASS 后才可执行；它是唯一允许读取本地 DeepSeek key 和启动真实 O3 run 的任务。

## 10. 关键现有代码位置

- Shared contract：`eval/harness/trace_contract.py`
  - 当前 `CONTRACT_VERSION = "v1"`。
  - `CapturedSpan` 当前没有 `ended`，links 只保存 trace ID 字符串。
  - `rerank` 当前不是 capability-required。
- In-process capture：`eval/harness/trace_capture.py`
  - `on_end()` 是唯一记录点，但 ended 事实未进入 model。
  - summary 明确 `collector="none"`、`phoenix_verified=false`。
- Unified CLI：`eval/run.py`
  - 当前所有 benchmark 都允许 `--no-runner`。
  - 未接 O3 profile、Phoenix config、exporter-before-capture 或 final gate exit mapping。
- Headless driver：`eval/driver_headless.py`
  - `ToolRegistry` 声明了 `SearchKnowledge`，但 `_TOOL_HANDLERS` 没有实现。
  - 当前 runner exception 会 direct fallback，并修改 `task_description`。
- Result model：`eval/adapter.py`
  - `EvalResult` 当前没有 safe evidence carrier。
- Harness lifecycle：`eval/harness/runner.py`
  - 已拥有 run/instance/scorer lifecycle。
  - 当前 finalization 没有 force flush、Phoenix readback 或 checksum immediate verification。
- Artifacts：`eval/harness/artifacts.py`
  - checksum 已递归覆盖 scorer/traces。
  - 当前 environment dump 是全环境变量加名称匹配式 redaction；O3 必须改为显式 allowlist。
- Python→Go client：`orchestrator/rag/backend.py`
  - 当前只有 `X-Trace-ID`，没有 W3C injection。
- Python OTel setup：`orchestrator/config/env.py`
  - 当前 `configure_otel()` 只返回 shutdown callable。
- Tool protocol：`orchestrator/runtime/tools.py` 与 `orchestrator/rag/models.py`
  - 已有 SearchKnowledge schema 和 `KnowledgeSearchRequestPayload`，应复用，不新建平行协议。
- Go telemetry：`internal/telemetry/genai/tracer.go`
  - 当前缺 `SetStatus`、`ForceFlush`、baggage join injection。
- Go semconv：`internal/telemetry/genai/semconv.go`
  - 当前缺 authoritative `rag.index_name`。
  - `HashQuery()` 实际是 FNV-64 uppercase，与 SHA-256 注释不一致。
- Go handler：`internal/handler/orchestrator_handler.go`
  - 当前 retrieve span 名称与 required attrs/status 不完整。
- Go service：`internal/service/search_service.go`
  - embedding 已局部包住真实 `CreateEmbedding()`。
  - reranker 真实 `Rerank()` 目前没有 canonical span。
- Go wiring：`cmd/server/main.go`
  - 当前给 SearchService 传 `cfg.Elasticsearch.IndexName`。
  - `configs/server.yaml` 中该值是 `knowledge_base`，而 authoritative `cfg.Corpus.TextIndex` 是 `knowledge_base_v2_bge_m3`。

## 11. 当前 main 工作区保护边界

会话开始时 main 分支为：

- branch：`main`
- HEAD：`7c1521c7224cec0e67d2ab3445cabbc08e2c8f92`（`7c1521c7`）

当时已有以下 unrelated dirty/untracked WIP；O1–O3 实施不得 reset、checkout、clean、覆盖或顺手修改它们。

Modified：

- `docs/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md`
- `docs/superpowers/specs/2026-08-11-swebench-report-gate-design.md`
- `eval/swebench_work/compare_arms.py`
- `eval/swebench_work/mechanism_report.py`
- `eval/swebench_work/verify_arms.py`
- `eval_results/INTERVIEW-PORTFOLIO.md`
- `tests/eval/test_compare_arms.py`
- `tests/eval/test_localization_reaches_artifacts.py`
- `tests/eval/test_mechanism_report.py`
- `tests/eval/test_verify_arms.py`

Untracked：

- `docs/PROGRESS-2026-08-11.md`
- `eval/swebench_work/proxy_patch.py`
- `eval/swebench_work/report_gate.py`
- `eval/swebench_work/run_tau2bench_10.py`
- `eval/swebench_work/run_tau2bench_v2.py`
- `eval/swebench_work/score_honest_v2.py`
- `eval/swebench_work/score_honest_v3.py`
- `tests/eval/test_report_gate.py`

上述是会话启动快照；下一个窗口必须重新运行 `git status --short --branch`，并与本列表比较，不可假设状态未变。

## 12. 本窗口的 worktree 尝试与异常

用户说“继续”后，开始使用 `superpowers:using-git-worktrees` 准备隔离实施，但未进入代码修改阶段。

发生的事实：

1. 调用 native `EnterWorktree(name="o1-o3-trace-closure")`。
2. 工具返回保护性拒绝：认为目标目录可能通过 `core.worktree` redirect 写到隔离区之外。
3. 只读检查显示：
   - main checkout 的 `git-dir` 与 `git-common-dir` 都是 `.git`。
   - `core.worktree` 没有配置值。
   - Git 注册了新 worktree：
     `D:/vscode/localcode/.claude/worktrees/o1-o3-trace-closure`
   - branch：`worktree-o1-o3-trace-closure`
   - worktree HEAD：`4a4ed30b44234b6a916525dda3eba8956d6dbd85`
   - 该 HEAD 早于 main 的 `7c1521c7`，因此不能直接把它当成 current-HEAD 实施环境。
   - worktree 被标记为本 Claude session locked。
4. 使用 `git -C` 只读验证时，该目录确实解析为独立 worktree：
   - toplevel：该 worktree 目录。
   - git-dir：`.git/worktrees/o1-o3-trace-closure`。
   - common-dir：主仓库 `.git`。
   - status：clean，branch 为 `worktree-o1-o3-trace-closure`。
5. 随后尝试用 native tool 切入现有 worktree，但工具 schema 要求 `name`/`path` 二选一；多次错误地同时传入两个字段，均只产生 input-validation error，没有产生文件或 Git 修改。
6. 用户随后中断并要求生成交接文档。

下一个窗口必须先检查：

```text
git worktree list --porcelain
git status --short --branch
git rev-parse HEAD
```

处理原则：

- 不修改 Git config 来绕过 native guard。
- 不假设上述 worktree 在新会话仍 locked 或仍存在。
- 不自动删除用户或其他 Agent worktree。
- 这个 `o1-o3-trace-closure` worktree 是本会话创建且尚无代码修改，但删除前仍应重新确认它 clean、无独有 commit，并确认 session lock 状态。
- 如果 native tool 支持切入已有 worktree，调用时只传 `path`，不要传空 `name`，也不要同时传 `name` 和 `path`。
- 进入任何实施 worktree 后，必须确认它基于当前 main HEAD；当前记录的 `4a4ed30b` 不满足要求。
- 不得在旧 HEAD 上实施后把结果误称为 current-HEAD O3。

## 13. 本窗口明确未执行的动作

截至本文写入时：

- 没有修改 O1–O3 生产代码。
- 没有修改 O1–O3 测试。
- 没有运行 Task 1 RED tests。
- 没有启动 MySQL、Elasticsearch、embedding、reranker、Go server 或 Phoenix。
- 没有读取 DeepSeek API key。
- 没有调用模型。
- 没有运行真实 O3。
- 没有手工发送 span。
- 没有创建 O3 canonical artifact。
- 没有执行 `git add`、commit、push、PR、reset、checkout 或 clean。
- 没有删除 container、volume、index、MySQL row、MinIO object、model cache、historical artifact 或业务数据。

因此下一个窗口应从 **隔离环境恢复 + Task 1 TDD** 开始，而不是假设任何实施任务已经完成。

## 14. 下一个窗口的精确启动步骤

1. 完整读取第 1 节列出的两份 `CLAUDE.md`、权威设计地图、实施计划和本交接文档。
2. 重新获取：
   - `git status --short --branch`
   - `git rev-parse HEAD`
   - `git worktree list --porcelain`
3. 检查 `o1-o3-trace-closure` worktree 是否仍存在、clean、无独有 commit、是否仍 locked。
4. 使用 native worktree 机制建立或进入一个**基于当前 main HEAD** 的隔离环境；不得改 Git config 或破坏现有 WIP。
5. 进入隔离环境后，确认 Python/Go 依赖可用；不要无必要安装新依赖。
6. 运行与 Task 1 直接相关的 clean baseline，而不是立即跑真实 O3：
   - trace contract/capture/capability 当前测试。
   - 必要时 focused Go telemetry baseline。
7. 按本文 §9 的 Task 1 开始 TDD：先 RED tests，再最小实现，再 focused regressions。
8. 每个 task 结束：
   - 运行计划中指定的 focused tests。
   - 记录 `git diff --stat`。
   - 检查没有触碰 unrelated WIP。
   - 按项目 `CLAUDE.md` 的持久授权，每个有意义且验证通过的阶段可以自动 commit 并 push；只提交本任务改动，不混入 main 上已有的 unrelated WIP。
   - commit message 概括本阶段事实，并按项目要求附 `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`。
9. Tasks 1–9 全部 PASS 前，不读取 key、不启动真实 O3 acceptance run。
10. Task 10 前必须重新获得/确认真实运行授权，并执行环境/pin/data preflight；任何 mismatch 都 `BLOCKED`，不得修数据来制造 PASS。

## 15. 验证矩阵

| Gate | 需要的证据 | Required result |
|---|---|---|
| O1 contract | focused pytest：trace contract/capture/capability | v2 topology/privacy/status tests PASS；唯一 evaluator。 |
| O2 Python | headless bridge、strict runner、propagation tests | 真实 SearchKnowledge transport/evidence；O3 无 direct fallback。 |
| O2 Go | focused telemetry/handler/service tests | 真实 retrieve/embed/rerank spans、W3C parent、safe status/pins。 |
| Phoenix adapter | parser/filter/poll tests | exact run/instance selection、normalized allowlist、无 marker contract。 |
| Artifact gate | O3 finalization tests | failure artifacts 保留；checksum 最后写且立即验证；blocked exit 2。 |
| Regression | full Python + `go test ./... -count=1` | 两者实际 PASS；不能把环境 skip 说成 VERIFIED。 |
| Real acceptance | one fixed `trace_o3/go-q001` run | current-HEAD Phoenix-backed PASS + real official scorer + checksum-valid tree。 |

## 16. 安全、数据与发布边界

- 诚实评测，严禁 qrels/gold/定位提示泄漏。
- 模型调用测试只可从 `D:\Obsidian\code-autogrowth\项目进展\api-key.md` 安全读取 DeepSeek official key，模型固定 `deepseek-v4-pro`。
- Key 只进入进程环境，不打印、不写命令历史、不写 artifact/trace/docs、不硬编码、不提交。
- 仓库必须保持 PRIVATE；密钥轮换只能由密钥所有者执行。
- 不删除 Docker container/volume、ES index、MySQL/MinIO 数据、模型 cache、历史 artifact。
- Docker Desktop 可在确有需要时隐藏启动，但不包含任何删除/重建数据操作。
- 实际 index/pin 不匹配时 fail closed；不修数据来“修证据”。
- 所有 PDF 入口继续只使用 MinerU 显式 OCR；Tika 只限非 PDF Office 文档。
- GPT-5.6 Sol 复核只能标 `AI_REVIEWED` 或 `DISPUTED`，不得冒充真人复核。
- 16GB 主机并行重任务/Agent fan-out 上限为 2。
- 有意义进展需写入仓库 `docs/PROGRESS-2026-08-12.md`，并在权限可用时同步到 `D:\Obsidian\code-autogrowth\私人\localcode\PROGRESS-2026-08-12.md`。

## 17. 最终 acceptance 命令方向（仅 Task 10）

只有 Tasks 1–9 全部真实 PASS 且获得真实执行授权后，才能运行等价于：

```text
C:\Python312\python.exe -m eval.run \
  --benchmark trace_o3 \
  --model deepseek-v4-pro \
  --limit 1 \
  --output-dir eval_results \
  --base-url https://api.deepseek.com/v1
```

实际运行还必须带经过 preflight 的 OTLP/Phoenix project、Go internal-token 等环境配置，但不得把 secret 写进命令、日志或文档。

只有同时满足以下所有条件才能把 O1/O2/O3 标记为 `VERIFIED`：

- command exit 0。
- shared v2 O3 assertion 为 PASS。
- official scorer output 真实存在。
- Phoenix exact readback 与 normalized trace 匹配。
- `execute_tool SearchKnowledge -> rag.retrieve -> embedding/rerank` 真实 parentage 成立。
- scorer 是同 instance 的 sibling branch。
- privacy scan 无泄漏。
- canonical tree 完整。
- `RunArtifacts.verify_checksums()` 返回空问题列表。

任何一项失败都保留 checksum-valid failure artifact，并诚实标为 `BLOCKED` 或 `IMPLEMENTED`，不能宣称 closure。
