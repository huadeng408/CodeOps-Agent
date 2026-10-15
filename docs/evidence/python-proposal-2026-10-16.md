# Python 冻结提案执行

范围：#18 已确认的 Python 提案执行前提。工程状态 `IMPLEMENTED`；
`VERIFIED` 仅指本轮源码回归、受控真实 OpenSandbox 集成和便携构建。
完整 Python 模型任务、十轮浏览器对话及发布验证仍未完成，不据此关闭 #18。

源码提交：`8102785a1764ce16790682567ab8387f1a16b0b5`；基线：
`351d0af25ce2866b340708c187f6710a76415e47`。两个审查轴均为 `AI_REVIEWED`、0 发现。
提交后三点差异与受审阅的八文件暂存差异完全一致，SHA-256 为
`f8946961d8ba34954c5e7c6b6d82e54d36057caa85783b206e2ce8329daf3cba`。

## 行为与保护

- 原入口、提案显示命令、实际 Runner 请求及冻结输入校验都限定 Go。现有 Python profile 现在沿用同一提案、沙箱和应用链，显示固定命令 `python -m unittest discover -v`。
- 基线与提案共享原有离线 bootstrap，其字节与基线提交相同；脚本只将已批准的 pytz 安装到沙箱 scratch，不安装到宿主或源目录。wheel 的版本、来源、MIT 许可、大小和 checksum 保持原批准值。
- 提案 intent/outcome 绑定同一 dependency operation，并核对准备时的基线身份；旧 Go schema1 事件仍按原格式读取。冻结执行只接受匹配的语言 profile、固定 argv 和合法依赖身份，保留完整归档、源码 checksum、只读转换及停止确认。
- 公共回归使用真实 SQLite、Git、SessionRunner 和 Go Edit；只有外部 conversation/runtime 及下载 transport 使用 fixture。覆盖未提交原文和 index 保留、重开重放、批准应用、外来 owner 拒绝，以及依赖被替换后的拒绝。Python 结果继续经过既有审批和文件应用边界。

## 本轮验证

| 验证 | 完整结果 | 退出码 |
| --- | --- | --- |
| 定向 Session／sandbox 回归 | 11 个顶层测试通过；含子测试 54 通过、0 失败、0 跳过 | 0 |
| 本机 Windows 全量 Go | 1224 个顶层通过、76 跳过；含子测试 1752 通过、79 跳过；47 个测试包通过、13 个包无测试文件 | 0 |
| `go vet ./...` | 通过 | 0 |
| 受控真实 OpenSandbox Python | 1/1 unittest，通过依赖导入、nobody 身份及源文件写入拒绝断言；输出完整，输入 SHA 绑定，停止已确认 | 0 |
| 官方 Windows 便携构建 | 3219 个运行文件与 605 个源码指纹全部核对 | 0 |

定向命令为：

```text
go test ./internal/session ./internal/sandbox -run 'TestPythonProposal|TestProposal|TestTaskBaseline|TestTaskDependency|TestOpenSandboxRunnerReturnsCompleteCommandOutcomeAndStopsOwnedSandbox' -count=1 -json
go test ./... -count=1 -json
go vet ./...
python scripts/build-windows.py --output 'deployments/windows/.local/迁移空间/CodeOps Agent python-proposal'
```

本机设置测试专用 `CODE_AGENT_TEST_PYTZ_WHEEL`，复用批准的缓存文件并校验原 pin；
没有这个输入或缺少 Windows 原生文件保护时，相应测试明确跳过，不隐式下载或替换依赖。
初始两次测试 fixture 配置失败、两项确认 Go-only 限制的 RED、GREEN、全部跳过及原始日志均保留。

[源码 CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38804182881)四项均通过：
Go 47 个测试包及 vet、13 个无测试文件包；Python 2563 通过、12 跳过、3 warnings；
Frontend 12 通过及 build；Windows startup contracts 10 通过。
这些源码结果不计作真实模型任务或发布证据。

真实 Runner 探针沿用现有分组和固定镜像，未调用模型。任务资源已停止回收；
原有 33 个容器身份、控制服务原停止状态、批准 manifest 和测试源文件保持原值。
该探针使用受控测试源码，不能替代公开 schedule 任务的 81 项基线或完整模型验收。

## 新产物与后续输入

- 新包 manifest SHA-256：`563bd0dd15a249360792a529addf1dec258ec9dd6dede1fe34e67682c4261dbe`；服务器 `ebfc2cf5cbb84a618120a85649e056c78b2642e4253433a877427e6d0345e60e`；启动器 `82008752e10a589659dad235738077892dd6c3fe41644083c5e73f2e898c347a`。
- 下一次任务冻结输入 SHA-256：`6842a7a0b74770cb0602b7353811f435887c8332d461fe997f908ff83bb349d5`，包含 609 个参与源码／验证器指纹；两个正常公开任务、每项 12 个场景、模型／镜像 pin 和持久批次保持原值。原工作副本共 132 个文件、index 及 dirty/new 状态均核对。
- 原失败收据和旧安装包保留；新驱动仅更换本轮路径和身份。本轮未重复浏览器取消专项，后续沿用已验证的可访问名称及同时处理响应等待的操作方式。
- 产物索引：`deployments/docker/.local/receipts/python-proposal-20261016/artifact-manifest.json`，51 项，SHA-256 `006f966584d4ea095594d6fa1119efd2c2f8bf6df70bb18c75ab9cbbc80b33a8`。已复核全部 checksum，确认产物不含控制服务凭据，文本密钥形状扫描 0 发现。

#17/#18 保持 OPEN；两项十轮实际浏览器任务、完整成功 Trace、#19 故障矩阵与发布验收仍按各自范围推进。
