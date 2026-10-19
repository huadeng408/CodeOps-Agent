# #19：文件、兼容、物料与首批验收对账

本轮完成 [#19](https://github.com/huadeng408/CodeOps-Agent/issues/19) 六项首批验收检查和对账，
Windows milestone 1 在该声明范围成立。独立 Standards 0／Spec 最终 0，`AI_REVIEWED`；
v1 的入口误报成功问题及修复复审均保留。交付提交、源码 CI 与正式状态见本票的后续交付记录。
工程实现、单测／集成、产品和完整发布分开记录；完整发布仍 `BLOCKED`。
父规格 #1 的原始正文保持，后续专项不追加为已关闭基础票的门禁。

## 文件与兼容范围

逐个比较原运行冻结的文件指纹，而非仅比较 Git SHA：原 #10 的应用核心、Ledger、旧 snapshot
读取及 restore 共 11 个运行文件与现用 v5 完全相同。准备／Git 元数据、Python 提案、Runner、
Memory 和取消的实际变化另由当前回归及[五类产品场景](milestone-v5-matrix-2026-10-19.md)覆盖，
没有声称整个旧构建与当前构建等价。

| 原要求 | 当前可核对证据 |
| --- | --- |
| dirty/new/empty/missing | 当前实际浏览器准备的 owned fixture 精确保留八项存在／内容状态，包括 dirty、新文件、空文件与缺失；原副本及 index 保持 |
| create/edit/delete/rename | 原 #9 四类实际产品效果、全部失败及冻结分母保留；相同应用核心与现用 Windows 生命周期回归核对。新 #17/#18 的实际批准应用分别保留 |
| 路径／link 拒绝 | 当前 Windows 两项真实 junction／元数据 alias 拒绝通过，路径边界回归与 Linux CI 的 symlink 范围分别记录；四项 Windows symlink 创建权限不足仍为 SKIP，不能称作完整 Windows symlink 攻击矩阵 |
| 并发与重复请求 | 当前真实模型／提案／测试／浏览器批准遭原基线并发变化拒绝；原内容／存在／index 三类冲突及 request identity 重放保护保留 |
| 多文件中断 | 原 16 个 intent/outcome 中断边界、3 类冲突、新基线再次批准 1 项，以及此前三个失败或中止产品运行全部保留；没有把它们改称十六次 HTTP 服务都被杀 |
| CLI／HTTP 与旧数据 | 当前原生 Go 三包 43 PASS、0 FAIL/SKIP，退出 0；覆盖旧 snapshot 只读／幂等导入、源变化拒绝、单向 EventStore、fork/rewind 回退、owner 绑定和不支持的 CLI undo 拒绝；不宣称存在新的 CLI 提案应用入口 |
| 旧 full-stack profile | 当前生产 Go、真实 MySQL/Redis/MinIO 和 Chromium 最终 9/9，通过重启后鉴权历史、真实拒绝和失联状态检查；三次运行及未运行项分别保留 |
| 数据保护与路由回退 | 新 v6b 真实登录并重开原 1,493-event Session 后，切回原 v5 EXE 再次登录／重开；原五个 Session 的 225-event 前缀和整个预算投影不变，未复制或清空 Ledger、identity 或工作区 |

前轮现用 Windows 五包定向回归为 161 父子 PASS／7 SKIP，退出 0；
补验 4 PASS／0 SKIP，退出 0，实际使用已批准 wheel，覆盖两项 Python 冻结提案和两项 junction。
原七项跳过仍保留，补验不抹去它们；其中四项 Windows symlink 及一个子进程 helper 未执行。
本轮新增三包 43 项回归的 515 个 Go／协议输入运行前后相同。

## 修复旧测试入口并运行真实 profile

原 full-stack 启动实现移至 `deployments/docker/full-stack-test.ps1`；
`tests/e2e/full_stack_startup.ps1` 保留为单向入口。
三个服务固定归入 Compose project `localcode`，各有 service、owner、purpose 和 run identity。
从缓存将镜像解析为 image ID 后启动，新 bind 数据全部置于 `deployments/docker/.local/data/`；
服务停止和收据由同一入口管理，没有使用现有业务 volume。

既有浏览器检查曾把未探测的 full-stack 沙箱硬写成 BLOCKED、保留旧 token 文案，
并把能力行数写死为 8。现在按明确契约检查 full-stack 的 UNKNOWN／未探测原因，
local-core 的 BLOCKED 条件不变；核对实际已确认量、批准上限、原预留，
并要求失联前后的能力行数一致且全部 UNKNOWN。
没有修改产品能力状态或界面实现来配合测试。

| 产品尝试 | 完整九项分母 |
| --- | --- |
| full-stack-v1 | 3 PASS／1 FAIL／5 NOT RUN；旧 sandbox 状态断言失败，退出 1 |
| full-stack-v2 | 6 PASS／1 FAIL／2 NOT RUN；旧八行断言失败，退出 1 |
| full-stack-v3 | 9 PASS／0 FAIL／0 NOT RUN，退出 0 |

历史 profile 是八项；当前入口已有 token 展示检查，因此实际分母为九，没有缩减。
三次 opt-out 调用均保留 SKIP／退出 0，未创建资源。
第一轮观察器额外统计数据文件时遇到 Linux mysql.sock 的 Windows stat 错误；
原 native 退出码、三项停止状态和修正后的只读资源核验另存，不改成产品成功。
不再扫描这些数据库内容，也不虚构完整数据内容 checksum。

三轮各自三项服务均停止并保留所属新数据目录；原 34 项既有容器的 ID、image、name、state 不变。
后轮还核对前轮新容器保持原状态。最终运行中的 source/assets 均未变化，617 项入口输入逐项匹配。
浏览器使用生产服务；没有模型调用，不把 profile 检查升级为代码任务或完整发布验证。

随后在入口审阅中复现：部署 helper 缺失时，PowerShell 非终止错误会使兼容 wrapper 错误退出 0；
RED 保留该原生 0 和 self-check 退出 1。wrapper 增加一行 `ErrorActionPreference=Stop` 后，
真实 CLI 的缺 helper 分支退出 1、实际 opt-out 退出 0，fixture helper 的退出 23 原样透传，
三项 GREEN self-check 退出 0；后者只验证 CLI 适配，不算产品运行。
最终 617 项输入中仅这一个 wrapper 改变，其余 616 项（含部署 helper、浏览器与产品）逐项相同；
helper 本身在任何分支前已设置相同 Stop 策略。该差异的验证限于入口错误和退出码，
没有把旧九项结果改写成该行之后的另一轮 full-stack 运行。

## 便携包与组件

已核对 98 个链接 Go 模块、3 个 frontend runtime 包、53 个顶层 Python wheel；
setuptools 的 12 份 vendored metadata 独立列出。
原 222 个 notice 路径保留；四个仅有 metadata 的 Python 包已按固定上游版本补入六份材料，
原来源、版本、内容 checksum 与[许可组装证据](windows-python-notices-2026-10-19.md)一致。
这是组件 pin 与许可材料核对，不作完整生产法律合格声明。

v6b manifest 为 `c4b410aba1eda23bfbd640111d39639491bffeff2ddf4a329e6efa16a9cd3405`。
相对 v5，运行源码没有变化；源码清单差异只在构建器、组件 pin 和随包 README。
实际文件新增六份 notice，原有十个文件变化为两项重建 EXE、七份安装 RECORD 和 README；
没有把二进制重建称为二进制完全相同。

本轮实际从无关临时目录启动这个带空格路径的 v6b，完成真实登录、历史 API／能力读回，
原 Go Session 仍为 1,493 条事件。控制服务保持停止，sandbox 明确 BLOCKED、Trace UNKNOWN，
历史 READY；没有宣称此时具备副作用执行条件。
随后切回原 v5 路由再次实际登录、重开旧 Session；两次所属 Job 和浏览器均停止。
预算和五个原生 Session 前缀相同；原 v5／新 v6b 的 3,219／3,225 个文件重新核对不变。
这不替代 #17/#18 的十轮任务，也不是用户业务数据恢复或完整发布演练。

## AC01–AC09 与分母保留

| 父规格组 | 首批声明范围与证据 |
| --- | --- |
| AC01 启动／身份／历史 | 原便携验证、当前 full-stack 9/9 与 v6b／v5 实际数据重开；执行前提缺失时仍拒绝 |
| AC02 两个公开任务 | [Go #17](go-browser-task-trace-v5-2026-10-19.md) 14 run：12 completed／2 canceled，最终项目 86 顶层／152 父子 PASS；[Python #18](python-browser-task-2026-10-16.md) 11 completed，排除纯规划保守计十轮，项目／提案 99/99；各自全部尝试、旧失败及 pin 保留 |
| AC03 文件与应用 | 上述十四项矩阵、11 个核心指纹、新鲜回归与实际原基线冲突拒绝；跳过范围明确保留 |
| AC04 五类恢复 | 同轮五类真实产品结果及完整 7-run／8-attempt 分母；预期失败、unknown 和未导出 Trace 根不删去 |
| AC05 Memory | Go 同任务 53 条、Python 同任务 8 次真实反思／32 条经验及各自失败；本轮重启后空新 Session 的五条来源核验与 completed 答复；原跨仓库拒绝证据保留 |
| AC06 成功代码任务 Trace | Go 正式 14 树／449 span、Python 11 树／540 span 均按批准 v2 PASS；legacy FAIL 和所有 ERROR 保留。受控故障的无根树另记 partial，不伪称通过 |
| AC07 预算与并发 | 原持久批次、服务商一亿 token 上限；累计确认 51,263,480，历史 unknown／5,306,024,576 原预留及未知费用保留。当前 ActiveCalls=0、TaskID 为空 |
| AC08 兼容／回退 | 当前 43 项原生公开边界、真实 full-stack 和 v6b→v5 路由重开；单一 Ledger，无双写或数据删除 |
| AC09 UI／便携材料 | 已选布局的实际交互、原 frontend tests/build 与新鲜源码 CI 各守其范围；当前两包指纹、组件／notice pin 和真实 launcher 退出归属核对 |

重新核对旧八份封存范围的 1,928 项记录，加后续公开任务、v2 分析、许可及交付封存：
共 6,804 个唯一 path/checksum/size 记录全部匹配，0 缺失／不符。
旧严格 FAIL、provider 失败、unknown、原预算和所有未完成尝试没有改写。
正式成功代码任务的完整性要求只用于该范围；不把缺根故障诊断改写为完整，也不把它新增为基础票门禁。

## 验证、产物与交付边界

`python .scratch/milestone-v5-compatibility-20261019/run_compat_tests.py`：43 PASS、退出 0。
真实 full-stack 使用原测试入口、Go 1.26.0 与缓存镜像；最终命令退出 0、九项全过。
wrapper 额外 RED／GREEN 与 `bind_final_entry.py` 的实际结果如上；绑定核验退出 0。
`verify_prior_seals.py`：6,804 项匹配、退出 0；包／数据重开核对及 `git diff --check` 退出 0。
没有改变 Go/Python 运行源码、协议或 frontend 实现，未在本地重复无关全量；交付源码 CI 单独记录。
两轴审阅和最终文档差异核对不产生 `HUMAN_REVIEWED`，也不扩大上述 runtime 范围。
完整发布 gate 本轮未运行，完整发布仍 `BLOCKED`。

本机产物入口：`.scratch/milestone-v5-compatibility-20261019/`、
`deployments/docker/.local/receipts/full-stack-services-*/`、
`output/playwright/full-stack-*/` 和 `deployments/docker/.local/receipts/milestone-v6b-portable-20261019/`。
固定输入、每次退出码、原生资源和失败分母分别保存；这些都是只读验证投影。
用户 AGENTS.md、既有数据、旧封存和父规格正文保持。
