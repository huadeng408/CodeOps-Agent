# SWE-bench 双臂报告门禁设计

日期：2026-08-11  
状态：已批准、已实现、已用冻结双臂 artifact 复验（VERIFIED）

## 1. 背景与问题

本轮 astropy-20 双臂实验已有三份报告脚本：

- `eval/swebench_work/verify_arms.py`：确认 A/B 臂身份；
- `eval/swebench_work/compare_arms.py`：配对结果；
- `eval/swebench_work/mechanism_report.py`：搜索、编辑、交付和定位机理。

独立审计与真实中间产物复现确认：当前三份脚本各自按目录 mtime 发现输入，并各自决定“是否可以读”。因此出现了一个结构性旁路：`verify_arms.py` 对仍在运行的 B 臂正确返回 exit 2 后，另外两份脚本仍会重新选择同一个中间 run、返回 exit 0 并输出暂态 totals。该行为会让报告取决于读取时刻，而不是冻结实验。

同时确认以下口径缺陷：

1. `verify` 与 `compare/mechanism` 没有共享、可校验的输入身份；
2. B 臂只需至少一条 localization/mandate 即可能通过，而不是要求所有源实例都有接线证据；
3. dev-side contaminated 实例 `astropy__astropy-12907` 只从 paired verdict 排除，仍进入 empty patch、edit 和 localization hit@k；
4. failure 分类在报告层被统称为 infrastructure，丢失 `scorer/agent/budget/timeout` 语义；
5. 报告脚本不验证 canonical artifact 是否 finalized、完整且 checksum 匹配。

审计中“baseline 的 `failures.jsonl` 未被 checksum 覆盖”这一条经实际文件核对不成立：A 臂 `checksums.sha256` 第 3 行已包含它。本设计不以该错误事实为依据，但仍要求所有报告依赖文件通过完整 checksum 验证。

## 2. 目标与非目标

### 2.1 目标

- 三份报告只读取同一组、显式锁定、已经 finalized 且 checksum-valid 的 A/B run。
- `verify` 成为 `compare` 与 `mechanism` 不可绕过的门禁，而不是依赖人工执行顺序。
- 保留设计地图 §32.5 的既定实验口径：源 cohort 固定为 astropy-20；少量 scorer 无 verdict 时，按双方都有官方 verdict 的 eligible 交集配对；单臂 `failed-scorer > 3` 时整臂重跑。
- 污染排除、failure taxonomy 和所有分母在三份报告中一致。
- 输出可复现：receipt 锁定 run identity 和文件哈希，后续文件变化会令报告 fail closed。

### 2.2 非目标

- 不修改 benchmark、runner、driver、模型 prompt 或官方 scorer。
- 不重写现有 A/B artifact。
- 不将 astropy-20 结果包装成 SWE-bench Verified 分数。
- 不引入数据库、外部服务或新依赖。
- 不把 measured 集合不完全相等本身定义为失败；它与设计地图允许的 scorer transport flake 相冲突。

## 3. 方案选择

采用“共享 finalized-run 门禁 + verification receipt”。

未采用的方案：

- 三个脚本各自加固：改动略小，但三份完成态、checksum、污染和 taxonomy 逻辑仍会漂移。
- 只加总控脚本：无法阻止原脚本被单独调用并输出无效 totals。

## 4. 架构

新增轻量共享模块：

`eval/swebench_work/report_gate.py`

职责仅限报告输入，不参与实验执行：

1. 解析显式 A/B run；
2. 验证 run 完成态与 canonical artifact；
3. 验证 checksums；
4. 加载冻结 subset cohort；
5. 统一污染排除与 failure 分类；
6. 生成和验证 verification receipt。

三份报告脚本不得自行按 mtime 发现 run。它们只调用共享模块。

### 4.1 默认 run 与 CLI 覆盖

本次实验默认值固定为：

- baseline：`swebench-deepseek-v4-pro-a5cb0378`
- optimized：`swebench-deepseek-v4-pro-eea6403e`

CLI 支持显式覆盖 `--baseline-run` 和 `--optimized-run`，供未来实验复用。run 参数是 arm 目录下的 basename，不接受越过 arm 根目录的路径。

### 4.2 FinalizedRun

共享模块返回一个不可变的 `FinalizedRun` 数据对象。不可变性必须递归成立：`frozen=True` 之外，公开 mapping 使用只读 mapping（如 `MappingProxyType`），序列容器使用 tuple/frozenset，嵌套 JSON 对象也必须冻结；调用者不能在 receipt 验证后原地改写 manifest、outcomes 或 failure taxonomy。对象至少包含：

- arm、run_id、run_dir；
- manifest、summary；
- manifest SHA-256、checksums 文件 SHA-256；
- source cohort IDs 与 cohort SHA-256；
- instances、predictions、failures；
- measured official verdicts；
- report outcomes（官方 verdict + 非 scorer 终态失败的 `False`）；
- unmeasured_by_reason（仅 scorer 无 verdict）；
- failures_by_reason（完整当前 failure taxonomy）；
- scorer evidence scope（当前为 `last-invocation-only`）；
- excluded_contaminated；
- eligible IDs。

调用者不再自行读取同一批 JSONL。

## 5. Finalized artifact 门禁

一个 run 只有满足以下全部条件才可进入 receipt：

1. 目录位于预期 arm 根目录，run 目录本身必须是真实目录而非 symlink/reparse point，run_id 精确匹配；summary/manifest/span assertion 与所有 trace span 的非空 `eval.run_id` 必须等于该显式 basename；
2. 存在 `summary.json`、`run-manifest.json`、`checksums.sha256`，且 required/pinned 文件必须是 run 目录内的真实 regular file，不接受 symlink；
3. 报告层先严格解析 `checksums.sha256`：digest 必须是 64 位小写十六进制，路径必须是安全的 POSIX 相对路径，不得重复、绝对、含反斜杠或 `..`，不得出现指向 `checksums.sha256` 自身的条目，所有 required pinned 文件恰好出现一次；随后 `RunArtifacts.verify_checksums()` 必须返回空列表；
4. 必需文件存在且被 pin（`checksums.sha256` 自身只要求存在，不能 self-pin）：
   - `instances.jsonl`
   - `predictions.jsonl`
   - `events.jsonl`
   - `failures.jsonl`（允许 0 字节，但文件必须存在）
   - `summary.json`
   - `run-manifest.json`
   - `traces/trace-summary.json`
   - `traces/span-assertion.json`
   - scorer 原始证据文件；
5. 当前四个固定名 scorer 文件会被每次实例评分覆盖，只代表**最后一次 scorer invocation 的 pinned raw evidence**，不得声称覆盖 20 个 source ID；逐实例守恒依赖 pinned predictions/events/failures；
6. summary 与 manifest summary 的 total/completed/failed/skipped 一致；
7. 冻结 subset 精确包含 20 个唯一 ID；instances/predictions/failures/events 中出现的 ID 都必须属于 source cohort。instance row 数量按 append-only attempt 守恒：每次 `completed` 或 `failed-scorer` attempt 各写一条，scorer retry 可使同一 source ID 出现多条；resume-skip 不新增 row，runner 在 `record_instance()` 前发生的非 scorer failure 可没有 row；
8. events/failures 按 append-only retry/resume 历史核对，最后 terminal event 决定当前状态，summary counts 与每个 source ID 的最后 terminal event 守恒；
9. 每个 source instance 必须恰好归入：官方 verdict outcome、非 scorer 的 `False` report outcome、或 scorer-only unmeasured，不能静默消失；非 scorer failure 不得伪装成官方 `resolved=False`，但也不得移出固定分母；
10. 单臂 scorer 无 verdict 数不超过 3；超过则门禁失败并要求整臂重跑；
11. trace assertion 必须是可接受的完成态，且 trace 中所有非空 `eval.instance_id` 属于 source cohort；每个 source ID 恰好有一个 `eval.instance` span。不得强求现有 scorer-error span 带 `eval.instance_status`：当前 runner 只会通过异常上下文把该 span 的 OTel status 设为 `ERROR`。不能把运行中尚未写 trace 的状态当作零调用。

完成态校验不依赖目录 mtime、进程名或“文件暂时存在”。

## 6. Arm identity 与泄题门禁

`verify_arms.py` 基于 `FinalizedRun` 做逐 source-ID 验证：

- baseline：manifest `enabled=false`；20 个 source ID 均不得有 localization 或 mandate，且 scorer retry 产生的每条 instance row 也必须保持无 uplift。runner 在 `record_instance()` 前发生的非 scorer failure 可以没有 instance row，此时对该 source ID 仍视为“无 uplift 证据”，但该 failure 作为 `False` report outcome 留在分母；
- optimized：manifest `enabled=true`，必须覆盖 20 个 source ID，且每条记录的 attempt row（包括 scorer retry 的失败 attempt）都有 localization record 和 mandate；缺任一 source ID/row/evidence 均不能证明 optimized identity；
- 每个 localization record 均执行现有 answer-field key/value 泄漏检查；
- manifest components 与 tool_rounds 必须符合 arm 规格；
- 任一实例缺接线证据，整臂失败。

这将“至少一条 evidence”收紧为“完整 source cohort evidence”。

## 7. Verification receipt

默认输出：

`eval_results/harness-uplift-20260810/verified-arms.json`

receipt 原子写入，至少包含：

- schema_version；
- verdict=`VERIFIED`；
- baseline / optimized run_id；
- 两臂 manifest SHA-256；
- 两臂 checksums.sha256 自身 SHA-256；
- cohort path、cohort SHA-256、20 个 source IDs 与 eligible IDs；
- contaminated ID 与原因；
- official verdicts、report outcomes、scorer-only unmeasured_by_reason、完整 failures_by_reason；每条 current failure 同时保存归一化 bucket 与原始 `failures.jsonl.category`，detail 只保存 terminal status 与原始 failure message 的 SHA-256，不复制可能含临时路径或敏感异常文本的 message；
- scorer evidence scope=`last-invocation-only`；
- arm identity audit 结果；
- receipt payload SHA-256 或等价自校验字段。

receipt 不复制模型答案、patch 或数据集 answer fields。

`compare_arms.py` 和 `mechanism_report.py` 默认要求该 receipt；也支持 `--verified-arms` 显式路径。使用时重新：

1. 解析 receipt；
2. 重新定位两臂 run；
3. 重算 manifest/checksum/cohort 哈希；
4. 重新跑严格 checksum manifest parser 与现有 checksum verifier；
5. 逐项比较 source/eligible IDs、official verdicts、outcomes、scorer-only unmeasured、failure taxonomy/details、contamination、scorer evidence scope；
6. 确认 arm identity verdict 仍为 VERIFIED。

任一步失败，脚本非零退出，且不写 JSON 输出。

## 8. 统计口径

### 8.1 Cohort 层级

报告必须显式区分：

- source cohort：固定 20；
- eligible cohort：固定 19；
- official measured per arm：有官方 verdict 的 eligible 条目；
- report outcomes per arm：official verdict 加非 scorer 终态 `False`，不含 scorer-only unmeasured；
- paired outcomes：两臂 eligible report outcomes 的交集；
- paired official-verdict-only：可选诊断，不得替代 failure-inclusive paired result。

`12907` 可以出现在 raw diagnostics，但不得进入任何 headline score 或 mechanism 指标。

### 8.2 配对结果

- headline 仍写“astropy-20 subset 上 k/20”，numerator 只取 eligible true outcomes；必须同时标出 1 个 contaminated exclusion、scorer-unmeasured 数和非 scorer failure taxonomy，不能把无 verdict 当 false；
- 非 scorer 的 agent/budget/timeout/oom/infra/cancelled/skipped/unknown 终态失败写入 report outcomes 为 `False`，留在固定 source/headline 分母和配对 outcome 分母，但不得冒充 official scorer verdict；
- paired outcome 分母是两臂 eligible outcomes 的交集，只有 scorer-only unmeasured 会从该交集消失；可以另列 official-verdict-only diagnostic，但不得取代 failure-inclusive paired result；
- McNemar 只报告 failure-inclusive paired outcomes 的两个 discordant counts，不报 p-value；
- 若 scorer failure 阈值超限，receipt 无法生成，因此 compare 不运行。

### 8.3 Failure taxonomy

保留 `failures.jsonl.category` 原值并归一化到至少以下分组。原始 category 必须进入 receipt-safe failure record（与 bucket 分字段保存），不能只留下归一化后的 `unknown`：

- scorer
- agent
- budget
- timeout
- oom
- infra
- cancelled/skipped
- unknown

> **2026-08-11 计划审查后更正：** 本节原文只写“`error_ids` / scorer callback exception 属于 unmeasured”，但未明确其他 failure 的分母语义。权威设计地图 §20.1(7)/§20.2 要求失败、超时、跳过、缺失实例进入分母，因此本规格明确：**只有 scorer 无 verdict 是 unmeasured；其他终态 failure 是非官方的 `False` report outcome，保留在 source/headline 与 paired outcome 分母。**

`error_ids` / scorer callback exception 属于 unmeasured scorer，不得转成 `resolved=False`；agent/budget/timeout 不得统称 infrastructure，也不得从 report outcome 分母消失。

### 8.4 Mechanism 统计

以下全部只从 eligible cohort 计算：

- search calls、edit calls、search:edit；
- instances traced、instances with edits、max chat round；
- empty/non-empty patches；
- localization rankings、judgeable、hits、misses、hit@1、hit@3、mean rank；
- missed instance IDs。

若 trace 文件不存在或 receipt 不成立，不能用 `0:0` 代替未知，必须门禁失败。

## 9. CLI 行为

### verify_arms.py

- 输入：默认固定 run 或显式 run 参数；
- 成功：打印 arm audit，写 receipt，exit 0；
- 失败：打印阻断原因，不写/不更新 receipt，exit 2。

### compare_arms.py / mechanism_report.py

- 输入：receipt 路径；
- 成功：输出文本，可选写 JSON，exit 0；
- 失败：只输出 `BLOCKED` 与原因，不输出 totals，不写 JSON，exit 2。

现有无参数顺序保持：

```text
python eval/swebench_work/verify_arms.py
python eval/swebench_work/compare_arms.py
python eval/swebench_work/mechanism_report.py
```

但后两条现在真正依赖第一条产生的 receipt。

## 10. 测试策略

先写失败测试，再实施：

1. 中间 run 有 predictions 但无 summary/manifest/checksum：三脚本拒绝；
2. run 目录是指向同 arm sibling 的 symlink/reparse point：即使 embedded run ID 与链接 basename 一致也拒绝；
3. verify exit 2 后 compare/mechanism 不输出 totals 且 exit 非零；
4. 较新的错误 run 或 discarded run 不能靠 mtime 被选中；
5. receipt 锁定的 run 被替换、manifest/checksum/cohort hash 变化：拒绝；
6. receipt 内 normalized outcomes/failures/contamination/evidence-scope 被修改且 payload hash 被重算：仍拒绝；
7. optimized 只有 1/20 localization 或 mandate：拒绝；
8. baseline 缺 manifest：拒绝；
9. checksum missing/mismatch/unpinned/duplicate/bad digest/absolute/traversal/self-pin：拒绝；
10. summary/manifest 相互一致但与最后 terminal events 不守恒、`total != 20`、或任一非空 trace span 的 `eval.run_id` 不匹配：拒绝；
11. `FinalizedRun` 的嵌套 manifest/outcomes/failure records 不可原地修改；
12. `12907` 不影响 empty patch、edit、search、judgeable、hit@1、hit@3、mean rank、headline numerator 或配对分母；
13. failure taxonomy 保留 scorer/agent/budget/timeout；每条 failure 同时保留原始 category 和归一化 bucket，非 scorer failure 作为 `False` outcome 留在分母但不冒充 official verdict；
14. 只有 scorer 无 verdict 的实例进入 unmeasured_by_reason，不能静默消失；
15. 单臂 scorer failure ≤3 时 receipt 可生成并按 eligible outcome 交集配对；>3 时拒绝；
16. 四个固定名 scorer raw 文件只标 `last-invocation-only`，不得据此声称 20 个实例均有 raw scorer evidence；
17. trace 缺失不能渲染 `0:0`；
18. happy path receipt 可复验，JSON/文本结果可重复生成；
19. 输出仍明确“NOT a SWE-bench Verified score”，无 p-value。

最终运行 focused tests、`tests/eval` 回归、`git diff --check`、`git diff --stat`，并由独立 reviewer 检查门禁是否仍可旁路。

## 11. 状态口径与文档更新

修复完成后：

- 报告门禁代码和测试：`VERIFIED`（focused receipt/report suite：142 passed、13 skipped）；
- completed A/B artifacts 已由严格门禁加载，`verified-arms.json` 已原子生成，并由 consumer 重新加载 artifact、重跑 checksum/identity/normalized receipt comparison 后精确复验；
- canonical 最终证据为 `verified-arms.json`、`paired-comparison-final.json`、`mechanism-report-final.json`；早期同名无 `-final` 的 comparison/mechanism JSON 是中间快照，不作最终引用；
- `eea6403e` 已 finalized 且 arm identity 通过：20/20 instance attempt rows 均有 localization 与 mandate；完整 `tests/eval` 回归为 983 passed、13 skipped、1 个既有 SQLAlchemy warning；
- C3 validation/retry 是否接到真实 runner 路径是独立问题，按调用链审计仍为 `IMPLEMENTED`、非 `VERIFIED`，不因本报告修复而改变状态。
