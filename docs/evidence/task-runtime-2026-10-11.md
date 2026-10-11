# Shared Runner evidence — 2026-10-11

工程切片：`IMPLEMENTED`，源码 `08af7c76ead4abe23ab9369b5d8b7bf9472bf4ec`。
完整 [Issue #6](https://github.com/huadeng408/CodeOps-Agent/issues/6) 继续 OPEN／`BLOCKED`。
本项是现有 Go Docker/WSL Runner 的执行边界；两个语言用小 fixture，不能升级为
公开项目正常基线、产品入口、模型代码任务或新候选沙箱的 runtime 证据。

## Change and review

Go 先 create 获得完整 CID，再按 CID attach；退出／取消只处理本次创建的临时
容器，不能用 Docker 客户端结束推断容器已停。创建响应丢失、无可信 CID 或
清理无法确认时保持 unknown／126，保留不透明创建标识；明确的本地缺失镜像拒绝
仍为 125。jobs 不把未知结果标为 killed。

同步捕获及 setup 诊断上限为 1 MiB，保留头尾和截断标志；Bash/Git 及 Spill
区分完整输出与捕获子集。共享 argv 拒绝宿主路径中的 Docker mount 分隔符，
具体 Runner 也检查真实 trust root。

显式 `sandbox.toolchain` 支持 `go`／`python`，要求 digest-pinned 镜像、trust
root 和只读工作区；两个 CLI 调用方传递同一配置。即使 `enabled=false`，显式
工具链也强制经 Runner，不能落到宿主 adapter。旧 generic 开发 opt-out 未扩展。
编译 scratch 为容器内 `/run/codeops` 的 512 MiB tmpfs；原 `/tmp noexec`、
只读 root／workspace、非 root、资源限额、`--network none` 和 `--pull never` 保留。
Go cache／临时二进制及 Python bytecode 不写任务基线；本项未准备第三方依赖。

`implement` 要求的双轴审查为 `AI_REVIEWED`，Standards **0**、Spec **0** 剩余。
审查发现并修复 attach 名称替换、setup 截断状态、创建响应丢失／无可信 CID 的
对账标识，以及显式工具链配置落到 host 的路径。不是 `HUMAN_REVIEWED`。

## Final actual Runner run

运行 `task-runtime-bdc6ee46-3906-4148-83da-c78ff574f2d8`；先沿用
`scripts/rag-agent-e2e-runtime.ps1` 的 `Wait-DockerDaemonReady`，未删除已有容器、
volume、索引或业务数据。仅 Go 管理自己的新建临时容器。

命令：`go test -c -o <run>/sandbox.test.exe ./internal/sandbox`，随后
`go tool test2json -t -p code-agent/internal/sandbox <run>/sandbox.test.exe
-test.v -test.count=1 -test.run=TestDockerRunnerIntegration|TestSandboxRoutingRealProcessE2E|TestTaskToolchainIntegrationBaselines`。
构建／运行均退出 **0**；**9 pass test actions**（8 叶子用例＋1 suite parent）、
0 failed、0 skipped，完整名称保存在 receipt。覆盖只读、无网络、缺镜像拒绝、
2 MiB 输出限额、同步超时、交互 stdin／重复 kill 后精确 CID 不存在、实际路由，
以及 Go/Python 小项目各 1 条测试。WSL 本轮仅 argv／拒绝回归，未宣称实际 WSL运行。

固定的已有本地镜像：

- Go：`golang@sha256:699337d620559a59b4a2bb298ad59611e535d2ee755a34cf2d2a98f37578dc80`，
  实际 `go version go1.25.14 linux/amd64`。
- Python：`python@sha256:78387bc3881b8273120a12ebe6c1ab22b018ccc2c9adf565ae1ac9b536e184ea`，
  实际 `Python 3.12.14`。

镜像库存和各 fixture 原文件／库存未变。未调用模型、读取 API 凭据或重置 token 批次。
镜像身份检查不等于完整任务环境及所有传递部件已获许可／批准。

| Artifact | SHA-256 |
| --- | --- |
| `output/playwright/task-runtime-bdc6ee46-3906-4148-83da-c78ff574f2d8/receipt.json` | `0afcfd484021aca2c00e6ec2135fc7cd29094ce8ab590c3e7395ab38b42bd658` |
| 同目录 `sandbox.test.exe` | `6179000aa56ba9bf898ab7e59de5771423b9b0e24e1ea09ce77e2cfa8e089330` |
| 同目录 `runtime.log` | `00308f3b5304b37a5c8717d94a1962bce480488fb9280ff768064ab85f1c3f7f` |
| 同目录 `trace-50a9d64ffe3cb57cd574783457e6dd17.json` | `12c813138fcd4a2b20728d056b91c47eb7a7b69c8f222f81c659663bba77b1ed` |
| 同目录 `trace-943babcbbdf9a507fe5875ba3f660436.json` | `a517525d4369780355423a0dbf703e8560b1e502da8bfe1dad725c7f6af24e88` |

两个非零 trace ID 分别为 `50a9d64ffe3cb57cd574783457e6dd17` 和
`943babcbbdf9a507fe5875ba3f660436`。实际 Phoenix 的分页 spans API 读回每条
已结束的 Go root `sandbox.toolchain.baseline` 及 version／test 两个子 span，
共 6 spans；这是共享 Runner trace，不是完整模型产品 trace。

## Final regressions and source binding

定向 sandbox／tools／jobs／config／CLI 测试通过，退出 **0**。
最终 `go test ./... -count=1 -json`：**1,367 passed／60 skipped／0 failed
test actions，43 passing packages**，退出 **0**；`go vet ./...` 退出 **0**。
`git diff --cached --check` 退出 **0**；17 个精确 staged 源码路径的秘密形状扫描
零匹配（不输出匹配值）。用户已有 `AGENTS.md` 未修改、未暂存。

完整回归 receipt：
`output/playwright/workspace-phoenix-regressions-9c201d9c-fc24-4b56-a8a3-f9b3e11bf0e8/receipt.json`，
SHA-256 `0f4f229df801031051a56ea2326fa0b4e60e657d33d1b91e0324ef12905534bb`。
原始日志及跳过分母全部保留；Python/frontend 源码未改，本轮未额外运行其完整测试。

提交后绑定：
`output/playwright/task-runtime-source-binding-27abdc46-a1b1-4db2-8fad-65e252465fac/receipt.json`，
SHA-256 `ef7165d34f3253ac3b59c8607095a62c30ee4b7566a01f7c29302d83dccce56c`。
运行时 439 个、完整回归 442 个源码文件的原始 hash／artifact 与
`08af7c76` Git blob 逐项一致，仅 Git 比较允许 CRLF 规范化，不改原始 receipt。

## Retained failures and limits

- 首轮 RED：输出保留 2 MiB，超时后容器仍在运行。日志
  `.scratch/matt-refactor/task-runtime-red.log`，SHA-256
  `6ff1293a8926c32d2d51ab076bdda0041cdbda013ea543b19a6a26c057d57b21`。
- create 改造中保留缺镜像退出码差异及 Docker not-found 文案断言失败，均修复；
  `.scratch/matt-refactor/task-runtime-green.log`／`task-runtime-green-2.log` 保留。
- lost-response／flood、invalid-CID 和 CLI opt-out RED 都保留：
  `task-create-unknown-red.log`（`53ded0db0091a1ae00b2b2bfd234a10b226f58c38769eefa4e5cba17fe12b4d1`）、
  `task-create-id-red.log`（`0cf5507dc38c1ca0161cbbd00e288b81fe69211d62a054c16b5101715f0f2c6f`）、
  `task-cli-profile-red.log`（`a98d27822730d7214a1c7e3f96fa58cf813eedc9b4a020b85dffa57b1dc1a055`）。
  自进程模拟只证明分类／截断策略，不伪装真实 daemon 故障。
- 第一轮 full Go/vet 命令通过，但最终审查修复时源码改变，其 receipt 退出 **1**：
  `workspace-phoenix-regressions-627ea46f-c42f-4de0-bfee-ec0edd1f8bf5`，
  SHA-256 `de2089dec2ef4838c2e929ae04816a97f8003e9ca4f02b1b1ad301af19eebc3e`。
  中间 9-action/Phoenix 运行 `task-runtime-ea91554a-676b-4cc9-b37c-425ef48b52fb`
  保留；两者均不继承为最终源码验证。

用户已选择测试入口 A，并要求先调查 GitHub 沙箱。候选比较见
[固定源码调研](../sandbox-options-2026-10-11.md)；microsandbox Windows WHP 仍 preview，
未安装或运行候选。pytz 是 `schedule` 的时区测试依赖，来源未批准、未下载。
完整 #6 尚缺批准的依赖准备、产品入口／UI 与两个公开项目正常基线证据。
若选择新候选，另需其实际验收；调研建议没有增加正式任务的关闭门槛。
不关闭 Issue，不运行或宣称发布 gate 已通过。
