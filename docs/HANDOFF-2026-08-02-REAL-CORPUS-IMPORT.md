# 交接文档：真实语料导入与 alias 切换（2026-08-02 晚间）

> 本文档写给下一个执行 Agent。目标：把 3095 份官方技术语料导入 v2 索引并完成 alias 切换。
> 代码层面的根因已经解决并提交；**当前唯一阻塞是 Docker 容器未启动（环境问题）**。

## 0. 关键结论（先读这个）

1. **代码 bug 已解决**（提交 `3f05bb1`）：非 PDF 文档走结构化管道时，Go 客户端 `for index, chunk := range slice` 的 **range 值拷贝**导致 `ParserVersion`/`SourceSHA256` 填充从未写回 slice——`Validate()` 永远看到空值。这是之前 4 次"修复无效"的真正根因。**决定性单元测试** `pkg/orchestrator/client_fill_test.go::TestChunkClientFillsMissingProvenance` 已证明修复生效。
2. **当前环境状态**：Docker Desktop 已重启（用户授权可直接重启），但**容器尚未启动**（9200/3306/9092 全不通）。RAG server（code-server.exe PID 40440）在跑但依赖不可用。
3. **下一个动作**：`docker compose up -d` 启动全部容器 → 确认 Kafka/ES/MySQL 健康 → 重导一个文档验证 ES v2 首次写入 → 批量 pilot 导入。

## 1. 当前事实

### Git 状态（HEAD `3f05bb1`，全部已推送）
| 提交 | 内容 |
|---|---|
| `ee793bd` | 6 个上游仓库真实锁定（commit + license hash），manifest 验证通过 |
| `3a48eef` | GPU BGE-M3 embedding 服务（sentence-transformers 后端）+ 本机 preflight PASS |
| `f7820bd` | 非 PDF 文档走结构化管道（`_text_to_elements` + CorpusGeneration + 上传类型扩展） |
| `144239c` | source_sha256 客户端填充（首次尝试，因 range 拷贝未生效） |
| `3f05bb1` | **根因修复**：指针写回 slice + 决定性测试 |

工作树：`AGENT.md`（用户文件，不提交）、`scripts/embedding_server.py`（有未提交修改，可能是用户/linter 改动——**先检查 diff 再决定是否提交**）、`.tmp-go-cache-*`（临时目录）、`CLAUDE.md`/`CLAUDE-CODE-RECOVER-*`（用户文件）。

### 服务状态（2026-08-02 20:5x 实测）
| 服务 | 端口 | 状态 |
|---|---|---|
| Docker 容器（es/mysql/minio/redis/kafka/tika/zookeeper/phoenix/reranker） | 9200/3306/9000/6379/9092/9998 | ❌ **全挂**（Docker Desktop 重启后未启动容器） |
| RAG server（code-server.exe PID 40440） | 8081 | ⚠️ 进程在跑但依赖不可用 |
| GPU embedding（本机 uvicorn） | 8009 | ⚠️ 需确认（Docker 容器版已停，本机版应保留） |
| Python worker（orchestrator.rag） | 8090 | ⚠️ 需确认 |

### 关键配置
- RAG server: `configs/server.yaml` → `ai.orchestrator.enabled: true`、`ingestion_enabled: true`、`base_url: http://127.0.0.1:8090`、`shared_secret: "codeagent-internal-dev"`
- Worker token: 启动时 `PAISMART_INTERNAL_TOKEN="codeagent-internal-dev"`（**必须带**，否则 503）
- Embedding 服务启动（本机 GPU）：
  ```
  EMBEDDING_MODEL="BAAI/bge-m3" EMBEDDING_REVISION="BAAI/bge-m3@8f1b7f9d..." \
  EMBEDDING_LOCAL_DIR="D:/tools/mineru-models/models/BAAI--bge-m3" EMBEDDING_PRELOAD=true \
  HF_HOME="D:\tools\mineru-models\hf-cache" HF_ENDPOINT="https://hf-mirror.com" \
  python -m uvicorn embedding_server:app --app-dir scripts --host 0.0.0.0 --port 8009
  ```
- **不要用 `HF_HUB_OFFLINE=1`**（会导致加载失败）；用 `HF_ENDPOINT=hf-mirror.com`
- corpus-loader 用户: `corpus-loader` / `corpus-loader-2026`（token 保存在 `$TEMP/corpus-token.txt`）
- 上传类型已扩展（html/rst/adoc/sgml/xml），支持语料格式

## 2. 已建立的资产

- **v2 物理索引**：`knowledge_base_v2_bge_m3`（1024 维 dense_vector + 27 字段 provenance/ACL，已验证）
- **alias**：`knowledge_base_current` → knowledge_base（旧索引继续服务）
- **6 个仓库 staging**：`C:\Users\ieeep\AppData\Local\Temp\corpus-pins\{go,python,git,docker,kubernetes,postgresql}`（完整 clone，commit 与 manifest 锁定一致）
- **文档清单已验证**：3095 份（go 27 / python 554 / git 910 / docker 928 / kubernetes 468 / postgresql 208），HEAD + license 门全过
- **BGE-M3 模型**：`D:\tools\mineru-models\models\BAAI--bge-m3`（2.2GB，sentence-transformers 兼容结构，GPU 加载 5.8s，1024 维原生输出已验证）

## 3. 恢复步骤（按序执行）

### Step 1: 启动 Docker 容器
```powershell
docker compose up -d
# 验证：docker ps 看 es/mysql/redis/kafka 全部 Up
# curl http://127.0.0.1:9200/_cat/health  → yellow 正常
```
**注意**：Docker daemon 可能 500（内存压力）→ 按用户授权直接重启 Docker Desktop：
```powershell
Stop-Process -Name "Docker Desktop" -Force; Start-Sleep 5; Start-Process "C:\Program Files\Docker\Docker\Docker Desktop.exe"
```
然后重试 `docker compose up -d`。**Kafka 容器可能单独没起来**（之前多次出现）——`docker compose up -d kafka` 单独启动。容器起来后**停掉 Docker 版 embedding**（它是 fastembed 不支持 BGE-M3）：`docker stop codeagent-embedding`，保留本机 GPU 版。

### Step 2: 启动本机服务（若未运行）
1. GPU embedding（见上文命令，端口 8009）
2. Python worker：`PAISMART_INTERNAL_TOKEN="codeagent-internal-dev" python -m uvicorn orchestrator.rag.main:app --host 127.0.0.1 --port 8090`
3. RAG server：`/tmp/code-server.exe`（**必须重新构建**：`go build -a -o /tmp/code-server.exe ./cmd/server`——普通 `go build` 可能用旧缓存，导致 provenance 填充不生效！）
4. 重新登录拿 token：`curl -X POST :8081/api/v1/users/login -d '{"username":"corpus-loader","password":"corpus-loader-2026"}'`

### Step 3: 单文档验证（决定性）
导入 go 的一个 doc 文件（走上传 API check→chunk→merge），监控：
```bash
# 导入后轮询 ES v2
until curl -s http://127.0.0.1:9200/knowledge_base_v2_bge_m3/_count | grep -q '"count":[1-9]'; do sleep 5; done
```
若 ES v2 出现数据，验证文档结构：`document_id/corpus_generation=techdocs-2026-07-30-v1/model_version=BAAI/bge-m3@.../vector=1024维/source_sha256 非空`。

**若仍报错**（`source_sha256 is required` 或 `parser_version is required`）：
- 检查运行二进制是否含 `3f05bb1` 修复：重新 `go build -a` 并确认 server 重启
- 检查错误来自 `processChunkExternal`（补丁已在此）还是客户端（`pkg/orchestrator/ingestion_client.go:166` 附近指针写回）

### Step 4: 批量导入
```bash
python scripts/corpus/import_docs.py --token-file $TEMP/corpus-token.txt --staging $TEMP/corpus-pins --source go --limit 20   # pilot 每来源 20-50
# 全部 6 来源 pilot 通过后全量
```
**注意**：`import_docs.py` 需要 `--token-file` 参数；Python 读路径要用 Windows 路径（`$TEMP` 环境变量），Git Bash 的 `/tmp` 在 Python 里不可见。

### Step 5: 后续（导入完成后）
1. 污染扫描：`python -m eval.contamination.scanner`（脚本已就绪）
2. 检索评测：真实 qrels + `python -m eval.retrieval.metrics`
3. preflight → alias 切换（`scripts/rag/preflight-cutover.ps1` + `switch-alias.ps1`，runbook 见 `docs/releases/RAG-CUTOVER-techdocs-v2.md`）
4. 真实 Agent E2E（中文查英文）

## 4. 已知坑（务必注意）

1. **`go build` 缓存坑**：必须 `go build -a` 强制重建，否则 provenance 修复不生效（已踩 2 次）
2. **`range` 值拷贝**：改 slice 元素必须 `&slice[i]`，不能用 `for _, v := range`（这就是 3f05bb1 修的问题）
3. **worker 503**：`PAISMART_INTERNAL_TOKEN` 必须与 server.yaml `shared_secret` 一致（`codeagent-internal-dev`）；启动 worker 的进程必须带该 env
4. **worker parse 超时**：大文件下载 MinIO 可能 120s 超时（`request_timeout_seconds` 配置）；重试即可
5. **内存压力**：系统 15.7GB，Docker 全家桶 + GPU 推理 + IDE 同时跑会内存耗尽 → Docker daemon 500、MySQL busy buffer、ES 超时。**若出现：重启 Docker Desktop（已授权）**，必要时停 tika/kafka/zookeeper 省内存
6. **`HF_HUB_OFFLINE=1` 会导致模型加载失败**（sentence-transformers 找不到本地路径）——只用 `HF_ENDPOINT=hf-mirror.com`
7. **embedding 端口冲突**：Docker 版（fastembed）与本机 GPU 版会同时监听 8009——停 Docker 版
8. **非 PDF 文档**：worker `chunk()` 已走 `_text_to_elements` 结构化路径（`ingestion_chunk_text_structured` 日志）；PDF 走 MinerU OCR

## 5. 回滚边界

- **代码**：按提交逆序回滚（`3f05bb1` → `144239c` → ...）
- **数据**：v2 索引空 → 无风险；alias 未切换（`knowledge_base_current` 仍指旧索引）
- **用户文件**：AGENT.md / CLAUDE.md / CLAUDE-CODE-RECOVER-PARENT-THREADS-20260801.md 不提交、不覆盖
- **删除**：不得删除 Docker volume、ES 索引、模型缓存（`D:\tools\mineru-models`）、staging 仓库

## 6. 完成定义（本阶段）

- ES v2 有真实数据（≥ 1 文档，含完整 provenance + 1024 维向量）
- 6 来源 pilot 导入通过
- 全量导入完成（3095 份目标）
- 污染扫描 + 检索评测报告生成
- alias 切换 + 真实 Agent E2E 通过（若门槛达标）

## 7. 重要用户偏好（来自本会话）

- 用户要求 **ultracode 多智能体编排**用于大型任务（Workflow 工具）
- 用户已授权：Docker Desktop 可直接隐藏启动/重启（不删除容器/volume/数据）
- 用户用中文沟通；提交信息英文 + Co-Authored-By
- 进展必须同步到 `D:\Obsidian\code-autogrowth\私人\localcode\PROGRESS-YYYY-MM-DD.md`（中文）
