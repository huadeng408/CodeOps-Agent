# 压缩保留当前输入的源码修复

范围为 [#27](https://github.com/huadeng408/CodeOps-Agent/issues/27) 的 Python
压缩选择修复，基线为 `7611d491ea533868d32cf78f47eb1602f1512d2d`。
这不是完整 200 轮、旧取消恢复或发布资格。

## 复现与行为

公开 `ConversationRunner.run` 接缝在一次六个 Read 的批量结果后，下一次模型请求
丢失当前用户输入；同时压缩报告的消息数包含了没有 canonical 来源的本轮消息。
修复前回归退出 1，修复后退出 0。没有可压缩历史时也保留当前输入及六组工具配对，
不产生压缩更新。

现在 Harness 请求只让原有选择器读取带 canonical 来源的历史，
以第一个没有来源的当前消息为保留边界。本轮用户、模型及工具消息继续存在；
沿用原来的 token 策略、工具配对与 Go provenance／active-input 拒绝规则。
没有新增依赖、协议或事实存储。当前轮次自身过大时仍可能耗尽窗口，
本修复不授权删除当前输入或放松 Go 校验。

原 v11／v5 的 3510 个事件已重新读取验证。用原第 138 轮的实际 Surface 文本、
按协议重组的六个 Read 和固定测试摘要作离线对照：基线丢失当前输入，
修复后保留输入及工具配对，退出 0。该对照没有调用模型或执行工具。
原失败的被拒绝压缩更新没有完整留存，因此这是可复现的拒绝路径，
不能证明原第 138 轮一定由它触发。原尝试仍为 137/200，
取消 unknown、保留任务槽及 [原始失败记录](product-long-dialogue-2026-10-20.md) 均不改写。

## 验证与原始产物

- Python 相关回归：`python -B -m pytest -q tests/test_compaction_provenance.py tests/test_compactor.py tests/test_conversation_tool_adjacency.py tests/test_server.py`，119 PASS，退出 0。
- 第一次相关回归 115 PASS／4 FAIL、退出 1 保留；四项失败来自旧测试构造的未初始化 runner，已沿用邻近代码的默认属性读取方式修复。
- Go 兼容回归：`go test -json ./internal/session ./tests/go -run 'Compaction|ConversationHistory' -count=1 -timeout=180s`，叶子 21 PASS／0 SKIP／0 FAIL，退出 0。
- 完整 Python：`python -B -m pytest -q --junitxml=.scratch/long-dialogue-recovery-20261020/full-python.xml`，2655 PASS／17 SKIP／0 FAIL，完整分母 2672，31 项依赖弃用警告，退出 0。
- Standards／Spec 独立 AI_REVIEWED 各 0 项问题；Spec 另在内存中替换回旧方法，两个新用例均从 PASS 变为 FAIL。Python AST、精确暂存凭据／机器路径扫描及 `git diff --cached --check` 均退出 0。当前提交的源码 CI 结果由正式票交付评论绑定。
- 产品／真实模型／发布验证未运行；旧产品包未替换。

本机原始证据位于 `.scratch/long-dialogue-recovery-20261020/`，没有随源码发布。
`native-before.json` SHA-256 为
`4d4ede5b5a63f7f5997c44ea5f7451f8471210ec900e4900bc789ace95fac83c`；
原边界 Surface 为 `190a6b56380dcc13c25ba95c212a61159c38982ed0b04ef6ee75b10f2e18cf2c`；
离线对照日志为 `f8923638219e5adaa14c0080d080783fb44f54eee11e460ccd276c59ca2ee8e7`。
