# OpenSandbox Python 产品基线：2026-10-12

范围对应 [#22](https://github.com/huadeng408/CodeOps-Agent/issues/22)，依赖已关闭的
[#21](https://github.com/huadeng408/CodeOps-Agent/issues/21)。沿用已选 A 右侧卡片、
公开 TaskWorkspaces／Runner 接缝和 Matt Pocock implement → TDD → 双轴 code-review。
单测／集成及本机产品范围为 `VERIFIED`；完整模型任务、#23 和发布范围另行验收。

## 固定输入与真实运行

- schedule `82a43db1b938d8fdf60103bd41f329e06c8d3651`（MIT），完整克隆，32 个源码文件。
  冻结测试文件包含全部 81 个 unittest 方法，没有 pytest fixture 或参数化测试。
- Go 准备唯一批准的 pytz 2026.5 MIT wheel：506,342 字节，SHA-256
  `e658af3757f9e26a9d25dd2aff38335acd92bc9104f890a894b2c1ba28311b03`。
  来源、transfer、包身份和许可证校验、独立 dependency operation、Ledger intent／outcome
  均由 Go 执行；无主机安装或额外任务依赖。
- OpenSandbox 上游仍为 `f89eff24bbd15ae1c37a47769f7667849e679be0`；
  Python 任务镜像 `sha256:94f67c2689d2201547d34bfe30872ae7526e1c93fb38726ca06a579fb8758eae`；
  最终服务镜像 `sha256:0f9b2e77f21275fac13ddb85f32cfccb0ac1ca8eb83c0393a2b5a7d5cb2ce633`；
  策略 `4ab2cf3641dc68c510f85dd77404906b1a2a0852084499c4a7e616e569256711`。
  execd／egress 沿用 #21 的固定组件，构建均 `--pull=false --network=none`。
  部署契约见 [本机环境](../opensandbox-local.md)。

最终 native v6 来自基点 `bac537bcf9d6ad6e5cc31fc41d2e29edb3d7b959` 的本次工作区，
执行前冻结 470 个源码输入及实际二进制／manifest。账号来自 #21 的正常注册／登录；
本轮驱动使用 Go JWT issuer 为该账号恢复 USER HttpOnly cookie 并保留，未将这次驱动
恢复计作正常登录验证。

最终产品会话 `745c3fc2987b09c5bfa0fed9bea791d9`：从界面新建会话、准备 32 文件、
由 Go 下载依赖，再点击“运行 Python 基线”。先在沙箱 scratch 用
`pip --no-index --no-deps` 安装唯一只读 wheel，随后运行
`python -m unittest discover -v`：**81/81 通过，0 失败、0 跳过，退出码 0，输出未截断**。
未修改上游测试或降低选择。上游 CI 的 pytest／coverage／mypy 工具检查未运行。

测试后原源码及副本各 32 个文件、原 HEAD 和 index 摘要均相符，原 Git status 干净。
依赖字节／摘要保持相符。重复请求仍为同一 operation／event 7；真实进程重启后，
依赖 provenance 和测试结果从同一 Ledger 恢复，不重跑测试或重新下载。
1280／1600 浏览器截图已检查；输出可聚焦、完整分母可滚动查看，无横向页面溢出。

实际结束 Trace 从 Phoenix 读回：测试根及 Runner 子 span 的 trace ID 为
`61b889df22683f483228201081c7d5fe`，两者均有 end_time、status UNSET，父链相符。
子 span 绑定实际执行标识与退出码；依赖准备的真实结束 Trace 单独保存。
精确 task／sidecar 物理资源已终止；原批次 `02cfc8d9c93975b3ea63f27cc0a3331d`
仍为 100,000,000 上限、used/reserved 0，任务槽释放。**本轮模型调用为 0**。

## 拒绝、隔离与回归

| 验证 | 完整结果 |
| --- | --- |
| Go 公共准备接缝 | 外 owner、陈旧 cursor 拒绝；wrong hash／redirect／wrong length／truncated／interrupted transfer／unknown admission＋source change 六种结果保留，重开不下载，新 request 不替换原事实 |
| Go 公共 Runner／CLI | Python 无依赖 ID 在创建前拒绝，Go profile 拒绝 Python artifact；Docker／WSL 不静默忽略依赖；profile CLI 保留语言并使策略摘要变化，旧 Go 默认保留，不覆盖已有输出 |
| 真实产品 HTTP | 无身份 401、额外 URL selector 400、新下载 request 409；原结果与 Ledger 保留 |
| 真实服务拒绝 | missing／writable／extra dependency、foreign cache、other package、extra environment、network allow、unapproved image，8/8 返回 409，创建前拒绝 |
| 真实 Python Runner 隔离诊断 | UID/GID 65534，CapEff 0、NoNewPrivs 1；root／workspace／dependency 写入均拒绝；凭据名称 0；实际两个只读 bind、scratch、资源与 sidecar 限制已核对 |
| 包层断网 | 同一实际双栈 peer：8/8 positive 可达，Python 任务 8/8 拒绝 IPv4／IPv6 TCP、UDP、DNS UDP／TCP；退出码均 0 |
| `go test ./... -count=1 -json` | 退出码 0；1420 pass／60 skip／0 fail test actions（含 parent 和 subtest）；44 包 pass、14 无测试包；原 JSONL 和全部 skip 保留 |
| `go vet ./...` | 退出码 0 |
| `npm test` | 退出码 0，12/12 pass，0 skip／fail |
| `npm run build`、native v6 build、`git diff --check` | 各退出码 0 |

实际 Runner 诊断不替代产品测试；单元／外部 HTTP、CLI fixture 不升级为真实运行证明。
所有 RED、编译失败和后续 GREEN 原文保留。两轴源码审查分别为 `AI_REVIEWED`，
Standards remaining 0、Spec remaining 0；不宣称人工复核。

## 保留的失败与边界

- 初始 filtered schedule 克隆含 promisor metadata，被 Go 在准备 intent 前拒绝。
  完整克隆另行冻结；原克隆、失败会话和 HTTP 409 保留。
- v2 准备通过，但租约到期，未执行测试；未改租约或覆盖原副本。
- v3 首个实际测试请求：profile 生成器漏写 Python 语言，固定服务在创建前返回 409。
  原结果保持 unknown，原任务槽曾保留；未把它改写成 passed。完整生命周期列表、
  operation 标签物理库存、固定服务代码均证实零 task／sidecar 后，显式 operator
  通过 Go Budget.FinishTask 只释放该槽；Ledger 增加结束事实，批次、用量和未知测试
  原文保留。此人工对账不计作产品自动恢复。
- 修正语言后的首次服务启动使用了不符合现有核验表示的 no-new-privileges 参数，
  Ready 拒绝，尚未执行；独立重部署使用 `no-new-privileges=true`，未放宽验证。
- v4 全部 81 项通过；随后补入 Python 缺依赖 ID 的创建前拒绝，旧 v4 源码绑定保留，
  最终 v6 重新跑全部 81。实际 baseline 请求分母为 3：1 unknown、2 passed。
  原 81 项失败运行的未执行项不会计作通过。
- 进程重启时首个浏览器导航早于监听端口，ERR_CONNECTION_REFUSED 保留；
  就绪后重开同一结果，没有重跑副作用。

收据在忽略目录：
`.scratch/opensandbox-integration/product-python-8a61461b-7dc9-4abc-8619-bd3afd22e98f/receipt.json`。
它绑定最终源码、完整运行输入、产物及 SHA-256、命令／退出码、真实 Trace、全部尝试和分母。
本机凭据、数据库、wheel、镜像构建树和原运行产物不进入提交；提交后另存绑定核验。
没有运行模型任务、host orchestrator pytest 或发布门禁；完整发布和 #23 保持各自缺口。
