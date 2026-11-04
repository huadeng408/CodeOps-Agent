# 仓库整理与验证入口修复

范围：基于 `2c29eee6a5e7d47c393ebb8a59e2ed4f57ea07cd` 清理已确认的废弃实现、重复依赖、过期导航和无效验证等待；产品授权、发布资格和历史运行结论不变。

## 检测与处理

初始清单为1,485个tracked文件、201份Markdown、17,333行。按引用、实际调用路径及新旧事实核对，未按体积或日期直接删除。

| 确认的问题 | 已实施处理 |
| --- | --- |
| 23个废弃／重复私有函数 | 删除旧RAG分片链及其启动预热、未调用的CLI／Memory／MCP辅助函数；三个重复Skill文件删除，稳定catalog保留 |
| 两份RAG依赖入口冲突 | 删除无人引用的 `orchestrator/rag/requirements.txt`；以 `pyproject.toml` 为入口，移除废弃splitter并用官方离线命令同步 `uv.lock`，未升级其他包 |
| 已失败仍派发后续检查 | 公共验证CLI在失败／超时／证据不完整后停止，保存原失败与所有 `not_run`；部分通过不能成为PASS |
| PDF清理正则乱码 | 修复中文页码匹配；公共parse回归同时核对结构化元素和来源不变 |
| 9份被新记录替代的叙述、重复任务状态 | 原样移入归档；四份旧入口另存原件。13份共118,240字节均有SHA，Git属性保护字节不被换行转换 |
| 入口文档过长、历史命中干扰 | AGENTS／Goal／任务导航／证据索引由252行缩至127行；默认搜索排除归档，历史仍可按路径读取 |

[归档清单](../archive/execution-cleanup-2026-11-04/manifest.json)记录原路径、字节数和SHA；旧AGENTS改名保存，避免自动加载为嵌套规则。当前目标从[Goal](../GOAL.md)进入。

## 验证

| 检查 | 真实结果 |
| --- | --- |
| 新增高层回归：失败／超时停止、离线ingestion启动、PDF清理 | 修复前4失败，修复后4通过；原失败保留 |
| 受影响Python功能文件 | 128通过／1跳过，exit0 |
| Windows公共验证CLI与真实子进程 | 86通过／1跳过，exit0；完整配方fixture另2通过，exit0 |
| `go test -json -p 1 -count=1 ./internal/admission ./internal/cli ./internal/mcp ./internal/memory ./internal/skills` | 五包通过，含子用例652通过／14跳过，exit0；CLI包515.071秒 |
| `uv lock --offline --no-python-downloads --check` | 同步前exit1；官方更新及同步后检查exit0，只删除14行废弃依赖 |
| 文档／归档／独立审查 | 当前链接目标无缺失；13/13原件hash匹配；Standards与Spec均GO，锁文件遗漏已修复，无未解决finding |

命令、JUnit、Go JSON、原始RED及审查记录保留在 `.scratch/repository-cleanup-20261104/`。跳过项保留，不当作已运行；全套Go／Python／前端及Windows启动检查由本提交的源码CI执行，源码CI不代替产品或发布验收。

续办仍从同一Session的第194项累计attempt3开始，见[真实断点](product-long-dialogue-checkpoint-2026-11-04.md)。旧193个合格项、202次实际Run、失败、取消、unknown与预留不改写；共享续跑判定和最后七项尚待完成。
