# OpenSandbox Go 产品基线

范围：[#21](https://github.com/huadeng408/CodeOps-Agent/issues/21)。实现为
`IMPLEMENTED`；Go 单测／集成、前端和本机真实 Go 产品验证分别为 `VERIFIED`。
Python 基线、模型任务、完整取消／CLI、便携包和生产发布不在本轮验收范围。

现有已鉴权产品使用已选 A 右侧卡片，按固定入口 `go test ./... -count=1`
运行准备好的 Task Workspace。Go 先核对 owner、CAS、租约及基线，持久化
intent，取得现有 admission 任务槽，再调用 OpenSandbox Runner。结果写回
Session Ledger；同请求读回同一 operation，重开不重放副作用。

## 实际部署与边界

上游固定为 OpenSandbox `f89eff24bbd15ae1c37a47769f7667849e679be0`，Apache-2.0。
受约束的本机适配、离线构建输入和恢复步骤见[部署契约](../opensandbox-local.md)
及 `deployments/opensandbox/`。它明确修改默认部署的环境继承、资源创建和删除
行为；默认上游配置不自动视为获批准的环境。固定源的 790 个文件已核验，
控制平面 49 个 wheel、Go 模块及系统包的来源、摘要和许可证保留在准备收据中。
公开补丁／配置生成自检重现全部 139 个部署文件与策略摘要，重复生成拒绝覆盖。

实际 pin：

| 输入 | 身份／SHA-256 |
| --- | --- |
| server image | `sha256:251dee367f30917b924fc84925bb6f015275733323b73ef1c026a2446c5150d7` |
| execd image | `sha256:bee747874c443f7dc7fa154e6e41384eb8633855521cd7ba1e991f7afe091a91` |
| egress image | `sha256:a4a332e74aab57bae9794e6eead7f8db5c1dd6793e63555f6e54da3ea88dbe5f` |
| Go task image | `sha256:c35ffc50ff2d4e97ea90f973b29836945912397204592a0b06a0b6fbbba59b58` |
| policy | `4693e59e5664e71fd80cebc35b39f0c06efd8e37b88636f4cb0b464e9c693d0c` |
| manifest | `63b635fcdb66f5ac4859a960054534d2020641c90dfd6ca6c38c75ef2fcc2f2f` |
| product v4 executable | `900a30e08b3e15b9cdb8737d47d7c390d590b0ba3ef7d4f796faa144c88a3a3b` |

Go 核对实际控制平面的只读 root、权限、scratch、两个批准挂载、固定命令、
`/app` 工作目录及固定镜像环境加三个批准配置；额外或重复环境字段拒绝。
任务不隐式拉取镜像，不回退到其他后端／宿主机。实际任务以 UID/GID 65534
运行，CapEff 为 0、no-new-privileges 为 1；root／workspace 不可写，隔离
scratch 可写，不能读取 PID 1 环境，API／execd／模型／代理凭据不继承。
实际 cgroup 为 1 GiB、2 CPU、128 pids。清理核对物理容器、sidecar 和 operation，
不依赖可被替换的逻辑名字，也不删除已有 volume 或产品数据。

双栈受控 peer 的 TCP、UDP 和 TCP／UDP DNS 端口正对照共 8 项可达；最终
Runner 下 8 项均受 packet policy 拒绝。此处 DNS 验证的是端口可达性。
另验字面 argv、真实退出 7、110 万字节分行输出受 1 MiB 上限约束及截断标记。
连续无换行输出产生过大事件时，解码器拒绝该帧并保留 `unknown`／截断状态，
退出 126；初始预期退出 0 的探针失败亦保留，不计为成功输出验证。
原始 nft 规则和容器审计保留；独立探针只证明对应边界，产品结果另验。

## 完整 chi 基线与故障

仓库固定为 chi `9b9fb55def404397748a9fc7e044efe9db1d618e`。
其 98 个冻结文件全部准备，基线摘要为
`2bb85eb0c031c9996db9ef0a3f3afd177aae1a0b48cce5305cfeef8c61682594`。
Go 的可信启动配置仅放行该提交中两份已知公开 TLS fixture 的精确路径／摘要；
其他 PEM、缺少批准、改变内容或改变提交仍拒绝。未修改、跳过或缩减上游测试。

最终浏览器会话 `7e94a9348ae39eeaa74663f2136e3fdf` 的两个包均通过：
chi 为 5.297 秒，middleware 为 26.543 秒；退出 0，输出未截断。
原 HEAD、status 和 index 字节摘要前后相同；准备副本逐项摘要保持原基线。
重复请求、刷新和重开后 operation
`67a6c7560c38649cbd901354075c1e91c1b2a5d0c3f1c0df747defb1cd77279f`
与 eventCount 5 保持不变，已确认结束的任务槽释放。正常准备／运行／刷新／
文件清单／历史切换控件已检查，1600×900 与 1280×800 截图可见卡片，无页面横向溢出。

真实 Phoenix 后端读回 trace `c5c91e0d2dc4d44017501078e56c1217`，
结束的 `workspace.test-baseline` 根 span 与 `opensandbox.run` 子 span
具有正确父子关系；根绑定 Session／operation／baseline，子绑定实际 runtime、
退出码及截断字段，两者通过同一 trace 和父子关系关联。
本轮模型调用及 token 已用／预留均为 0。

原失败完整保留：v1 排除公开 TLS fixture 后 middleware 失败，退出 1；v2
复制阶段仍拒绝 fixture，准备受阻且浏览器 driver 中断；v3 完整基线通过；
v4 在两项审阅修复后重新通过。它们保留各自源码指纹和输出，不改写为成功。
边界探针的凭据继承失败、过期配置和构建／启动失败亦保留。

两个独立故障实例分别使用原版本与最终 v4，在观察到实际 task 容器后中断
本机 Go 进程；重启相同 Ledger 后均为 `unknown`、eventCount 4、同 operation，
重复 POST 不重放。原任务和 sidecar 到期后已清理，Ledger intent 与任务槽仍保留，
等待后续对账。未用人工成功记录补齐未知结果，也未重置既有 100M token 批次。

验证使用独立、保留的本机集成身份／Ledger。首次注册与登录走正常表单；
浏览器 driver 恢复后，对同一原身份签发 USER 测试 token 并设置 cookie。
这是测试鉴权准备，未替换身份、升级权限或改变产品鉴权代码；秘密只在进程内存。

## 回归、审阅和收据

| 命令／范围 | 退出码 | 完整结果 |
| --- | --- | --- |
| `go test ./... -count=1 -json`（最终 v4） | 0 | 1405 通过、60 跳过、0 失败；43 包通过、15 包无测试 |
| `go vet ./...`（最终 v4） | 0 | 通过 |
| `npm test` | 0 | 12 通过、0 跳过／失败 |
| `npm run build` | 0 | TypeScript 与 Vite 构建通过 |
| Go profile 生成自检 | 0 | 139 文件及策略摘要相同；重复输出拒绝，退出 1 |
| `git diff --cached --check`／精确 staged scan | 0 | 无凭据、机器路径或运行产物进入提交 |

双轴 `AI_REVIEWED`：Spec 初始 P1 删除入场竞态、Standards 初始 P2 服务启动
输入核验均按公开接缝 RED 1／GREEN 0 修复；后续补审仅纠正 Trace 字段归因说明，
源码及运行产物未变。最终剩余 0／0。未宣称人工逐条复核。

不可变[产品收据](../../.scratch/opensandbox-integration/product-go-f912c8f8-ae95-4727-97fc-971e6ee15ef6/receipt.json)
冻结最终参与运行的源码、部署／依赖输入、完整分母、失败、截图、Trace 和产物摘要。
基点为 `36a433e1d339a051119536d54fd32973d36f2dc8` 加本轮源码；
交付后的[绑定核验](../../.scratch/opensandbox-integration/product-go-f912c8f8-ae95-4727-97fc-971e6ee15ef6/commit-binding.json)
按实际源码及产物核对，旧准备收据保留原 SHA 与结果。
用户既有 AGENTS 私有尾行保留且未提交。
