# 任务能力随会话更新

这是 [#1](https://github.com/huadeng408/CodeOps-Agent/issues/1) 中既有能力状态／结束入口的
普通前端修复，沿用当前布局和已确认的浏览器／HTTP 接缝；不修改或关闭父规格。
起点为 `205e43e5a5f4d1485a6c540c1da5651da102f6c0`。

实际 v9 的 #28／#27 运行中，启动时批次尚无任务占用，能力面板后来没有随 Session
更新重新读取，结束按钮直到手动检查能力才出现。原运行收据保持原身份。
本次源码修复与原便携包运行、完整 200 轮及发布范围分别记录。

## 改动与边界

`CapabilityPanel` 在所选 Session 或其事件数量改变时复用原能力查询，继续取消旧请求、
等待服务端状态并保留结束拒绝。`MessageList` 收到比当前游标新的实时事件时，复用
已有的 Session 刷新回调，让后台反思的后续事件也更新外层会话状态。
没有新定时器、自动结束、模型调用、Go 权限／计量改动、依赖或布局。

旧结束入口的浏览器 fixture 同步当前预算文案；活动调用受阻与已结束用量 unknown
保持区分。CI 沿用既有 preview 服务，增加运行能力与结束入口的浏览器回归。

## 验证范围

- 第一轮公共浏览器回归：一次 UI 发送后缺少结束按钮，退出 1；增加既有能力查询的
  Session 依赖后，同例退出 0。不是通过直接调用组件内部方法来构造结果。
- 第二轮：前台结束后注入外部后台事件，原界面未更新活动状态，退出 1；补上实时事件到
  外层 Session 的刷新后退出 0，并继续覆盖后台结束后重新可用。两轮 RED 均保留。
- `frontend/` 中 `npm test` 退出 0：11 PASS／0 FAIL／0 SKIP。
  `npm run build`（含 TypeScript 检查）退出 0。
- 对实际 production build 的三个浏览器 fixture 均退出 0：新能力状态 7／7、结束入口
  7／7、双标签重连 6／6。覆盖初始未占用、首次准入、后台活动／结束、预算不变、
  不自动结束、服务端拒绝、跨 Session 拒绝展示、额度耗尽后结束、重连及草稿／无重复发送。
  所有 fixture 均零付费模型调用，无 page error；它们不替代真实 Go/provider 产品证据。
- `git diff --check` 通过。Go／Python 本机回归和真实 provider 回归未在这个前端范围内
  重跑；上游 502／503 的独立 #27 结果见[原完整分母](product-long-dialogue-2026-10-19.md)。
  完整发布未运行，不据此把本修复标为 BLOCKED 或将其作为 #27 的新门禁。

源码 SHA-256：`App.tsx` 为
`f5840597421f97f35b90b63c6ed587a2a1b804277d67c049b6bf7186fdf48fc5`，
`CapabilityPanel.tsx` 为 `47943fe5e68fa15946cb43496f504440f5e36a74f79781c31ef071ca2eff9597`。
构建 JS SHA-256 为 `27cc017a991be755c7c7fcde19c112ba8fa541fd5a4d5f4c690adf4f69662955`。

原始 RED／GREEN、命令退出码、日志及截图保存在
`.scratch/task-capability-refresh-20261020/`；最终能力 fixture 收据 SHA-256
`f3edef430c9b91e50649228758cb8de8a4b4b671100c3af01fee1a588d958dc8`。
最终结束 fixture 收据为 `d2bb531c0192f2a5e89e97be5c1155b2aaea11d8538af1db7f106ffb64b02399`，
重连为 `49c2fdf92e37b3d1e82005a363f7a8e2d4e27772119653ce67b4a2f55f3c8071`。
交付记录另列独立 Standards／Spec 复审、精确提交和新鲜 CI；不把 AI_REVIEWED 称为人工复核。

## v10 实际入口补验

后续[独立 #27 v10／v4 尝试](product-long-dialogue-2026-10-19.md)使用本修复源码
`60b5b9b7` 和上述相同前端构建指纹。实际会话经历三轮答复、两次失败及后台尝试；
所有调用停止后，实际断言结束按钮可见且启用，全程未手动检查能力。
点击结束得到 HTTP 200，native ActiveCalls=0、TaskID 为空，用量及原预留
均未改变。实际 UI 观察收据 SHA-256 为
`a8d3d6d711342fbc49c6dd6fb1d3fd1f7b27950d06cd3f2b638a0111cd19d5a3`，
结束响应为 `0095ff65fcac7e2a03db1676b851d94e41ea6ff62b7ff7b4bf7deafe0b8f37a2`。
这是该入口最终可结束状态的真实补验；首次准入、后台禁用／再启用和跨会话拒绝等
逐步转换或边界仍由前述 fixture 分别证明。
#27 的 3／200、全部 Memory／provider 失败及完整发布缺口保留，不由本项补验替代。
