# O3 跨来源多实例真实 Receipt（2026-08-14）

## 本次目标

将仅覆盖一个 Go 问题的 O3 开发 smoke 扩展为三个不同公开来源的真实实例，验证
Harness、SearchKnowledge、真实 embedding/rerank、逐实例 scorer 和 Phoenix trace
在同一 run 中不会串实例。该工作不修改 queries、qrels、阈值、索引 alias 或模型提示。

## 实现

- `eval/benchmarks/trace_o3.py` 固定实例集为：
  - `trace-o3/go-q001`
  - `trace-o3/dk-q001`
  - `trace-o3/kb-q001`
- 三个实例分别来自 Go、Docker、Kubernetes 的公开 TechDocs query/qrels；agent 只收到
  问题和“先使用 SearchKnowledge”的要求，仍不会看到 document ID、section path、qrels 或
  scorer 规则。
- `load_instances()` 拒绝重复和未知 instance ID；子集选择保留调用方指定顺序。
- scorer 从当前 instance ID 导出 query ID，并且只读取该 query 的 qrels；每个实例写入独立
  `scorer/o3-techdocs-<query-id>-score.json`，不会覆盖其他实例的原始 scorer 输出。
- `eval.run_o3` 在 manifest 中显式记录固定实例集和 `model_concurrency: 1`。`HarnessRun`
  以顺序循环运行实例，因此本次及默认 O3 receipt 的模型并发为 1，低于中转站上限 10。

## 测试与真实验收

代码提交：`551ffc7e feat: run O3 receipt across distinct sources`

TDD 证据：新测试先因单实例硬编码、未知/重复实例未受支持、第二实例 scorer 被拒绝而失败；
最小参数化实现后，通过：

```text
C:\Python312\python.exe -m pytest \
  tests\eval\test_trace_o3_benchmark.py \
  tests\eval\test_run_o3.py \
  tests\eval\test_trace_contract.py \
  tests\eval\test_phoenix_readback.py \
  tests\eval\test_harness_trace_artifacts.py \
  tests\eval\test_rerank_span.py -q
67 passed
```

真实命令：

```text
.\scripts\run-o3-receipt.ps1 -OutputDir eval_results/o3 -StartupTimeoutSeconds 120
```

真实 artifact：`eval_results/o3/trace-o3-e6d455e0`

| 验收项 | 观察结果 |
|---|---|
| Git SHA | `551ffc7ea636e66e5de376e8ea8a0b287d02a589` |
| 模型 / 并发 | `gpt-5.6-sol` / 1 |
| 实例 | Go、Docker、Kubernetes 各 1 个，`completed=3`，`failed=0` |
| scorer | 三个独立原始输出；三个 verdict 均为 `retrieved_relevant_document` |
| 检索 | 每个实例的首个相关文档分别属于 Go、Docker、Kubernetes 对应 source |
| Trace | Phoenix API readback，33 spans，唯一 trace ID，三个 instance ID 都被 join |
| Contract | shared trace contract v2：`PASS`，无 problems、无 error spans |
| Artifact | checksum 重新验证返回 `[]` |
| 清理 | receipt 后端口 `8081` 无 listener，短生命周期 Go server 不再运行 |

真实运行前的只读服务检查：Phoenix `/healthz` 为 200；reranker `/health` 为
`ready=true`；`knowledge_base_current` 唯一解析到 `knowledge_base_v2_bge_m3`。

## 诚实边界和新发现

- 本 receipt 是 `non_release_dev_smoke`，不是 hidden holdout、发布级多数据集分数、
  人工复核或模型身份不可变证明。manifest 仍正确标记 `MODEL_IDENTITY_UNVERIFIED`。
- 三个问题和 qrels 已作为开发样本公开给实现者，不能被转述为未见测试集结果。
- 原始 Go 服务 stdout 日志会记录完整 `knowledge-search` request/response，其中可能包含
  query 与文档正文；这些日志没有进入 receipt artifact 或 Git，但与 trace/artifact 的
  内容最小化原则不一致。后续应在不改变检索结果的前提下，收紧生产 HTTP request logging
  的 body capture，并先用失败测试覆盖“认证/诊断仍可用、正文不落日志”。

## 四条主线快照

- 自研 Harness：81% `[########--]`。真实 O3 已从单实例扩展到跨来源三实例；仍缺 release
  级多 benchmark 固定样本和长期/故障恢复统计。
- 多模态 RAG：64% `[######----]`。MinerU + 显式 OCR 与文本检索链可用；仍缺视觉 index、120
  多模态 qrels、bbox/ViDoRe bake-off。
- 评测集：60% `[######----]`。本次增加真实跨来源开发 receipt；仍缺 disputed qrels 的独立
  Sol 双轮复核、真人复核和发布级污染隔离。
- 可观测性：84% `[########--]`。真实 Phoenix 多实例 parent/join、rerank/scorer 链已验收；
  仍缺长时指标/告警和生产 HTTP 日志内容最小化。

## 后续优先级

1. 以 TDD 收紧 `knowledge-search` 的原始 request/response body 日志，确保不把 query 或正文
   写进生产日志，同时保留可操作的状态、时延、run/instance 诊断。
2. 推进多模态视觉索引、120 条 qrels 和 ViDoRe/bbox bake-off；PDF 路由继续仅使用 MinerU +
   显式 OCR，Tika 仅用于非 PDF Office 文档。
3. 对评测集运行 GPT-5.6 Sol 两个独立 pass 的拟人工复核，结果只能标为 `AI_REVIEWED` 或
   `DISPUTED`，不得冒充人工复核。
