# 视觉链路 Pilot 设计（page visual / ColQwen2）

状态：`DESIGNED`（仅设计，未实现；本轮不改任何代码/配置/服务，不下载任何模型）

日期：2026-08-04

上游依据：`docs/superpowers/specs/2026-08-02-multimodal-rag-agent-platform-design.md` §4.2 / §4.3 / §7.3 / §7.4；
`docs/superpowers/specs/2026-08-03-rag-v2-cutover-dataready-design.md`（文本主链 cutover 进行中，本设计与其完全隔离）。

## 0. 任务边界

- 本文档只做研究 + 设计：候选视觉模型的许可证/显存/hf-mirror 可得性调研（WebSearch + hf-mirror API 实测）、现有视觉脚手架代码核对、pilot 索引与 bake-off 方案设计。
- **不实现**：不写运行代码、不改 `configs/server.yaml`、不创建索引、不下载模型权重、不启动任何服务。
- 文本主链（`knowledge_base_v2_bge_m3` 全量导入 / alias / preflight）正在并行推进，本设计不依赖、不触碰其任何状态。

## 1. 现状核对（代码审计结论）

### 1.1 VisualIndexManager（`pkg/es/visual_index.go`）

| 检查项 | 结论 |
|---|---|
| 与文本索引物理隔离 | ✅ 已隔离。`VisualV2Mapping` 无 `text_content`/`embedding_text`/BM25 字段，只有 `visual_vector`（`dense_vector`，cosine，`index: true`）+ provenance 字段（`document_id/page_id/page_index/page_ref/asset_ref/asset_sha256/source_sha256/element_id/element_type/bbox/bbox_scaled/kind/model/model_revision`）。视觉向量永不进入文本 mapping，反之亦然（注释明示 design spec §4.2）。 |
| dims fail-closed | ✅ `EnsureVisualIndex` 对已存在索引做 `verifyVisualMapping`：vector 字段必须是 `dense_vector` 且 dims 精确匹配，否则拒绝（"refusing incompatible mapping"）。 |
| alias 语义 | ✅ `ReadVisualAlias` 遇到 404 返回 `nil, nil`——alias 不存在即"视觉路径关闭"，与设计"生产 alias 默认不存在"一致；`SwitchVisualAlias` 用单次 `_aliases` API 原子完成 remove 旧 + add 新。 |
| 配置默认值 | ✅ `internal/serverconfig/config.go`：`VisualPilotPrefix = "knowledge_page_visual_pilot"`、`VisualAlias = "knowledge_page_visual_current"`、`AllowAliasSwitch = false`（安全默认，不切换）。 |
| 现有测试 | `pkg/es/visual_index_test.go` 用 `knowledge_page_visual_pilot_v1` + **dims=128** + alias `knowledge_page_visual_current`——128 恰与 ColQwen2 patch 向量维度一致，mapping 参数化无需改代码即可适配。 |

### 1.2 Encoder（`orchestrator/rag/visual/encoder.py`）

- `encoder_status`：`ready` 仅当同时有 device 与 **pinned model revision**，否则显式 `disabled` 并给出原因（无 GPU / 未锁定 revision）。
- `VisualEncoder.encode_page/encode_crop`：非 ready 时 raise RuntimeError（fail loud，不伪造）；ready 时也是 `NotImplementedError`——**具体模型编码器尚未实现**，脚手架明确标注 "concrete encoder (ColPali/ColQwen) must implement"。
- 结论：disabled 状态符合预期；本设计为其选定具体模型（§3），实现留待后续任务。

### 1.3 Artifacts（`orchestrator/rag/visual/artifacts.py`）

- `page_artifact`（kind=page）与 `page_crop_artifacts`（kind=crop，仅 image/table/equation 元素）只消费 MinerU 渲染产物，携带 `source_sha256/page_id/element_id/bbox/bbox_scaled/model/model_revision`。
- `CROP_SCALE = 1000` 与 ColPali/ColQwen 系列的 1000 坐标尺度约定一致，bbox 可直接喂给 late-interaction 定位评估（bbox hit 指标）。

## 2. 候选模型调研（许可证 / 显存 / 单向量适配 / hf-mirror 可得性）

### 2.1 命名澄清

任务书提到 "ColQwen2 (bge-reranker/colqwen 系)"：ColQwen2 属于 **ColPali/ColVision 家族**（Illuin Tech / ViDoRe 团队，ColBERT 式 late-interaction 视觉检索器），与 BAAI 的 bge-reranker（纯文本 reranker）无同源关系。BGE 系列目前没有视觉检索/重排模型。另：HuggingFace 上**不存在**官方 `vidore/colqwen2-v1.3`（hf-mirror API 实测返回与不存在仓库完全相同的错误；WebSearch 亦确认官方 ColQwen2 发布为 v1.0，更新的迭代是 ColQwen2.5 v0.2）。

### 2.2 对比总表

| 模型 | HF 仓库（revision sha） | HF 许可证 | 商用/本地 | 原生单向量？ | 推理显存（估算） | hf-mirror 可得性（2026-08-04 实测） |
|---|---|---|---|---|---|---|
| **ColQwen2 v1.0**（推荐） | `vidore/colqwen2-v1.0-merged@2c9a09bb37ed19b63eb94ae6c29bb4e76eb6e3c2`（单文件 4.4GB bf16 合并权重；或 adapter 版 `vidore/colqwen2-v1.0@83a0134c8f274b3688d8dbde26de8a5b109ad8b4` + `vidore/colqwen2-base`） | **Apache-2.0**（仓库 license tag；基座 Qwen2-VL-2B-Instruct 亦 Apache-2.0） | ✅ 商用/本地均可，无附加条款 | ❌ 多向量：128 维 × ≤768 patch/页（ColBERT late-interaction）；单向量需 pooling 降格 | 权重 ≈4.4GB（bf16）；**8GB 卡可跑 batch 1–2**；16GB 舒适 | ✅ API/tree 均可匿名访问，gated=false |
| **DSE-Qwen2-2B**（备选） | `MrLight/dse-qwen2-2b-mrl-v1@3fde4464ea72da2a863ed8fa51f0f1b8045f0426` | **Apache-2.0**（仓库 license tag；基座 Qwen2-VL-2B-Instruct Apache-2.0） | ✅ 商用/本地均可 | ✅ **原生单向量** 1536 维（hidden_size 实测 config.json=1536），支持 Matryoshka 截断 | 权重 ≈4.4GB（bf16）；8GB 卡 batch 1–2 | ✅ 可匿名访问，gated=false |
| ColPali v1.3（排除） | `vidore/colpali-v1.3@1b5c8929330df1a66de441a9b5409a878f0de5b0` | 仓库 tag MIT，但合并权重含 PaliGemma 基座 → **Gemma Terms of Use**（`vidore/colpaligemma-3b-pt-448-base` license:gemma 实测） | ⚠️ Gemma 条款允许商用但附加 AUP/转让限制/登记义务，合规负担高于 Apache-2.0 | ❌ 多向量：128 维 × 1024 patch/页 | 权重 ≈6–7GB（bf16）；**8GB 卡全精度 OOM**（HF discussion #1 有 RTX 2070S 8GB OOM 报告），16GB 仅 batch 4 | ✅ 文件可访问，但许可证负担不因可得性消除 |
| ColQwen2.5 v0.2（暂缓） | `vidore/colqwen2.5-v0.2@6f6fcdfd1a114dfe365f529701b33d66b9349014`（adapter 240MB + `vidore/colqwen2.5-base`） | 仓库 tag MIT；基座 tag Apache-2.0（Qwen2.5-VL-7B-Instruct 官方 Apache-2.0） | ✅ 许可证干净 | ❌ 多向量：128 维 × ≤768 patch/页 | 基座 7B ≈15–16GB（bf16）→ **需 24GB 卡，本机不可行** | ✅ 可访问（但权重总量大） |
| VisRAG-Ret（排除） | `openbmb/VisRAG-Ret@95ef596df871b606167cb7e4b7215caf1bfdf761` | 仓库 tag apache-2.0，但 model card 声明权重须遵循 **MiniCPM Model License**（商用需登记/问卷的自定义条款，社区报告流程不透明） | ⚠️ 商用需向面壁登记，条款非标准 OSI 许可 | ✅ 原生单向量 2304 维（config.json hidden_size=2304 实测） | 权重 ≈5.6GB（bf16，MiniCPM-V 2.0 ≈2.8B）；8GB 临界 | ✅ 可访问，但 `custom_code` 需 trust_remote_code（供应链面扩大） |
| ColQwen2 "v1.3"（不存在） | `vidore/colqwen2-v1.3` | 无法核验 | — | — | — | ❌ hf-mirror API 返回 "Invalid username or password"，与虚构仓库响应一致 → 匿名不可得，且 WebSearch 无官方发布记录 |

### 2.3 许可证结论（含来源）

1. **ColQwen2 v1.0：Apache-2.0，可商用、可本地部署、无登记/AUP 附加义务。**
   - HF 仓库 license tag `apache-2.0`（hf-mirror Hub API 实测，`tags` 含 `license:apache-2.0`）；
   - 基座 Qwen2-VL-2B-Instruct：Qwen 官方博客明示 2B/7B 以 Apache 2.0 开源（<https://qwen.ai/blog?id=qwen2-vl>），HF LICENSE 文件 <https://huggingface.co/Qwen/Qwen2-VL-2B-Instruct/blob/main/LICENSE>。
2. **DSE：Apache-2.0（fine-tune 仓库），基座同为 Apache-2.0，可商用。** 来源：<https://huggingface.co/MrLight/dse-qwen2-2b-mrl-v1>（license tag 实测）；论文 <https://arxiv.org/abs/2406.11251>。注意不存在 `tencent/DSE-*` 官方仓库，canonical 仓库即 MrLight（第一作者）。
3. **ColPali v1.3：受 Gemma Terms of Use 约束。** 仓库 tag 为 MIT（adapter/代码），但 v1.3 是合并后的完整权重，含 PaliGemma 基座；基座仓库 `vidore/colpaligemma-3b-pt-448-base` license tag 实测为 `gemma`。Gemma 条款允许商用但附 AUP、限制条款转让、需保留归属，属自定义许可，合规审查成本高于 Apache-2.0。来源：<https://ai.google.dev/gemma/terms>、<https://huggingface.co/vidore/colpali-v1.3>。
4. **VisRAG-Ret：双重声明。** HF tag 为 apache-2.0，但 model card 明文要求权重使用遵循 MiniCPM Model License；OpenBMB 政策为 Apache-2.0 + 社区许可协议，商用需填写登记问卷（社区报告联系渠道不畅）。许可证结论存在歧义，按最严格解释处理。来源：<https://huggingface.co/openbmb/VisRAG-Ret>、<https://github.com/OpenBMB/MiniCPM-V/blob/main/LICENSE>、<https://github.com/OpenBMB/MiniCPM-V/issues/210>。
5. **第三方 ColQwen 变体警示**：如 `tsystems/colqwen2.5-3b-multilingual-v1.0` 等社区微调可能采用 CC BY-NC 4.0（非商用）——任何非 vidore 官方仓库必须逐个核验，不得继承基座许可证假设。

### 2.4 显存与本机硬件约束

本机实测 GPU：**NVIDIA RTX 4060 Laptop（8GB VRAM）**，系统内存 15.7GB（Docker 全家桶 + GPU 推理 + IDE 并发时有内存压力，见 `docs/HANDOFF-2026-08-02-REAL-CORPUS-IMPORT.md` §6）。

| 模型 | 权重（bf16） | 8GB 卡可行性 | 依据 |
|---|---|---|---|
| ColQwen2 v1.0（2.2B） | ≈4.4GB | ✅ batch 1–2，页级 ≤768 patch | 合并权重文件 4.4GB 实测；同规模 ColPali 8GB OOM 案例反推上界 |
| DSE（2.2B） | ≈4.4GB | ✅ batch 1–2 | 同规模 |
| VisRAG-Ret（2.8B） | ≈5.6GB | ⚠️ 临界（权重+激活贴近上界） | hidden_size 2304 实测 |
| ColPali v1.3（3B） | ≈6–7GB | ❌ 全精度 OOM；4-bit 量化亦勉强 | HF discussion #1（RTX 2070S 8GB OOM）；Vespa 实测 bs=4 需 16GB |
| ColQwen2.5 v0.2（7B） | ≈15–16GB | ❌ 需 24GB | 基座 Qwen2.5-VL-7B 参数量 |

**关键约束**：BGE-M3 embedding 服务（权重 2.2GB）已常驻同一块 8GB GPU。视觉编码器与 BGE-M3 **不可同时驻留**——bake-off 离线编码窗口必须与文本导入/embedding 服务串行（先停 embedding 服务或确认无导入任务），查询时分的共存策略在门槛通过后再设计（§7 风险 R1）。

## 3. 推荐模型及理由

**推荐：ColQwen2 v1.0（`vidore/colqwen2-v1.0-merged@2c9a09bb37ed19b63eb94ae6c29bb4e76eb6e3c2`）作为视觉 pilot 主模型；DSE（`MrLight/dse-qwen2-2b-mrl-v1@3fde4464...`）作为单向量对照与 fallback。**

理由：

1. **许可证最干净**：全链 Apache-2.0（模型仓库 + 基座），商用/本地零附加义务；ColPali（Gemma ToU）与 VisRAG-Ret（MiniCPM 登记条款）均有合规负担。
2. **本机 8GB 可行**：2B 级权重 4.4GB，batch 1–2 可编码；ColPali/ColQwen2.5 在本机不可行。
3. **质量档位最高**：late-interaction（ColBERT 式）家族在 ViDoRe v1 榜首领先（ColPali 基线 nDCG@5 81.3，其后迭代 +5.3；ViDoRe v2 榜首 >90，<https://huggingface.co/blog/manu/vidore-v2>）；ColQwen2 是其中 Apache-2.0 的唯一 2B 级选项。
4. **与设计规范预设一致**：spec §4.2 已预设 "ViDoRe/ColPali 系列离线 bake-off + late-interaction 只对 text top 20–50 页面重排"；ColQwen2 128 维 patch 向量与 `visual_index_test.go` 已用的 dims=128 一致，`CROP_SCALE=1000` 与其坐标约定一致——脚手架零改动。
5. **hf-mirror 可得性实测通过**（匿名、非 gated）；huggingface.co 本机直连失败（HTTP 000），hf-mirror 是唯一通道，可得性已验证。

**两阶段用法**（对齐 spec §4.3）：

- **召回阶段（单向量）**：对 ColQwen2 的 patch 多向量做 mean-pooling 得 128 维页级向量写入 `visual_vector`（现 mapping 零改动），ES ANN 召回 page top 50。⚠️ pooling 是有损降格（多向量→单向量），效果由 bake-off 实测裁决；若召回质量不达标，fallback 为 DSE 原生 1536 维单向量（同架构基座、同 Apache-2.0，仅换模型与 dims 重建 pilot 索引，命名规范天然支持）。
- **重排阶段（late-interaction）**：仅对 text/visual 召回合并后的 **top 20–50 页**做 ColQwen2 MaxSim 重排。多向量不进 ES 主召回路径，存 sidecar（按 `page_id` 键控的本地向量文件，fp16，≈0.4MB/页 × 2000 页 ≈ 0.8GB，可承受），避免 1.5M patch 文档灌爆 ES。

**排除项记录**：ColPali v1.3（Gemma 条款 + 8GB OOM）；ColQwen2.5 v0.2（7B 超本机显存，保留为未来 24GB 硬件升级路径）；VisRAG-Ret（MiniCPM 许可歧义 + trust_remote_code + HF 下载量仅 725 采用度低）；colqwen2-v1.3（不存在/不可得）。

## 4. page visual pilot 索引设计

### 4.1 命名

- 物理索引：`knowledge_page_visual_pilot_<model>_<revision>_<date>`（spec §4.2 原文格式），token 规则：小写字母/数字/下划线，revision 取 HF commit sha 前 8 位。
  - 主推荐实例：`knowledge_page_visual_pilot_colqwen2v10_2c9a09bb_<YYYYMMDD>`（dims=128，pooled 页级向量）。
  - DSE fallback 实例：`knowledge_page_visual_pilot_dseqwen2_3fde4464_<YYYYMMDD>`（dims=1536 或 MRL 截断维）。
- 生产 alias：`knowledge_page_visual_current`——**默认不存在**（`ReadVisualAlias` 404 → 视觉路径 disabled）；仅当 §5.4 门槛全部通过后，用 `SwitchVisualAlias` 单次原子调用创建/切换。`AllowAliasSwitch` 保持 `false` 直到人工批准。
- 模型 revision 写入每条文档 `model`/`model_revision` 字段：`model=vidore/colqwen2-v1.0-merged`，`model_revision=2c9a09bb37ed19b63eb94ae6c29bb4e76eb6e3c2`（对齐文本链 `BAAI/bge-m3@<commit>` 纪律）。

### 4.2 mapping 结论

现有 `VisualV2Mapping(dimensions)` **无需修改**即可承载 pilot：`dims` 参数化（128 / 1536），fail-closed 校验已就位。唯一设计增量（实现阶段，不在本文档范围）：可选新增 `multivec_ref` keyword 字段指向 sidecar 多向量对象，用于 MaxSim 重排溯源；不加也不影响召回链路。

## 5. Bake-off 方案

### 5.1 语料（500–2000 页）

- 从 techdocs 语料（6 源）选取 **page 化子集**：将选定文档渲染为页图（经 MinerU 渲染管线，遵守 artifacts.py 契约"只消费渲染产物，不读原 PDF"），目标 500–2000 页，覆盖 image/table/equation/版面密集四类页面各占比例（配比在实现时随语料盘点确定）。
- 每页产物即 `page_artifact`（kind=page）+ 元素级 `page_crop_artifacts`（kind=crop）；`source_sha256/page_ref` 保证可回溯到文本文档。
- **开放项**：当前 6 源为 MD/HTML 仓库文档（文本链用 Tika/native 解析），页图渲染路径（文档→PDF→页图 或 直接页渲染）在实现阶段确定；本设计只约束页数与产物 schema。

### 5.2 多模态 qrels（≥120 条，前置依赖）

- 按 spec §7.3 分层：**120 条多模态 qrels**，覆盖正文、图片、表格、公式、版面、多页关系、中文跨语言 7 层；每条指向 `document_id/section_path/page_id/element_id/bbox`；争议项双人复核。
- 现状：多模态 qrels **尚未建设**（当前只有 180 条文本 qrels 任务书 `docs/qrels/BUILD-180-QRELS.md` 在执行）——需另立任务书（建议命名 `docs/qrels/BUILD-120-MULTIMODAL-QRELS.md`），防污染规则与文本 qrels 一致（qrels 永不写入任何索引）。
- 评测前跑 contamination scanner（`eval/contamination/scanner.py`）对 qrels vs pilot 页/chunk 做四层检查。

### 5.3 对照 run（至少 5 个）

| run | 召回 | 重排 | 目的 |
|---|---|---|---|
| `text_baseline` | BM25+BGE-M3 hybrid（映射到 page） | — | text-only 基线 |
| `vis_pool_colqwen2` | ColQwen2 pooled 128 维 ANN | — | 单向量召回降格损失测量 |
| `text_plus_vis_rerank` | text_baseline top 100 → 页级 top 20–50 | ColQwen2 MaxSim | **主方案**（spec §4.3 形态） |
| `vis_recall_plus_rerank` | vis_pool top 50 | ColQwen2 MaxSim | 视觉独立召回上界 |
| `vis_dse` | DSE 1536 维 ANN | — | 原生单向量 fallback 证据 |

每个 run 锁定：模型 sha、语料 generation、渲染资产 sha256、seed、dims、batch、GPU peak；artifact 结构复用 spec §8.2（`run-manifest.json/predictions.jsonl/summary.json/...`）。

### 5.4 门槛（对齐 spec §7.4，全部通过才切 alias）

1. 多模态 **nDCG@5** 相对 `text_baseline` 有**可重复提升**（≥2 次独立 run 同向）；
2. image/table subset **Recall@5 不下降**；
3. **bbox hit ≥ 0.85**（命中 element 的 bbox IoU 判定，依赖 `bbox_scaled` + `CROP_SCALE=1000`）；
4. **无 OOM**；编码与重排的 **P95 延迟、GPU peak 显存在批准预算内**（本机预算建议：peak ≤7.5GB，留 0.5GB 余量）；
5. 无维度/revision 校验失败（`EnsureVisualIndex` fail-closed 零告警）。

## 6. 与文本主链的隔离保证（不污染 `knowledge_base_v2_bge_m3`）

1. **物理隔离**：视觉向量只写入 `knowledge_page_visual_pilot_*` 索引；`VisualV2Mapping` 无文本字段，文本链 `KnowledgeIndexManager` 的 mapping/写入路径无视觉字段（代码已核对）。
2. **alias 命名空间隔离**：`knowledge_page_visual_current` ≠ `knowledge_base_current`；`SwitchVisualAlias` 只操作视觉 alias；文本 alias 切换由独立的 cutover 流程持有，两者无共享 API 调用点。
3. **默认关闭**：视觉 alias 默认不存在；`AllowAliasSwitch=false`；encoder 未锁定 revision 时恒 disabled 且 fail loud——不存在"静默启用视觉路径"的可能。
4. **流水线隔离**：视觉 artifacts 只消费 MinerU 渲染产物，不复用/不修改文本 chunk 写入器；bake-off 期间文本链导入（3095 全量）可照常进行，唯一共享资源是 GPU 显存（§2.4 串行纪律）。
5. **评测隔离**：qrels/predictions 只落 `results/` 与 `data/eval/`，永不进任何 ES 索引；contamination scanner 在门槛评审前强制运行。
6. **回滚**：视觉 pilot 失败 = 不创建 alias（零回滚成本）；已建 pilot 索引可整体删除，不影响文本链任何状态。

## 7. 关键风险

| # | 风险 | 等级 | 缓解 |
|---|---|---|---|
| R1 | **8GB 显存争抢**：视觉编码器与 BGE-M3（2.2GB 常驻）无法共存；系统内存 15.7GB 亦紧张 | 高 | bake-off 编码窗口与文本导入串行；batch=1 起步、peak 监控；查询时分共存策略（按需加载 vs BGE-M3 CPU 化）门槛通过后再立项 |
| R2 | **pooled 单向量召回质量未验证**：mean-pooling 丢失 late-interaction 优势，可能召回不达标 | 高 | bake-off 设 `vis_dse` fallback run 与 `text_plus_vis_rerank` 主方案（主方案召回仍走文本链，视觉只做重排，天然规避该风险） |
| R3 | **多模态 qrels 未建成**：≥120 条门槛数据不存在，评审无法启动 | 高 | 独立任务书建设（§5.2），与文本 qrels 同规格防污染 |
| R4 | **语料页化缺口**：现有 6 源为 MD/HTML，无原生页图 | 中 | 实现阶段确定渲染路径；本设计以产物 schema 为契约，不锁死渲染器 |
| R5 | **中文跨语言视觉能力未证实**：ColQwen2 训练/ViDoRe 以英文为主 | 中 | qrels 分层强制含中文跨语言子集，单独出分，不达标则视觉门槛整体不过 |
| R6 | **hf-mirror 可得性漂移**：今日实测可得，不代表实现日仍可得；v1.3 类仓库已证明可能不可得 | 中 | 下载成功后立即 sha256 固化 + staging 离线副本；model manifest 锁 revision（spec §9 纪律） |
| R7 | **ES 无原生 MaxSim**：late-interaction 重排必须在应用层做 | 低 | 设计即如此（sidecar + orchestrator 重排），top 20–50 规模下 CPU 亦可承载 |
| R8 | **第三方 ColQwen 变体许可证陷阱**（CC BY-NC 等） | 低 | 只允许 vidore 官方仓库进入 manifest；变体逐个核验 |

## 8. 明确不做（本阶段）

- 不下载任何模型权重、不启动编码/推理服务（含 hf-mirror 拉取）。
- 不写/改任何代码与配置（encoder 具体实现、mapping 增量、importer、server 接线均留待实现任务）。
- 不创建任何 ES 索引、不触碰任何 alias（`knowledge_page_visual_current` 保持不存在，`knowledge_base_current` 不受影响）。
- 不建设 qrels 数据本体（仅规定其规格与任务书位置）。
- 不改动正在运行的文本导入/cutover 相关服务。

## 9. 来源快照（2026-08-04 核验）

许可证与模型事实（WebSearch + hf-mirror Hub API/tree 实测）：

- ColQwen2 v1.0（Apache-2.0 tag、sha、文件树）：`https://hf-mirror.com/api/models/vidore/colqwen2-v1.0[-merged]`；基座许可 <https://qwen.ai/blog?id=qwen2-vl>、<https://huggingface.co/Qwen/Qwen2-VL-2B-Instruct/blob/main/LICENSE>
- ColPali v1.3 与 Gemma 条款：<https://huggingface.co/vidore/colpali-v1.3>、基座 license:gemma（API 实测）、<https://ai.google.dev/gemma/terms>、<https://github.com/illuin-tech/colpali>
- ColQwen2.5 v0.2 与 Qwen2.5-VL Apache-2.0：<https://huggingface.co/vidore/colqwen2.5-v0.2>、<https://qwen.ai/blog?id=qwen2.5-vl>
- VisRAG-Ret 与 MiniCPM 许可：<https://huggingface.co/openbmb/VisRAG-Ret>、<https://github.com/OpenBMB/MiniCPM-V/blob/main/LICENSE>、<https://github.com/OpenBMB/MiniCPM-V/issues/210>
- DSE：<https://huggingface.co/MrLight/dse-qwen2-2b-mrl-v1>、<https://arxiv.org/abs/2406.11251>
- 显存依据：ColPali 8GB OOM <https://huggingface.co/vidore/colpali-v1.3/discussions/1>；T4 16GB bs=4 与 ~2.5s/页 <https://vespa-engine.github.io/pyvespa/examples/colpali-benchmark-vqa-vlm_Vespa-cloud.html>、<https://blog.vespa.ai/scaling-colpali-to-billions/>；patch 存储 ≈527KB/页 <https://www.spheron.network/blog/colpali-multimodal-document-rag-gpu-cloud/>；≈755 patch/页 <https://milvus.io/blog/how-to-build-multimodal-rag-with-colqwen2-milvus-and-qwen35.md>；≤768 patch 上限 <https://huggingface.co/vidore/colqwen2-v0.1>
- ViDoRe 质量档位：ColPali 基线 nDCG@5 81.3 / v2 榜首 >90 <https://huggingface.co/blog/manu/vidore-v2>；官方 scorer <https://github.com/illuin-tech/vidore-benchmark>

代码核对：`pkg/es/visual_index.go`、`pkg/es/visual_index_test.go`、`internal/serverconfig/config.go`、`orchestrator/rag/visual/encoder.py`、`orchestrator/rag/visual/artifacts.py`（本机 2026-08-04 读取）。

硬件：`nvidia-smi` 实测 RTX 4060 Laptop 8188MiB（2026-08-04）。
