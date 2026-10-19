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
