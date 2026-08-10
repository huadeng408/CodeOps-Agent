# 面试 Portfolio：一次真实 SWE-bench 跑通，挖出二十一个缺陷

> 状态：**非定稿。三个 Agent 基准仍无可发布分数。**本文档的价值不在覆盖面，在于一条被证据钉住的缺陷猎捕链。
> 分支 `main`。**证据基线 commit `4a4ed30b`（2026-08-10）**，`push-clean` 已合入 main 并推送。唯一权威口径：`docs/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md`（§20.4 / §20.7 / §20.8 / §22–§26 / §30 / §31）。本文与设计地图冲突时以设计地图为准。
> 状态词只用 `DESIGNED` / `IMPLEMENTED` / `VERIFIED` / `BLOCKED`（§2 定义）。测试存在 ≠ `VERIFIED`。

## 1. 一句话

把**一条** SWE-bench 实例的官方评分从头驱动到真出判定，过程中挖出 **13 个缺陷**，其中 **10 个是同一个形状**：

> **一次基础设施故障，被当成一条业务判定报了出来。**
> 于是「什么都没测到」和「Agent 没修对」在产物里长得一模一样。

再加 2 个「判定没有原始证据」的同族缺陷，和 1 个普通缺陷。这套评测管线此前从未真正评过一次分——**而它每次都以 exit 0 告诉我一切正常。**

这个形状还有一个镜像版本，是在修契约时才看清的：**一个 PASS 不可达的门禁同样不携带信息。**SWE-bench 不做检索，却被无条件要求 `rag.retrieve`/`embedding`，于是它能拿到的最好结果永远是 `INCOMPLETE`——判定恒定，观察它就什么也没说。「不可能失败的 preflight 不是 preflight」和「不可能通过的门禁不是门禁」，是同一条原则的两面。

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

## 3. 二十一个缺陷，三个形状

| # | 缺陷 | 为什么它是「基础设施故障伪装成业务判定」 | 状态 |
|---|---|---|---|
| 1 | 两处评分调用点都硬写 `namespace=None`，覆盖上游默认值 `"swebench"` | `None` = 本地构建每个镜像；本地构建要在构建容器内 `git clone` 项目**全部历史**。astropy 的克隆真的连上 GitHub、流了 10 分钟，死在 `curl 92 HTTP/2 stream 0 was not closed cleanly: CANCEL`。**镜像没建成 → 判定不存在 → 记为「没修好」。** 官方评分在第一个真正到达它的实例上必然失败 | `VERIFIED` |
| 2 | scorer 崩溃后仍记 `ok:1`、进程退 0 | 一次未测量的运行报告成功。已改为抛 `OfficialScorerUnavailable` 走 `ERROR_SCORER`，而不是把 `resolved:False` 并进 prediction | `VERIFIED` |
| 3 | 裸 `text=True` 让 scorer stdout 按 locale（gbk）解码 | reader thread 在 byte `0x93` 抛 `UnicodeDecodeError` 后**死掉**，`communicate()` 返回残缺缓冲，调用方收不到任何异常。落盘的「判定」是 `official: resolved=False (… : 0`——`Instances resolved` 这个标签被吃掉了。**一个编码故障把自己伪装成了评分结论** | `VERIFIED` |
| 4 | `--dry-run` 在 pin preflight **之前**返回，打印 `manifest validated` | 对 pin 完全缺失的 benchmark 也退 0。**一个不可能失败的 preflight 不是 preflight**——人们用来确认「我配好了吗」的唯一命令，恰恰是检测不出配错的那条 | `VERIFIED` |
| 5 | 官方原始输出从来不是发布证据 | 真报告留在 `logs/run_evaluation/`，**在产物树外、因而在 `checksums.sha256` 外**；判定只以 harness 自己那条被截断的转述存在 | `VERIFIED`（本轮真实 run 产出 `scorer/` 4 文件并入 pin） |
| 6 | `error` 字段记的是「模型还在用工具」，不是「出错了」 | `done.success` 回答的是「模型自己干净收尾了吗」，工具轮次用尽也记 `False`。于是产物自相矛盾：`error='runner completed with done.success=False'` 与 `resolved=True`、`ok:1` 并存 | `VERIFIED`（代码已修 + 12 tests） |
| 7 | 下游 scorer 谓词把**正确** patch 记为 unresolved 并标 `ERROR` | `run_swebench_honest_10.py:246` 用 `r.get("model_patch") and not r.get("error")` 当 resolved。缺陷 6 一污染 `error`，这条正确 patch 就被记成失败。**该脚本任何历史 10 实例数字都按轮次耗尽的条数低估** | `VERIFIED`（该脚本未跟踪，属探索期产物） |
| 8 | harness 转述结构性有损：`detail = result.stdout[-200:]` | 200 字符尾切片会从中间切断官方 summary。修掉编码故障后，`h5-full` 的 `scorer_status` 里 `Instances resolved` **仍然缺失**。判定之所以可信，是因为 `_read_official_resolution()` 读官方 `report.json`，**不是**因为这条转述——这正说明转述永远不能当证据 | `VERIFIED`（**已修**：`_summarise_official_stdout()` 逐条提取官方 7 个计数标签，原始 stdout 落 `scorer/`） |
| 9 | 关键 pin 为空值仍能通过 preflight | 本轮 `run-manifest.json` 的 `model_revision`、`prompt_hash`、`qrels_hash`、`physical_index` **全是空字符串**，preflight 照样放行。所以这次 run 依然带着 `MODEL_IDENTITY_UNVERIFIED` | `VERIFIED`（**已修**：`pin_contract.py` 三档 REQUIRED / CAPABILITY / DEGRADABLE，空值 pin 不再放行） |
| 10 | 污染扫描的取数故障映射成「发现污染」 | 形状不符时 `AttributeError` 逃到解释器 → 退出码 1 = 冻结 policy 的 `BLOCKING` = **「发现了污染」**。同一形状的第 8 例。已改为显式验形 + rc 2（取数故障） | `VERIFIED`（设计地图 §30.1） |
| 11 | `numpy` 被两个模块 import 却从未声明 | 普通缺陷，不属本形状。已补进 `eval` extra | `VERIFIED` |
| 12 | 契约把 `invoke_agent`/`execute_tool` 归给 `go-agent`，而这条路径根本没有 Go agent | headless 驱动在 Python 进程内执行 Read/Write/Edit/Bash/Glob/Grep。缺失时报 `go-agent`，会把下一个人指去启一个这条路径永不联系的服务——**producer 字段本身就是为消除「不可行动判定」而存在的，它却给出了不可行动的判定**。同时暴露一个更隐蔽的诱惑：把 Go agent 做成可声明能力就能让判定变绿，而那会豁免掉唯一能证明工具真的执行过的 span。修法只能是去真实站点补 span | `VERIFIED`（`4a673590`，真实 run 判定 `PASS`） |
| 13 | `core.autocrlf=true` 改写字节，被记成「策略被篡改」 | `data/` 下 25 个 JSON/JSONL 的**字节就是证据**：策略由 `.sha256` 旁挂文件钉住，queries/qrels 字节写在 `contamination-policy.v1.json` 里。Windows 检出把 LF 换成 CRLF 后，测试报 `POLICY_HASH_MISMATCH` 和 `bytes changed since the freeze`——读起来是「有人动了金标」，真相是「Git 换了行尾」。合并分支时一次性触发 **78 个测试失败，全部同一个原因**。更糟的是同一份策略内部就不自洽：split-policy 按 LF 钉、queries/qrels 按 CRLF 钉，说明这些 pin 记录的是**某台机器上 Git 过滤器的产物**，而非提交的字节，在 Linux CI 上必然失败 | `VERIFIED`（`736e1964`，`.gitattributes` + 重钉 + 29 个回归测试） |

| 14 | 官方 scorer 的 `run_id` 只按实例命名（`f"swebench-{instance_id}"`），**跨 run 恒定** | 官方 harness 用 `run_id` 派生 `logs/run_evaluation/<run_id>/…`，于是同一实例的每次 run **共用同一棵日志树**。一个 **0 字节 patch 的实例读到 8.4 小时前另一次 run 的 `report.json`，记为 `resolved=True`**——而同一条记录里的官方 summary 明写 `empty_patch_ids: [12907]`、`resolved_ids: []`。这是第二个形状：**不是基础设施故障伪装成业务判定，而是另一次 run 的判定伪装成这次的**。对 before/after 对照实验致命：两臂可静默共用判定 | `VERIFIED`（双层修复 + 11 条回归） |
| 15 | `max_tool_rounds=8` 硬编码 | 对 astropy 规模的 repo，「搜索→读→改→验」四步做不完，**预算耗尽点恰好落在编辑之前**。于是分数近乎 0，而失败方向再次一致指向「模型弱」 | `VERIFIED`（已参数化） |
| 16 | 任务描述从不声明交付形态 | prompt 只给 issue 文本，不说「评分只读 `git diff`」。模型按对话直觉输出代码块。`astropy-13236` 的回复是**针对该 issue 正确的修复**，`git diff` 为空 → 判 fail。**已解决的问题被记成没解决** | `VERIFIED`（已加 grading contract） |
| 17 | `_compact_messages` 的 tool 邻接修复用了**反向守卫** `if orphan_start > 0` | 该分支只在「已经有 anchor」时才修，**恰好跳过唯一真正坏的 `orphan_start == 0`**——窗口第一条就是 tool 结果、前面根本没有 assistant(tool_calls)。**守卫上方的注释描述的正是 `== 0`，代码测的是它的补集。**DeepSeek 直接拒绝整个请求（`HTTP 400`），实例 solve 中途死亡 | `VERIFIED`（已修 + 11 条回归） |
| 18 | `run_arm.py` 无凭证 preflight | 首次实验用一个每次调用都 401 的 key 跑满 20 实例：照样 clone astropy、照样起镜像、照样调官方 scorer，**产出的数字描述的是凭证而不是 Agent**。与缺陷 4 同源：能失败的检查必须真的能失败 | `VERIFIED`（1 次请求即闸断） |
| 19 | 全局预算按「单实例」尺寸配，跑 20 实例时**中途截断** | `Budget` 的四个上限是常量，与实例数无关。跑到第 7 个实例时 token 累计触顶，剩下 13 个实例带着 `BudgetExceeded` 记成 `ERROR_AGENT`——**读起来是 Agent 自己放弃了**。同时 `classify_error` 把所有 `BudgetExceeded` 都归给 Agent，掩盖了「是 harness 的预算不够，不是 Agent 不行」。两臂 token 消耗天然不同（B 臂轮次多），这条会**系统性地惩罚优化臂** | `VERIFIED`（预算按实例数缩放 + 新增 `ERROR_BUDGET` 类别，区分累计上限与单实例瞬时上限，12 条回归） |
| 20 | artifact 里没有任何字段说明「这是哪一臂」 | 两臂的 run 目录只靠人为放在 `arm-a-baseline/` / `arm-b-optimized/` 区分，**run-manifest 内部无记录**。目录一改名或一移动，两臂就无法区分，而对照实验的全部结论都建立在「这个数字属于哪一臂」上 | `VERIFIED`（manifest 写入 `harness_uplift` 块，含 enabled/各组件开关/轮次） |
| 21 | 官方 scorer 遇瞬时网络故障即判 `failed-scorer`，不重试 | 一次 `SSLEOFError` 就让实例记成 scorer 失败并从配对集合中掉出。**丢样本本身不致命，但它不是随机丢**：网络抖动与镜像拉取时长相关，而两臂的镜像拉取量不同 | `VERIFIED`（9 类瞬时网络签名识别 + 单次重试；非瞬时故障仍然 fail-closed） |

**计数口径**：21 条里 **11 条**（1、2、3、4、6、7、9、10、15、16、19）是「基础设施/配置故障 → 业务判定」；**2 条**（5、8）是「判定没有原始证据」；**1 条**（14）是新形状「另一次 run 的判定伪装成这次的」；**1 条**（16）是它的镜像「已解决的问题被记成没解决」；**1 条**（17）是守卫写反；**2 条**（18、20）是缺失的前置检查/缺失的溯源字段；其余为普通缺陷。缺陷 8、9 此前记为「未修」，**现已修复**，上表已就地更新。

**19、20、21 的共同点值得单独说**：它们都不是「Harness 跑不动」，而是**「Harness 跑得动，但产出的数字回答不了我要问的问题」**。三条都是在设计对照实验的过程中发现的——正是「我要拿这个数字下什么结论」这个问题把它们逼出来的。缺陷 19 尤其危险：它会系统性地惩罚优化臂（B 臂轮次更多、token 更多），如果没先修，我会得到一个**方向正确但幅度被压低、甚至反向**的结果，并且完全有理由相信它。

## 3.5 一个平庸的 Harness 为什么平庸：先量机理，再改架构

这是本项目最完整的一次「诊断 → 设计 → 对照」闭环，也是我最愿意被追问的一段。

### 症状

自研 Harness 在 astropy-20 subset 上几乎拿不到分。最省事的解释是「模型不够强」，而且所有表面证据都支持它：`resolved=False` 连成一片。

### 我实际做的事：不看分数，看 trace

一次真实 run 的 `traces/trace-summary.json`（233 spans）按 `eval.instance_id` 聚合后：

| 事实 | 数值 |
|---|---|
| 工具调用构成 | Grep 42 / Read 30 / Glob 9 / Bash 15 = **96 次搜索类**，**Edit 仅 2 次** |
| 零编辑实例 | **11 个已评分实例中 9 个 `edits=0`** |
| 轮次 | 几乎每个实例 `chats=9`，而 `max_tool_rounds=8` |
| 空 patch | 9/10 实例 `model_patch` 为 **0 字节**，`tokens_out` 却是 1251–20091 |

**决定性的那条证据**：`astropy-13236` 的 `answer` 字段里是一段**针对该 issue 正确的修复代码**（`NdarrayMixin` 的 `AstropyFutureWarning`）。它只存在于回复文本里，`git diff` 为空，因此判 fail。

而唯一两个 `edits≥1` 的实例（`13033`、`14365`），**正是唯一产出非空 patch 的实例**。

### 机理

8 轮预算在「找 bug」阶段被搜索类调用耗尽，模型在最后一轮被迫把修复**写成回复**而不是**写进文件**；而 scorer 只读 `git diff`。

所以低分与推理能力无关——**瓶颈在预算分配与交付形态**。这也解释了为什么换更强的模型救不回分数。

### 按机理对症的四项改动

| 组件 | 对症 | 设计要点 |
|---|---|---|
| **分层定位**（BM25 两级：文件 → 函数） | 96:2 的搜索/编辑比 | 文件级用 `ast` 抽 module docstring + 类/函数名做 profile，**不用全文**——否则长模块靠词频压过短而精确的模块。粒度**停在函数级而非行级**：一个自信但错误的行号，比一个诚实的函数区间更有害 |
| **轮次预算** `8 → 24` | 预算耗尽点落在编辑之前 | 不设无限：无限预算只把「卡住的 agent」变成「卡住且昂贵的 agent」，并让增益无法归因 |
| **交付契约**（prompt） | 模型不知道评分读什么 | 明写「评分读 `git diff`，不读你的回复」「回复里的修复得 0 分」「不要改测试」。**单独列为一个组件而不是混进定位里，是为了让写作时无法把它的效果记到机器上** |
| **验证 + 一次反馈重试** | 9/10 空 patch | 证据带**方向**：`DISQUALIFYING`/`REGRESSION`/`WEAK_POSITIVE`/`NO_EVIDENCE`，**绝不把弱证据升格为判定**。空 patch / 只改测试直接失格 → 触发**一次**重试。不循环：反馈针对提交形态，第三次也改不了 |

架构借鉴 Agentless 的 localize→repair 分层；粒度选择依据是仓库级修复的粒度研究（函数级优于文件级与行级）。best-of-N 选择也已实现，末位 tie-break 用 `sha256(diff)` 升序——**任意但固定**，因为公开的 test-time-compute 结果记录了在采样预算 8 上的**倒退**，成因之一正是随机 tie-break。它默认关闭：开启会让运行时间三倍，而实测损失不在这里。

### 我会主动说出口的三件事

1. **定位用词法 BM25 而不是向量检索**，因为项目现有 ES 索引装的是 techdocs 语料（24,877 chunk），**不含仓库源码**，接不上；为每实例现建 embedding 索引，成本高于它省下的轮次。这是约束下的选择，不是最优解。
2. **对照实验做之前，我先修掉了一个会让实验彻底失效的缺陷**（缺陷 14）：官方 scorer 的 `run_id` 跨 run 恒定，两臂会静默共用判定。如果没先发现它，我会拿到一个「涨了」的数字而完全不知道它是假的。
3. **归因必须诚实**：空 patch 的主因是轮次预算与交付形态，这两项的修复**近乎必然**把分数从「几乎 0」抬起来。所以若优化臂显著更高，诚实的说法是「**预算 + 交付契约 + 定位三者之和，其中前两项是基础工程缺陷的修复，不是精妙架构的胜利**」。为了让这句话可被检验而不只是一句谦辞，每个组件都有独立的消融开关。

### 报告口径

结果只能写成「**astropy-20 subset 上 k/20**」，**不是** SWE-bench Verified 分数：单仓库、dev 口径、含一个用于调试因而不独立的实例。不报 p 值（N=20 上是装饰），报 **McNemar 不一致对计数**。

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
| 缺陷 8/9 修复 | 缺陷 8：`_summarise_official_stdout()` + `_SUMMARY_LABELS` 逐条提取官方 7 个计数标签，原始 stdout 落 `scorer/`；缺陷 9：`pin_contract.py` 三档 REQUIRED / CAPABILITY / DEGRADABLE，空值 pin 不再放行 | `VERIFIED`（此前记为「未修」，**已闭环**） |
| 缺陷 14–18 修复 | 14：per-process `_SCORING_SESSION` + `not_before` 双层守卫（11 条回归）；15/16：轮次预算参数化 + grading contract（29 条）；17：tool 邻接反向守卫（11 条回归）；18：凭证 preflight | `VERIFIED`（Python 全量 **1116 passed**，81s） |
| 分层定位 / 验证 / 选择 | `localize.py` 28 条、`validate.py` 20 条、`select.py` 20 条（含全排列下胜者唯一）、对照报告工具 14 条 | `IMPLEMENTED`（真实 run 未产出，不签 `VERIFIED`） |
| **两臂对照实验** | A 臂运行中（`arm-a-baseline/…-be6d049d`，约 4.3 min/实例）；旧 run 受缺陷 14 污染，已移入 `_discarded-arm-a-contaminated/`（未删除，供复核），**其数字不得引用** | `BLOCKED`（A 臂未跑完，B 臂未开始） |
| **H5 官方 1 实例门禁** | 五项**同时**满足于 `h5-full-traces-20260810-join/…-6c6eeac0`（161.0s）：manifest、prediction、官方 `scorer/` 四文件、`traces/` 两文件、`checksums.sha256` 12 条**逐条重算全部匹配**。trace 判定 `PASS`，`problems: 0`，20 span 同属一个 trace、0 悬空父节点、单一根 `eval.run`；链路 `eval.run → eval.instance → invoke_agent → 9×chat + 7×execute_tool（Read×3/Glob×2/Edit/Bash）`，`scorer.official` 挂 `eval.instance`；20 个 span 全带 `eval.run_id`，且只有一个取值。官方判定 `resolved=true`、`FAIL_TO_PASS` 2/2 | `VERIFIED`（n=1，非分数）。**口径限制**：该 `PASS` 依赖 SWE-bench 以 `TRACE_CAPABILITIES = ()` 声明的 `rag.retrieve`/`embedding` 豁免（豁免已写入产物、可审计）。RAG 基准仍必须产出这两类 span |
| 生产 trace instrumentation（O1–O3） | 已提交（`4a673590`、`5e002b96`、`64f9299c`）。两处修的都是「契约本身错了」而非 run 错了：① `invoke_agent`/`execute_tool` 归给 `go-agent`，但 headless 驱动在 Python 进程内跑 Read/Write/Edit/Bash/Glob/Grep，链路里没有 Go agent——报 `go-agent` 会把人指去启一个这条路径永不联系的服务，正是 producer 字段本该消除的「不可行动判定」。改为 `agent-runtime` 并在真实站点补 span；**没有**把 Go agent 做成可声明能力，那会豁免掉唯一能证明工具真的跑了的 span。② `chat` span 缺 `eval.run_id`。用 OTel baggage 传播，**context 作用域而非时间作用域**——「capture 打开期间创建的都盖章」会把 run id 盖到无关后台线程的 span 上，契约的 join 检查就会接受伪造证据；`test_trace_join.py` 用「context 外创建的 span 必须不被盖章」这条对照组把差别钉死 | `VERIFIED`（926 tests） |
| SWE-bench 10 实例 | 10/10 非空 patch，`resolved` 未知；官方评分从未在这 10 条上完成 | 探索结果，非分数 |
| Terminal-Bench 4 实例 | 官方 Harness 总结果 `0/4`；最新单任务 `0/1 test_timeout` | `IMPLEMENTED`（heredoc/base64 修复未验收） |
| tau2-bench 10 实例 | `avg_reward=0.7`；前 5 题经 prompt tuning（0.4→1.0→0.7）= development-set contamination；缺 pins/provenance | 探索结果，非分数 |
| RAG 三路检索 | nDCG@10 0.5201 / 0.6547 / 0.5636，数值健全（nDCG 全落 [0,1]） | **不可发布**，见 §6 |
| qrels 仲裁 | 23 `AI_REVIEWED` / 157 `DISPUTED` / **0 `HUMAN_REVIEWED`** | `IMPLEMENTED`（仲裁逻辑已修正） |
| 四层污染扫描 | 24,822 chunks × 180 queries 四层全跑完、0 命中，verdict `CLEAN`，退出码 0 捕获，71.75s < 600s SLA；层 4 经 4,477,860 对独立复算 max 0.795592 < 0.8 | `VERIFIED`（设计地图 §30） |
| Phoenix | 6006 + OTLP 4317/4318 可达。本地 capture 已产出**真实** 20 span 生产 trace（不再是合成），但**尚未验证经 OTLP 导出到 Phoenix 后仍完整**——capture 与 exporter 是同一 provider 上的两个 processor，共存已实现，端到端未验收 | 基础连通 + 本地 trace `VERIFIED`；Phoenix E2E 仍 `IMPLEMENTED`（属 O3） |
| 多模态 RAG（M1–M5） | `data/eval/multimodal` 只有 `README.md` | `DESIGNED`，未开始 |
| dev/holdout 防火墙（E3） | `split-manifest.v1.json`：`dev_size=180`、`holdout_size=0`、`holdout_status=BLOCKED` | `BLOCKED` |
| 统一 Harness 旁路 | legacy `benchmark_mod.run()` 旁路已删除并经 AST 断言（0 call node） | `IMPLEMENTED` |
| DeepSeek key 历史 | 曾公开于 `origin/main`（见 §7）。**工程侧已闭环**：仓库置为 private 后才推送含该 blob 的历史 | `BLOCKED`（**密钥轮换仍需密钥所有者操作**；轮换前禁止转 public / 分享 bundle） |

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

1. ~~**O1/O2 生产 instrumentation**~~ → **已完成**：真实 run `…-6c6eeac0` 产出 `traces/trace-summary.json` + `span-assertion.json` 并进 `checksums.sha256`，trace 判定 `PASS`，H5 五项全满足。扩规模仍排在门禁之后：H5 过了才谈把实例从 1 提到 50，且 50 实例需要重新验证预算与超时口径，不是把 `--limit` 改个数字。
2. **修缺陷 8、9**：`scorer_status` 停止承担证据职责；空值 pin 必须让 preflight 失败。
3. **E2 模型身份**：响应侧 provider/model/revision 落盘，解除 `MODEL_IDENTITY_UNVERIFIED`。
4. **人工复核**：157 行 DISPUTED + 23 条 `AI_REVIEWED` 分层抽检。没有真人参与，RAG 永远不出发布数字。
5. **E3 holdout**：`holdout_size=0` 必须解除；被 tuning 过的 tau2 前 5 题与 astropy-12907 永久留在 dev。
6. **key 轮换**（最高优先级，与工程无关）：已公开，只有所有者能吊销。
7. M1–M5 多模态、E6 统一重跑：门禁通过后再启动。

---

**面试口径提醒**：本文档没有一个可发布的基准分数。可以拿出来讲的是 `h5-full-20260810` 这一份产物、它前面三份失败产物构成的受控对照、以及那 8 条同形缺陷。任何超出这个范围的数字，请当场说明它是探索结果。
