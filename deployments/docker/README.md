# 本机 Docker 管理目录

本项目新增 Docker 配置、受控启动入口、构建目录和本机运行物料集中在这里。
Docker Desktop 的容器统一属于 Compose project `localcode`，用 service 区分
控制服务、任务沙箱、egress、Trace 和临时测试；不为每次测试另开项目或散落顶层容器。

`compose.ps1` 是固定项目的入口，复用现存 Compose 定义和 volume namespace：
参数先写 Compose 子命令，再写该子命令的选项；不接受修改 project／配置入口的参数。

```powershell
./deployments/docker/compose.ps1 ps -a
./deployments/docker/compose.ps1 config --no-interpolate --no-env-resolution
./deployments/docker/compose.ps1 up -d mysql redis
```

目录约定：可提交的 Compose／Dockerfile／启动脚本放在此目录；本机生成的
配置和构建输入放 `.local/profiles/` 与 `.local/builds/`，运行日志和收据放
`.local/logs/` 与 `.local/receipts/`，新增 bind 数据放 `.local/data/`。
`.local/` 整体忽略，凭据只从进程环境或已批准 secret manager 注入。
Session Ledger 继续使用唯一的规范存储，不因 Docker 整理复制或重建账本。

新增临时容器必须有明确的 owner、purpose、run/operation identity 和已知停止后的
回收方式。优先 Compose；必要的受控 Docker API／CLI 创建也必须在创建时加入
固定 project/service 分组标签，构建与运行物料仍使用本目录。缺少分组和生命周期
声明时先修正启动入口，再创建容器。

已部署 OpenSandbox 的策略是镜像内不可变内容，客户端追加任意 metadata 会被
旧策略拒绝。后续控制镜像升级须由可信 server 给 task／egress 设置固定分组标签，
完成原有 pin 与隔离验证后再启用；不能通过放宽请求 metadata 来绕过策略。
在此之前，旧服务只保留其已有资源，不通过它新增未分组沙箱。

历史根 Compose 和 `deployments/opensandbox/` 等构建配方作为兼容输入保留。
本次规则只约束后续资源；现有容器、volume、批准 manifest、镜像 pin、数据和
验收证据不自动迁移、重命名或删除。以后修改实际 Docker 启动流程时，应先读取本文件。

Docker 容器、镜像层和 named volume 实际保存在 Docker 数据盘中，不能像普通
Windows 文件夹那样逐个搬动。这里统一的是项目分组与管理物料；数据盘还包含
其他项目。磁盘清理按已核对归属的明确清单执行，保留业务数据、运行 pin 和证据。
