# #20 CF-01：本机静态预览准备

工程 `IMPLEMENTED` 限于静态预览构建和配置；本机 UI／构建按下述范围 `VERIFIED`。
Cloudflare 实际账户、Free 计划、用量和公开部署尚未验证，对应范围 `BLOCKED`。
[#20](https://github.com/huadeng408/CodeOps-Agent/issues/20) 保持 OPEN；
本轮不修改已完成 #19 的范围、父规格或完整发布结论。

## 最小变更与外部行为

基线为 `7e177467920a021a3b4cdcb71937a345a89a6592`。
复用现有 LoginPage 和 Vite，不增加依赖、不重做布局、不生成示例模型回复。
只有显式 `static-preview` 构建跳过鉴权探测，展示静态能力说明及项目使用链接；
登录／注册输入、提交按钮和程序化表单提交均保持无副作用。
普通构建继续请求当前身份并提交原登录协议；服务端鉴权不变。

新增 assets-only 配置只指向 `frontend/dist-preview/`，采用 SPA fallback；
没有 Worker main、云数据服务、模型网关、Tunnel 或付费绑定。
普通 `frontend/dist/` 和预览产物分开，CI 对两种构建均执行 typecheck/build。
配置依据为官方[静态资源部署](https://developers.cloudflare.com/workers/static-assets/)
和[单页路由契约](https://developers.cloudflare.com/workers/static-assets/routing/single-page-application/)；
定价文档不能代替实际账户计划／费用核验。

## 验证与完整分母

| 范围 | 当轮结果 |
| --- | --- |
| 原构建的真实浏览器 RED | 11 项计划：1 PASS／1 FAIL／9 NOT RUN，退出 1；缺少静态标识且尝试一次 `/users/me`，网络 guard 拒绝该请求 |
| 修复后本地静态浏览器 v1／v2 | 两次各 11 PASS／0 FAIL／0 NOT RUN，退出 0，分别保留；首页、说明、登录／注册禁用、程序化提交、文档链接、深链、刷新、assets、无 API/socket、无 page error |
| 普通 production 构建浏览器 | 6 PASS／0 FAIL／0 NOT RUN，退出 0；显式 HTTP 鉴权拒绝 fixture 下，身份 GET、可用输入、原登录 POST 和错误展示保持；没有真实 Go 登录或模型调用 |
| `npm test` | 12 PASS／0 FAIL／0 SKIP，退出 0 |
| `npm run build` | typecheck 和 production build 退出 0 |
| `npm --prefix frontend run build:preview` | typecheck 和 static-preview build 退出 0；三个 public 文件与实际浏览器加载输入一致 |
| 配置／产物绑定 | `freeze_local.py` 退出 0；无 Worker／额外绑定，三文件与冻结输出匹配，secret／机器路径扫描 0 个匹配路径，不输出值 |
| 测试工具的错误收尾 fixture | 两项：截图拒绝和 newPage 失败。原脚本 self-check 退出 1，遗漏关闭／收据；修正后两项 PASS、self-check 退出 0，各自测试脚本仍退出 1，保留 11 项 FAIL／NOT RUN 分母并关闭所属 browser；没有真实浏览器／Go/provider |

深链与刷新结果来自本机 Vite 静态 HTTP，不冒充 Cloudflare 线上路由结果。
普通登录六项只证明前端与 fixture 协议，不能替代 Go 身份／owner 产品证据。
RED、NOT RUN 和两种构建的 source/asset 指纹分别保留，未覆盖为成功结果。
所属本机预览服务和测试浏览器均已停止；未创建或删除 Docker 资源及业务数据。

独立审阅发现的测试工具收尾问题已单独修正：setup 纳入保护，截图失败不会跳过关闭和收据，
关闭失败保留 unknown／非零退出。只改变测试工具，UI 与三个实际静态产物未变；
实际浏览器 v2 再次执行完整十一项，原 v1 和两项错误 fixture 不替代或覆盖它。

## 账户与部署边界

用户已在 Chrome 打开 Cloudflare 页面。浏览器 inventory 能识别该标签页，
两次内容绑定超时；命名并刷新 Chrome 会话后，内容读取返回 `Debugger unattached`。
未读取到产品计划、Free 配额或账单，账户身份不进入提交。
本机 PATH 未找到 Wrangler 命令，也未找到 Cloudflare 进程环境配置；这不等同于断言账户未登录。

当前只交付可审阅本机产物和配置。没有上传、公开 URL、OAuth／API key 创建、
订阅或新增云资源，也没有调用模型；不宣称真实 Cloudflare 费用为零或远程能力已验证。
实际部署仍需可读取的批准账户、Free 计划／计费状态核验和具体产物的发布授权；
CF-02／CF-03 与可选 CF-04／CF-05 分别验收。

本机只读证据入口 `.scratch/cloudflare-static-preview-20261019/`：
`red/`、`green/`、`green-v2/`、`normal/` 保存各自原生命令／退出、浏览器记录和加载的三文件；
`cleanup-red/`／`cleanup-green/` 保留错误收尾的边界 fixture。
`local-summary.json`／`local-summary-v2.json` 保存各自配置与源码／产物 SHA-256、扫描和声明范围。
使用说明见 [部署目录](../../deployments/cloudflare/README.md)，交付审阅／源码 CI 由正式票后续记录。
未改变 Go/Python 运行源码或协议，未为本地静态范围重复模型／无关本地全量验证。
完整发布 gate 本轮未运行，完整发布仍 `BLOCKED`。
