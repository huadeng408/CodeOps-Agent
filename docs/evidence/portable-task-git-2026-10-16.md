# 隔离工作区的可移植 Git 元数据

工程状态：`IMPLEMENTED`，源码／集成与四项 Go profile Git 只读产品范围 `VERIFIED`。原副本保护和旧 lease 兼容验证通过。十轮代码任务、完整 Trace／Memory 和发布分别验收，不因本修复关闭 #17／#18／#19。

## 问题与实现

真实浏览器任务中的合法 Git status／diff 退出 128：原 `.git` 文件指向 Windows 原仓库的管理目录，该目录不在 Linux 沙箱唯一的 `/workspace` 挂载内。

修复位于 `internal/worktree/`。Go 复用 rooted Git reader，为新任务准备自包含的 `.git` 浅快照：保留原 HEAD、对应 commit／tree 和准入文件 blob，不复制宿主 config、其他 refs 或历史对象。排除文件不导出 blob，其 HEAD index 项保留并标记 skip-worktree。原仓库登记继续保留，原文件和原 index 不改写。

Go 在既有 Task Workspace Ledger payload 中绑定 `git_metadata_sha256`；验证覆盖快照控制文件、index、对象、目录与实际字节，拒绝篡改、额外路径和不安全链接。旧 payload 没有该字段时，沿用原 `.git` 文件验证分支；旧工作区、收据和数据不迁移、不覆盖。

凭据形态的已准入 HEAD 内容仍拒绝。固定 HEAD／路径下已批准的公开 fixture 可以使用精确原字节，或不含 CR 的 Git LF blob 精确转 CRLF 后匹配原审批 SHA；工作副本 pin 与复制字节不变。新快照固定生成 `core.autocrlf=true`，避免 Windows checkout 在 Linux Git 中被误报为全仓修改。提案和应用继续按 Go 的原始字节／checksum 核验。

Git 和 Bash 仍通过既有 Sandbox Runner。未增加宿主执行回退、原仓库挂载、依赖、协议生成物、前端布局、预算规则或 Trace 门禁。

## 回归与完整失败分母

沿用已确认的公开 `Manager.PlanTask`／`PrepareTask`／`VerifyTask` 接缝。宿主原生 Git 只检查测试自建的可信元数据 fixture，不执行真实任务代码。

- 首个 RED：准备后使原 fixture 仓库不可访问，Git status 退出 128；最小修复后通过。另验 diff、HEAD 内容读取、log、HEAD index、排除 blob 不可读和原文件／index 不变。
- 公开 fixture 换行 RED：1.77 秒，PrepareTask 拒绝原 LF blob／批准 CRLF 工作副本。加入上述精确兼容后通过。
- checkout 噪声 RED：LF HEAD 子例及父例失败，CRLF HEAD 子例通过。改固定安全配置后两例通过，真实内容改动仍显示，Git 读取后 VerifyTask 通过。
- 篡改 HEAD、index、config、shallow、对象及增加未记录 metadata 均拒绝；已准入 HEAD blob 含未批准凭据时拒绝，存储与原 index 保持不变。旧 linked lease 的恢复、控制文件及 index 拒绝测试保留。
- 初版全 Go 为 1,782 PASS／81 SKIP；fixture 兼容版为 1,783 PASS／81 SKIP，均退出 0，不能代替最终源码验证。
- checkout 修正版首次全量为 1,784 PASS／81 SKIP／2 FAIL（父子计数，同一 `internal/sandbox` fixture 就绪失败），退出 1。未修改该包；原错误没有记录内部原因，不宣称已证明唯一根因。该子例独立重跑通过，随后重新执行完整分母。
- 同源码第二次默认并行全量仍为 1,784 PASS／81 SKIP／2 FAIL，这次是既有 launcher fixture 在 30 秒进程启动期限内没有输出；不是同一断言失败。两份失败原文均保留，未更改生产或测试超时；最后限制包并行度为 2 重跑完整测试。

最终 `go test ./... -count=1 -p 2 -json`：1,786 PASS、81 SKIP、0 FAIL；47 个 package PASS、13 个无测试文件，退出 0。计数包含父子测试，跳过原文保留。最终 `go vet ./...`、staged diff 检查与不输出匹配值的秘密扫描均退出 0。官方 v4 便携包从固定缓存构建成功，重新执行 frontend build，3,219 个运行文件与源码指纹均核对。

验证摘要 SHA-256：`1b881b0a1143b826f3da392054526c124a899b5dc1e462b39b54e9c7750c38af`。其中包含六份源码指纹、所有验证调用的退出码、五次完整 Go 分母、两次默认并行启动失败、独立复审与产品封存绑定。最终全量限制包并行度，不改变测试断言；并行启动期限失败的唯一原因仍未证明。

## 真实产品分母

新运行物料均位于 `deployments/docker/.local/`，资源固定 project `localcode`；只操作本轮拥有的浏览器、便携启动器及原为 exited 的控制／Trace 服务。现用业务容器、volume、镜像和数据保留。

| 尝试 | 结果 |
| --- | --- |
| 首次新构建准备 | 准备失败，公开 fixture 的 LF／CRLF 兼容缺口；无模型调用，失败 Session 与观察器接口匹配错误均保留 |
| fixture 修正版 | 实际浏览器批准四项 Git，均经 OpenSandbox 执行、退出 0、stopped=true；模型本轮完成，但 98 文件换行噪声使正确 diff 范围未通过。3 项调用均结算，确认 34,756 tokens；18 个真实后端已结束 span 为诊断读取，不升级为完整 Trace 验收 |
| 最终换行修正版 | 四项实际 Git 均退出 0、stopped=true；status 仅显示原有 `.gitignore` 与 `local_workspace_note.go`，diff 为 1 文件／2 行增加，HEAD go.mod 与原提交正确；模型本轮完成，Go 再核验工作区仍 prepared。3 项调用均结算，确认 30,834 tokens；实际 UI 结束任务后 ActiveCalls=0、任务槽空，所属资源已停止 |

三次产品尝试共新增确认 65,590 tokens，新增 unknown 为 0，费用仍未知。264 个原副本／冻结输入文件与 4 份 index 均保持相同；两份已准备任务各 99 文件及 index 不变，用户 AGENTS.md 不变。Docker 原有 33 个容器、96 个镜像和 332 个 volume 的身份集合保留；未重新哈希业务 volume 的文件内容。

最终只读运行从真实后端读回 18 个已结束 span，0 ERROR。既有代码修改 Trace CLI 校验 612 个源码 pin 与实际 binary 后，因 `real code modification required` 拒绝本只读输入，退出 3；原拒绝与诊断读回同时保留，没有把它冒充成功代码修改 Trace 或本修复的 BLOCKED。产品 102 项封存清单 SHA-256：`12b0bf36356a022b92f40ca7f8b6a3d56b5c2fdc6f88c88c4051a639fb4f1ee4`。

原 c704f0f7 构建的公开 Go 重试另有完整记录：3 个 canonical run／4 次实际执行，2 failed／1 canceled，0 个完整有意义轮次；45 settled＋2 unknown 共 47 项调用，确认 1,426,776 tokens。失败轨迹产生的真实反思首次写入三条带来源的 Experience，第二次采用空集合；不能替代成功十轮任务 Memory。候选最终有编译错误与 q=0 语义错误，保持未应用。

该原重试严格 Trace 仍为 FAIL／BLOCKED；诊断保留 4 个实际根、254 个已结束 span 及 4 项缺失引用，未放宽门禁。157 项封存清单 SHA-256：`8cc13f726f30a31bc04e280e1c89cf5ec39db140d22bc122e9abdf8ab561d74a`。原两公开任务的十轮功能证据和失败均不改写。

## Standards

独立最终 0 项发现，包括两次实际兼容修正后的增量复审。

## Spec

独立最终 0 项发现。两轴均为 `AI_REVIEWED`，不构成人工逐条复核。

## 收据与边界

基点 `c704f0f73b4fa298a0698d70fe7f3294559de009`。源码回归、三个 RED、失败构建、三次产品尝试、冻结输入、原生 Ledger 导出、审查及最终源码绑定均保留。

本机源码收据位于 `.scratch/git-portable-metadata-20261016/`；产品收据位于 `deployments/docker/.local/receipts/git-portable-20261016/` 及 `-v2/`、`-v3/`。旧任务重试在 `browser-task-retry-20261016/`。这些派生收据不代替 Go Session Ledger。

旧工作区不自动迁移，原有 Git 挂载限制可能仍在；新任务使用新格式。浅快照只提供当前 HEAD 的准入内容，未开放完整历史。Python Runner 的 Git 支持保持原约束，本轮 runtime 仅验证 Go profile。Python／frontend 测试及完整发布未运行；官方便携构建重新执行前端 build。服务商不可变模型版本和费用仍未知，历史 unknown 与预留保留。用户既有 AGENTS.md 修改不纳入本次提交。
