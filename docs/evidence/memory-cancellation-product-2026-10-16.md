# 取消后续行的真实 Memory 与跨进程 fixture 修正

工程状态：`IMPLEMENTED`。取消终态修复已在真实产品中完成“待审批取消 → 同一会话完成 → 真实反思写回”；该范围 `VERIFIED`。另外修正旧跨进程 fixture 对异步恢复的过早读取。#17／#18 的完整十轮任务和成功 Trace 不由本记录替代；发布未运行。

## 真实产品范围

产品使用 `7bba5f5f08e9607028612537c4c1e0ca8c60fa27` 的新 Windows 构建。官方 assembler 使用已有校验缓存，构建退出 0；manifest SHA-256 为 `dd1db7071aadac14e6a04a773487e3e27550bfed7385f0493c0c8f4ac04e6d7d`。3,219 个随包文件和 606 个构建源文件已核对，额外验证范围共冻结 610 个源码指纹。该 commit 的 [CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38863148223) 四个 job 全部通过。

复用原 Go Ledger、已批准 OpenSandbox／模型配置和服务商 100,000,000-token 硬上限。新建本轮专用本机账户，通过实际注册／登录界面创建只读 chi 任务；当前副本中的已批准修改是本次保护基线。没有向模型提供预期反思、参考 patch 或修复答案。

| 范围 | 结果 |
| --- | --- |
| 实际浏览器取消 | 第一轮停在未批准、未派发的只读 Git diff；实际点击取消，Go 原生 #184 记录 `cancellation_state=stopped` |
| 同一会话续行 | 第二轮正常只读问答完成，原生 #221 为 completed；第一轮取消完整保留，不计为完成轮次 |
| 真实 Memory | #223 提交完整轨迹，包含原 canceled 来源；#224 保存真实 provider 的反思提案，4 个候选经 Go 校验并进入一次 catalog update，四条 Experience 均为 revision 1 |
| 完整计量 | 8 项准入／8 项结算：7 项前台、1 项反思；新增 unknown 为 0，确认消耗 165,917 tokens，批次累计 24,876,914；费用未知，原 unknown／预留保持 |
| 原副本保护 | 132 个原文件、两份 index 与本轮执行前基线一致；新修改为 0，没有执行任务代码、测试、提案或应用 |
| 可选后端读回 | 两轮完整树共 50 个已结束 span；取消轮 34 个含 1 个 ERROR，完成轮 16 个无 ERROR，包含真实 Memory reflection。只记录诊断读回，不作为完整成功代码任务 Trace 收据 |
| 结束及资源 | 经实际浏览器结束任务；活跃调用为 0、任务槽为空；所属产品与 Playwright 退出，两项控制资源恢复原 stopped 状态 |

源 Session 为 `951cd21c8b7b5e646381ab3061c0ad24`。轨迹 checksum 为 `dcde98047ec44cc6ca98f4c6fd6d89e04d2e37c7077c4686509e39fd7545ef77`，反思 proposal 与 catalog 的 source identity 相同。模型请求使用 `grok-4.6`，反思响应报告 `grok-4.6-build`；该报告不构成已验证的不可变服务商 revision。模型问答和经验没有逐条人工复核，不将其内容全部视为正确或标为 `HUMAN_REVIEWED`。

计划原本等待 Bash 审批，实际较早出现 Git 的 pending 审批，因此使用观察到的同一个未执行工具边界验证轨迹恢复；决定及原 payload 保留。未额外执行 Bash 取消或完整 Go 基线，不扩大本次范围。

33 个容器的身份、状态、分组和挂载与保留基线相同，96 个镜像和 332 个 volume 的身份保留。未重算 volume 文件内容或全部 image／volume 元数据，未停止 Docker／WSL 全局服务或删除业务数据。

## 同时保留的失败

- 先用新构建恢复已显式结束的旧 Go 任务，Go 确实补交完整轨迹；模型反思被已有 closed-task 门禁拒绝，新增模型调用为 0。它只验证轨迹修复及关闭任务的拒绝边界，没有绕过门禁继续收费；后续成功来自上表的新受控任务。
- 第一份浏览器启动因为非交互 stdin EOF 提前收尾，没有注册、任务或模型请求；所属资源已停止，单独记录为 `NOT_RUN`。
- 第二份驱动误将创建会话的 200 当成 201；通过正常已鉴权 GET 读回真实 Session，没有重复创建。
- 取消观察器误用按钮可见文字，实际 accessible name 是“取消代码任务”；等待响应的 Promise 超时导致原驱动退出 1，原产品仍运行，未发生取消 POST。恢复时只在内存中从精确的本轮 launcher 读取其生成的测试密码，先核对 executable、Windows 用户和数据目录，再经正常登录进入新浏览器 context。没有重置密码、绕过鉴权或重发原模型请求；同一 Session 随后完成取消和续行。

五项冻结场景在上述实际范围内通过；两份浏览器启动、观察器失败、两次模型轮次和全部反思／调用分母均保留。上述成功不删除旧 Go／Python 十轮任务的 Memory／严格 Trace 失败，也不宣称本次只读问答完成了代码修改型评测。

## 跨进程 fixture 修正

先前可选 fixture 的 7/10、精确 HEAD 基线 6/10 失败保留于[源码修复记录](memory-cancellation-classification-2026-10-16.md)。其 `Recover` 已异步排队，fixture 却立即查询 Recall：查询与提交竞速，使来源、隔离和预算检查读到不同完成阶段。

`internal/memory/ledger_process_test.go` 现在复用已有 `recoveredMemory`／`awaitRecoveredMemory`，每次 `Recover` 后等待两个测试用户各自的真实 Commit 返回，再检查 Recall 和幂等性。十项断言、独立进程、nil conversation、错误与有界超时均保留；没有修改生产行为或新增等待抽象。实际文件 SHA-256 为 `3a427a61cf64d90a88493b7d0e8c408b5a0c19e3fc7bfc64bf7baca090a56d3c`，与双轴复核的 overlay 候选相同。

设置 `CODE_AGENT_RUN_MEMORY_INTEGRATION=1` 后实际源码验证：

- `go test ./internal/memory -run '^TestLedgerMemoryAcrossProcessRestart$' -count=3 -json`：退出 0，三次各 10/10；先前 overlay 的三次各 10/10 另存，不混作实际源码结果。
- `go test ./internal/memory -count=1 -json`：退出 0，41 个父／子测试 PASS，0 FAIL、0 SKIP；其中跨进程检查 10/10。
- `go vet ./internal/memory`：退出 0。上述命令使用已安装 Go 1.26.0、`GOTOOLCHAIN=local`、`GOPROXY=off`，未关闭 checksum 验证。

真实产品在这项后续 test-only 修改前已封存，继续绑定原 7bba5f5f 构建，不改写其源码 pin。本项不涉及 Python、frontend 或协议；本次未重跑其本机测试／build，完整发布未运行。

## 收据

- 启动恢复：`deployments/docker/.local/receipts/memory-cancellation-product-20261016/v1/`，22 项清单 SHA-256 `8b8f26bb642c7523dee9112e2ef9299278182117d665e6c29f8238dcba55d9dc`。
- 提前关闭的浏览器准备：`deployments/docker/.local/receipts/memory-cancellation-browser-20261016/v1/`，10 项清单 SHA-256 `905d0680fbcce1254254559846bd82ecb5ae79badba9246f3a003c0c3f878aa4`。
- 成功的聚焦浏览器范围：同级 `v2/`，54 项清单 SHA-256 `942e69d59702cbbb26de0784b095ad29fb9850c5bef8b49376d227bddffc28f5`；含原生／admission／catalog 导出、Memory 来源、实际 UI、截图、完整 spill、后端 span、观察器失败和所属进程停止收据。
- Fixture RED／GREEN 与双轴候选审查：`.scratch/memory-cancellation-product-20261016/fixture-overlay/`；实际源码命令、全部检查和输出指纹：同级 `fixture-actual/`。

两个封存命令 `python .scratch/memory-cancellation-product-20261016/seal-startup.py`、`python .scratch/memory-cancellation-browser-20261016/seal-browser.py` 均退出 0。产物逐项重算通过，已核对已知凭据未进入聚焦浏览器收据；私有运行产物不进入提交。审查分类为 `AI_REVIEWED`，产品／集成／发布各自记录范围。
