# 组件复用入口

当前实现与缺口从 [GOAL.md](GOAL.md) 和 [设计导航](DESIGN-MAP.md) 进入。
选型只为当前任务服务；现有调用方能满足需要时继续复用。

| 任务 | 先检查的现有能力 | 扩展条件 |
| --- | --- | --- |
| 代码执行 | Go Harness 与已批准的 OpenSandbox profile | 当前执行契约缺少明确能力，再比较外部 runner |
| 模型与计量 | 现有 Provider 接口、Go admission 与服务商预算 | 新协议或传输需求有真实案例，再适配兼容 SDK |
| Context／Memory | 当前 Context、Go Ledger 与反思／召回 | 明确质量或成本缺口后，做同任务对照；索引仍是派生数据 |
| Workflow | 已有 LangGraph、Worker 与 checkpoint | 真实规模或跨机需求明确后，再比较其他调度器 |
| 前端与浏览器 | 当前 React 组件和已安装的 Playwright | 围绕已选布局和具体交互复用组件 |
| Trace 与评测 | 现有 OTel、后端读回及官方 scorer adapter | 缺少接口时补适配；保留原始失败和完整分母 |

具体候选见 [候选导航](reuse-landscape.md)。这不是安装清单或验收门禁。

2026-10-08 的完整调查已原样归档为
[历史原文](archive/policy-cleanup-2026-10-20/docs--reuse-components.md)。
其中的凭据、沙箱、接入缺口和实施次序描述调查时状态，不代表当前待办。
源码与来源定位仍可用于追溯；实施前按选定版本重新核对兼容性和许可。
