# 面试 Portfolio：一次真实 SWE-bench 跑通，挖出十一个缺陷

> 状态：**非定稿。三个 Agent 基准仍无可发布分数。**本文档的价值不在覆盖面，在于一条被证据钉住的缺陷猎捕链。
> 分支 `push-clean`。**本文全部证据基线为 commit `092692f8`（2026-08-10）**；写作期间 `8577fd48` 已落地并有 O1/O2 未提交改动进入工作树，均未触及本文所引产物（见 §5「生产 trace instrumentation」行）。唯一权威口径：`docs/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md`（§20.4 / §20.7 / §20.8 / §22–§26 / §30 / §31）。本文与设计地图冲突时以设计地图为准。
> 状态词只用 `DESIGNED` / `IMPLEMENTED` / `VERIFIED` / `BLOCKED`（§2 定义）。测试存在 ≠ `VERIFIED`。

## 1. 一句话

把**一条** SWE-bench 实例的官方评分从头驱动到真出判定，过程中挖出 **11 个缺陷**，其中 **8 个是同一个形状**：

> **一次基础设施故障，被当成一条业务判定报了出来。**
> 于是「什么都没测到」和「Agent 没修对」在产物里长得一模一样。

再加 2 个「判定没有原始证据」的同族缺陷，和 1 个普通缺陷。这套评测管线此前从未真正评过一次分——**而它每次都以 exit 0 告诉我一切正常。**

这就是我想在面试里讲的东西：不是「模型能拿多少分」，是**「一个数字凭什么算证据」**。

## 2. 那一次跑通：三个产物构成的受控对照

同一个实例、同一个 506 字节 patch，只动一个变量，三份产物都在磁盘上：

| 产物目录 | 官方 stdout 关键行 | `resolved` | harness 自报 |
|---|---|---|---|
| `h5-smoke-20260810-014712/…-369d7a52` | scorer 根本没跑（`requests.exceptions.ConnectionError`，exit 1） | `False` | **`ok:1 / failed:0`** |
| `h5-smoke-20260810-122909/…-c09bbb49` | `completed: : 0` / `empty patches: 1` | `False` | `ok:1`（patch 为空） |
| `h5-smoke-20260810-123749/…-bfad8522` | `Instances with errors: **1**` | `False` | `ok:1`（patch 506B） |
| **`h5-full-20260810/…-3b0569f2`** | `Instances with errors: **0**` | **`True`** | `ok:1` |

第 1 行是本项目最难看也最重要的一条证据：**scorer 抛了 ConnectionError、一个字节都没测，产物里写着 `ok:1`、进程退 0。** 第 3 → 第 4 行是 `namespace` 修复的受控前后对照——patch 一字未改，`errors: 1` 变成 `errors: 0`，官方判定从没有变成 `True`。

官方 scorer 的原始结论（`scorer/report.json`，逐字节存盘并入 checksums）：

```json
"astropy__astropy-12907": {
  "patch_successfully_applied": true,
  "resolved": true,
  "tests_status": { "FAIL_TO_PASS": {"success": 2, "failure": 0},
                    "PASS_TO_PASS":  {"success": 13, "failure": 0} }
}
```

Agent 只拿到 problem statement，输入里没有 gold patch，自己产出了正确修复（506 字节）：

```diff
--- a/astropy/modeling/separable.py
+++ b/astropy/modeling/separable.py
@@ -242,7 +242,7 @@ def _cstack(left, right):
         cright = np.zeros((noutp, right.shape[1]))
-        cright[-right.shape[0]:, -right.shape[1]:] = 1
+        cright[-right.shape[0]:, -right.shape[1]:] = right
```

**这条能说什么**：统一 Harness 的 `prepare → solve → score` 生命周期在一个真实实例上跑到了官方判定，`FAIL_TO_PASS` 2/2、`PASS_TO_PASS` 13/13。
**这条不能说什么**：n=1，不是能力分数；这条实例在修 `namespace` 的过程中被反复使用，已属 **development set**，永久不得进 holdout。

## 3. 十一个缺陷，一个形状

| # | 缺陷 | 为什么它是「基础设施故障伪装成业务判定」 | 状态 |
|---|---|---|---|
| 1 | 两处评分调用点都硬写 `namespace=None`，覆盖上游默认值 `"swebench"` | `None` = 本地构建每个镜像；本地构建要在构建容器内 `git clone` 项目**全部历史**。astropy 的克隆真的连上 GitHub、流了 10 分钟，死在 `curl 92 HTTP/2 stream 0 was not closed cleanly: CANCEL`。**镜像没建成 → 判定不存在 → 记为「没修好」。** 官方评分在第一个真正到达它的实例上必然失败 | `VERIFIED` |
| 2 | scorer 崩溃后仍记 `ok:1`、进程退 0 | 一次未测量的运行报告成功。已改为抛 `OfficialScorerUnavailable` 走 `ERROR_SCORER`，而不是把 `resolved:False` 并进 prediction | `VERIFIED` |
| 3 | 裸 `text=True` 让 scorer stdout 按 locale（gbk）解码 | reader thread 在 byte `0x93` 抛 `UnicodeDecodeError` 后**死掉**，`communicate()` 返回残缺缓冲，调用方收不到任何异常。落盘的「判定」是 `official: resolved=False (… : 0`——`Instances resolved` 这个标签被吃掉了。**一个编码故障把自己伪装成了评分结论** | `VERIFIED` |
| 4 | `--dry-run` 在 pin preflight **之前**返回，打印 `manifest validated` | 对 pin 完全缺失的 benchmark 也退 0。**一个不可能失败的 preflight 不是 preflight**——人们用来确认「我配好了吗」的唯一命令，恰恰是检测不出配错的那条 | `VERIFIED` |
| 5 | 官方原始输出从来不是发布证据 | 真报告留在 `logs/run_evaluation/`，**在产物树外、因而在 `checksums.sha256` 外**；判定只以 harness 自己那条被截断的转述存在 | `VERIFIED`（本轮真实 run 产出 `scorer/` 4 文件并入 pin） |
| 6 | `error` 字段记的是「模型还在用工具」，不是「出错了」 | `done.success` 回答的是「模型自己干净收尾了吗」，工具轮次用尽也记 `False`。于是产物自相矛盾：`error='runner completed with done.success=False'` 与 `resolved=True`、`ok:1` 并存 | `VERIFIED`（代码已修 + 12 tests） |
| 7 | 下游 scorer 谓词把**正确** patch 记为 unresolved 并标 `ERROR` | `run_swebench_honest_10.py:246` 用 `r.get("model_patch") and not r.get("error")` 当 resolved。缺陷 6 一污染 `error`，这条正确 patch 就被记成失败。**该脚本任何历史 10 实例数字都按轮次耗尽的条数低估** | `VERIFIED`（该脚本未跟踪，属探索期产物） |
| 8 | harness 转述结构性有损：`detail = result.stdout[-200:]` | 200 字符尾切片会从中间切断官方 summary。修掉编码故障后，`h5-full` 的 `scorer_status` 里 `Instances resolved` **仍然缺失**。判定之所以可信，是因为 `_read_official_resolution()` 读官方 `report.json`，**不是**因为这条转述——这正说明转述永远不能当证据 | `VERIFIED`（本轮新发现，**未修**） |
| 9 | 关键 pin 为空值仍能通过 preflight | 本轮 `run-manifest.json` 的 `model_revision`、`prompt_hash`、`qrels_hash`、`physical_index` **全是空字符串**，preflight 照样放行。所以这次 run 依然带着 `MODEL_IDENTITY_UNVERIFIED` | `VERIFIED`（本轮新发现，**未修**） |
| 10 | 污染扫描的取数故障映射成「发现污染」 | 形状不符时 `AttributeError` 逃到解释器 → 退出码 1 = 冻结 policy 的 `BLOCKING` = **「发现了污染」**。同一形状的第 8 例。已改为显式验形 + rc 2（取数故障） | `VERIFIED`（设计地图 §30.1） |
| 11 | `numpy` 被两个模块 import 却从未声明 | 普通缺陷，不属本形状。已补进 `eval` extra | `VERIFIED` |

**计数口径**：11 条里 **8 条**（1、2、3、4、6、7、9、10）是「基础设施故障 → 业务判定」；**2 条**（5、8）是「判定没有原始证据」；**1 条**（11）是普通缺陷。第 8、9 条是本轮新发现且**尚未修复**，写在这里是因为未修的已知缺陷也是证据。

## 4. 为什么这个形状值得当作方法论

这 8 条缺陷分布在 4 个不同模块、由 4 个人在 4 个时间点写下，却收敛到同一条错误假设：

> **「拿到了返回值」被当成「测量成功」。**

每一条单独看都像小问题（少传一个参数、少写一个 `encoding=`、字段名起得不好）。合起来的后果是：**这套评测系统在长达数周里，每次都以 exit 0 报告一切正常，而它一次都没有真正评过分。** 而且失败方向是一致的——`resolved=False`、`ERROR`、`0/N`——所以它看起来像「模型不行」，恰好是最容易被接受、最不会被追查的那种结论。

我从这里得出的三条工程结论：

1. **判定必须来自官方原始产物，不能来自任何转述。**缺陷 3 和 8 是同一条经验的两次学费：转述会被编码搞坏，也会被切片搞丢。现在 `resolved` 只从官方 `report.json` 读，原始输出逐字节落 `scorer/` 并进 `checksums.sha256`。
2. **fail-closed 必须区分「测不了」和「测了但没过」。**这两者一旦同型，所有的坏消息都会被读成模型能力问题。
3. **preflight 必须能失败。**`--dry-run` 和空值 pin（缺陷 4、9）说明：一个只会通过的检查，比没有检查更糟——它提供虚假保证。

## 5. 现在能证明什么、不能证明什么

| 项 | 事实证据 | 状态 |
|---|---|---|
| SWE-bench 单实例官方判定 | `h5-full-20260810/…-3b0569f2`：`resolved=True`，`FAIL_TO_PASS` 2/2、`PASS_TO_PASS` 13/13，506B patch，instance wall time 116.25s，tokens 76,493/5,725 | `VERIFIED`（n=1，非分数） |
| `scorer/` 原始输出落盘并 pin | 4 文件（`report.json` 2,165B / `run-summary.json` 17,749B / `run_instance.log` 4,403B / `test_output.txt` 302,799B）；`checksums.sha256` 覆盖全树 10 文件；`verify_checksums()` 返回空列表 | `VERIFIED` |
| 缺陷 1/2/3/4/6/10/11 修复 | 各自 focused tests + 上述受控产物；Python 全量 **805 passed, exit 0**（65.76s，实测于 HEAD `092692f8`，在下方 O1/O2 未提交改动落入工作树**之前**） | `VERIFIED` |
| 缺陷 5 落盘实现 | 由本轮真实 run 产出，不再只是「若存在则会被哈希」 | `VERIFIED` |
| 缺陷 8/9 | 本轮新发现，代码未改 | `IMPLEMENTED` 之前的阶段：仅 `DESIGNED`（已定位，未修） |
| **H5 官方 1 实例门禁** | 门禁要求单产物**同时**含 manifest / prediction / official score / **trace** / checksums。前 4 项已由真实 run 满足（manifest 存在但关键 pin 为空，见缺陷 9），**`traces/` 子树无人写** | **`BLOCKED`（5 项中 4 项）** |
| 生产 trace instrumentation（O1–O3） | **工作树内正在进行**（未提交）：`eval/harness/trace_contract.py`、`trace_capture.py` + 3 个测试文件为未跟踪新增，`runner.py`/`artifacts.py` 有未提交改动（`SPAN_SCORER_OFFICIAL`、`record_trace()`）。**但至今没有任何 run 产出过 `traces/` 目录** | 进行中，未验收 |
| SWE-bench 10 实例 | 10/10 非空 patch，`resolved` 未知；官方评分从未在这 10 条上完成 | 探索结果，非分数 |
| Terminal-Bench 4 实例 | 官方 Harness 总结果 `0/4`；最新单任务 `0/1 test_timeout` | `IMPLEMENTED`（heredoc/base64 修复未验收） |
| tau2-bench 10 实例 | `avg_reward=0.7`；前 5 题经 prompt tuning（0.4→1.0→0.7）= development-set contamination；缺 pins/provenance | 探索结果，非分数 |
| RAG 三路检索 | nDCG@10 0.5201 / 0.6547 / 0.5636，数值健全（nDCG 全落 [0,1]） | **不可发布**，见 §6 |
| qrels 仲裁 | 23 `AI_REVIEWED` / 157 `DISPUTED` / **0 `HUMAN_REVIEWED`** | `IMPLEMENTED`（仲裁逻辑已修正） |
| 四层污染扫描 | 24,822 chunks × 180 queries 四层全跑完、0 命中，verdict `CLEAN`，退出码 0 捕获，71.75s < 600s SLA；层 4 经 4,477,860 对独立复算 max 0.795592 < 0.8 | `VERIFIED`（设计地图 §30） |
| Phoenix | 6006 + OTLP 4317/4318 可达；trace 为五 span 合成、无 RAG span | 基础连通，生产 E2E 未验证 |
| 多模态 RAG（M1–M5） | `data/eval/multimodal` 只有 `README.md` | `DESIGNED`，未开始 |
| dev/holdout 防火墙（E3） | `split-manifest.v1.json`：`dev_size=180`、`holdout_size=0`、`holdout_status=BLOCKED` | `BLOCKED` |
| 统一 Harness 旁路 | legacy `benchmark_mod.run()` 旁路已删除并经 AST 断言（0 call node） | `IMPLEMENTED` |
| DeepSeek key 历史 | **已公开**（见 §7） | `BLOCKED`（需密钥所有者轮换） |

## 6. RAG 数字为什么不可发布（此前的口径是错的）

本文档旧版把三行 RAG 指标标为「可重算探索实验」。**这个限定词是误导性的**：它把问题说成「还没锁定、重算一下就好」，而真实问题是**标签本身没有经过仲裁**。

机检结论（`data/eval/techdocs/reports/e5-release-report.json`，退出码 **3**）：

```
verdict: NOT_RELEASE_ELIGIBLE
TOO_MANY_DISPUTED            157/180 = 0.872 disputed > allowed 0.2
GOLDEN_SET_TOO_SMALL         23 arbitrated rows < required 60
GOLDEN_FRACTION_TOO_LOW      23/180 = 0.128 < required 0.33
GOLDEN_SET_ALL_POSITIVE      all 23 arbitrated rows are positive
SOURCE_COVERAGE_INCOMPLETE   no arbitrated rows for: docker, kubernetes
PURE_NEGATIVE_SET_UNREVIEWED all 17 pure-negative rows are unarbitrated
```

| Method | Recall@5 | MRR@10 | nDCG@10 | 口径 |
|---|---:|---:|---:|---|
| BM25 | 0.5500 | 0.4169 | 0.5201 | **不可发布**：金标底座 87.2% 未仲裁 |
| BGE-M3 | 0.6667 | 0.5453 | 0.6547 | **不可发布**：同上 |
| Hybrid RRF | 0.6167 | 0.4093 | 0.5636 | **不可发布**：同上 |

具体地说：**180 条 query 里 157 条（87.2%）的相关性标签从未被裁定**，23 条已裁定的**全是正例、0 负例**——因此 precision 与 false-positive rate 在这个集合上**根本无定义**；`docker`、`kubernetes` 两个来源一条金标都没有。在这样的底座上算出的 nDCG，无论重算多少次都稳定地重现同一个不可信的数。发布级报告必须建立在锁定金标集之上（设计地图 §20.6.3 E5）。

另外两条口径必须说清：

- **`AI_REVIEWED` 不是人工复核。**它是离线重仲裁的机器一致（六个 bool 维度两轮全 True、`contamination=none`、`min(confidence) ≥ 0.7`）。复核器**不产生真人 `reviewer_hash`，永不签发 `HUMAN_REVIEWED`**。
- **复核模型身份未验证。**请求名配的是 `gpt-5.6-sol`，但 `revision=unknown`、未存响应侧身份、`OPENAI_BASE_URL` 指向 DeepSeek。**不能声称由 GPT-5.6 Sol 完成复核。**

**待人工复核**：157 行仲裁工作表（`data/eval/techdocs/review/disputed-worksheet.jsonl`，157 行）+ 23 条 `AI_REVIEWED` 的分层抽检。这两件事没有真人参与就永远停在这里。

## 7. API Key：已经公开，轮换是唯一补救（口径修正）

本文档旧版写「key 为公司提供不可轮换，因此目标是**永不在 GitHub 暴露**」。**这个前提已被证伪**——它此刻**已经公开**：

| commit | key 在树中 | 已在 `origin/main` |
|---|---|---|
| `f278daee`（`eval/swebench_solve_light.py`） | 是 | **是 —— 已公开** |
| `721e63b7`（`eval/swebench_work/run_swebench_honest_v2.py`） | 是 | 否 |

泄露面也比早先记录的大：本地 `main` 上 **6 个** commit 的 diff 触及 key（旧记录 3 个），`push-clean` 上 4 个。三处 key-shaped token 的 SHA-256 与在用的公司 key **摘要一致**（只比对摘要，全程未打印任何值）。`origin` 是公开仓库。

**重写历史救不回已公开的密钥**：GitHub 保留推送过的 commit 对象，悬空 commit 仍可按 SHA 访问，fork/clone/扫描器可能已取走。**唯一真实补救是吊销并轮换**，只能由密钥所有者走公司流程，且不该等面试之后。存在「删除 key 的 commit」这件事本身，恰恰证明其父 commit 的树里有 key。

当前工作树侧：11 个 `eval/swebench_work/*.py` 全部改为 `os.environ["DEEPSEEK_API_KEY"]` fail-closed，无真实 key 默认值，工作树 secret scan 0 残留。历史重写需用户对受影响 refs 单独明确批准，并先建私有备份 ref/bundle。

## 8. 架构与诚实评测 SOP

```
eval/run.py（唯一入口：budget / resume / artifacts / pin preflight）
  └─ HarnessRun ─ adapter.prepare → solve → score ─ 官方 runner/scorer
       ├─ SWE-bench（WSL2 Docker，官方 swebench.harness.run_evaluation）
       ├─ Terminal-Bench（Docker/tmux，terminal_bench.Harness）
       └─ tau2-bench（API tool-call，tau_bench.run.run）
  产物：run-manifest / instances / predictions / events / scorer/ / checksums.sha256
```

反作弊原则（`CLAUDE.md` 与设计地图 §20.2）：

1. Agent 只获得与真实场景一致的输入：问题描述 + 代码仓库。Prompt 不含文件路径、修复方向、答案提示。
2. 所有评分走官方 scorer，不接受自评；嵌入 gold patch 的连通性 smoke 必须单独标注，不得计入能力。
3. synthetic 数据显式顶层标记，不混入 official。
4. 每个 scorer 输出逐字节保存原文，不解包后重新打分。
5. 失败分类必须区分基础设施故障 / 模型失败 / 超时——这正是本文 §3 那 8 条缺陷的根本教训。

工程细节（跨平台 harness 的真实成本）：Windows Docker 路径 5 层 monkey-patch（`put_archive` / `exec_run` / `send_keys` / `copy_to_container` / `PurePosixPath`）；WSL2 内 swebench 无法直连 Docker Hub → curl-through-proxy；tmux `send_keys` 阻塞超时 → 非阻塞 + 预热；`send_keys` 把 heredoc 拆成逐行发送破坏 shell 语法 → base64 传输。

## 9. 证据索引

| 路径 | 内容 |
|---|---|
| `eval_results/h5-full-20260810/swebench-deepseek-v4-pro-3b0569f2/` | 本文核心产物：10 文件全 pin，`verify_checksums()` 干净 |
| └ `scorer/report.json` | 官方逐实例判定 `resolved: true` |
| └ `scorer/test_output.txt` | 容器内真实 pytest 输出，302,799 B |
| └ `run-manifest.json` | `git_sha=dd62f972` + `dirty_hash`；`model_revision`/`prompt_hash` 为空（缺陷 9） |
| `eval_results/h5-smoke-20260810-014712/…-369d7a52/` | **scorer 未运行却 `ok:1`** 的原始证据 |
| `eval_results/h5-smoke-20260810-122909/…-c09bbb49/` | 编码故障 `completed: : 0` 的原始证据 |
| `eval_results/h5-smoke-20260810-123749/…-bfad8522/` | 修复前 `Instances with errors: 1`（同一 506B patch） |
| `eval_results/h5-smoke-20260810-121356/…-b5fe347d/` | fail-closed 正确工作：`ok:0 / failed:1` + `failures.jsonl` |
| `deepseek-v4-pro.h5-rescore-namespace-v1.json` | 单变量重评分：`resolved_instances=1`、`error_instances=0` |
| `data/eval/techdocs/reports/e5-release-report.json` | RAG 发布资格机检：`NOT_RELEASE_ELIGIBLE`，rc=3 |
| `data/eval/techdocs/review/disputed-worksheet.jsonl` | 157 行待人工仲裁 |
| `data/eval/techdocs/splits/split-manifest.v1.json` | `holdout_size=0`、`holdout_status=BLOCKED` |

复核命令：

```bash
PYTHONPATH=. python -m pytest -q              # 805 passed, exit 0
PYTHONPATH=. python -c "from eval.harness.artifacts import RunArtifacts; \
  print(RunArtifacts('swebench-deepseek-v4-pro-3b0569f2','eval_results/h5-full-20260810').verify_checksums())"
```

今日提交：`dd62f972`（修 5 个缺陷）、`66c6dfa2`（真实 run + `scorer/` 落盘）、`092692f8`（`error` 语义）、`f31db059`（并回 `origin/main`，避免强推丢掉 `06e4d38c` 的 terminalbench +169 行）。

## 10. 下一步（顺序由设计地图 §20.8 决定，不得跳 Gate）

1. **O1/O2 生产 instrumentation**（工作树内进行中，未提交）→ 目标是让真实 run 写出 `traces/`，解除 H5 的最后一项（当前 5 项中 4 项）。**代码存在不等于门禁通过**：必须有一次真实 run 产出 `traces/trace-summary.json` + `span-assertion.json` 并进 checksums。扩规模排在门禁之后：H5 未过就把实例从 1 提到 50，只会放大不可信的数字。
2. **修缺陷 8、9**：`scorer_status` 停止承担证据职责；空值 pin 必须让 preflight 失败。
3. **E2 模型身份**：响应侧 provider/model/revision 落盘，解除 `MODEL_IDENTITY_UNVERIFIED`。
4. **人工复核**：157 行 DISPUTED + 23 条 `AI_REVIEWED` 分层抽检。没有真人参与，RAG 永远不出发布数字。
5. **E3 holdout**：`holdout_size=0` 必须解除；被 tuning 过的 tau2 前 5 题与 astropy-12907 永久留在 dev。
6. **key 轮换**（最高优先级，与工程无关）：已公开，只有所有者能吊销。
7. M1–M5 多模态、E6 统一重跑：门禁通过后再启动。

---

**面试口径提醒**：本文档没有一个可发布的基准分数。可以拿出来讲的是 `h5-full-20260810` 这一份产物、它前面三份失败产物构成的受控对照、以及那 8 条同形缺陷。任何超出这个范围的数字，请当场说明它是探索结果。
