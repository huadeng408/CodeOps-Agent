# #26 CLI 的 context_window 传递

对应 [#26](https://github.com/huadeng408/CodeOps-Agent/issues/26)。配置加载器和
`/config` 原本保存／展示 `context_window`，但 CLI 构建受管 Orchestrator 参数时未传入，
Python 因而使用自身默认值。此修复限于 CLI 参数与实际受管子进程边界。

## 修改与边界

`internal/cli/app.go` 克隆调用方参数；未显式指定窗口时，把正的配置值传给既有
`--context-window` 参数。空的参数列表复用既有默认启动入口。
显式的分离值和 `--context-window=<值>` 保留优先级，无效显式值也不被配置值覆盖；
验证仍由原子进程解析器执行。未设置值不追加窗口参数。

未新增依赖、ProcessConfig 字段、协议、执行器或事实写入者。
现有 ProcessManager 仍负责子进程与凭据剥离；已经运行的外部 Orchestrator 不被重配置。
未改浏览器布局或 portable launcher；已有 v7 包不因源码改动自动成为本修复的打包证据。

## RED／GREEN 与完整分母

固定起点为 `c2d063dc16e0a4acc6cc023e88d97604bb087ef2`。
在既有 public CLI／真实子进程 gRPC 接缝运行外部传输 fixture；没有替换 CLI 或进程管理器。
子进程的 Health 返回它实际收到的窗口值，所有实例通过正常 CLI 退出回收。
此 fixture 不运行真实 Python/provider compaction，也不作为产品或发布证据。

- 首次 RED：`4096` 配置的子进程读回 `unset`，测试命令退出 1。
- 第一轮最小修复后同一测试退出 0。
- 显式参数 RED：配置值覆盖了原 `8192`／`16384`，两项保留为失败，命令退出 1。
- 最终六个子场景通过：配置值、默认 `256000`、未设置、显式分离值、显式等号值、
  无效显式值原样保留。每项同时检查调用方带剩余容量的参数存储未被修改。
- 最终定向测试退出 0，含六个子场景及其父测试共七个 PASS 事件；无失败或跳过。
  单独的子进程 helper 在普通全量中按 fixture 入口跳过，不能计作产品验证。

## 验证输入和命令

源码、相关依赖、两轮失败和成功输出保存在
`.scratch/cli-context-window-20261019/`。`validation-inputs.json` 记录八项文件指纹；
指纹在定向运行后、相关回归运行时采集，期间未改源码，不宣称是运行前冻结。
使用已固定的 Go 1.26.0 Windows 工具链，`GOPROXY=off`；未下载新依赖、调用模型或启动 Docker。

- `go test ./internal/cli -run '^TestCLIConfiguredContextWindowReachesManagedChild$' -count=1 -json`：最终 0。
- `go test ./internal/cli ./internal/orchestrator ./internal/config -count=1 -json`：0；
  三包通过，184 个 PASS／4 个 SKIP／0 FAIL 测试事件（包含父测试与子场景）。
  四项跳过为两个子进程 helper、需要外部环境的真实 RAG、Windows 上不可用的 symlink 场景；
  其原始输出和分母保留，没有把跳过计为已验证。
- `go vet ./internal/cli ./internal/orchestrator ./internal/config`：0。
- 精确 staged diff、secret scan、双路审查及交付源码 CI 单独留存。

本轮工程状态 `IMPLEMENTED`；`VERIFIED` 限于上述 CLI／受管子进程传参范围，无该范围 blocker。
真实 Python/provider compaction、200 assistant-turn 浏览器、窗口效果和 token 对照未运行；
完整发布仍按原契约验收，#1 原文／OPEN、#19/#24/#25 已验收范围及暂缓的 #20 保留。
