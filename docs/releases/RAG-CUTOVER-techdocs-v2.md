# RAG v2 Alias Cutover TechDocs（发布证据门）v2

> 适用范围：`knowledge_base`（legacy 文本索引）→ `knowledge_base_v2_bge_m3`（BGE-M3 原生 1024 维结构化语料）的原子 alias 切换。
> 配套脚本：`scripts/rag/preflight-cutover.ps1`、`scripts/rag/switch-alias.ps1`、`scripts/rag/rollback-alias.ps1`。
> 本 runbook 只做**证据门控的 alias 操作**，绝不删除任何物理索引。

## 不变式（所有阶段强制）

1. **数据只通过 alias 切换，不删物理索引。** `knowledge_base` 与 `knowledge_base_v2_bge_m3` 在稳定观察期结束前都保留；旧 `knowledge_base` 是只读回滚来源。
2. **每次 aliases API 调用必须是一次原子调用**（remove + add 在同一 body 内），与 `pkg/es/knowledge_index.go` 的 `SwitchAlias`/`RollbackAlias` 语义一致；绝不允许先 remove 后 add 的两步操作。
3. **发布被证据阻塞时立即停止**：preflight 任一 FAIL、contamination 存在高相似未审查项、切换后真实 E2E 失败 → 进入第 5 节回滚流程。
4. **所有输出保存为证据**：命令输出用 `Tee-Object` 落盘到 `results/releases/cutover-<YYYYMMDD-HHmmss>/`，包括 preflight 清单、switch/rollback 完整请求与响应、E2E 记录。
5. **任何删除操作（旧 index、旧备份、历史评测工件、Docker volumes）是独立破坏性任务，需要用户单独批准**，本 runbook 不执行。

## 1. 前置条件（Prerequisites）

切换前必须全部满足，缺一不可：

- [ ] **BGE-M3 revision 已锁定为真实不可变 commit。** `configs/server.yaml` 的 `embedding.model_revision` 当前是占位符 `BAAI/bge-m3@8f1b7f9d4c2a6e5b0d9c8f7a6b5c4d3e2f1a0b9c`，必须替换为下载并验证过的真实 HF commit；embedding 服务 `/health` 必须上报该 revision 与 `dimensions: 1024`，`pkg/embedding/preflight.go` 的 `ValidateEmbeddingContract` 通过。
- [ ] **v2 物理索引已创建。** `knowledge_base_v2_bge_m3` 存在，mapping 为 `KnowledgeV2Mapping(1024)`（`vector` 为原生 `dense_vector`，`dims: 1024`，`index: true`，`similarity: cosine`），无视觉向量字段。
- [ ] **语料已导入。** v2 索引 chunk 数 > 0；MySQL `knowledge_document` 有对应记录；MinIO `uploads` bucket 对象数可读；v2 中 distinct `document_id` 与 MySQL 文档数一致（无 orphan、无未导入源）。
- [ ] **contamination 报告干净。** `results/contamination/<日期>.jsonl` 已生成且**阻塞项为 0**（格式见 §2.4）。
- [ ] **检索门槛已通过。** `eval/retrieval/` 的 Recall@5 / MRR@10 / nDCG@10 报告达到既定门槛，且检索失败页/空结果/错误命中在允许范围内。
- [ ] **alias 当前状态符合预期。** `knowledge_base_current` 目前指向 `knowledge_base`（未切换过）。
- [ ] **工具链就绪。** Windows 10 1803+（含 `curl.exe`）；mysql 客户端可访问 `127.0.0.1:3306/codeagent`（只读 SELECT 即可）；`mc` 已配置只读别名（一次性操作，仅列对象不写）：`mc alias set localcode-preflight http://127.0.0.1:9000 minioadmin minioadmin`。

## 2. 发布前检查（Preflight）

### 2.1 命令

从仓库根目录执行（只读，不改任何数据；输出用 `Tee-Object` 落盘）：

```powershell
$cutoverDir = "results/releases/cutover-$(Get-Date -Format 'yyyyMMdd-HHmmss')"
New-Item -ItemType Directory -Force -Path $cutoverDir | Out-Null

powershell -ExecutionPolicy Bypass -File scripts/rag/preflight-cutover.ps1 `
    -ContaminationReport results/contamination/2026-08-02.jsonl `
    -EsBaseUrl http://127.0.0.1:9200 `
    -LegacyIndex knowledge_base `
    -V2Index knowledge_base_v2_bge_m3 `
    -ReadAlias knowledge_base_current `
    -ExpectedAliasTarget knowledge_base `
    -ExpectedVectorDims 1024 `
    -EmbeddingServiceUrl http://127.0.0.1:8009 `
    -PhoenixUrl http://127.0.0.1:6006 `
    -MySqlHost 127.0.0.1 -MySqlPort 3306 -MySqlUser codeagent `
    -MySqlPassword codeagent -MySqlDatabase codeagent `
    -MySqlCountQuery "SELECT COUNT(*) FROM knowledge_document" `
    -MinioAlias localcode-preflight -MinioBucket uploads `
    | Tee-Object -FilePath "$cutoverDir/preflight.log"
```

### 2.2 检查项（脚本逐项打印 PASS/FAIL/SKIP）

| 检查 | 说明 | 阻塞 |
|---|---|---|
| es cluster health | `GET /_cluster/health`，非 red | 是 |
| legacy index exists | `HEAD /knowledge_base` | 是 |
| v2 index exists | `HEAD /knowledge_base_v2_bge_m3` | 是 |
| v2 mapping/model | `GET /{v2}/_mapping`：`vector` 必须 `dense_vector` 且 `dims=1024`（`KnowledgeV2Mapping(1024)`） | 是 |
| alias state | `GET /_alias/knowledge_base_current` 必须指向 `knowledge_base`（已切换则 FAIL） | 是 |
| legacy es count / v2 es count | `GET /{index}/_count`；v2 必须 ≥ `-MinEsChunkCount`（默认 1） | 是 |
| mysql count | 只读 `SELECT COUNT(*) FROM knowledge_document`（命令可用 `-MySqlClient`/`-MySqlClientExtraArgs` 自定义，例如 docker wrapper） | 是 |
| minio count | `mc ls --recursive --json <alias>/uploads`（`-MinioCountCommand` 可自定义；`-SkipMinio` 可显式跳过） | 是 |
| orphan check | v2 中 distinct `document_id`（size 上限 20000 的 terms 聚合，GET with body，经 `curl.exe`）与 MySQL 文档数一致性 | 是 |
| embedding model | `GET http://127.0.0.1:8009/health`：`dimensions=1024` 且 `model_revision` 与锁定 revision 一致 | 是 |
| contamination | 解析 `-ContaminationReport`（JSONL），见 §2.4 | 是 |
| metrics/trace | `GET http://127.0.0.1:6006`（Phoenix）；不可达时 **SKIP（优雅降级，不阻塞）** | 否 |

### 2.3 退出码

- `0`：所有检查 PASS（允许 SKIP）→ `PREFLIGHT PASSED`，可进入第 3 节演练。
- `1`：存在 FAIL → `PREFLIGHT FAILED: N failing check(s). Cutover is BLOCKED.`，**不得切换**；修复后重跑并保存新证据。

### 2.4 Contamination 报告格式（JSONL，每行一个对象）

```json
{"id": "chunk-1a2b", "source": "techdocs/…/file.pdf", "similarity": 0.91, "reviewed": false}
```

- 字段：`id`（可选）、`source`（可选）、`similarity`（0–1）、`reviewed`（bool）。
- **阻塞规则**：任意行满足 `similarity >= -ContaminationSimilarityThreshold`（默认 0.85）且 `reviewed != true` → preflight FAIL。
- 该格式由 Task 9.1 的 `eval/contamination/scanner.py` 产出，只隔离和报告，不自动删除任何数据。

## 3. 测试 alias 演练（switch → rollback）

在**测试 alias**（`knowledge_base_cutover_drill`，应用从不读取）上用真实物理索引演练一次完整 switch + rollback，保存完整请求/响应。演练不改动生产 alias `knowledge_base_current`。

### 3.1 Switch（drill）

```powershell
powershell -ExecutionPolicy Bypass -File scripts/rag/switch-alias.ps1 `
    -Alias knowledge_base_cutover_drill `
    -Target knowledge_base_v2_bge_m3 `
    -NoPrevious `
    -EsBaseUrl http://127.0.0.1:9200 `
    | Tee-Object -FilePath "$cutoverDir/drill-switch.log"
```

> `-NoPrevious` 表示“无 previous 目标”，即只做 add（新 alias 演练场景）。不要写成 `-Previous @()`：经 `powershell -File` 从 PowerShell 控制台调用时，空数组/空字符串实参会被吞掉导致 `MissingArgument`。`-Previous ''` 在 bash/cmd 直调时可用，但统一推荐 `-NoPrevious`。

预期输出要点：`=== REQUEST ===` 打印完整请求体（`POST http://127.0.0.1:9200/_aliases`，body 为单个 `{"actions":[{"add":{"index":"knowledge_base_v2_bge_m3","alias":"knowledge_base_cutover_drill"}}]}`）；`=== RESPONSE ===` 打印 `HTTP 200` 与 `{"acknowledged":true}`；结尾 `VERIFIED: knowledge_base_cutover_drill now points at knowledge_base_v2_bge_m3`；`SWITCH OK.` 退出码 0。

### 3.2 Rollback（drill，逆操作）

```powershell
powershell -ExecutionPolicy Bypass -File scripts/rag/rollback-alias.ps1 `
    -Alias knowledge_base_cutover_drill `
    -Target knowledge_base `
    -Current @("knowledge_base_v2_bge_m3") `
    -EsBaseUrl http://127.0.0.1:9200 `
    | Tee-Object -FilePath "$cutoverDir/drill-rollback.log"
```

预期输出要点：REQUEST 为单个 `{"actions":[{"remove":{"index":"knowledge_base_v2_bge_m3","alias":"knowledge_base_cutover_drill"}},{"add":{"index":"knowledge_base","alias":"knowledge_base_cutover_drill"}}]}`；RESPONSE `HTTP 200` + `{"acknowledged":true}`；`ROLLBACK OK.` 退出码 0。

### 3.3 演练验收

- [ ] 两份日志都包含完整 REQUEST 与 RESPONSE，且 switch 与 rollback 的 body 互为逆操作。
- [ ] 演练前后 `GET /_alias/knowledge_base_current` 的目标不变（始终 `knowledge_base`）。
- [ ] drill alias 最终指向 `knowledge_base`（无害；如需移除该测试 alias，可另行执行一次 add/remove 的 aliases 调用——仍是 alias 级操作，不影响任何物理索引；可选且需在证据日志中记录）。

## 4. 生产切换（单次原子调用 + 真实 Agent E2E）

### 4.1 切换前确认

- 第 2 节 preflight 全部 PASS 且证据已保存；第 3 节演练成功且证据已保存。
- 操作者（用户）已明确批准本次生产切换。

### 4.2 切换（仅一次调用）

```powershell
powershell -ExecutionPolicy Bypass -File scripts/rag/switch-alias.ps1 `
    -Alias knowledge_base_current `
    -Target knowledge_base_v2_bge_m3 `
    -Previous @("knowledge_base") `
    -EsBaseUrl http://127.0.0.1:9200 `
    | Tee-Object -FilePath "$cutoverDir/switch-prod.log"
```

- 请求体必须是**单个** `{"actions":[{"remove":{"index":"knowledge_base","alias":"knowledge_base_current"}},{"add":{"index":"knowledge_base_v2_bge_m3","alias":"knowledge_base_current"}}]}`——remove 与 add 在同一个 body 中原子生效。
- 预期 `HTTP 200` + `{"acknowledged":true}` + `VERIFIED: knowledge_base_current now points at knowledge_base_v2_bge_m3`，退出码 0。
- 若退出码非 0 或响应非 2xx：**未发生任何变更**（原子调用整体失败），立即进入第 5 节回滚流程。

### 4.3 切换后立即执行真实 Agent E2E（中文查英文）

切换成功后**立刻**（不等待任何窗口）执行真实检索链路验证：以中文 query 查询英文 techdocs 语料，必须命中 v2 语料的英文文档。

1. 运行真实 RAG E2E runner（MinerU 解析 + ingest + SearchKnowledge）：

   ```powershell
   powershell -ExecutionPolicy Bypass -File scripts/rag/rag-agent-e2e.ps1 `
       | Tee-Object -FilePath "$cutoverDir/e2e-real.log"
   ```

2. 追加一次中文查英文的定向检索：调用 Go 服务内部检索端点（如 `POST /internal/orchestrator/knowledge-search`，query 例如「codeagent 的上传文件解析流程是什么」——英文语料、中文提问），断言返回的命中来自 `knowledge_base_v2_bge_m3`（响应中的 `target_index`/`model_version` 为 v2 值），并保存完整请求/响应与检索结果。
3. E2E 记录保存到 `$cutoverDir/e2e-zh-en.log`，包含退出码、命中 chunk 的 `document_id`、`target_index`。

**通过标准**：真实 E2E 退出码 0，中文查英文命中 v2 索引且结果非空。任一失败 → 立即进入第 5 节回滚。

## 5. 回滚流程（inverse aliases API）

### 5.1 触发条件（满足其一立即回滚）

- 切换调用失败（非 2xx / 退出码非 0）。
- 切换后真实 Agent E2E 失败或中文查英文检索为空/命中错误索引。
- 切换后观察到的错误率、延迟或检索指标明显劣于 legacy 基线。

### 5.2 回滚命令（单次原子调用，与切换互为逆操作）

```powershell
powershell -ExecutionPolicy Bypass -File scripts/rag/rollback-alias.ps1 `
    -Alias knowledge_base_current `
    -Target knowledge_base `
    -Current @("knowledge_base_v2_bge_m3") `
    -EsBaseUrl http://127.0.0.1:9200 `
    | Tee-Object -FilePath "$cutoverDir/rollback-prod.log"
```

- 请求体：`{"actions":[{"remove":{"index":"knowledge_base_v2_bge_m3","alias":"knowledge_base_current"}},{"add":{"index":"knowledge_base","alias":"knowledge_base_current"}}]}`。
- 预期 `HTTP 200` + `{"acknowledged":true}` + `VERIFIED: knowledge_base_current now points at knowledge_base`，退出码 0。
- 回滚后重跑第 4.3 节的 E2E 验证（应命中 legacy 索引）。

### 5.3 回滚边界（强制）

- **绝不删除 `knowledge_base_v2_bge_m3`**：它仍是已验证的候选索引与未来重试来源。
- **绝不删除 `knowledge_base`**：它是当前回滚目标。
- **绝不删除** MySQL 行、MinIO 对象、备份、模型缓存或历史评测工件。
- 回滚只是 aliases API 的逆操作；数据双向保留，随时可再次切换（需重新走完整证据门）。

## 6. 稳定观察期与旧资产清理

### 6.1 观察期（切换成功后）

建议观察期 **≥ 7 天**，期间持续记录（每日快照到 `results/releases/cutover-<ts>/observe/`）：

- 检索质量：Recall@5 / MRR@10 / nDCG@10 不低于 legacy 基线；空结果与错误命中率不劣化。
- 服务健康：P95 检索延迟、错误率、Phoenix 中 Go root / retrieval / embedding / rerank / LLM spans 连续可用（Phoenix 不可达期间记录降级状态，不阻塞观察）。
- 数据一致性：无新增 orphan（v2 distinct `document_id` 与 MySQL 文档数保持一致）。
- 真实 Agent E2E 抽查（含中文查英文）保持通过。

### 6.2 观察期结束后的旧资产清理（需单独批准）

观察期结束、指标达标后，**旧资产清理仍是一次独立的破坏性任务**：

- 候选清单（**默认全部保留**）：`knowledge_base` 旧索引、`knowledge_base_cutover_drill` 测试 alias、旧备份与历史评测工件、Docker volumes、模型缓存。
- 任何删除必须：由**用户单独明确批准**（本 runbook 的批准不构成清理批准）→ 形成新的破坏性任务计划 → 执行时再跑一次 preflight 确认 `knowledge_base_current` 已稳定指向 v2 → 执行并在证据台账中记录。
- 未获批准前，一律只切 alias、不删数据；回滚总边界见计划文档「回滚总边界」一节。

## 附录 A：脚本参数速查（与代码实现一一对应）

| 脚本 | 参数 | 默认值 | 说明 |
|---|---|---|---|
| preflight-cutover.ps1 | `-ContaminationReport` | （必填） | 污染报告 JSONL 路径 |
| | `-EsBaseUrl` / `-LegacyIndex` / `-V2Index` / `-ReadAlias` / `-ExpectedAliasTarget` | `http://127.0.0.1:9200` / `knowledge_base` / `knowledge_base_v2_bge_m3` / `knowledge_base_current` / `knowledge_base` | ES 端点与索引/alias 命名 |
| | `-ExpectedVectorDims` | `1024` | `KnowledgeV2Mapping` 期望维度 |
| | `-EmbeddingServiceUrl` / `-ExpectedModelRevision` | `http://127.0.0.1:8009` / 锁定 BGE-M3 revision | embedding 契约检查 |
| | `-MySqlHost` / `-MySqlPort` / `-MySqlUser` / `-MySqlPassword` / `-MySqlDatabase` / `-MySqlClient` / `-MySqlClientExtraArgs` / `-MySqlCountQuery` | 本机 codeagent 库 / `mysql` / 空 / `SELECT COUNT(*) FROM knowledge_document` | 只读 SELECT 计数，命令可配置 |
| | `-MinioEndpoint` / `-MinioAlias` / `-MinioBucket` / `-MinioCountCommand` / `-SkipMinio` | `http://127.0.0.1:9000` / `localcode-preflight` / `uploads` / `mc` / 关 | 只读列对象计数 |
| | `-PhoenixUrl` | `http://127.0.0.1:6006` | 指标/trace，不可达时 SKIP |
| | `-ContaminationSimilarityThreshold` | `0.85` | 阻塞阈值 |
| switch-alias.ps1 | `-Alias` / `-Target` / `-Previous` / `-NoPrevious` / `-EsBaseUrl` | — / — / `@("knowledge_base")` / 关 / `http://127.0.0.1:9200` | 单次原子 remove+add；`-NoPrevious` 用于新 alias 演练（只 add） |
| rollback-alias.ps1 | `-Alias` / `-Target` / `-Current` / `-EsBaseUrl` | — / — / `@("knowledge_base_v2_bge_m3")` / `http://127.0.0.1:9200` | 单次原子 remove+add（逆操作） |

## 附录 B：证据台账清单

| 证据 | 文件 |
|---|---|
| preflight 清单（全部 PASS） | `$cutoverDir/preflight.log` |
| 演练 switch 完整请求/响应 | `$cutoverDir/drill-switch.log` |
| 演练 rollback 完整请求/响应 | `$cutoverDir/drill-rollback.log` |
| 生产 switch 完整请求/响应 | `$cutoverDir/switch-prod.log` |
| 真实 Agent E2E + 中文查英文 | `$cutoverDir/e2e-real.log`、`$cutoverDir/e2e-zh-en.log` |
| 回滚（如发生）完整请求/响应 | `$cutoverDir/rollback-prod.log` |
| 观察期每日快照 | `$cutoverDir/observe/` |
