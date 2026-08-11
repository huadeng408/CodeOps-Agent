# SWE-bench 双臂报告门禁设计

日期：2026-08-11  
状态：已批准，待实现

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

共享模块返回一个不可变的 `FinalizedRun` 数据对象，至少包含：

- arm、run_id、run_dir；
- manifest、summary；
- manifest SHA-256、checksums 文件 SHA-256；
- source cohort IDs 与 cohort SHA-256；
- instances、predictions、failures；
- measured verdicts；
- unmeasured_by_reason；
- excluded_contaminated；
- eligible IDs。

调用者不再自行读取同一批 JSONL。

## 5. Finalized artifact 门禁

一个 run 只有满足以下全部条件才可进入 receipt：

1. 目录位于预期 arm 根目录，run_id 精确匹配；
2. 存在 `summary.json`、`run-manifest.json`、`checksums.sha256`；
3. `RunArtifacts.verify_checksums()` 返回空列表；
4. 必需文件存在且被 pin：
   - `instances.jsonl`
   - `predictions.jsonl`
   - `events.jsonl`
   - `failures.jsonl`（允许 0 字节，但文件必须存在）
   - `summary.json`
   - `run-manifest.json`
   - `traces/trace-summary.json`
   - `traces/span-assertion.json`
   - scorer 原始证据文件；
5. summary 与 manifest summary 的 total/completed/failed/skipped 一致；
6. `instances.jsonl` 的 ID 集合与冻结 subset 的 20 条 ID 完全一致且无重复；
7. prediction、failure 和 event 中的 ID 都属于 source cohort；
8. 每个 source instance 必须恰好归入 measured verdict 或 unmeasured failure，不能静默消失；
9. 单臂 scorer 无 verdict 数不超过 3；超过则门禁失败并要求整臂重跑；
10. trace assertion 必须是可接受的完成态，不能把运行中尚未写 trace 的状态当作零调用。

完成态校验不依赖目录 mtime、进程名或“文件暂时存在”。

## 6. Arm identity 与泄题门禁

`verify_arms.py` 基于 `FinalizedRun` 做逐实例验证：

- baseline：manifest `enabled=false`、20/20 无 localization、20/20 无 mandate；
- optimized：manifest `enabled=true`、20/20 有 localization record、20/20 有 mandate；
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
- cohort path、cohort SHA-256、20 个 source IDs；
- contaminated ID 与原因；
- measured IDs、unmeasured_by_reason；
- arm identity audit 结果；
- receipt payload SHA-256 或等价自校验字段。

receipt 不复制模型答案、patch 或数据集 answer fields。

`compare_arms.py` 和 `mechanism_report.py` 默认要求该 receipt；也支持 `--verified-arms` 显式路径。使用时重新：

1. 解析 receipt；
2. 重新定位两臂 run；
3. 重算 manifest/checksum/cohort 哈希；
4. 重新跑 checksum verifier；
5. 确认 arm identity verdict 仍为 VERIFIED。

任一步失败，脚本非零退出，且不写 JSON 输出。

## 8. 统计口径

### 8.1 Cohort 层级

报告必须显式区分：

1. **source cohort**：冻结 astropy-20，共 20 条；
2. **eligible cohort**：source cohort 去掉 contaminated `12907`，共 19 条；
3. **measured per arm**：该臂有官方 verdict 的 eligible 条目；
4. **paired measured**：两臂 measured 交集。

`12907` 可以出现在 raw diagnostics，但不得进入任何 headline score 或 mechanism 指标。

### 8.2 配对结果

- headline 仍写“astropy-20 subset 上 k/20”，但必须同时标出 unmeasured 数，不能把无 verdict 当 false；
- 独立性更强的比较使用 paired measured 分母，并列出 A-only/B-only unmeasured 原因；
- McNemar 只报告两个 discordant counts，不报 p-value；
- 若 scorer failure 阈值超限，receipt 无法生成，因此 compare 不运行。

### 8.3 Failure taxonomy

保留 `failures.jsonl.category` 原值，输出至少分组：

- scorer
- agent
- budget
- timeout
- oom
- infra
- cancelled/skipped
- unknown

`error_ids` / scorer callback exception 属于 unmeasured scorer，不得转成 `resolved=False`；agent/budget/timeout 不得统称 infrastructure。

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
2. verify exit 2 后 compare/mechanism 不输出 totals 且 exit 非零；
3. 较新的错误 run 或 discarded run 不能靠 mtime 被选中；
4. receipt 锁定的 run 被替换、manifest/checksum/cohort hash 变化：拒绝；
5. optimized 只有 1/20 localization 或 mandate：拒绝；
6. baseline 缺 manifest：拒绝；
7. checksum missing/mismatch/unpinned：拒绝；
8. `12907` 不影响 empty patch、edit、search、judgeable、hit@1、hit@3、mean rank 或配对分母；
9. failure taxonomy 保留 scorer/agent/budget/timeout；
10. 无 verdict 的实例进入 unmeasured_by_reason，不能静默消失；
11. 单臂 scorer failure ≤3 时 receipt 可生成并按 measured 交集配对；>3 时拒绝；
12. trace 缺失不能渲染 `0:0`；
13. happy path receipt 可复验，JSON/文本结果可重复生成；
14. 输出仍明确“NOT a SWE-bench Verified score”，无 p-value。

最终运行 focused tests、`tests/eval` 回归、`git diff --check`、`git diff --stat`，并由独立 reviewer 检查门禁是否仍可旁路。

## 11. 状态口径与文档更新

修复完成后：

- 报告门禁代码和测试：`IMPLEMENTED`；
- 只有用 completed A/B artifacts 实际生成并复验 receipt 后，才可标 `VERIFIED`；
- 当前 `eea6403e` 仍在运行时不得出最终 paired、k/20 或 hit@k；
- C3 validation/retry 是否接到真实 runner 路径是独立问题，按调用链审计结论记录，不因本报告修复而改变状态。
