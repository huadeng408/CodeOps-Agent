# 恢复执行后的完整 Trace 采集

工程状态 `IMPLEMENTED`。`VERIFIED` 限于公开 reader、外部 HTTP／CLI 回归，以及真实 Phoenix 中已有失败任务的完整采集。该任务严格 Trace 仍为 `FAIL`；#17/#18 的成功任务 Memory／Trace、#19 和完整发布继续按各自范围验收。

源码固定点 `9698beef6c7384535536c0618b32904554d550b8` 加本轮三项源码／测试修改。没有修改 evaluator、输入契约、阈值、计量、发布要求或票的验收标准。本次采集修复结束前，用户已批准版本化 Trace 验收调整；该方案另行实现和验证。

## 修复与边界

原 `read_product_spans` 遇到同一个 canonical run 的多个实际 Go 根时立即拒绝，导致恢复前后整棵树均未保存。现在 reader 按实际唯一 trace 分页读回全部 span，将验收判断继续交给原严格 evaluator。没有合成根、删除 ERROR、隐藏失败尝试或创建第二个事实写入者。

重复根、异源 trace、敏感字段和分页异常继续拒绝；同一 trace 只读取一次。新增回归覆盖恢复尝试位于相同或不同 trace；外部 CLI 仍因两个根及 ERROR 返回 `FAIL`／`BLOCKED`、退出 3，同时完整保存六个实际 fixture span。这是集成验证，不是新模型任务证据。

## 真实后端读回

使用原 c704 构建失败尝试的不可变 Session/admission 导出及原 Phoenix 后端。三条 canonical run 均经当前 native 审计；重新读回包含四次实际执行的 254 个 span，全部已结束，25 个 ERROR／取消状态。全部身份及规范化字段与原完整诊断逐项相同；工具／模型分母仍为 104／47，四个缺失 provider span 保留。

原严格 evaluator 仍报告多根、带父根、错误／取消和 durable run 未成功；结果 `FAIL`。新读回按 canonical run 排列，`primary_trace_id` 展示值相应变化；按原顺序解释相同 span 时，严格报告与原报告逐字段相同。没有用改变顺序或展示值消除失败。

三次检查均保留：首次六次启动期请求断开连接、退出 1；第二次第三个请求已读全树，但检查脚本将主 trace 展示值的顺序差异判为不等、退出 1；第三次保存原始比较结果，核对全部字段及原顺序严格报告、退出 0。前两次不是产品验收通过，也没有删去。

只启动并恢复停止原有 Compose project `localcode` 的固定 Phoenix；没有新建／删除容器、镜像、volume，未启动模型或任务执行器。每次前后 Budget 投影及调用事件完全相同，ActiveCalls=0、任务槽为空。旧 unknown、预留和未知费用保留；原严格收据及原 157 项封存产物保持相同。

新分析指纹与原 runtime／输入绑定分别记录；原冻结输入、源码 SHA、收据结果、分母和 checksum 没有重写。该采集是对真实历史失败运行的新分析，不能升级为成功十轮任务或当前构建的产品 E2E。

## 验证

| 范围／命令 | 结果 |
| --- | --- |
| 公开 reader RED | 两个恢复分支均因原 multiple-roots 拒绝失败，2 FAIL／4 deselected，退出 1；日志保留 |
| Trace 三文件 | `python -m pytest -q tests/eval/test_phoenix_readback.py tests/eval/test_product_trace.py tests/eval/test_trace_contract.py`；78 PASS、0 FAIL/SKIP，退出 0 |
| 完整 Python | `python -m pytest -q`；2,565 PASS、17 SKIP、0 FAIL，3 个 warnings，597.10 秒，退出 0；跳过项不升级为通过 |
| 真实 Phoenix 采集 | 三次检查分母如上；最终 3 run／4 根／254 span，原严格 FAIL，采集检查退出 0 |
| 静态检查及审阅 | `git diff --cached --check`、精确五路径核对及不输出匹配值的秘密扫描退出 0，AGENTS.md 未暂存；源码 Standards 0／Spec 0，AI_REVIEWED，不代表人工复核 |

本轮没有更改 Go、frontend、协议、依赖或随包产品 runtime；不重跑完整 Go／frontend，不重建 Windows 包，不执行完整发布门禁。CLI 回归与真实导出检查实际调用原生 Go 审计。所有 Python／native 命令使用已安装的 Go 1.26.0、`GOTOOLCHAIN=local` 和 `GOPROXY=off`。

本机命令、完整输出、失败及新分析指纹位于 `.scratch/trace-recovered-readback-20261016/`；Docker 检查脚本与派生产物分别位于约定的 `.local/profiles/` 和 `.local/receipts/` 下同名三个尝试目录，均保持忽略。验证摘要 `validation-summary.json` 的 SHA-256 为 `a4b0a7f9a9afc121502b4dc8edbe4b268685545dc2f1ff7fad44e2994080eb92`；48 项产物清单 `artifact-manifest.json` 的 SHA-256 为 `00194113d60fcc785ad0c19cef476af6a8896778d003041aa8030f0bf49026ef`。另核对 264 个原输入文件及四份 index 不变；不提交运行数据或机器路径。
