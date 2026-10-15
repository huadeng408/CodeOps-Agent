# Windows 便携包

适用环境：Windows 10/11 x64，已安装系统 UCRT，并有默认浏览器。
完整解压后双击 `CodeOps-Agent.exe`；启动窗口负责运行状态和退出，浏览器提供现有产品界面。
关闭启动窗口会停止本次拥有的 Go/Python 子进程；身份、Session Ledger 和业务数据保留。

包内包含固定版本的 Go Harness、Python embedded、Python wheels、MinGit 和前端资源。
运行时使用随包组件，启动时不下载、不编译，也不搜索开发机 Python/Go/Node/Git。
`bundle-manifest.json` 列出组件版本、许可、来源及逐文件 SHA-256；文件损坏时拒绝启动。
Python 的路径配置隔离系统安装和用户 site-packages，应用依赖随包分发。

凭据从启动进程环境提供，包内不含 `.env`、密钥或用户数据库。
模型、端点、请求上界及已批准沙箱的配置沿用
[模型契约](https://github.com/huadeng408/CodeOps-Agent/blob/main/docs/provider-configuration.md)和
[OpenSandbox 契约](https://github.com/huadeng408/CodeOps-Agent/blob/main/docs/opensandbox-local.md)。
Docker/WSL、模型凭据及仓库授权是相应执行能力的外部前提；缺失时能力面板明确显示受阻原因，
本机登录和历史仍可打开。启动器不启动或删除外部 Docker 服务、volume 或业务数据。
OpenSandbox 配置使用标准 `%ProgramFiles%\Docker\Docker\resources\bin\docker.exe`；
没有该外部 Docker CLI 或批准的 runtime pin 时，执行能力保持受阻。

首次建立本机身份，需在启动进程环境提供 `CODE_AGENT_LOCAL_SETUP_USER` 和至少 12 字符的
`CODE_AGENT_LOCAL_SETUP_PASSWORD`，然后在既有注册页登记该身份。已有身份通过登录页登录。
默认数据保存在 `%APPDATA%\CodeOps-Agent`；显式 `CODE_AGENT_PORTABLE_DATA_HOME` 可改数据目录，
该目录必须在安装目录之外。`CODE_AGENT_SESSION_LEDGER_PATH` 等已有显式配置继续生效。
启动窗口显示本轮 URL；输入 `exit` 或关闭该窗口结束产品。仅关闭浏览器标签不会结束服务。
未提供 `JWT_SECRET` 时使用每次启动的随机密钥，重启后需重新登录，持久身份和历史保留。

## 构建

在源码根目录使用已有构建工具准备前端，然后组装到一个尚不存在的目录：

```powershell
npm --prefix frontend test
npm --prefix frontend run build
python scripts/build-windows.py --output deployments/windows/.local/CodeOps-Agent
```

构建阶段从 `components.json` 的固定来源获取 archive，并按 SHA-256 校验；
Python wheels 使用 `requirements.lock` 的精确版本和 hash。
构建缓存位于本目录 `.local/downloads/`，不会进入提交；交付目录与开发 checkout 分开。
重建使用新目录，失败的构建保留用于诊断，不覆盖已有产物或用户数据。
构建前冻结源码指纹，结束时核对；期间源码变化会拒绝交付。Python 应用只收录 Git 已纳管的
运行源码，新文件须先进入受审阅的 Git 清单；忽略的日志、数据库和本机配置不进入包。
构建命令会从当前源码重新生成前端资源。

## 许可与来源

- CodeOps-Agent：包内 `licenses/CodeOps-Agent-LICENSE`。
- CPython：包内 `python/LICENSE.txt`，版本、上游 archive 和对应源码链接见 manifest。
- MinGit：独立进程组件；包内 `git/LICENSE.txt` 和 `git/ucrt64/share/licenses/` 保留
  Git 及附带组件的许可。固定上游 release 与对应源码链接见 manifest。
- Python 包：完整 `.dist-info/METADATA` 和上游随 wheel 提供的 license 文件保留于
  `python/packages/`；版本、wheel hash 与声明许可见 manifest，锁文件另存于 `licenses/`。
- Go：工具链许可与专利声明在 `licenses/Go-*`；实际链接模块的版本、校验和和许可文件
  分别记录于 manifest 与 `licenses/go/`。
- 前端：实际生产依赖的版本、npm integrity 和许可在 manifest、`licenses/frontend/`；
  随包保留构建所用的 package-lock。

实际便携验收证据按任务记录，不以构建成功替代登录、真实模型问答、迁移或退出验收。
