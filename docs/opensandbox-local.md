# 本机 OpenSandbox 基线环境

本契约用于 Windows Docker Desktop 上的 Go 基线环境。浏览器经本机 Go 产品
鉴权、准备 Task Workspace、记录测试 intent、取得持久任务槽，再调用
OpenSandbox Runner。Session Ledger 保存结果；OpenSandbox 的 SQLite 只保存
容器资源投影。测试入口固定为 `go test ./... -count=1`。

## 构建输入

上游固定为 [OpenSandbox f89eff24](https://github.com/opensandbox-group/OpenSandbox/tree/f89eff24bbd15ae1c37a47769f7667849e679be0)，
Apache-2.0。在独立副本应用 `deployments/opensandbox/patches/` 中的两个补丁；
从仓库根目录指定 Git patch 的目标前缀，避免在嵌套 checkout 内静默跳过补丁。
将 `server/opensandbox_server/codeops_profile.py` 加入上游 server 包。

分别在上游 `components/execd` 和 `components/egress` 构建 Linux amd64 静态组件，
使用已核验的 Go 编译器、锁定模块和本地缓存：`CGO_ENABLED=0`、
`GOTOOLCHAIN=local`、`GOPROXY=off`、`GOSUMDB=off`；构建参数为
`-mod=readonly -trimpath -buildvcs=false -ldflags "-buildid= -B none -X github.com/alibaba/opensandbox/internal/version.GitCommit=f89eff24bbd15ae1c37a47769f7667849e679be0"`。
保存编译器摘要、组件摘要、`go version -m` 和原始退出码。

按 Dockerfile 的 COPY 输入准备各自构建目录：

- execd／task-go：静态 execd、上游 LICENSE 和全部组件依赖许可证。
  task-go 还包含本仓库 `bootstrap.sh` 和零字节 `empty.env`。execd 和 bootstrap
  的真实摘要必须与 task-go Dockerfile 标签一致。
- egress：静态 egress、经签名仓库与固定包摘要核验的 nftables／iptables／iproute2
  Debian 依赖包，以及组件和依赖许可证。
- server：打补丁后的 server 包、上游 LICENSE、上游 `uv.lock` 的冻结 production
  requirements，以及逐项 hash 核验过的 Linux Python wheel。此处是控制平面依赖；
  任务依赖按各票的独立来源与摘要准备。

基础镜像 pin 在各 Dockerfile 中。确认它们已存在于本机，再使用
`docker build --pull=false --network=none`。保存镜像 `Id`、构建输入与全部失败尝试。
任务运行只接受本机存在且与批准身份相符的组件，服务不自动拉取镜像。

## 冻结本机策略

在新建 server 构建目录之外准备非秘密 JSON 输入，字段为 `source_sha`、
`task_images`（当前 Go 镜像一个）、`execd_image`、`egress_image`、
`execd_sha256`、`bootstrap_sha256`、`workspace_root` 和 `network`。
镜像值使用完整 `sha256:<64 hex>`。`workspace_root` 是 Docker Desktop 的
`/run/desktop/mnt/host/<drive>/...` 路径，对应本机批准的隔离存储根；
网络是本轮新建、受控的双栈 Docker bridge。

从项目根运行 `go run ./cmd/opensandbox-profile <server-build-directory> <input.json>`。
它拒绝未知字段和已有输出，生成 `codeops-runtime.toml`、`codeops-profile.json` 和
`codeops-policy-inputs.json`，并打印策略 SHA-256。摘要绑定配置、非秘密 pin 和
全部实际 server 文件。随后离线构建 server 镜像，固定它的真实 `Id`。

控制平面在所选网络中运行，端口只映射 `127.0.0.1:<private-port>:8080`。
采用只读 root、drop ALL、no-new-privileges、512 MiB、2 CPU、128 pids，
`/run/codeops-control` 与 `/tmp` 各为至多 64 MiB 的 noexec/nosuid/nodev tmpfs。
只绑定 Docker socket，以及同路径只读的批准存储根。使用镜像默认命令，工作目录
固定为 `/app`；环境必须与固定镜像环境加下述批准配置一致，拒绝额外覆写和重复字段。
四个 `codeops.opensandbox.*` 标签绑定 source SHA、policy SHA、execd image 和
egress image。API key 与 execd token 只通过进程环境传入：
`OPENSANDBOX_SERVER_API_KEY`、`CODEOPS_EXECD_ACCESS_TOKEN`；execd token 是
64 位小写十六进制。保存容器物理 ID，记录每项实际限制并核验。

本机运行 manifest 包含 `source_sha`、`endpoint`（显式 loopback `/v1`）、
`server_container_id`、`server_image`、`execd_image`、`egress_image` 和
`policy_sha256`。为该文件计算 SHA-256，交由 Go 配置批准。生成文件、本机路径、
运行收据和凭据保存在忽略目录／进程环境中。

## 产品入口与边界

沿用 `configs/local-core.yaml` 的仓库、隔离存储、身份与 Ledger 配置，设置：

| 环境变量 | 内容 |
| --- | --- |
| `CODE_AGENT_TASK_RUNTIME` | `opensandbox` |
| `CODE_AGENT_TASK_IMAGE` | 批准的 Go 任务镜像 pin |
| `CODE_AGENT_OPENSANDBOX_MANIFEST` | 本机 manifest 文件 |
| `CODE_AGENT_OPENSANDBOX_MANIFEST_SHA256` | 该文件 SHA-256 |
| `CODE_AGENT_OPENSANDBOX_API_KEY` | 控制平面 API key |
| `CODE_AGENT_OPENSANDBOX_EXECD_TOKEN` | 控制平面使用的 execd token |

登录后在右侧卡片准备工作区并运行 Go 基线。结果显示实际命令、退出码、输出及
截断标记。重复请求和重开读取同一 Ledger 结果；保留未知 intent，需核验后处理。
测试不调用模型，仍使用既有持久任务槽，释放任务槽不会重置 token 批次。

任务进程为 UID/GID 65534、无 capability，root 与工作区只读；编译器使用有界
scratch。egress sidecar 采用 dns+nft 与 IPv4／IPv6 全拒绝策略。Go 核对实际镜像、
只读 mount、资源限制、控制平面启动输入和 sidecar 所有权。删除请求携带已记录的
物理 task／sidecar／operation ID，重绑或无法确认的资源保留供核验。

冻结 chi `9b9fb55def404397748a9fc7e044efe9db1d618e` 的正常 HTTP/2 测试需要两份
已公开的 TLS 测试夹具。Go 启动策略只准入这个提交下 `testdata/cert.pem` 的
`f089d14120e9f344fa13a9e45f46098bd8c19c1b3d883526edd8fc5270810bb8` 与
`testdata/key.pem` 的 `1f2d4e39e1a799bd668e0fa10fde499505048a4ea6ba0828b0343edc848b2a16`。
文件标为 `public_test_fixture`，字节变化或主机准入缺失会拒绝准备／核验。
实际文件内容只进入受控副本，Ledger 保存摘要。

## 恢复与验收

停用此入口时移除本轮启用配置，保留 Ledger、Task Workspace 和所有失败证据。
需回收时按已记录的本轮资源身份处理；已有容器、volume、索引和业务数据保留。
部署改动重新核验受影响 pin、限制和包层断网；镜像／标签不足以证明隔离。

每轮验收冻结源码、配置、镜像和任务输入，检查真实浏览器结果、原仓库／index／
准备副本、精确资源终止，以及真实后端的结束根／子 Trace。完整分母包含失败、
部分结果与跳过；本基线证据按范围记录，完整发布遵循 `release-gate.md`。
