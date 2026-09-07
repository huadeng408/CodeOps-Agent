# CodeOps Agent Frontend

React + TypeScript + Vite 前端项目，对接 localcode 后端 API。

## 开发

```bash
cd frontend
npm install
npm run dev
```

访问 http://localhost:3000

## 构建

```bash
npm run build
```

## 技术栈

- React 19
- TypeScript 7
- Vite 8
- 原生 CSS（复刻 deepseek-harness 设计系统）

## 功能

- ✅ 会话管理（项目分组、状态标识）
- ✅ 事件溯源展示
- ✅ 暗色/亮色主题切换
- ✅ WebSocket 实时消息（短期一次性 ticket、游标重放和断线重连）
- ✅ Checkpoint 创建与追加式恢复
- ✅ 工具/系统事件的安全展示（由 canonical Session Ledger 驱动）

浏览器认证使用后端下发的 HttpOnly cookie；前端不会把 access token 或
refresh token 写入 `localStorage`。完整生产级验收仍需真实运行时 receipt，
包括服务重启、双用户隔离和 200-turn 长对话，不能由本地构建结果替代。
