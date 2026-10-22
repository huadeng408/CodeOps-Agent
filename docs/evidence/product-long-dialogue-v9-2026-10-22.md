# 独立长对话 v9：首轮刷新与阶段核验

BeeAPI 路由恢复后，按用户授权重新冻结独立的原 200 项任务。
本记录只核验同一 Session 的前 40 项和首轮刷新；后续项继续执行，
[#27](https://github.com/huadeng408/CodeOps-Agent/issues/27) 保持 OPEN。
没有拼接 [v8 的 0/200 失败](product-long-dialogue-v8-2026-10-22.md) 或其他尝试。

## 运行绑定

- 便携 v15 → Go Harness → Python → 实际浏览器，Session
  `cfdff4a6df8558daefe1333184942f5c`。
- 运行源码 `f46e7d13880a56b9483da46a3dd762ba0666ed77`；
  冻结输入 SHA-256 为
  `c2d4a79037a2016188a246589f5cf7302c07ab7782cec55f7f7db4772a70138d`。
- 原 200 条输入 SHA-256 为
  `3b53ca87f1e5a21acc26636d4597574baccf7024adfe6fd00a825230e3143e2f`；
  grok-4.6、Anthropic Messages 路由、32,768 应用窗口和原计量批次保持。
  模型名称不代表已验证的服务商 revision，费用状态仍为 unknown。
- 本节点核对 621 个源码、3225 个运行文件、17 个过程指纹及加载的 AGENTS；
  333 个受保护原文件、Git 索引和 HEAD 均保持原指纹。

## 前 40 项的实际分母

| 范围 | 观察结果 |
| --- | --- |
| 计划项 | 前 40/200 完成；本前缀无失败、取消、空回复或新请求重试 |
| 请求 | 40 个不同已准入身份、40 个 completed run；42 次实际浏览器 POST |
| 刷新 | 第 40 项后重开同一 Session；前后 957 个原生事件完全相同 |
| 压缩 | 6 次真实模型压缩；当前输入、来源 checksum 与来源工具配对通过 |
| Memory | 40 份原生轨迹投影一致；5 次反思失败及 blocked 事实保留 |
| Trace | 40 个 run、454 个结束 span：449 UNSET、5 ERROR |
| 工具 | 42 次 Read 调用／结果／后台工具 span 对应，全部退出 0 |
| 模型 | 126 次原生预留与后台 attempt 身份／purpose 完全对应；均 HTTP 200、用量已确认 |

126 次模型调用包含 foreground 80、memory_reflection 40、compaction 6。
本前缀已确认输入 2,609,347 tokens、输出 295,692 tokens；没有改写旧 unknown 或原预留。
分母用第 41 项实际原生输入作为边界，后续运行不计入本前缀结果。

反思错误发生在第 13、14、16、26、28 项；后台 stage 均为 `reflector`，
code 均为 `reflection_invalid_or_unavailable`，与五条原生 blocked 事实对应。
HTTP 200 和轨迹投影一致均不表示这些反思成功，也不据此推断具体解析原因。

第 15、28 项首响应为游标冲突 409，观察器断言因此返回；实际前端以同一请求 ID
重试，分别核对到唯一 run 后继续，没有操作员新发请求。原首响应、错误及对账保留。
早期压缩前的 Surface 读取失败、一次结果转发错误和一次只读结构探查错误也保留，
后续核验使用成功读回的独立快照，不改写原失败。

六份实际压缩摘要的功能约束段落均经 `AI_REVIEWED`：最初九项功能／兼容约束、
实施阶段边界、隔离规则和无关脏文件保护规则仍在；这不等于全部摘要或 200 项语义验收。
刷新截图显示同一标题及历史，面板最初仍在加载；第 41 项提交截图显示隔离工作区
已准备、99 项文件清单及可用测试入口。完整浏览器错误汇总仍待最终读回。

## 可复现观察

以下只读命令均退出 0，完整命令及 stdout／stderr 指纹在
`.scratch/product-long-dialogue-20261022-v9/`：

- `verify-browser-boundary.py 40`：原生前缀、请求、Surface／Memory 投影、运行指纹和原件。
- `read-backend.py boundary40 <全部 40 个终态文件>`：实际 Phoenix 后台读回。
- `join-runtime-cutoff.py boundary40`：原生根／工具／模型／计量与后台逐项对应。
- `check-boundary40-models.py`：完整前缀模型身份分母核对。

原生、准入和后台原始观察保留在
`deployments/docker/.local/receipts/product-long-dialogue-20261022/go/v9/`；
截图在 `output/playwright/product-long-dialogue-20261022/go/v9/`。
两个原有控制容器继续服务本轮，Compose project 为 `localcode`，镜像和身份保持。

| 观察文件 | SHA-256 |
| --- | --- |
| `boundary40-after-session.json` | `27dc7377c1fdce0c4db2c758e6bd698b226c3b8c7e6265f2972d8d5324b622b4` |
| `boundary40-backend-index.json` | `b7f875be7d50e310f6345a7ecbdc6841735609700e3d1e47c8ec71c4d624a35c` |
| `boundary40-verification.json` | `6338ad248e75b0bc1906745c4792ae265abbe44f515a4f316b98e9dc4fd51781` |
| `boundary40-native-trace-join.json` | `05193f62f4c3302552271a729d9c5f24d7c3f17a930bc775a852c090ecb9ed32` |
| `boundary40-model-denominator.json` | `2bc91282c9bae280fa25900cd8ef0de3337975b3590bb8f6ec2a2f26d6f1de04` |

本节点验证范围为前 40 项的实际完成、首轮刷新和上述结构／身份对应。
80／120／160 刷新、100 进程重启、后段代码与项目测试、完整目标召回和最终
200 轮 Trace／计量／正常结束／资源恢复仍须各自验收；本记录不证明完整产品或发布合格。

本次说明文档交付运行
`python -m pytest -q tests/eval/test_ci_scope.py tests/eval/test_release_gate_workflow.py tests/eval/test_release_evidence.py`：
23 passed，0 failed，0 skipped，退出 0。未在本机重跑完整 Go／Python／frontend 套件；
完整发布验证未运行。前述原生／后台观察分别声明其实际产品验证范围。
