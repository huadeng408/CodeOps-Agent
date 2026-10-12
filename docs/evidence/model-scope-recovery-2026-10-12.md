# 模型占用的确认进程恢复 — 2026-10-12

关联 [#3](https://github.com/huadeng408/CodeOps-Agent/issues/3)，基点
`05c2b73a62bcb06b5e0e27b033bbb50ec49376b8`。工程状态 `IMPLEMENTED`。
本记录验收同一 Ledger 中模型 scope 的进程身份、确认退出后的显式恢复，
以及现有生产 HTTP／CLI 共用结束边界。完整模型产品与发布未运行，#3 保持开放。

## 行为与证明边界

Go 在原 `scope_bound` 事实中增加 platform、PID、规范化的 OS 创建身份和
本机身份摘要；不写 capability 明文，不新增事实数据库，不因绑定创建
token 批次。Windows 使用进程 handle、创建 FILETIME 和零等待停止状态；
Linux 使用 boot ID、进程 start ticks 和进程状态，并核对 `NSpid` 所反映
的 proc PID 视图及 PID／user namespace。OS 契约参考
[Windows 进程时间](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getprocesstimes)、
[Windows 等待状态](https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-waitforsingleobject)、
[Linux proc stat](https://www.man7.org/linux/man-pages/man5/proc_pid_stat.5.html) 和
[Linux proc status](https://www.man7.org/linux/man-pages/man5/proc_pid_status.5.html)。

现有显式结束先检查 owner、根任务、run／child／工具停止状态和本地 scopes，
再要求所有待恢复 scopes 的原 holder 已由 OS 确认停止。仅在证据齐备时
追加 `scopes_recovered`，记录原 scope 与进程身份，通过同一 Ledger CAS
解除占用；冲突后重新读取并重新证明。原绑定和用量事实保留。随后仍由
既有结束 CAS 拒绝新出现的活动 scope。恢复操作本身不结束任何进程。

仍活跃、缺旧进程元数据、不同主机、畸形创建身份、未知 OS 权限／可见性
均拒绝恢复。Linux proc PID 视图不明确时拒绝绑定，不创建额度；其他
平台暂不支持该身份证明，也拒绝新绑定。释放写入失败而 holder 仍活跃
的情况继续保留占用。进程退出不代表 provider 用量对账：未结算或 unknown
用量仍保留完整预留，费用 unknown 不变。

## 新鲜验证

| 范围／命令 | 完整分母／退出码 |
| --- | --- |
| `go test ./... -count=1 -json` | 1,465 pass、71 skip、0 fail；45 测试包通过、13 无测试包；0 |
| `go vet ./...` | 0 |
| `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 go test ./tests/e2e -run '^TestProductionLocalCore' -count=1 -json -timeout 300s` | 14 pass、2 需批准现场环境的 opt-in skip、0 fail；0 |
| `python -m pytest -q` | 2,473 pass、17 skip、31 warnings、0 fail；532.71 秒；0 |
| 固定 Go 镜像内运行 `admission-linux-v4.test` 的进程场景 | 3 个父测试、7 个子测试，10 pass actions；0 |
| 同一二进制的受控 `unshare --pid` 场景 | 1 个父测试实际执行并通过；0 |

Go 数量包括父／子 test actions，不是独立产品场景分母。Windows 与
Linux 检查均启动真实、测试自有的独立 Go helper：活跃 holder 拒绝结束，
终止该 helper 后重开 Ledger 可恢复。只有故障 fixture 终止自己的进程。
legacy、foreign、无效 PID／创建身份／platform 场景使用明确标注的历史
平台事实 fixture；不替换项目模块，也不声称真实发生 PID 重用。

生产 HTTP 测试通过真实 server、认证用户、SQLite 和独立 holder 覆盖两个
场景。已结算场景活跃时 409，helper 退出并重启 HTTP 后 200、重复 200，
同批次 11 tokens 保留；缺 usage 场景前后均 409，103 reserved 全部保留。
模型响应来自外部 HTTP fixture，金额与 token 数据不作为真实 API 证据。

Linux 诊断镜像固定为
`sha256:c35ffc50ff2d4e97ea90f973b29836945912397204592a0b06a0b6fbbba59b58`。
镜像不含 machine-id，检查显式挂载 synthetic fixture，未声称真实产品
机器身份。普通进程检查使用离线、只读、cap-drop ALL 的容器；namespace
故障 fixture 单独增加 `SYS_ADMIN` 和 `seccomp=unconfined`，用来实际产生
多层 NSpid。该诊断权限没有进入原 OpenSandbox 生产 profile，已有容器、
volume 和业务数据均保留。前端与 Python 源码没有改动，未另跑浏览器 UI。

原 1 亿 token 批次只读读回：身份仍为
`02cfc8d9c93975b3ea63f27cc0a3331d`，上限 100,000,000，已用和预留均 0，
任务槽空、usage 已知、费用 unknown。本轮真实模型调用 0，未读取密钥
文件，未创建替代批次或重置累计额度。

## Standards

`AI_REVIEWED`：0 项未解决发现。规范化创建身份、PID 上界、NSpid 视图
校验及 PID／user namespace 绑定已解决此前 P1；同 Ledger、CAS、未知
用量保留及生产 HTTP 测试符合既有标准。未声明人工逐条复核。

## Spec

`AI_REVIEWED`：本切片无缺项、越界或错误实现；0 项未解决发现。仅在
OS 证明原 holder 停止后恢复，旧／foreign／权限不明及 unknown usage
继续拒绝。完整 #3 的真实模型、对账／重试、aux／child／恢复与 Trace
验收仍待执行。两轴各 0 项未解决发现。

## 原始失败与不可变收据

保留有效 killed-holder RED、畸形／非规范创建身份 RED，以及首次
gofmt 通配符诊断、Windows WAIT_TIMEOUT 类型编译诊断、Linux 原生命令
flag 诊断、namespace 无必需诊断权限和 user uid_map 拒绝。最终通过
不覆盖这些失败，Linux 旧二进制和原始日志全部保留。

不可变收据：`.scratch/model-scope-recovery/receipt.json`，绑定 967 个
源码／依赖摘要、本地指令摘要、命令、分母、退出码、OS fixture、完整
原始日志与原批次读回。SHA-256：
`4a127afb7fff4aff551c399e45bb8955bc10f970e0a49ffa2ec7fac02a6f8273`。
精确 staged paths、秘密扫描、当前提交和源码 CI 绑定另存补充，旧结束
入口收据不改写；用户 AGENTS 私有 footer 保持未暂存。

本切片的单测和集成按声明范围验收；完整模型产品和发布未运行。仍需
活跃 holder 的失败释放对账、未知 usage 对账、有界可计量重试、真实
模型／Python／反思／child／完整故障恢复、浏览器代码任务和后端 Trace
读回。工程进程恢复不代替完整产品或发布合格，不据此关闭 #3。
