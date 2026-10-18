# Windows 便携包：四个 Python 包的固定许可材料

对应 [#19](https://github.com/huadeng408/CodeOps-Agent/issues/19) 的组件物料缺口。
本轮实现并验证未来 Windows 包的六份许可材料组装及完整性拒绝边界。
没有更换运行依赖、模型、工具 schema 或 Trace 合同；原 v5 包和旧收据保持不变。
完整 #19、父规格与发布继续独立验收。

## 修改与固定来源

复用现有构建器的下载缓存和 SHA-256 校验，在组装时核对实际 wheel 包名／版本，
再将固定上游材料收入 `licenses/python/`。缺包、版本变化、路径越界、
Windows 保留设备名、尾点名称、非法 hash 或材料损坏时拒绝构建。
材料和逐文件摘要进入现有 manifest，不新增安装或启动下载步骤。

| 包 | 固定版本 | 上游固定源码与材料 |
| --- | --- | --- |
| grpcio-tools | 1.71.2 | [grpc commit](https://github.com/grpc/grpc/tree/c65cf153238102c16f5f11573767384ef112d17a)：LICENSE、NOTICE.txt |
| langchain-core | 0.3.86 | [langchain commit](https://github.com/langchain-ai/langchain/tree/9971fd8caafcfddddc94492cdecb02ce67b82da6)：LICENSE |
| langsmith | 0.14.8 | [langsmith-sdk commit](https://github.com/langchain-ai/langsmith-sdk/tree/19d52419f47c93ca2c57a0d51c58d255f5949092)：LICENSE |
| sqlite-vec | 0.1.9 | [sqlite-vec commit](https://github.com/asg017/sqlite-vec/tree/e9f598abfa0c06b328d8fe5da9c3760cce74be10)：LICENSE-APACHE、LICENSE-MIT |

版本由固定 tag／源码版本声明核对，原材料 Git blob SHA-1 与 SHA-256 均已核对。
langsmith 的动态版本仅通过 AST 读取声明模块，没有执行上游代码。
六个准确 URL 和摘要纳入 [组件 pin](../../deployments/windows/components.json)。
本轮输入摘要为 `87b068e0335ea17143bc70978cc798f62a2cf4c4f85da9c70e5d4eebfd27790d`。

## TDD 与审查

沿用已批准的分发 pin／完整性接缝，测试通过材料组装接口观察实际文件结果。
前三轮分别复现缺少收集器、缺包／版本变化未拒绝、路径／hash 未提前拒绝，再作最小修正。
首次 Spec 审查发现 Windows `NUL` 等设备名可复制成功却不落盘；
新增四项实际失败用例后，在同一收集器补齐拒绝，最终 13 项通过。
保留首次 Standards 0／Spec 1 P2；修正后两轴均 0，均为 AI_REVIEWED。

| 命令／范围 | 结果 |
| --- | --- |
| `python -m pytest -q tests/test_windows_bundle_notices.py` | 13 PASS、0 FAIL/SKIP，退出 0 |
| `python -m py_compile scripts/build-windows.py tests/test_windows_bundle_notices.py` | 退出 0 |
| `python -m pytest -q` | 2,623 PASS、17 SKIP、5 warnings，退出 0，完整收集分母 2,640 |
| 两项 subprocess 恢复测试，设置 `PYTHONUTF8=1` 后定向补验 | 2 PASS、1 既有弃用警告，退出 0；原两项 reader 解码警告未再出现 |
| Go／前端单测及完整发布 | 本修正范围未重跑；实际打包执行现有前端 build 与两项 Go build，不升级为上述测试或发布通过 |

全量前的一次旧快照运行因审查修正被主动中止，原部分输出保留，不计作完整通过。
最终全量中的五条警告和十七项跳过均保留；UTF-8 补验不删除原记录，
也不宣称默认 Windows 编码问题已由本次源码修复。
全量输出摘要：`fa16dcc470951c818b969a1c2c6e0153930223adfa58cdfadd37ec7ce1a09c64`；
补验输出摘要：`5d896265fe2c9b5f5428766cb050077f88f5a2286f5487a54f6e01e91df5f477`。

## 实际离线构建

构建使用已存在的 Python 3.12.4／pip 24.0、Go 1.26.0、
53 个锁定 wheel、两项 archive 和六份校验后的材料。
仅本次进程设置 `PIP_NO_INDEX=1` 和既有 wheel 缓存；没有下载或安装新开发依赖。
原命令入口为 `python scripts/build-windows.py --output <fresh-directory>`。

首次使用不带 pip 的测试 venv，原构建退出 1；保留该部分目录和 stderr。
改用已有构建解释器在另一新目录完成，构建自身退出 0。
外层观察脚本在记录退出码后打印 Vite 字符时遇到 cp936 编码错误，外层退出 1；
原始输出、构建结果和 manifest 均保留，独立重算文件摘要后确认完整，没有重建或改写结果。

新包有 612 项源码输入、3,225 项实际文件；全部指纹匹配。
仍为 53 个顶层 Python wheel、98 个链接 Go 模块和 3 个 frontend runtime 包。
相较原 v5 仅增加这六个 notice 路径，没有移除运行路径。
构建时固定点为 `428c62b43ce5fd1e7d11528d168ebd69f44fe7f4`，
当时未提交的三项组装内容为构建器、组件 pin 和随包 README，均由 manifest 的源码指纹绑定。
运行源码未变化；不将该 Git SHA 当作干净构建的声明。

新 manifest SHA-256：`c4b410aba1eda23bfbd640111d39639491bffeff2ddf4a329e6efa16a9cd3405`。
独立核对产物 SHA-256：`4380826e348fee0e3543c9d3da625de96d633942e458893cd497314e353da536`。
原 v5 manifest 及 3,219 项运行文件、用户 AGENTS.md 均不变。

## 产物与边界

本机记录在 `.scratch/windows-python-notices-20261019/`；
新包位于 `deployments/windows/.local/Portable Git 20261019 notices v6b/`。
这些是只读验证产物；Session Ledger 的事实边界不变。
本轮未以新包执行产品任务、登录／迁移验收或发布门禁，也不声明整个产品的法律许可结论。
四包材料的组装与完整性范围可交付，#19 其他故障、兼容与整体物料验收继续推进。
