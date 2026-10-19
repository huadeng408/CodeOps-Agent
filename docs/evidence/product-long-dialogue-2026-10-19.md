# #27 长对话与 compaction

对应 [#27](https://github.com/huadeng408/CodeOps-Agent/issues/27)。完整目标仍为实际便携
产品中的 200 个有意义 assistant turn、真实 compaction、工具续行和中途恢复。
本节先记录代码／输入准备阶段；本票保持 OPEN，不以短样本或源码回归替代该分母。

## 启动窗口配置：源码范围

起点 `fc49c41de52fa43eb82c1ea3d29b9f052ba1cfd3`。
`orchestrator/server.py` 的既有 `--context-window` 参数复用 `read_env`，从
`CODE_AGENT_CONTEXT_WINDOW` 取得默认值。未设置／空白沿用 256000，显式 CLI 参数优先；
整数解析及其他窗口语义保持原有行为。便携启动器继承普通配置环境，因此沿用原启动链。
Windows 使用文档同步说明该配置；未增加依赖、执行器、协议或 Session 写入者。

TDD 在既有 public server CLI／parser 接缝运行：设置 32768 的首轮 RED 实际得到
256000，命令退出 1；一行默认值连接后同例退出 0。
最终九项覆盖未设置、空值、空白、正值、trim、显式分离／等号参数优先级和整数拒绝；
显式有效参数可以覆盖无效环境默认值。这个配置窗口不等于已验证的模型 revision 或计费用量。

## 本阶段验证和完整记录

- 定向 `python -m pytest -q tests/test_server.py -k 'context_window_uses_startup_environment or context_window_keeps_integer_validation'`：
  0；9 PASS、68 deselected、1 个依赖弃用警告。
- 相关完整四文件：`tests/test_server.py`、`tests/test_compactor.py`、
  `tests/test_compaction_provenance.py`、`tests/test_context_runner_events.py`。
  首次 0／145 PASS／3 warnings，含两个 Windows 子进程 stderr 读取线程的 GBK 解码警告；
  此输出保留，不能称作无损采集。
- 仅对测试进程设置 `PYTHONUTF8=1`、`PYTHONIOENCODING=utf-8` 后重跑同一完整四文件：
  0；145 PASS、0 SKIP、0 FAIL、1 个依赖弃用警告，无线程解码警告。未改系统设置或测试实现。
- 相关源码／依赖／指令九项指纹在定向测试后、相关回归前采集；源码在回归期间保持相同。
  代码、命令、两轮完整输出及 JUnit 位于 `.scratch/product-long-dialogue-20261019/`；
  不把事后指纹说成 RED 之前冻结。
- 新 Go、前端、provider 或发布验证不由本机 parser 检查代替；交付源码 CI 单独记录。

## 200 轮输入与后续执行

200 个不同正常工作输入已生成，SHA-256
`3b53ca87f1e5a21acc26636d4597574baccf7024adfe6fd00a825230e3143e2f`。
内容为公开 Go 仓库的文件审阅、组合边界分析和一项局部 RequestID 配置功能及真实测试；
没有参考解、gold、补丁、虚构回复或向模型注入的修复提示。反思／compaction 不计入 200 assistant turn。
阶段性刷新与第 100 轮空闲重启已在规格和输入中声明，全部失败／重试及未运行项保留。

新自有原工作副本通过本地 Git transport 准备，99 份允许文件复制到既定 Docker `.local/data/`
目录；旧原副本的 index 未改变。这是 Task Workspace 以外的输入准备，未在 host 编辑 Task Workspace。
实际运行还须在调用前冻结新包、完整资源／任务／批次身份和该输入清单。

本代码准备阶段未发起本专项模型调用，200 轮产品验证仍未运行。
`IMPLEMENTED`／源码范围 `VERIFIED` 只描述启动配置；本票完整 runtime、token/质量对照、
更广恢复矩阵和整体发布分别保留，不修改 #1 原文或暂缓 #20 的决定。

## 原 v8 真实运行：未完成完整分母

该轮在 2026-10-20 安全结束，完整产品范围为 `BLOCKED`，#27 保持 OPEN。
200 个计划项中完成 74 项；第 75 项及其唯一允许的新请求均发生 provider 临时错误，
没有 assistant 答复。第 76–200 项共 125 项未运行，不能以成功前缀替代完整分母。
实际 76 个 run／76 个不同 request ID、74 个非空 assistant 答复与两次失败全部保留；
第 75 项不再无限续发。失败调用的 unknown 用量／原预留不改写为零。

原包源码 commit 为 `82d698d94fa429a831706b5188a8886944ff7d1c`，bundle manifest SHA-256
`3a0028bdde589492159dcfa6d99ac95d1fdcf76369663d8f5c8290a1be5f277e`；
预付冻结输入 SHA-256 `966f7b1422e0552cdf54a02cc162b35d889f3b069549f5c5f70d659597aba95b`。
200 项原 prompt 字节及包内 3,225 个文件保持原指纹。后续 #28 只改仓库 Go 源码和说明，
该轮仍执行原独立 v8 包；没有热替换或 host Task Workspace 编辑。

- Go native 导出有 1,839 个事件；`cmd/trace-audit` 对全部 76 个实际 run 分别退出 0，
  校验 hash、canonical Surface、actor/root、原 admission 前缀和已结束任务。
- 发生 16 次实际模型 compaction，包含第 75 项失败前已持久化的摘要。
  第 40 项实际浏览器刷新后，892 个 native 前缀事件和导出字节／checksum 完全相同。
  第 80／120／160 项刷新和第 100 项空闲 Go/Python 重启均未运行，不能算通过。
  原 observer 故障、NOT_SENT 和第 4／9／30 项同 request ID 的 HTTP 冲突恢复分别保留；
  无重复 canonical run。早期 v1 注册准备失败没有 Session 或付费调用，亦未删除。
- 实际后端读回全部 76 个 run，共 691 个唯一且已结束的 span，父链无缺失或环。
  对上 76 个 native run root、181 个模型准入 ID／purpose 和 88 个工具身份。
  679 个 UNSET、12 个 ERROR 均保留；ERROR 包含两次反思、两次失败 root、
  三次 provider attempt、两次 chat 和三次 provider call。
  这只是该停止尝试的 native／结构诊断，不是产品 Trace 或完整任务验收：
  本轮尚未到代码修改、项目测试、需求回忆及提案阶段。
- 181 次模型准入包含 153 foreground、12 memory_reflection 和 16 compaction。
  三次已结束调用用量未知，原 reservation 保留。原批次最终已确认 56,760,832 tokens，
  比开始前增加 5,429,662；总预留 5,606,024,576，含旧预留，不能视作实际消耗。
  费用仍 unknown。未 reset、手动结算或要求逐笔批准；服务商 1 亿硬上限保持原约定。
- 通过实际产品入口结束任务，HTTP 200；Go 确认 ActiveCalls=0、TaskID 为空。
  333 个受保护文件、4 个 index 未变；所属 launcher／子进程全部退出，浏览器关闭，
  两个既有 controller 恢复为初始 exited，cleanup errors 为空，无清理其他环境或业务数据。

完整运行物料位于既定 Docker `.local/receipts/product-long-dialogue-20261019/go/v2/`。
诊断 audit SHA-256 `708c1c856a608bc4a3dd0409fbe3b7aec8a2a1a141f41f85b022de03c79696a7`，
保留全部运行、后端读回、调用未知项、保护核验和完整未运行列表。
Memory 的压缩后轨迹拒绝另见 [#28 修复范围](memory-compaction-2026-10-20.md)，
不能把其短专项或源码结果计作本票的 200 轮通过。后续完整运行另行固定新版本和输入；
本轮原 SHA、结果、失败、分母和 checksum 不覆盖。
