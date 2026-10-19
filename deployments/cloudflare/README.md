# Cloudflare 静态预览

对应 [#20 的 CF-01](https://github.com/huadeng408/CodeOps-Agent/issues/20)。
复用现有 React/Vite 界面，不包含 Go 服务、用户数据、模型回复或代码任务结果。
预览明确标识能力，登录和注册不可提交；本机产品仍使用普通构建及 Go 鉴权。

在仓库根目录构建：

```powershell
npm --prefix frontend run build:preview
```

`frontend/dist-preview/` 是独立的生成目录，不会覆盖普通 `frontend/dist/`。
静态资源配置只指向这个预览目录，不包含 Worker 脚本、云数据库、网关或其他服务绑定。
按 [Static Assets 文档](https://developers.cloudflare.com/workers/static-assets/)使用 assets-only 部署，
按[单页应用路由](https://developers.cloudflare.com/workers/static-assets/routing/single-page-application/)提供深链刷新。
官方[静态资源计费说明](https://developers.cloudflare.com/workers/static-assets/billing-and-limitations/)
与实际账户计划分别核验；本机构建不证明账户费用或云端可用性。

本机检查：在 `frontend/` 执行以下命令，另一个终端执行最后一行。

```powershell
npm test
npm run build
npm run build:preview
npm run preview -- --outDir dist-preview --host 127.0.0.1 --port 4173 --strictPort
node tests/staticPreview.browser.mjs
```

该浏览器检查只验证本地静态页面、深链／刷新、禁用提交及没有 API/WebSocket 请求；
它不替代 Cloudflare 实际部署、Free 计划／用量、远程权限或 Go/provider 验收。

公开部署前先核对批准账户的 Workers Free 计划、当前计费功能及静态产物指纹，
并取得该具体产物的发布授权。随后在仓库根目录使用已核对版本的 Wrangler：

```powershell
wrangler whoami
wrangler deploy --config deployments/cloudflare/wrangler.jsonc
```

配置中不写账户凭据或本机路径；部署后仍需核对真实 URL、深链刷新、资源及用量。
本文件不自动执行部署、OAuth、订阅或公网入口；Tunnel／Access 与 AI Gateway 分别验收。
