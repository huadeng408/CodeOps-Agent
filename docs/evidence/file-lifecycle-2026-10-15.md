# #9 文件生命周期提案：2026-10-15

工程状态 IMPLEMENTED；VERIFIED 限于下述单测、集成与产品接缝。验收范围是 Windows 本机单用户的新增、修改、删除、
重命名和多文件用户批准；复用已选右侧卡片、Go TaskWorkspaces 和 Session Ledger。
完整进程故障恢复与对账矩阵属于 #10；Memory、Trace、十轮与完整发布分别验收。

## 行为与原要求

| 原要求 | 实现与验收接缝 |
| --- | --- |
| 存在、操作与内容分开；空文件与缺文件、rename 双路径 | Go 修改事实增加可选存在字段与目标路径；Python 同步 Delete／Rename 工具定义；真实空文件创建／删除、重命名与 diff |
| 全部受影响原基线、授权根、link／reparse 在效果前核对 | 共用 Windows ApplyFiles 固定祖先、metadata、index、其他输入并独占打开全部既有源；晚项已知冲突使零项执行 |
| 每项 intent／outcome 重建；不宣称整批原子性 | 同一 Ledger 的 application-item-intent／outcome；父结果由逐项事实重建；旧单文件 API 单向调用共用模块 |
| 部分／CAS／写入／unknown 留完整分母、禁止盲重试 | 首项成功、第二项 intent CAS 拒绝后保留一个完成和三项未执行；模块重建及重放保留用户新文件；outcome 不可用保留 unknown |
| Go／Python 调用方、单向兼容、原单文件保持绿色 | Go HTTP／CLI 修改元数据适配；旧事实字节不改写，读取时补操作标签；既有确认／拒绝、重放与授权回归 |

已删除的原目标再被 rename 替换时，最终提案采用源删除和目标修改，
不会遗漏原目标或生成在未变化基线上无法应用的 rename。新增父目录的实际效果也记录在该项结果。
整批可能部分完成；无法确认写入、关闭、结果落账时不宣称事务成功，不自动回滚或重试。

旧恢复适配不能表达新生命周期或显式既有空文件，因此在 intent／文件效果前拒绝。
旧 CLI `/undo` 的阻断同时来自持久空条目和 canonical lifecycle invocation；
Undo snapshot 保存失败而 invocation 成功时，重开仍禁止误用较早撤销项。
该保护有 SQLite 保存失败回归；它不提供完整生命周期撤销。

Changes 一直是 Harness 本地 Go 元数据，不属于 protobuf ToolResult 传输字段，
本轮没有更改线协议，也没有手工修改生成物。Python 只声明工具，实际效果由 Go 授权和记录。

## 产品与失败范围

实际生产 Go HTTP、鉴权、Ledger、Windows 文件系统、已批准分组 OpenSandbox 与 Chromium
执行同一组四项修改：修正 Go 文件、新增嵌套空文件、删除既有空文件、重命名既有文件。
原基线真实测试失败，模型工具后的测试及冻结提案重测通过；批准后核对四项实际效果、
原 HEAD／index 和隔离结果，回读／重放不再次写入。
外部模型 HTTP 和编排 gRPC 为 fixture，真实付费调用为 0；该证据证明生命周期与用户批准，
实际模型生成能力沿用 [#7 的独立范围证据](proposal-closeout-2026-10-14.md)。

部分失败由公开 Go Ledger 接缝注入第二项 CAS 拒绝；实际 Chromium 使用明确标注的
HTTP fixture 检查“部分应用，需对账”、四项分母、已完成／未执行和无重复应用按钮。
它不作为真实 OS 进程崩溃证据。

原始产品事实、输入清单和浏览器收据分轮保存在
`deployments/docker/.local/receipts/file-lifecycle-20261015/`；工程日志在
`.scratch/file-lifecycle-20261014/`。原始运行目录不可覆盖，脱敏收据仅保存结果及指纹。

## 当轮验证

最终源码审阅 Standards／Spec 各 0 项未解决发现，审查为 AI_REVIEWED。
首次完整 Go 的分母是 1,610 pass／75 skip／2 fail test events，43 包通过、
13 无测试、2 失败；保留交互进程输出超时和未暂存删除使源码清单无法读取的原错误。
首次完整 Python 是 2,506 pass／17 skip／1 fail，非合作 adapter 的 0.25 秒断言用时约
0.30 秒；该项及相关工具目录共 16 项单独重跑通过，未放宽断言。

首轮产品运行因测试错误地要求无内容变化 rename 具有 `+++` 行而失败，第二轮因
浏览器测试错误地按独立节点匹配 summary 内操作文本而失败；修正测试后产品链路通过。
首次部分 UI fixture 缺必需文件 mode 而失败，首次旧 UI 入口停止导致连接失败；
修正 fixture／启动本轮入口后通过。所有红阶段与失败原始结果保留。

| 最终命令 | 完整结果与产物 |
| --- | --- |
| `go test ./... -count=1 -json` | 1,622 pass／75 skip／0 fail test events；45 包 pass／13 无测试；退出 0；`go-full-final.jsonl` |
| `go vet ./...` | 退出 0；`go-vet-final.log` |
| `.venv/Scripts/python.exe -m pytest -q` | 2,507 pass／17 skip／0 fail；3 warnings；退出 0；`python-full-final.log` |
| `npm test`、`npm run build`（frontend） | 12 pass／0 skip／0 fail；类型检查／构建退出 0 |
| `node tests/taskProposal.browser.mjs` 及同入口 `--partial`（frontend） | 旧卡片 6／6、部分结果 5／5 fixture 检查通过；各退出 0 |
| `go test ./internal/cli ./internal/session ./tests/go -run 'Test(CLIUndoLifecycleFence\|WorkbenchRestoreCodeChanges\|Undo\|Lifecycle)' -count=1 -json` | 15 pass／0 skip／0 fail；退出 0；`legacy-fences-v2.jsonl` |
| `python .scratch/file-lifecycle-20261014/run_runtime.py final-v2` | 实际生产 Go HTTP／分组 OpenSandbox／Chromium；1 test、7／7 检查通过；退出 0 |

产品分母为四个独立顶层运行：前两项失败、后两项通过；每项计划四项原文件效果与七项
浏览器检查，未到达的检查如实标为未运行。最终运行有父 intent／outcome 各 1 条和
逐项 intent／outcome 各 4 条。最后一轮的全部运行输入与当前内容逐项核对相同，
源码追溯为 `578bb464154f29ba88a897083e1cc9c0b1b0b5b6` 加本轮实现。
单测、集成、上述产品接缝通过；新付费模型调用、完整崩溃矩阵和发布未运行。
本票声明范围无未解决 blocker。

最终完整回归、源码绑定、命令退出码与各轮分母见
[脱敏收据](file-lifecycle-2026-10-15.receipt.json)。
